package controlstore

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestMigration002AppliesToExisting001DatabaseAndReopens(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "control.db")
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations() error = %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			checksum TEXT NOT NULL,
			applied_at TEXT NOT NULL
		)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	if _, err := db.ExecContext(ctx, migrations[0].contents); err != nil {
		t.Fatalf("apply migration 001: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO schema_migrations(version, checksum, applied_at) VALUES (?, ?, ?)`,
		migrations[0].version, migrations[0].checksum, formatTime(testEpoch)); err != nil {
		t.Fatalf("record migration 001: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close 001 database: %v", err)
	}

	store, err := Open(path, Options{})
	if err != nil {
		t.Fatalf("Open() upgrade error = %v", err)
	}
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count != 2 {
		t.Fatalf("migration count = %d, want 2", count)
	}
	if _, err := store.db.ExecContext(ctx, `SELECT key FROM cooldowns LIMIT 1`); err != nil {
		t.Fatalf("query W2 table: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close upgraded store: %v", err)
	}
	store, err = Open(path, Options{})
	if err != nil {
		t.Fatalf("reopen upgraded store: %v", err)
	}
	defer store.Close()
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("migration count after reopen = %d, %v", count, err)
	}
}

func TestOpenRejectsMigration002ChecksumMismatch(t *testing.T) {
	store, _, path := openTestStore(t)
	if _, err := store.db.Exec(`UPDATE schema_migrations SET checksum = 'sha256:tampered-w2' WHERE version = 2`); err != nil {
		t.Fatalf("tamper migration 002 checksum: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := Open(path, Options{}); err == nil || !contains(err.Error(), "migration 002 checksum mismatch") {
		t.Fatalf("Open() error = %v, want migration 002 checksum mismatch", err)
	}
}

func TestOutboxEffectValidationAndOrdinaryAckRejected(t *testing.T) {
	store, _, _ := openTestStore(t)
	ctx := context.Background()
	job, step := createStartedJobWithStep(t, store, "effect-validation", "publish")
	running, err := store.StartStep(ctx, job.ID, step.ID, "worker", time.Minute)
	if err != nil {
		t.Fatalf("StartStep() error = %v", err)
	}
	cases := []OutboxEffect{
		{},
		{CooldownKey: "alert"},
		{CooldownCommitUntil: testEpoch.Add(time.Hour)},
		{CooldownKey: "alert", CooldownCommitUntil: testEpoch.Add(time.Hour), DecisionID: "decision"},
		{DecisionID: "decision"},
	}
	for index, effect := range cases {
		_, err := store.CompleteStepAndEnqueue(ctx, step.ID, running.LeaseToken, []NewOutbox{{
			Destination: "discord", IdempotencyKey: "invalid-effect", Payload: []byte(`{}`), Effect: &effect,
		}})
		if err == nil {
			t.Fatalf("effect case %d unexpectedly succeeded", index)
		}
	}
	if got := mustGetStep(t, store, step.ID); got.Status != StepRunning || got.LeaseToken != running.LeaseToken {
		t.Fatalf("step after validation failures = %+v", got)
	}
}

func TestCooldownOnlyAfterAck(t *testing.T) {
	store, clock, _ := openTestStore(t)
	ctx := context.Background()
	job, step := createStartedJobWithStep(t, store, "cooldown", "publish")
	running, err := store.StartStep(ctx, job.ID, step.ID, "worker", time.Minute)
	if err != nil {
		t.Fatalf("StartStep() error = %v", err)
	}
	commitUntil := testEpoch.Add(30 * time.Minute)
	if _, err := store.CompleteStepAndEnqueue(ctx, step.ID, running.LeaseToken, []NewOutbox{{
		Destination: "discord", IdempotencyKey: "cooldown-message", Payload: []byte(`{"raw":true}`),
		Effect: &OutboxEffect{CooldownKey: "price:US:AAPL", CooldownCommitUntil: commitUntil},
	}}); err != nil {
		t.Fatalf("CompleteStepAndEnqueue() error = %v", err)
	}
	if _, err := store.GetCooldown(ctx, "price:US:AAPL"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cooldown before delivery error = %v, want ErrNotFound", err)
	}
	claimed, err := store.ClaimOutbox(ctx, 1, "sender", time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimOutbox() = %+v, %v", claimed, err)
	}
	message := claimed[0]
	if _, err := store.AckOutbox(ctx, message.ID, message.LeaseToken); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("ordinary AckOutbox error = %v, want ErrInvalidTransition", err)
	}
	if _, err := store.AckOutboxAndCloseDecision(ctx, message.ID, message.LeaseToken); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("wrong specialized ack error = %v, want ErrInvalidTransition", err)
	}
	if _, err := store.AckOutboxAndCommitCooldown(ctx, message.ID, "wrong-token"); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("wrong token error = %v, want ErrLeaseLost", err)
	}
	if _, err := store.GetCooldown(ctx, "price:US:AAPL"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cooldown after rejected acks error = %v, want ErrNotFound", err)
	}
	clock.Advance(time.Minute)
	if _, err := store.AckOutboxAndCommitCooldown(ctx, message.ID, message.LeaseToken); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired token error = %v, want ErrLeaseLost", err)
	}
	claimed, err = store.ClaimOutbox(ctx, 1, "sender-2", time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("reclaim outbox = %+v, %v", claimed, err)
	}
	acked, err := store.AckOutboxAndCommitCooldown(ctx, claimed[0].ID, claimed[0].LeaseToken)
	if err != nil || acked.Status != OutboxAcked {
		t.Fatalf("AckOutboxAndCommitCooldown() = %+v, %v", acked, err)
	}
	cooldown, err := store.GetCooldown(ctx, "price:US:AAPL")
	if err != nil || !cooldown.CommitUntil.Equal(commitUntil) {
		t.Fatalf("GetCooldown() = %+v, %v", cooldown, err)
	}
	gotJob, err := store.GetJob(ctx, job.ID)
	if err != nil || gotJob.Status != JobSucceeded {
		t.Fatalf("job after specialized ack = %+v, %v", gotJob, err)
	}
}

func TestSpecializedAckRejectsTerminalJobWithoutChangingState(t *testing.T) {
	store, _, _ := openTestStore(t)
	ctx := context.Background()
	job, claimed := createClaimedCooldownOutbox(t, store, "terminal-job", "terminal-job:cooldown")
	if _, err := store.db.ExecContext(ctx, `
		UPDATE job_runs SET status = ?, finished_at = ?, updated_at = ? WHERE id = ?`,
		JobFailed, formatTime(testEpoch), formatTime(testEpoch), job.ID,
	); err != nil {
		t.Fatalf("make job terminal: %v", err)
	}
	beforeJob, err := store.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob() before ack error = %v", err)
	}
	beforeEffect, err := getOutboxEffect(ctx, store.db, claimed.ID)
	if err != nil {
		t.Fatalf("getOutboxEffect() before ack error = %v", err)
	}

	if _, err := store.AckOutboxAndCommitCooldown(ctx, claimed.ID, claimed.LeaseToken); err == nil {
		t.Fatal("specialized ack with terminal job unexpectedly succeeded")
	}
	if _, err := store.GetCooldown(ctx, beforeEffect.CooldownKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cooldown after rejected ack error = %v, want ErrNotFound", err)
	}
	afterOutbox, err := getOutboxByID(ctx, store.db, claimed.ID)
	if err != nil {
		t.Fatalf("getOutboxByID() after ack error = %v", err)
	}
	if afterOutbox.Status != OutboxClaimed || afterOutbox.LeaseToken != claimed.LeaseToken ||
		afterOutbox.LeaseUntil == nil || !afterOutbox.LeaseUntil.Equal(*claimed.LeaseUntil) {
		t.Fatalf("outbox changed after terminal-job ack = %+v", afterOutbox)
	}
	afterEffect, err := getOutboxEffect(ctx, store.db, claimed.ID)
	if err != nil || *afterEffect != *beforeEffect {
		t.Fatalf("effect after terminal-job ack = %+v, %v; want %+v", afterEffect, err, beforeEffect)
	}
	afterJob, err := store.GetJob(ctx, job.ID)
	if err != nil || afterJob.Status != beforeJob.Status || !afterJob.UpdatedAt.Equal(beforeJob.UpdatedAt) ||
		!afterJob.FinishedAt.Equal(*beforeJob.FinishedAt) {
		t.Fatalf("job changed after rejected ack = %+v, %v; want %+v", afterJob, err, beforeJob)
	}
}

func TestSpecializedAckRollsBackWhenEffectApplicationFails(t *testing.T) {
	store, _, _ := openTestStore(t)
	ctx := context.Background()
	job, claimed := createClaimedCooldownOutbox(t, store, "effect-rollback", "effect-rollback:cooldown")
	if _, err := store.db.ExecContext(ctx, `
		CREATE TEMP TRIGGER fail_cooldown_effect
		BEFORE INSERT ON cooldowns
		BEGIN
			SELECT RAISE(ABORT, 'forced cooldown effect failure');
		END`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = store.db.Exec(`DROP TRIGGER IF EXISTS fail_cooldown_effect`)
	})

	if _, err := store.AckOutboxAndCommitCooldown(ctx, claimed.ID, claimed.LeaseToken); err == nil || !contains(err.Error(), "forced cooldown effect failure") {
		t.Fatalf("specialized ack error = %v, want forced effect failure", err)
	}
	outbox, err := getOutboxByID(ctx, store.db, claimed.ID)
	if err != nil {
		t.Fatalf("getOutboxByID() error = %v", err)
	}
	if outbox.Status != OutboxClaimed || outbox.LeaseToken != claimed.LeaseToken ||
		outbox.LeaseUntil == nil || !outbox.LeaseUntil.Equal(*claimed.LeaseUntil) {
		t.Fatalf("outbox after effect rollback = %+v, want original claimed lease", outbox)
	}
	effect, err := getOutboxEffect(ctx, store.db, claimed.ID)
	if err != nil {
		t.Fatalf("effect missing after rollback: %v", err)
	}
	if _, err := store.GetCooldown(ctx, effect.CooldownKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cooldown after effect rollback error = %v, want ErrNotFound", err)
	}
	gotJob, err := store.GetJob(ctx, job.ID)
	if err != nil || gotJob.Status != JobRunning {
		t.Fatalf("job after effect rollback = %+v, %v", gotJob, err)
	}
}

func TestCooldownEffectNackReclaimOldTokenAndDeadDoNotCommit(t *testing.T) {
	store, clock, _ := openTestStore(t)
	ctx := context.Background()
	job, claimed := createClaimedCooldownOutbox(t, store, "nack-dead", "nack-dead:cooldown")
	oldToken := claimed.LeaseToken
	retryAt := testEpoch.Add(time.Minute)
	if _, err := store.NackOutbox(ctx, claimed.ID, oldToken, Failure{Code: "TEMP", Retryable: true}, retryAt); err != nil {
		t.Fatalf("NackOutbox() error = %v", err)
	}
	if _, err := store.GetCooldown(ctx, "nack-dead:cooldown"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cooldown after nack error = %v, want ErrNotFound", err)
	}
	clock.Advance(time.Minute)
	reclaimed, err := store.ClaimOutbox(ctx, 1, "sender-2", time.Minute)
	if err != nil || len(reclaimed) != 1 {
		t.Fatalf("reclaim after nack = %+v, %v", reclaimed, err)
	}
	if reclaimed[0].LeaseToken == oldToken {
		t.Fatalf("reclaim reused old lease token %q", oldToken)
	}
	if _, err := store.AckOutboxAndCommitCooldown(ctx, claimed.ID, oldToken); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("old token specialized ack error = %v, want ErrLeaseLost", err)
	}
	if _, err := store.GetCooldown(ctx, "nack-dead:cooldown"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cooldown after old-token ack error = %v, want ErrNotFound", err)
	}
	if _, err := store.DeadLetterOutbox(ctx, reclaimed[0].ID, reclaimed[0].LeaseToken, Failure{Code: "PERMANENT"}); err != nil {
		t.Fatalf("DeadLetterOutbox() error = %v", err)
	}
	if _, err := store.GetCooldown(ctx, "nack-dead:cooldown"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cooldown after dead letter error = %v, want ErrNotFound", err)
	}
	outbox, err := getOutboxByID(ctx, store.db, claimed.ID)
	if err != nil || outbox.Status != OutboxDead {
		t.Fatalf("outbox after dead letter = %+v, %v", outbox, err)
	}
	gotJob, err := store.GetJob(ctx, job.ID)
	if err != nil || gotJob.Status != JobFailed {
		t.Fatalf("job after dead letter = %+v, %v", gotJob, err)
	}
}

func TestDecisionEffectRejectsCooldownAckWithoutAckOrClose(t *testing.T) {
	store, _, _ := openTestStore(t)
	ctx := context.Background()
	record, _, _, err := store.CreateDecisionNotification(ctx, NewDecisionNotification{
		Decision:     NewDecision{IdempotencyKey: "decision:wrong-effect", Proposal: []byte(`{}`), Settlement: []byte(`{}`)},
		Notification: NewOutbox{Destination: "discord", IdempotencyKey: "decision-notification:wrong-effect", Payload: []byte(`{}`)},
	})
	if err != nil {
		t.Fatalf("CreateDecisionNotification() error = %v", err)
	}
	claimed, err := store.ClaimOutbox(ctx, 1, "sender", time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimOutbox() = %+v, %v", claimed, err)
	}
	if _, err := store.AckOutboxAndCommitCooldown(ctx, claimed[0].ID, claimed[0].LeaseToken); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("cooldown ack for decision effect error = %v, want ErrInvalidTransition", err)
	}
	outbox, err := getOutboxByID(ctx, store.db, claimed[0].ID)
	if err != nil || outbox.Status != OutboxClaimed || outbox.LeaseToken != claimed[0].LeaseToken {
		t.Fatalf("decision outbox after wrong ack = %+v, %v", outbox, err)
	}
	gotDecision, err := store.GetDecision(ctx, record.ID)
	if err != nil || gotDecision.Status != DecisionOpen || gotDecision.ClosedAt != nil {
		t.Fatalf("decision after wrong ack = %+v, %v", gotDecision, err)
	}
	effect, err := getOutboxEffect(ctx, store.db, claimed[0].ID)
	if err != nil || effect.DecisionID != record.ID || effect.CooldownKey != "" {
		t.Fatalf("decision effect after wrong ack = %+v, %v", effect, err)
	}
}

func TestCreateDecisionNotificationRequiresRunningJobMatrix(t *testing.T) {
	statuses := []JobStatus{JobPending, JobRunning, JobSucceeded, JobFailed, JobCancelled}
	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			store, _, _ := openTestStore(t)
			ctx := context.Background()
			job, step := createJobWithStep(t, store, "decision-job-"+string(status), "decide")
			switch status {
			case JobPending:
			case JobRunning:
				if _, err := store.StartJob(ctx, job.ID); err != nil {
					t.Fatalf("StartJob() error = %v", err)
				}
				if _, err := store.StartStep(ctx, job.ID, step.ID, "worker", time.Minute); err != nil {
					t.Fatalf("StartStep() error = %v", err)
				}
			case JobSucceeded:
				if _, err := store.StartJob(ctx, job.ID); err != nil {
					t.Fatalf("StartJob() error = %v", err)
				}
				running, err := store.StartStep(ctx, job.ID, step.ID, "worker", time.Minute)
				if err != nil {
					t.Fatalf("StartStep() error = %v", err)
				}
				if _, err := store.CompleteStepAndEnqueue(ctx, step.ID, running.LeaseToken, nil); err != nil {
					t.Fatalf("CompleteStepAndEnqueue() error = %v", err)
				}
				if _, err := store.TrySucceedJob(ctx, job.ID); err != nil {
					t.Fatalf("TrySucceedJob() error = %v", err)
				}
			case JobFailed:
				if _, err := store.StartJob(ctx, job.ID); err != nil {
					t.Fatalf("StartJob() error = %v", err)
				}
				running, err := store.StartStep(ctx, job.ID, step.ID, "worker", time.Minute)
				if err != nil {
					t.Fatalf("StartStep() error = %v", err)
				}
				if _, err := store.FailStepAndJob(ctx, step.ID, running.LeaseToken, Failure{Code: "TEST"}); err != nil {
					t.Fatalf("FailStepAndJob() error = %v", err)
				}
			case JobCancelled:
				if _, err := store.db.ExecContext(ctx, `
					UPDATE job_runs SET status = ?, finished_at = ?, updated_at = ? WHERE id = ?`,
					JobCancelled, formatTime(testEpoch), formatTime(testEpoch), job.ID,
				); err != nil {
					t.Fatalf("cancel job: %v", err)
				}
			}

			input := linkedDecisionInput("job-"+string(status), job.ID, step.ID)
			_, _, created, err := store.CreateDecisionNotification(ctx, input)
			if status == JobRunning {
				if err != nil || !created {
					t.Fatalf("CreateDecisionNotification() = created %v, %v", created, err)
				}
				assertDecisionArtifactCounts(t, store, input, 1, 1, 1)
				return
			}
			if !errors.Is(err, ErrInvalidTransition) || created {
				t.Fatalf("CreateDecisionNotification() = created %v, %v; want ErrInvalidTransition", created, err)
			}
			assertDecisionArtifactCounts(t, store, input, 0, 0, 0)
		})
	}
}

func TestCreateDecisionNotificationStepStatusMatrix(t *testing.T) {
	statuses := []StepStatus{StepPending, StepRunning, StepSucceeded, StepFailed, StepCancelled}
	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			store, _, _ := openTestStore(t)
			ctx := context.Background()
			job, step := createJobWithStep(t, store, "decision-step-"+string(status), "decide")
			if _, err := store.StartJob(ctx, job.ID); err != nil {
				t.Fatalf("StartJob() error = %v", err)
			}
			switch status {
			case StepPending:
			case StepRunning:
				if _, err := store.StartStep(ctx, job.ID, step.ID, "worker", time.Minute); err != nil {
					t.Fatalf("StartStep() error = %v", err)
				}
			case StepSucceeded:
				running, err := store.StartStep(ctx, job.ID, step.ID, "worker", time.Minute)
				if err != nil {
					t.Fatalf("StartStep() error = %v", err)
				}
				if _, err := store.CompleteStepAndEnqueue(ctx, step.ID, running.LeaseToken, nil); err != nil {
					t.Fatalf("CompleteStepAndEnqueue() error = %v", err)
				}
			case StepFailed, StepCancelled:
				if _, err := store.db.ExecContext(ctx, `
					UPDATE step_runs SET status = ?, finished_at = ?, updated_at = ? WHERE id = ?`,
					status, formatTime(testEpoch), formatTime(testEpoch), step.ID,
				); err != nil {
					t.Fatalf("set step status %s: %v", status, err)
				}
			}

			input := linkedDecisionInput("step-"+string(status), job.ID, step.ID)
			_, _, created, err := store.CreateDecisionNotification(ctx, input)
			allowed := status == StepRunning || status == StepSucceeded
			if allowed {
				if err != nil || !created {
					t.Fatalf("CreateDecisionNotification() = created %v, %v", created, err)
				}
				claimed, err := store.ClaimOutbox(ctx, 1, "sender", time.Minute)
				if err != nil || len(claimed) != 1 {
					t.Fatalf("ClaimOutbox() = %+v, %v", claimed, err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidTransition) || created {
				t.Fatalf("CreateDecisionNotification() = created %v, %v; want ErrInvalidTransition", created, err)
			}
			assertDecisionArtifactCounts(t, store, input, 0, 0, 0)
		})
	}
}

func TestCloseOnlyAfterNotificationAck(t *testing.T) {
	store, clock, _ := openTestStore(t)
	ctx := context.Background()
	input := NewDecisionNotification{
		Decision:     NewDecision{IdempotencyKey: "decision:one", Proposal: []byte(`{"action":"review"}`), Settlement: []byte(`{"status":"recorded"}`)},
		Notification: NewOutbox{Destination: "discord", IdempotencyKey: "decision-notification:one", Payload: []byte(`{"decision":"one"}`)},
	}
	record, outbox, created, err := store.CreateDecisionNotification(ctx, input)
	if err != nil || !created || outbox.Effect == nil || outbox.Effect.DecisionID != record.ID {
		t.Fatalf("CreateDecisionNotification() = %+v, %+v, %v, %v", record, outbox, created, err)
	}
	replayed, replayOutbox, created, err := store.CreateDecisionNotification(ctx, input)
	if err != nil || created || replayed.ID != record.ID || replayOutbox.ID != outbox.ID {
		t.Fatalf("decision replay = %+v, %+v, %v, %v", replayed, replayOutbox, created, err)
	}
	conflict := input
	conflict.Decision.Proposal = []byte(`{"action":"ignore"}`)
	if _, _, _, err := store.CreateDecisionNotification(ctx, conflict); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("decision conflict error = %v, want ErrIdempotencyConflict", err)
	}
	claimed, err := store.ClaimOutbox(ctx, 1, "sender", time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimOutbox() = %+v, %v", claimed, err)
	}
	if _, err := store.AckOutbox(ctx, claimed[0].ID, claimed[0].LeaseToken); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("ordinary AckOutbox error = %v, want ErrInvalidTransition", err)
	}
	if _, err := store.NackOutbox(ctx, claimed[0].ID, claimed[0].LeaseToken, Failure{Code: "TEMP", Retryable: true}, testEpoch.Add(time.Minute)); err != nil {
		t.Fatalf("NackOutbox() error = %v", err)
	}
	if got, err := store.GetDecision(ctx, record.ID); err != nil || got.Status != DecisionOpen {
		t.Fatalf("decision after nack = %+v, %v", got, err)
	}
	clock.Advance(time.Minute)
	claimed, err = store.ClaimOutbox(ctx, 1, "sender-2", time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("reclaim = %+v, %v", claimed, err)
	}
	closedMessage, err := store.AckOutboxAndCloseDecision(ctx, claimed[0].ID, claimed[0].LeaseToken)
	if err != nil || closedMessage.Status != OutboxAcked {
		t.Fatalf("AckOutboxAndCloseDecision() = %+v, %v", closedMessage, err)
	}
	closed, err := store.GetDecision(ctx, record.ID)
	if err != nil || closed.Status != DecisionClosed || closed.ClosedAt == nil {
		t.Fatalf("closed decision = %+v, %v", closed, err)
	}

	deadInput := NewDecisionNotification{
		Decision:     NewDecision{IdempotencyKey: "decision:dead", Proposal: []byte(`{}`), Settlement: []byte(`{}`)},
		Notification: NewOutbox{Destination: "discord", IdempotencyKey: "decision-notification:dead", Payload: []byte(`{}`)},
	}
	deadRecord, _, _, err := store.CreateDecisionNotification(ctx, deadInput)
	if err != nil {
		t.Fatalf("create dead decision: %v", err)
	}
	claimed, err = store.ClaimOutbox(ctx, 1, "sender-3", time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim dead decision notification = %+v, %v", claimed, err)
	}
	clock.Advance(time.Minute)
	if _, err := store.AckOutboxAndCloseDecision(ctx, claimed[0].ID, claimed[0].LeaseToken); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired notification ack error = %v, want ErrLeaseLost", err)
	}
	if got, err := store.GetDecision(ctx, deadRecord.ID); err != nil || got.Status != DecisionOpen {
		t.Fatalf("decision after expired ack = %+v, %v", got, err)
	}
	claimed, err = store.ClaimOutbox(ctx, 1, "sender-4", time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("reclaim dead decision notification = %+v, %v", claimed, err)
	}
	if _, err := store.DeadLetterOutbox(ctx, claimed[0].ID, claimed[0].LeaseToken, Failure{Code: "PERMANENT"}); err != nil {
		t.Fatalf("DeadLetterOutbox() error = %v", err)
	}
	if got, err := store.GetDecision(ctx, deadRecord.ID); err != nil || got.Status != DecisionOpen {
		t.Fatalf("decision after dead letter = %+v, %v", got, err)
	}
}

func TestConcurrentDecisionNotificationReplay(t *testing.T) {
	store, _, _ := openTestStore(t)
	ctx := context.Background()
	input := NewDecisionNotification{
		Decision:     NewDecision{IdempotencyKey: "decision:concurrent", Proposal: []byte(`{"value":1}`), Settlement: []byte(`{"value":2}`)},
		Notification: NewOutbox{Destination: "discord", IdempotencyKey: "decision-notification:concurrent", Payload: []byte(`{"value":3}`)},
	}
	type result struct {
		record  DecisionRecord
		created bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, 8)
	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			record, _, created, err := store.CreateDecisionNotification(ctx, input)
			results <- result{record: record, created: created, err: err}
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	createdCount := 0
	decisionID := ""
	for item := range results {
		if item.err != nil {
			t.Fatalf("CreateDecisionNotification() error = %v", item.err)
		}
		if item.created {
			createdCount++
		}
		if decisionID == "" {
			decisionID = item.record.ID
		} else if item.record.ID != decisionID {
			t.Fatalf("decision IDs differ: %q and %q", decisionID, item.record.ID)
		}
	}
	if createdCount != 1 {
		t.Fatalf("created count = %d, want 1", createdCount)
	}
	var outboxCount, effectCount int
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM outbox_messages
		WHERE destination = ? AND idempotency_key = ?`,
		input.Notification.Destination, input.Notification.IdempotencyKey,
	).Scan(&outboxCount); err != nil {
		t.Fatalf("count concurrent outbox rows: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM outbox_effects WHERE decision_id = ?`, decisionID,
	).Scan(&effectCount); err != nil {
		t.Fatalf("count concurrent effect rows: %v", err)
	}
	if outboxCount != 1 || effectCount != 1 {
		t.Fatalf("concurrent rows = outbox %d/effect %d, want 1/1", outboxCount, effectCount)
	}
}

func createClaimedCooldownOutbox(t *testing.T, store *Store, suffix, cooldownKey string) (JobRun, OutboxMessage) {
	t.Helper()
	ctx := context.Background()
	job, step := createStartedJobWithStep(t, store, suffix, "publish")
	running, err := store.StartStep(ctx, job.ID, step.ID, "worker", time.Minute)
	if err != nil {
		t.Fatalf("StartStep() error = %v", err)
	}
	if _, err := store.CompleteStepAndEnqueue(ctx, step.ID, running.LeaseToken, []NewOutbox{{
		Destination: "discord", IdempotencyKey: suffix, Payload: []byte(`{"raw":true}`),
		Effect: &OutboxEffect{CooldownKey: cooldownKey, CooldownCommitUntil: testEpoch.Add(time.Hour)},
	}}); err != nil {
		t.Fatalf("CompleteStepAndEnqueue() error = %v", err)
	}
	claimed, err := store.ClaimOutbox(ctx, 1, "sender", time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("ClaimOutbox() = %+v, %v", claimed, err)
	}
	return job, claimed[0]
}

func linkedDecisionInput(suffix, jobID, stepID string) NewDecisionNotification {
	return NewDecisionNotification{
		Decision: NewDecision{
			IdempotencyKey: "decision:" + suffix,
			Proposal:       []byte(`{"proposal":true}`),
			Settlement:     []byte(`{"settlement":true}`),
		},
		Notification: NewOutbox{
			Destination:    "discord",
			IdempotencyKey: "decision-notification:" + suffix,
			Payload:        []byte(`{"notification":true}`),
		},
		JobID:  jobID,
		StepID: stepID,
	}
}

func assertDecisionArtifactCounts(t *testing.T, store *Store, input NewDecisionNotification, decisions, outboxes, effects int) {
	t.Helper()
	ctx := context.Background()
	var decisionCount, outboxCount, effectCount int
	if err := store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM decision_records WHERE idempotency_key = ?`, input.Decision.IdempotencyKey,
	).Scan(&decisionCount); err != nil {
		t.Fatalf("count decision records: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM outbox_messages WHERE destination = ? AND idempotency_key = ?`,
		input.Notification.Destination, input.Notification.IdempotencyKey,
	).Scan(&outboxCount); err != nil {
		t.Fatalf("count decision outbox rows: %v", err)
	}
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM outbox_effects AS e
		JOIN outbox_messages AS o ON o.id = e.outbox_id
		WHERE o.destination = ? AND o.idempotency_key = ?`,
		input.Notification.Destination, input.Notification.IdempotencyKey,
	).Scan(&effectCount); err != nil {
		t.Fatalf("count decision effect rows: %v", err)
	}
	if decisionCount != decisions || outboxCount != outboxes || effectCount != effects {
		t.Fatalf("artifact counts = decision %d/outbox %d/effect %d, want %d/%d/%d",
			decisionCount, outboxCount, effectCount, decisions, outboxes, effects)
	}
}
