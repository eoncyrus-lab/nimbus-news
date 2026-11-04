package controlstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var testEpoch = time.Date(2026, 7, 20, 8, 0, 0, 123456789, time.UTC)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *fakeClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *fakeClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = clock.now.Add(duration)
}

func openTestStore(t *testing.T) (*Store, *fakeClock, string) {
	t.Helper()
	clock := &fakeClock{now: testEpoch}
	path := filepath.Join(t.TempDir(), "control.db")
	store, err := Open(path, Options{Now: clock.Now})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store, clock, path
}

func TestOpenMigrationPragmasReopenAndForeignKeys(t *testing.T) {
	store, clock, path := openTestStore(t)
	ctx := context.Background()

	var migrationCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if migrationCount != 2 {
		t.Fatalf("migration count = %d, want 2", migrationCount)
	}
	var journalMode string
	if err := store.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("read journal mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal mode = %q, want wal", journalMode)
	}
	var foreignKeys int
	if err := store.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatalf("read foreign keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}
	_, err := store.db.ExecContext(ctx, `
		INSERT INTO step_runs(id, version, job_id, name, status, attempt, available_at, created_at, updated_at)
		VALUES ('step_bad_fk', 1, 'missing_job', 'bad', 'pending', 0, ?, ?, ?)`,
		formatTime(testEpoch), formatTime(testEpoch), formatTime(testEpoch),
	)
	if err == nil {
		t.Fatal("foreign key insert unexpectedly succeeded")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(path, Options{Now: clock.Now})
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	defer reopened.Close()
	if err := reopened.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatalf("count migrations after reopen: %v", err)
	}
	if migrationCount != 2 {
		t.Fatalf("migration count after reopen = %d, want 2", migrationCount)
	}
}

func TestWriterLockIsExclusiveAndReleased(t *testing.T) {
	clock := &fakeClock{now: testEpoch}
	directory := t.TempDir()
	path := filepath.Join(directory, "control.db")
	type result struct {
		store *Store
		err   error
	}
	start := make(chan struct{})
	results := make(chan result, 8)
	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			store, err := Open(path, Options{Now: clock.Now})
			results <- result{store: store, err: err}
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	var writer *Store
	writerCount := 0
	for result := range results {
		if result.err == nil {
			writerCount++
			writer = result.store
			continue
		}
		if !errors.Is(result.err, ErrWriterActive) {
			t.Fatalf("concurrent Open() error = %v, want ErrWriterActive", result.err)
		}
	}
	if writerCount != 1 {
		t.Fatalf("successful writer count = %d, want 1", writerCount)
	}
	lockInfo, err := os.Stat(path + ".lock")
	if err != nil {
		t.Fatalf("stat writer lock: %v", err)
	}
	if permissions := lockInfo.Mode().Perm(); permissions != 0o600 {
		t.Fatalf("writer lock permissions = %o, want 600", permissions)
	}

	other, err := Open(filepath.Join(directory, "other.db"), Options{Now: clock.Now})
	if err != nil {
		t.Fatalf("Open() different database error = %v", err)
	}
	if err := other.Close(); err != nil {
		t.Fatalf("close different database: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	reopened, err := Open(path, Options{Now: clock.Now})
	if err != nil {
		t.Fatalf("Open() after release error = %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("close reopened writer: %v", err)
	}
}

func TestWriterLockCanonicalizesSymlinkAliases(t *testing.T) {
	clock := &fakeClock{now: testEpoch}

	t.Run("database file symlink", func(t *testing.T) {
		directory := t.TempDir()
		realPath := filepath.Join(directory, "control.db")
		seed, err := Open(realPath, Options{Now: clock.Now})
		if err != nil {
			t.Fatalf("seed Open() error = %v", err)
		}
		if err := seed.Close(); err != nil {
			t.Fatalf("close seed: %v", err)
		}
		aliasPath := filepath.Join(directory, "control-alias.db")
		if err := os.Symlink(realPath, aliasPath); err != nil {
			t.Fatalf("create database symlink: %v", err)
		}

		writer, err := Open(realPath, Options{Now: clock.Now})
		if err != nil {
			t.Fatalf("Open() real path error = %v", err)
		}
		if alias, err := Open(aliasPath, Options{Now: clock.Now}); !errors.Is(err, ErrWriterActive) {
			if alias != nil {
				alias.Close()
			}
			writer.Close()
			t.Fatalf("Open() file alias error = %v, want ErrWriterActive", err)
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("close real-path writer: %v", err)
		}
		aliasWriter, err := Open(aliasPath, Options{Now: clock.Now})
		if err != nil {
			t.Fatalf("Open() file alias after release error = %v", err)
		}
		if err := aliasWriter.Close(); err != nil {
			t.Fatalf("close file-alias writer: %v", err)
		}
	})

	t.Run("parent directory symlink", func(t *testing.T) {
		root := t.TempDir()
		realDirectory := filepath.Join(root, "real")
		if err := os.Mkdir(realDirectory, 0o700); err != nil {
			t.Fatalf("create real directory: %v", err)
		}
		aliasDirectory := filepath.Join(root, "alias")
		if err := os.Symlink(realDirectory, aliasDirectory); err != nil {
			t.Fatalf("create parent symlink: %v", err)
		}
		realPath := filepath.Join(realDirectory, "control.db")
		aliasPath := filepath.Join(aliasDirectory, "control.db")
		seed, err := Open(realPath, Options{Now: clock.Now})
		if err != nil {
			t.Fatalf("seed Open() error = %v", err)
		}
		if err := seed.Close(); err != nil {
			t.Fatalf("close seed: %v", err)
		}

		writer, err := Open(realPath, Options{Now: clock.Now})
		if err != nil {
			t.Fatalf("Open() real parent path error = %v", err)
		}
		if alias, err := Open(aliasPath, Options{Now: clock.Now}); !errors.Is(err, ErrWriterActive) {
			if alias != nil {
				alias.Close()
			}
			writer.Close()
			t.Fatalf("Open() parent alias error = %v, want ErrWriterActive", err)
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("close real-parent writer: %v", err)
		}
		aliasWriter, err := Open(aliasPath, Options{Now: clock.Now})
		if err != nil {
			t.Fatalf("Open() parent alias after release error = %v", err)
		}
		if err := aliasWriter.Close(); err != nil {
			t.Fatalf("close parent-alias writer: %v", err)
		}
	})
}

func TestWriterLockRejectsDanglingDatabaseSymlink(t *testing.T) {
	tests := []struct {
		name       string
		linkTarget func(realPath string) string
	}{
		{
			name: "relative target",
			linkTarget: func(realPath string) string {
				return filepath.Base(realPath)
			},
		},
		{
			name: "absolute target",
			linkTarget: func(realPath string) string {
				return realPath
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clock := &fakeClock{now: testEpoch}
			directory := t.TempDir()
			realPath := filepath.Join(directory, "control.db")
			aliasPath := filepath.Join(directory, "alias.db")
			if err := os.Symlink(test.linkTarget(realPath), aliasPath); err != nil {
				t.Fatalf("create dangling database symlink: %v", err)
			}

			store, err := Open(aliasPath, Options{Now: clock.Now})
			if err == nil {
				store.Close()
				t.Fatal("Open() dangling database symlink unexpectedly succeeded")
			}
			for _, unexpectedPath := range []string{realPath, aliasPath + ".lock", realPath + ".lock"} {
				if _, statErr := os.Stat(unexpectedPath); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("failed Open() created %q: stat error = %v", unexpectedPath, statErr)
				}
			}

			realWriter, err := Open(realPath, Options{Now: clock.Now})
			if err != nil {
				t.Fatalf("Open() real path after rejected alias error = %v", err)
			}
			if err := realWriter.Close(); err != nil {
				t.Fatalf("close real-path writer: %v", err)
			}
		})
	}
}

func TestWriterLockRejectsSymlinkWithoutTouchingTarget(t *testing.T) {
	clock := &fakeClock{now: testEpoch}
	directory := t.TempDir()
	path := filepath.Join(directory, "control.db")
	sentinelPath := filepath.Join(directory, "sentinel")
	wantContent := []byte("do-not-touch")
	if err := os.WriteFile(sentinelPath, wantContent, 0o640); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	before, err := os.Stat(sentinelPath)
	if err != nil {
		t.Fatalf("stat sentinel before Open: %v", err)
	}
	lockPath := path + ".lock"
	if err := os.Symlink(sentinelPath, lockPath); err != nil {
		t.Fatalf("create malicious lock symlink: %v", err)
	}

	store, err := Open(path, Options{Now: clock.Now})
	if err == nil {
		store.Close()
		t.Fatal("Open() with symlink lock unexpectedly succeeded")
	}
	after, statErr := os.Stat(sentinelPath)
	if statErr != nil {
		t.Fatalf("stat sentinel after Open: %v", statErr)
	}
	if !os.SameFile(before, after) || before.Mode() != after.Mode() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("sentinel metadata changed: before=%+v after=%+v", before, after)
	}
	content, err := os.ReadFile(sentinelPath)
	if err != nil {
		t.Fatalf("read sentinel: %v", err)
	}
	if string(content) != string(wantContent) {
		t.Fatalf("sentinel content = %q, want %q", content, wantContent)
	}

	if err := os.Remove(lockPath); err != nil {
		t.Fatalf("remove malicious lock symlink: %v", err)
	}
	store, err = Open(path, Options{Now: clock.Now})
	if err != nil {
		t.Fatalf("Open() after removing lock symlink error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close writer after lock symlink removal: %v", err)
	}
}

func TestStepVersionAndCanonicalTimeChecks(t *testing.T) {
	store, _, _ := openTestStore(t)
	job, step := createJobWithStep(t, store, "step-version", "fetch")
	if job.Version != schemaVersion || step.Version != schemaVersion {
		t.Fatalf("versions = job %d/step %d, want %d/%d", job.Version, step.Version, schemaVersion, schemaVersion)
	}
	if _, err := store.db.Exec(`UPDATE step_runs SET version = 2 WHERE id = ?`, step.ID); err == nil {
		t.Fatal("step version CHECK unexpectedly accepted version 2")
	}
	if _, err := store.db.Exec(`UPDATE step_runs SET available_at = '2026-07-20T08:00:00Z' WHERE id = ?`, step.ID); err == nil {
		t.Fatal("canonical timestamp CHECK unexpectedly accepted non-fixed timestamp")
	}
	step = mustGetStep(t, store, step.ID)
	if step.Version != schemaVersion || len(formatTime(step.AvailableAt)) != 30 {
		t.Fatalf("stored step = %+v", step)
	}
}

func TestOpenRejectsMigrationChecksumMismatch(t *testing.T) {
	store, _, path := openTestStore(t)
	if _, err := store.db.Exec(`UPDATE schema_migrations SET checksum = 'sha256:tampered' WHERE version = 1`); err != nil {
		t.Fatalf("tamper checksum: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	_, err := Open(path, Options{})
	if err == nil {
		t.Fatal("Open() with checksum mismatch unexpectedly succeeded")
	}
	if got := err.Error(); !contains(got, "checksum mismatch") {
		t.Fatalf("Open() error = %q, want checksum mismatch", got)
	}
	_, err = Open(path, Options{})
	if errors.Is(err, ErrWriterActive) {
		t.Fatalf("Open() after initialization failure leaked writer lock: %v", err)
	}
	if err == nil || !contains(err.Error(), "checksum mismatch") {
		t.Fatalf("second Open() error = %v, want checksum mismatch", err)
	}
}

func TestJobAndStepStateTransitions(t *testing.T) {
	store, clock, _ := openTestStore(t)
	ctx := context.Background()
	job, step := createJobWithStep(t, store, "state", "fetch")
	if job.Status != JobPending || step.Status != StepPending {
		t.Fatalf("initial statuses = %s/%s, want pending/pending", job.Status, step.Status)
	}
	if _, err := store.StartStep(ctx, job.ID, step.ID, "worker", time.Minute); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("StartStep before job error = %v, want ErrInvalidTransition", err)
	}
	job, err := store.StartJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("StartJob() error = %v", err)
	}
	if job.Status != JobRunning {
		t.Fatalf("job status = %s, want running", job.Status)
	}
	if _, err := store.StartJob(ctx, job.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("second StartJob error = %v, want ErrInvalidTransition", err)
	}
	running, err := store.StartStep(ctx, job.ID, step.ID, "worker", time.Minute)
	if err != nil {
		t.Fatalf("StartStep() error = %v", err)
	}
	if running.Status != StepRunning || running.Attempt != 1 || running.LeaseToken == "" {
		t.Fatalf("running step = %+v", running)
	}
	retryAt := clock.Now().Add(5 * time.Minute)
	pending, err := store.RetryStep(ctx, step.ID, running.LeaseToken, Failure{Code: "TEMP", Message: "retry", Retryable: true}, retryAt)
	if err != nil {
		t.Fatalf("RetryStep() error = %v", err)
	}
	if pending.Status != StepPending || pending.LeaseToken != "" || pending.Failure == nil || !pending.Failure.Retryable {
		t.Fatalf("retried step = %+v, want pending with recorded failure", pending)
	}
	if _, err := store.StartStep(ctx, job.ID, step.ID, "worker", time.Minute); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("StartStep before retry due error = %v, want ErrInvalidTransition", err)
	}
	clock.Advance(5 * time.Minute)
	running, err = store.StartStep(ctx, job.ID, step.ID, "worker-2", time.Minute)
	if err != nil {
		t.Fatalf("StartStep after retry due error = %v", err)
	}
	if running.Attempt != 2 || running.Failure != nil {
		t.Fatalf("restarted step = %+v, want attempt 2 and cleared failure", running)
	}
	completed, err := store.CompleteStepAndEnqueue(ctx, step.ID, running.LeaseToken, nil)
	if err != nil {
		t.Fatalf("CompleteStepAndEnqueue() error = %v", err)
	}
	if completed.Status != StepSucceeded {
		t.Fatalf("step status = %s, want succeeded", completed.Status)
	}
	job, err = store.TrySucceedJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("TrySucceedJob() error = %v", err)
	}
	if job.Status != JobSucceeded {
		t.Fatalf("job status = %s, want succeeded", job.Status)
	}
	if _, err := store.TrySucceedJob(ctx, job.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("succeed terminal job error = %v, want ErrInvalidTransition", err)
	}
}

func TestFailureIsTerminalAndJobNeverSucceeds(t *testing.T) {
	store, _, _ := openTestStore(t)
	ctx := context.Background()
	job, step := createStartedJobWithStep(t, store, "failure", "fetch")
	running, err := store.StartStep(ctx, job.ID, step.ID, "worker", time.Minute)
	if err != nil {
		t.Fatalf("StartStep() error = %v", err)
	}
	failed, err := store.FailStepAndJob(ctx, step.ID, running.LeaseToken, Failure{Code: "PERM", Message: "failed"})
	if err != nil {
		t.Fatalf("FailStepAndJob() error = %v", err)
	}
	if failed.Status != StepFailed {
		t.Fatalf("step status = %s, want failed", failed.Status)
	}
	if _, err := store.StartStep(ctx, job.ID, step.ID, "worker-2", time.Minute); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("StartStep failed step error = %v, want ErrInvalidTransition", err)
	}
	if _, err := store.TrySucceedJob(ctx, job.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("TrySucceedJob failed job error = %v, want ErrInvalidTransition", err)
	}
	got, err := store.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if got.Status != JobFailed || got.Failure == nil || got.Failure.Code != "PERM" {
		t.Fatalf("job = %+v, want failed with PERM", got)
	}
}

func TestFailStepAndJobCancelsSiblingsAndOutbox(t *testing.T) {
	store, clock, _ := openTestStore(t)
	ctx := context.Background()
	job, steps := createStartedJobWithSteps(t, store, "failure-propagation", "failer", "sibling", "pending", "emitter")

	emitter, err := store.StartStep(ctx, job.ID, steps["emitter"].ID, "emitter-worker", time.Minute)
	if err != nil {
		t.Fatalf("start emitter: %v", err)
	}
	if _, err := store.CompleteStepAndEnqueue(ctx, emitter.ID, emitter.LeaseToken, []NewOutbox{
		{Destination: "discord", IdempotencyKey: "failure-pending", Payload: []byte(`{"n":1}`)},
		{Destination: "discord", IdempotencyKey: "failure-claimed", Payload: []byte(`{"n":2}`)},
	}); err != nil {
		t.Fatalf("complete emitter: %v", err)
	}
	claimed, err := store.ClaimOutbox(ctx, 1, "sender", time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimOutbox() = %v, %v", claimed, err)
	}

	failer, err := store.StartStep(ctx, job.ID, steps["failer"].ID, "failer-worker", time.Minute)
	if err != nil {
		t.Fatalf("start failer: %v", err)
	}
	sibling, err := store.StartStep(ctx, job.ID, steps["sibling"].ID, "sibling-worker", time.Minute)
	if err != nil {
		t.Fatalf("start sibling: %v", err)
	}
	failure := Failure{Code: "PERM", Message: "job failed"}
	failed, err := store.FailStepAndJob(ctx, failer.ID, failer.LeaseToken, failure)
	if err != nil {
		t.Fatalf("FailStepAndJob() error = %v", err)
	}
	if failed.Status != StepFailed {
		t.Fatalf("failed step status = %s, want failed", failed.Status)
	}
	for _, name := range []string{"sibling", "pending"} {
		cancelled := mustGetStep(t, store, steps[name].ID)
		if cancelled.Status != StepCancelled || cancelled.LeaseToken != "" || cancelled.LeaseUntil != nil {
			t.Fatalf("%s step after failure = %+v, want cancelled without lease", name, cancelled)
		}
		if cancelled.Failure == nil || cancelled.Failure.Code != failure.Code {
			t.Fatalf("%s step failure = %+v, want propagated failure", name, cancelled.Failure)
		}
	}
	job, err = store.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.Status != JobFailed {
		t.Fatalf("job status = %s, want failed", job.Status)
	}
	outboxes := mustListOutboxesForJob(t, store, job.ID)
	if len(outboxes) != 2 {
		t.Fatalf("outbox count = %d, want 2", len(outboxes))
	}
	for _, message := range outboxes {
		if message.Status != OutboxDead || message.LeaseToken != "" || message.LeaseUntil != nil || message.LastError != "PERM: job failed" {
			t.Fatalf("outbox after job failure = %+v", message)
		}
	}
	if _, err := store.AckOutbox(ctx, claimed[0].ID, claimed[0].LeaseToken); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("AckOutbox cancelled lease error = %v, want ErrLeaseLost", err)
	}
	if _, err := store.RetryStep(ctx, sibling.ID, sibling.LeaseToken, Failure{Code: "TEMP"}, clock.Now()); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("RetryStep cancelled sibling error = %v, want ErrInvalidTransition", err)
	}
	if _, err := store.CompleteStepAndEnqueue(ctx, sibling.ID, sibling.LeaseToken, []NewOutbox{{
		Destination: "discord", IdempotencyKey: "must-not-enqueue", Payload: []byte(`{"bad":true}`),
	}}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("CompleteStepAndEnqueue cancelled sibling error = %v, want ErrInvalidTransition", err)
	}
	var outboxCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM outbox_messages WHERE job_id = ?`, job.ID).Scan(&outboxCount); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if outboxCount != 2 {
		t.Fatalf("outbox count after rejected completion = %d, want 2", outboxCount)
	}
	if _, err := store.db.Exec(`UPDATE outbox_messages SET status = ?, available_at = ?, last_error = NULL WHERE id = ?`,
		OutboxPending, formatTime(clock.Now()), outboxes[0].ID,
	); err != nil {
		t.Fatalf("create defensive terminal-job outbox fixture: %v", err)
	}
	claimed, err = store.ClaimOutbox(ctx, 2, "sender-2", time.Minute)
	if err != nil {
		t.Fatalf("ClaimOutbox terminal job error = %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("ClaimOutbox terminal job returned %d messages", len(claimed))
	}
	if _, err := store.TrySucceedJob(ctx, job.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("TrySucceedJob failed job error = %v, want ErrInvalidTransition", err)
	}
}

func TestDeadLetterOutboxPropagatesJobFailure(t *testing.T) {
	store, _, _ := openTestStore(t)
	ctx := context.Background()
	job, steps := createStartedJobWithSteps(t, store, "dead-propagation", "emitter", "sibling")
	emitter, err := store.StartStep(ctx, job.ID, steps["emitter"].ID, "emitter-worker", time.Minute)
	if err != nil {
		t.Fatalf("start emitter: %v", err)
	}
	if _, err := store.CompleteStepAndEnqueue(ctx, emitter.ID, emitter.LeaseToken, []NewOutbox{
		{Destination: "discord", IdempotencyKey: "dead-propagation-1", Payload: []byte(`{"n":1}`)},
		{Destination: "discord", IdempotencyKey: "dead-propagation-2", Payload: []byte(`{"n":2}`)},
	}); err != nil {
		t.Fatalf("complete emitter: %v", err)
	}
	sibling, err := store.StartStep(ctx, job.ID, steps["sibling"].ID, "sibling-worker", time.Minute)
	if err != nil {
		t.Fatalf("start sibling: %v", err)
	}
	claimed, err := store.ClaimOutbox(ctx, 2, "sender", time.Minute)
	if err != nil || len(claimed) != 2 {
		t.Fatalf("ClaimOutbox() = %v, %v", claimed, err)
	}
	failure := Failure{Code: "DELIVERY", Message: "permanent rejection"}
	if _, err := store.DeadLetterOutbox(ctx, claimed[0].ID, claimed[0].LeaseToken, failure); err != nil {
		t.Fatalf("DeadLetterOutbox() error = %v", err)
	}
	job, err = store.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if job.Status != JobFailed || job.Failure == nil || job.Failure.Code != failure.Code {
		t.Fatalf("job after dead letter = %+v", job)
	}
	cancelled := mustGetStep(t, store, sibling.ID)
	if cancelled.Status != StepCancelled || cancelled.LeaseToken != "" || cancelled.LeaseUntil != nil {
		t.Fatalf("sibling after dead letter = %+v", cancelled)
	}
	outboxes := mustListOutboxesForJob(t, store, job.ID)
	for _, message := range outboxes {
		if message.Status != OutboxDead || message.LeaseToken != "" || message.LeaseUntil != nil {
			t.Fatalf("outbox after dead-letter propagation = %+v", message)
		}
	}
	if _, err := store.AckOutbox(ctx, claimed[1].ID, claimed[1].LeaseToken); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("AckOutbox propagated sibling error = %v, want ErrLeaseLost", err)
	}
	if _, err := store.CompleteStepAndEnqueue(ctx, sibling.ID, sibling.LeaseToken, nil); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("CompleteStepAndEnqueue propagated sibling error = %v, want ErrInvalidTransition", err)
	}
}

func TestSuccessGateBlocksPendingAndUnackedOutbox(t *testing.T) {
	store, _, _ := openTestStore(t)
	ctx := context.Background()
	job, step := createStartedJobWithStep(t, store, "gate", "publish")
	if _, err := store.TrySucceedJob(ctx, job.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("pending step gate error = %v, want ErrInvalidTransition", err)
	}
	running, err := store.StartStep(ctx, job.ID, step.ID, "worker", time.Minute)
	if err != nil {
		t.Fatalf("StartStep() error = %v", err)
	}
	_, err = store.CompleteStepAndEnqueue(ctx, step.ID, running.LeaseToken, []NewOutbox{{
		Destination: "discord", IdempotencyKey: "gate-message", Payload: []byte(`{"ok":true}`),
	}})
	if err != nil {
		t.Fatalf("CompleteStepAndEnqueue() error = %v", err)
	}
	if _, err := store.TrySucceedJob(ctx, job.ID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("unacked outbox gate error = %v, want ErrInvalidTransition", err)
	}
	got, err := store.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if got.Status != JobRunning {
		t.Fatalf("job status = %s, want running", got.Status)
	}
}

func TestCompleteStepAndEnqueueConflictRollsBack(t *testing.T) {
	store, _, _ := openTestStore(t)
	ctx := context.Background()
	firstJob, firstStep := createStartedJobWithStep(t, store, "first", "publish")
	firstRunning, err := store.StartStep(ctx, firstJob.ID, firstStep.ID, "worker", time.Minute)
	if err != nil {
		t.Fatalf("start first step: %v", err)
	}
	message := NewOutbox{Destination: "discord", IdempotencyKey: "same", Payload: []byte(`{"n":1}`)}
	if _, err := store.CompleteStepAndEnqueue(ctx, firstStep.ID, firstRunning.LeaseToken, []NewOutbox{message}); err != nil {
		t.Fatalf("complete first step: %v", err)
	}

	secondJob, secondStep := createStartedJobWithStep(t, store, "second", "publish")
	secondRunning, err := store.StartStep(ctx, secondJob.ID, secondStep.ID, "worker", time.Minute)
	if err != nil {
		t.Fatalf("start second step: %v", err)
	}
	if _, err := store.CompleteStepAndEnqueue(ctx, secondStep.ID, secondRunning.LeaseToken, []NewOutbox{message}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting complete error = %v, want ErrIdempotencyConflict", err)
	}
	rolledBack := mustGetStep(t, store, secondStep.ID)
	if rolledBack.Status != StepRunning || rolledBack.LeaseToken != secondRunning.LeaseToken {
		t.Fatalf("step after conflict = %+v, want original running lease", rolledBack)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM outbox_messages WHERE destination = 'discord' AND idempotency_key = 'same'`).Scan(&count); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	if count != 1 {
		t.Fatalf("outbox count = %d, want 1", count)
	}
}

func TestConcurrentInboxIdempotencyAndHashConflict(t *testing.T) {
	store, _, _ := openTestStore(t)
	ctx := context.Background()
	input := NewInbox{Producer: "news", MessageType: "article", IdempotencyKey: "item-1", Payload: []byte(`{"id":1}`)}
	type result struct {
		message InboxMessage
		created bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, 12)
	var wait sync.WaitGroup
	for range 12 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			message, created, err := store.PutInbox(ctx, input)
			results <- result{message: message, created: created, err: err}
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	createdCount := 0
	messageID := ""
	for result := range results {
		if result.err != nil {
			t.Fatalf("PutInbox() error = %v", result.err)
		}
		if result.created {
			createdCount++
		}
		if messageID == "" {
			messageID = result.message.ID
		} else if result.message.ID != messageID {
			t.Fatalf("message IDs differ: %q and %q", messageID, result.message.ID)
		}
	}
	if createdCount != 1 {
		t.Fatalf("created count = %d, want 1", createdCount)
	}
	var rowCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM inbox_messages`).Scan(&rowCount); err != nil {
		t.Fatalf("count inbox: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("inbox row count = %d, want 1", rowCount)
	}
	conflict := input
	conflict.Payload = []byte(`{"id":2}`)
	if _, _, err := store.PutInbox(ctx, conflict); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("hash conflict error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestInboxReplayIdentityAndSchedule(t *testing.T) {
	store, clock, _ := openTestStore(t)
	ctx := context.Background()
	availableAt := clock.Now().Add(10 * time.Minute)
	input := NewInbox{
		Producer: "news", MessageType: "article", IdempotencyKey: "scheduled",
		Payload: []byte(`{"id":1}`), AvailableAt: availableAt,
	}
	message, created, err := store.PutInbox(ctx, input)
	if err != nil || !created {
		t.Fatalf("PutInbox() = %+v, %v, %v", message, created, err)
	}
	zeroScheduleReplay := input
	zeroScheduleReplay.AvailableAt = time.Time{}
	replayed, created, err := store.PutInbox(ctx, zeroScheduleReplay)
	if err != nil || created || replayed.ID != message.ID || !replayed.AvailableAt.Equal(availableAt) {
		t.Fatalf("zero-schedule replay = %+v, %v, %v", replayed, created, err)
	}
	explicitSameReplay := input
	explicitSameReplay.AvailableAt = availableAt.In(time.FixedZone("UTC+8", 8*60*60))
	replayed, created, err = store.PutInbox(ctx, explicitSameReplay)
	if err != nil || created || replayed.ID != message.ID {
		t.Fatalf("explicit same-schedule replay = %+v, %v, %v", replayed, created, err)
	}
	differentType := input
	differentType.MessageType = "alert"
	if _, _, err := store.PutInbox(ctx, differentType); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("message type conflict error = %v, want ErrIdempotencyConflict", err)
	}
	differentSchedule := input
	differentSchedule.AvailableAt = availableAt.Add(time.Nanosecond)
	if _, _, err := store.PutInbox(ctx, differentSchedule); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("schedule conflict error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestInboxNackDueReclaimAndOldToken(t *testing.T) {
	store, clock, _ := openTestStore(t)
	ctx := context.Background()
	if _, _, err := store.PutInbox(ctx, NewInbox{
		Producer: "news", MessageType: "article", IdempotencyKey: "retry", Payload: []byte(`{"id":1}`),
	}); err != nil {
		t.Fatalf("PutInbox() error = %v", err)
	}
	claimed, err := store.ClaimInbox(ctx, 1, "worker-1", time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimInbox() = %v, %v", claimed, err)
	}
	firstToken := claimed[0].LeaseToken
	retryAt := clock.Now().Add(2 * time.Minute)
	if _, err := store.NackInbox(ctx, claimed[0].ID, firstToken, Failure{Code: "TEMP", Message: "later", Retryable: true}, retryAt); err != nil {
		t.Fatalf("NackInbox() error = %v", err)
	}
	claimed, err = store.ClaimInbox(ctx, 1, "worker-2", time.Minute)
	if err != nil {
		t.Fatalf("ClaimInbox before due error = %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("ClaimInbox before due returned %d messages", len(claimed))
	}
	clock.Advance(2 * time.Minute)
	claimed, err = store.ClaimInbox(ctx, 1, "worker-2", time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimInbox after due = %v, %v", claimed, err)
	}
	if claimed[0].Attempt != 2 || claimed[0].LeaseToken == firstToken {
		t.Fatalf("reclaimed inbox = %+v", claimed[0])
	}
	if _, err := store.AckInbox(ctx, claimed[0].ID, firstToken); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("old token AckInbox error = %v, want ErrLeaseLost", err)
	}
	acked, err := store.AckInbox(ctx, claimed[0].ID, claimed[0].LeaseToken)
	if err != nil {
		t.Fatalf("AckInbox current token error = %v", err)
	}
	if acked.Status != InboxProcessed {
		t.Fatalf("inbox status = %s, want processed", acked.Status)
	}
}

func TestInboxLeaseExpiresAcrossReopen(t *testing.T) {
	clock := &fakeClock{now: testEpoch}
	path := filepath.Join(t.TempDir(), "control.db")
	store, err := Open(path, Options{Now: clock.Now})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	ctx := context.Background()
	if _, _, err := store.PutInbox(ctx, NewInbox{
		Producer: "news", MessageType: "article", IdempotencyKey: "reopen", Payload: []byte(`{"id":1}`),
	}); err != nil {
		t.Fatalf("PutInbox() error = %v", err)
	}
	claimed, err := store.ClaimInbox(ctx, 1, "worker-1", time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimInbox() = %v, %v", claimed, err)
	}
	oldToken := claimed[0].LeaseToken
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	clock.Advance(time.Minute)
	store, err = Open(path, Options{Now: clock.Now})
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	defer store.Close()
	claimed, err = store.ClaimInbox(ctx, 1, "worker-2", time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimInbox after reopen = %v, %v", claimed, err)
	}
	if claimed[0].LeaseToken == oldToken || claimed[0].Attempt != 2 {
		t.Fatalf("reclaimed inbox = %+v", claimed[0])
	}
}

func TestDeadLetterInbox(t *testing.T) {
	store, clock, _ := openTestStore(t)
	ctx := context.Background()
	if _, _, err := store.PutInbox(ctx, NewInbox{
		Producer: "news", MessageType: "article", IdempotencyKey: "dead-inbox", Payload: []byte(`{"id":1}`),
	}); err != nil {
		t.Fatalf("PutInbox() error = %v", err)
	}
	claimed, err := store.ClaimInbox(ctx, 1, "worker-1", time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimInbox() = %v, %v", claimed, err)
	}
	messageID := claimed[0].ID
	firstToken := claimed[0].LeaseToken
	if _, err := store.DeadLetterInbox(ctx, messageID, "stale-token", Failure{Code: "BAD"}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("DeadLetterInbox stale token error = %v, want ErrLeaseLost", err)
	}
	clock.Advance(time.Minute)
	if _, err := store.DeadLetterInbox(ctx, messageID, firstToken, Failure{Code: "BAD"}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("DeadLetterInbox expired token error = %v, want ErrLeaseLost", err)
	}
	claimed, err = store.ClaimInbox(ctx, 1, "worker-2", time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimInbox after expiry = %v, %v", claimed, err)
	}
	if claimed[0].Attempt != 2 || claimed[0].LeaseToken == firstToken {
		t.Fatalf("reclaimed inbox = %+v", claimed[0])
	}
	activeToken := claimed[0].LeaseToken
	dead, err := store.DeadLetterInbox(ctx, messageID, activeToken, Failure{Code: "INVALID", Message: "discard"})
	if err != nil {
		t.Fatalf("DeadLetterInbox active token error = %v", err)
	}
	if dead.Status != InboxDead || dead.Attempt != 2 || dead.LastError != "INVALID: discard" {
		t.Fatalf("dead inbox = %+v", dead)
	}
	claimed, err = store.ClaimInbox(ctx, 1, "worker-3", time.Minute)
	if err != nil {
		t.Fatalf("ClaimInbox terminal message error = %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("ClaimInbox terminal message returned %d messages", len(claimed))
	}
	if _, err := store.AckInbox(ctx, messageID, activeToken); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("AckInbox terminal message error = %v, want ErrLeaseLost", err)
	}
	if _, err := store.NackInbox(ctx, messageID, activeToken, Failure{Code: "TEMP"}, clock.Now()); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("NackInbox terminal message error = %v, want ErrLeaseLost", err)
	}
}

func TestOutboxNackDueReclaimAndOldToken(t *testing.T) {
	store, clock, _ := openTestStore(t)
	ctx := context.Background()
	job, step := createStartedJobWithStep(t, store, "outbox-retry", "publish")
	running, err := store.StartStep(ctx, job.ID, step.ID, "worker", time.Minute)
	if err != nil {
		t.Fatalf("StartStep() error = %v", err)
	}
	if _, err := store.CompleteStepAndEnqueue(ctx, step.ID, running.LeaseToken, []NewOutbox{{
		Destination: "discord", IdempotencyKey: "outbox-retry", Payload: []byte(`{"id":1}`),
	}}); err != nil {
		t.Fatalf("CompleteStepAndEnqueue() error = %v", err)
	}
	claimed, err := store.ClaimOutbox(ctx, 1, "sender-1", time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimOutbox() = %v, %v", claimed, err)
	}
	messageID := claimed[0].ID
	firstToken := claimed[0].LeaseToken
	retryAt := clock.Now().Add(2 * time.Minute)
	nacked, err := store.NackOutbox(ctx, messageID, firstToken, Failure{Code: "TEMP", Message: "later", Retryable: true}, retryAt)
	if err != nil {
		t.Fatalf("NackOutbox() error = %v", err)
	}
	if nacked.Status != OutboxPending || nacked.Attempt != 1 || nacked.LastError != "TEMP: later" {
		t.Fatalf("nacked outbox = %+v", nacked)
	}
	claimed, err = store.ClaimOutbox(ctx, 1, "sender-2", time.Minute)
	if err != nil {
		t.Fatalf("ClaimOutbox before due error = %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("ClaimOutbox before due returned %d messages", len(claimed))
	}
	clock.Advance(2 * time.Minute)
	claimed, err = store.ClaimOutbox(ctx, 1, "sender-2", time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimOutbox after due = %v, %v", claimed, err)
	}
	if claimed[0].Status != OutboxClaimed || claimed[0].Attempt != 2 || claimed[0].LeaseToken == firstToken {
		t.Fatalf("reclaimed outbox = %+v", claimed[0])
	}
	if _, err := store.AckOutbox(ctx, messageID, firstToken); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("AckOutbox old token error = %v, want ErrLeaseLost", err)
	}
	acked, err := store.AckOutbox(ctx, messageID, claimed[0].LeaseToken)
	if err != nil {
		t.Fatalf("AckOutbox current token error = %v", err)
	}
	if acked.Status != OutboxAcked || acked.Attempt != 2 || acked.LastError != "" {
		t.Fatalf("acked outbox = %+v", acked)
	}
}

func TestOutboxLeaseExpiresAcrossReopen(t *testing.T) {
	clock := &fakeClock{now: testEpoch}
	path := filepath.Join(t.TempDir(), "control.db")
	store, err := Open(path, Options{Now: clock.Now})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	ctx := context.Background()
	job, step := createStartedJobWithStep(t, store, "outbox-reopen", "publish")
	running, err := store.StartStep(ctx, job.ID, step.ID, "worker", time.Minute)
	if err != nil {
		t.Fatalf("StartStep() error = %v", err)
	}
	if _, err := store.CompleteStepAndEnqueue(ctx, step.ID, running.LeaseToken, []NewOutbox{{
		Destination: "discord", IdempotencyKey: "outbox-reopen", Payload: []byte(`{"id":1}`),
	}}); err != nil {
		t.Fatalf("CompleteStepAndEnqueue() error = %v", err)
	}
	claimed, err := store.ClaimOutbox(ctx, 1, "sender-1", time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimOutbox() = %v, %v", claimed, err)
	}
	messageID := claimed[0].ID
	oldToken := claimed[0].LeaseToken
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	clock.Advance(time.Minute)
	store, err = Open(path, Options{Now: clock.Now})
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	defer store.Close()
	claimed, err = store.ClaimOutbox(ctx, 1, "sender-2", time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimOutbox after reopen = %v, %v", claimed, err)
	}
	if claimed[0].LeaseToken == oldToken || claimed[0].Attempt != 2 {
		t.Fatalf("reclaimed outbox = %+v", claimed[0])
	}
	if _, err := store.AckOutbox(ctx, messageID, oldToken); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("AckOutbox old token error = %v, want ErrLeaseLost", err)
	}
	acked, err := store.AckOutbox(ctx, messageID, claimed[0].LeaseToken)
	if err != nil {
		t.Fatalf("AckOutbox current token error = %v", err)
	}
	if acked.Status != OutboxAcked {
		t.Fatalf("outbox status = %s, want acked", acked.Status)
	}
}

func TestConcurrentOutboxClaimDoesNotDuplicate(t *testing.T) {
	store, _, _ := openTestStore(t)
	ctx := context.Background()
	job, step := createStartedJobWithStep(t, store, "outbox-claim", "publish")
	running, err := store.StartStep(ctx, job.ID, step.ID, "worker", time.Minute)
	if err != nil {
		t.Fatalf("StartStep() error = %v", err)
	}
	messages := make([]NewOutbox, 6)
	for index := range messages {
		messages[index] = NewOutbox{
			Destination: "discord", IdempotencyKey: fmt.Sprintf("claim-%d", index),
			Payload: []byte(fmt.Sprintf(`{"index":%d}`, index)),
		}
	}
	if _, err := store.CompleteStepAndEnqueue(ctx, step.ID, running.LeaseToken, messages); err != nil {
		t.Fatalf("CompleteStepAndEnqueue() error = %v", err)
	}
	start := make(chan struct{})
	results := make(chan []OutboxMessage, 2)
	errorsChannel := make(chan error, 2)
	var wait sync.WaitGroup
	for worker := range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			claimed, err := store.ClaimOutbox(ctx, 3, fmt.Sprintf("worker-%d", worker), time.Minute)
			results <- claimed
			errorsChannel <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatalf("ClaimOutbox() error = %v", err)
		}
	}
	seen := make(map[string]struct{})
	for batch := range results {
		for _, message := range batch {
			if _, ok := seen[message.ID]; ok {
				t.Fatalf("outbox %q claimed twice", message.ID)
			}
			seen[message.ID] = struct{}{}
		}
	}
	if len(seen) != len(messages) {
		t.Fatalf("claimed %d unique messages, want %d", len(seen), len(messages))
	}
}

func TestLastOutboxAckSucceedsJob(t *testing.T) {
	store, _, _ := openTestStore(t)
	ctx := context.Background()
	job, step := createStartedJobWithStep(t, store, "last-ack", "publish")
	running, err := store.StartStep(ctx, job.ID, step.ID, "worker", time.Minute)
	if err != nil {
		t.Fatalf("StartStep() error = %v", err)
	}
	if _, err := store.CompleteStepAndEnqueue(ctx, step.ID, running.LeaseToken, []NewOutbox{
		{Destination: "discord", IdempotencyKey: "last-1", Payload: []byte(`{"n":1}`)},
		{Destination: "discord", IdempotencyKey: "last-2", Payload: []byte(`{"n":2}`)},
	}); err != nil {
		t.Fatalf("CompleteStepAndEnqueue() error = %v", err)
	}
	claimed, err := store.ClaimOutbox(ctx, 2, "sender", time.Minute)
	if err != nil || len(claimed) != 2 {
		t.Fatalf("ClaimOutbox() = %v, %v", claimed, err)
	}
	if _, err := store.AckOutbox(ctx, claimed[0].ID, claimed[0].LeaseToken); err != nil {
		t.Fatalf("first AckOutbox() error = %v", err)
	}
	got, err := store.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if got.Status != JobRunning {
		t.Fatalf("job after first ack = %s, want running", got.Status)
	}
	if _, err := store.AckOutbox(ctx, claimed[1].ID, claimed[1].LeaseToken); err != nil {
		t.Fatalf("last AckOutbox() error = %v", err)
	}
	got, err = store.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if got.Status != JobSucceeded {
		t.Fatalf("job after last ack = %s, want succeeded", got.Status)
	}
}

func TestDeadLetterOutboxFailsJob(t *testing.T) {
	store, _, _ := openTestStore(t)
	ctx := context.Background()
	job, step := createStartedJobWithStep(t, store, "dead", "publish")
	running, err := store.StartStep(ctx, job.ID, step.ID, "worker", time.Minute)
	if err != nil {
		t.Fatalf("StartStep() error = %v", err)
	}
	if _, err := store.CompleteStepAndEnqueue(ctx, step.ID, running.LeaseToken, []NewOutbox{{
		Destination: "discord", IdempotencyKey: "dead", Payload: []byte(`{"n":1}`),
	}}); err != nil {
		t.Fatalf("CompleteStepAndEnqueue() error = %v", err)
	}
	claimed, err := store.ClaimOutbox(ctx, 1, "sender", time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimOutbox() = %v, %v", claimed, err)
	}
	dead, err := store.DeadLetterOutbox(ctx, claimed[0].ID, claimed[0].LeaseToken, Failure{Code: "DELIVERY", Message: "rejected"})
	if err != nil {
		t.Fatalf("DeadLetterOutbox() error = %v", err)
	}
	if dead.Status != OutboxDead {
		t.Fatalf("outbox status = %s, want dead", dead.Status)
	}
	got, err := store.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if got.Status != JobFailed || got.Failure == nil || got.Failure.Code != "DELIVERY" {
		t.Fatalf("job = %+v, want failed with DELIVERY", got)
	}
}

func TestInvalidPayloads(t *testing.T) {
	store, _, _ := openTestStore(t)
	ctx := context.Background()
	if _, _, err := store.PutInbox(ctx, NewInbox{
		Producer: "news", MessageType: "article", IdempotencyKey: "invalid", Payload: []byte(`{"broken"`),
	}); err == nil {
		t.Fatal("PutInbox invalid JSON unexpectedly succeeded")
	}
	job, step := createStartedJobWithStep(t, store, "invalid", "publish")
	running, err := store.StartStep(ctx, job.ID, step.ID, "worker", time.Minute)
	if err != nil {
		t.Fatalf("StartStep() error = %v", err)
	}
	if _, err := store.CompleteStepAndEnqueue(ctx, step.ID, running.LeaseToken, []NewOutbox{{
		Destination: "discord", IdempotencyKey: "invalid", Payload: []byte(`not-json`),
	}}); err == nil {
		t.Fatal("CompleteStepAndEnqueue invalid JSON unexpectedly succeeded")
	}
	stillRunning := mustGetStep(t, store, step.ID)
	if stillRunning.Status != StepRunning || stillRunning.LeaseToken != running.LeaseToken {
		t.Fatalf("step after invalid payload = %+v, want unchanged running", stillRunning)
	}
}

func TestCreateJobIdempotency(t *testing.T) {
	store, _, _ := openTestStore(t)
	ctx := context.Background()
	input := NewJob{Kind: "research", Producer: "api", TraceID: "trace", IdempotencyKey: "job-key"}
	job, created, err := store.CreateJob(ctx, input, []NewStep{{Name: "fetch"}, {Name: "publish"}})
	if err != nil || !created {
		t.Fatalf("CreateJob() = %+v, %v, %v", job, created, err)
	}
	replayed, created, err := store.CreateJob(ctx, input, []NewStep{{Name: "publish"}, {Name: "fetch"}})
	if err != nil || created || replayed.ID != job.ID {
		t.Fatalf("CreateJob replay = %+v, %v, %v", replayed, created, err)
	}
	conflict := input
	conflict.TraceID = "other-trace"
	if _, _, err := store.CreateJob(ctx, conflict, []NewStep{{Name: "fetch"}, {Name: "publish"}}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("trace conflict error = %v, want ErrIdempotencyConflict", err)
	}
	if _, _, err := store.CreateJob(ctx, input, []NewStep{{Name: "other"}}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("step conflict error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestConcurrentCreateJobIdempotency(t *testing.T) {
	store, _, _ := openTestStore(t)
	ctx := context.Background()
	input := NewJob{Kind: "research", Producer: "api", TraceID: "concurrent-trace", IdempotencyKey: "concurrent-job"}
	steps := []NewStep{{Name: "fetch"}, {Name: "publish"}}
	type result struct {
		job     JobRun
		created bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, 12)
	var wait sync.WaitGroup
	for range 12 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			job, created, err := store.CreateJob(ctx, input, steps)
			results <- result{job: job, created: created, err: err}
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	createdCount := 0
	jobID := ""
	for result := range results {
		if result.err != nil {
			t.Fatalf("CreateJob() error = %v", result.err)
		}
		if result.created {
			createdCount++
		}
		if jobID == "" {
			jobID = result.job.ID
		} else if result.job.ID != jobID {
			t.Fatalf("job IDs differ: %q and %q", jobID, result.job.ID)
		}
	}
	if createdCount != 1 {
		t.Fatalf("created count = %d, want 1", createdCount)
	}
	var jobCount, stepCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM job_runs`).Scan(&jobCount); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM step_runs WHERE job_id = ?`, jobID).Scan(&stepCount); err != nil {
		t.Fatalf("count steps: %v", err)
	}
	if jobCount != 1 || stepCount != 2 {
		t.Fatalf("stored rows = %d jobs/%d steps, want 1/2", jobCount, stepCount)
	}

	recovered, created, err := store.resolveConcurrentJobCreate(ctx, input, steps, errors.New("simulated unique insert race"))
	if err != nil || created || recovered.ID != jobID {
		t.Fatalf("resolveConcurrentJobCreate() = %+v, %v, %v", recovered, created, err)
	}
	conflict := input
	conflict.TraceID = "different-trace"
	if _, _, err := store.resolveConcurrentJobCreate(ctx, conflict, steps, errors.New("simulated unique insert race")); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("resolveConcurrentJobCreate conflict error = %v, want ErrIdempotencyConflict", err)
	}
}

func createJobWithStep(t *testing.T, store *Store, suffix, stepName string) (JobRun, StepRun) {
	t.Helper()
	job, created, err := store.CreateJob(context.Background(), NewJob{
		Kind: "research", Producer: "test", TraceID: "trace-" + suffix, IdempotencyKey: "job-" + suffix,
	}, []NewStep{{Name: stepName}})
	if err != nil || !created {
		t.Fatalf("CreateJob() = %+v, %v, %v", job, created, err)
	}
	return job, mustGetStepByName(t, store, job.ID, stepName)
}

func createStartedJobWithStep(t *testing.T, store *Store, suffix, stepName string) (JobRun, StepRun) {
	t.Helper()
	job, step := createJobWithStep(t, store, suffix, stepName)
	job, err := store.StartJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("StartJob() error = %v", err)
	}
	return job, step
}

func createStartedJobWithSteps(t *testing.T, store *Store, suffix string, names ...string) (JobRun, map[string]StepRun) {
	t.Helper()
	newSteps := make([]NewStep, 0, len(names))
	for _, name := range names {
		newSteps = append(newSteps, NewStep{Name: name})
	}
	job, created, err := store.CreateJob(context.Background(), NewJob{
		Kind: "research", Producer: "test", TraceID: "trace-" + suffix, IdempotencyKey: "job-" + suffix,
	}, newSteps)
	if err != nil || !created {
		t.Fatalf("CreateJob() = %+v, %v, %v", job, created, err)
	}
	job, err = store.StartJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("StartJob() error = %v", err)
	}
	steps := make(map[string]StepRun, len(names))
	for _, name := range names {
		steps[name] = mustGetStepByName(t, store, job.ID, name)
	}
	return job, steps
}

func mustGetStep(t *testing.T, store *Store, stepID string) StepRun {
	t.Helper()
	step, err := getStepByID(context.Background(), store.db, stepID)
	if err != nil {
		t.Fatalf("get step %q: %v", stepID, err)
	}
	return step
}

func mustGetStepByName(t *testing.T, store *Store, jobID, name string) StepRun {
	t.Helper()
	step, err := scanStep(store.db.QueryRow(`SELECT `+stepColumns+` FROM step_runs WHERE job_id = ? AND name = ?`, jobID, name))
	if err != nil {
		t.Fatalf("get step %q/%q: %v", jobID, name, err)
	}
	return step
}

func mustListOutboxesForJob(t *testing.T, store *Store, jobID string) []OutboxMessage {
	t.Helper()
	rows, err := store.db.Query(`SELECT `+outboxColumns+` FROM outbox_messages WHERE job_id = ? ORDER BY id`, jobID)
	if err != nil {
		t.Fatalf("list outbox for job %q: %v", jobID, err)
	}
	defer rows.Close()
	var messages []OutboxMessage
	for rows.Next() {
		message, err := scanOutbox(rows)
		if err != nil {
			t.Fatalf("scan outbox for job %q: %v", jobID, err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate outbox for job %q: %v", jobID, err)
	}
	return messages
}

func contains(value, substring string) bool {
	for index := 0; index+len(substring) <= len(value); index++ {
		if value[index:index+len(substring)] == substring {
			return true
		}
	}
	return false
}
