package scheduler

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/internal/controlstore"
)

type clock struct{ now time.Time }

func (clock *clock) Now() time.Time              { return clock.now }
func (clock *clock) Advance(value time.Duration) { clock.now = clock.now.Add(value) }

func openScheduler(t *testing.T) (*Scheduler, *controlstore.Store, *clock, string) {
	t.Helper()
	clock := &clock{now: time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC)}
	path := filepath.Join(t.TempDir(), "control.db")
	store, err := controlstore.Open(path, controlstore.Options{Now: clock.Now})
	if err != nil {
		t.Fatalf("controlstore.Open() error = %v", err)
	}
	scheduler, err := New(store, Options{Producer: "scheduler", Worker: "worker", Lease: time.Minute, RetryDelay: time.Minute, Now: clock.Now})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return scheduler, store, clock, path
}

func TestIdempotencyKeyUTCStable(t *testing.T) {
	instant := time.Date(2026, 7, 23, 16, 0, 0, 123, time.FixedZone("CST", 8*60*60))
	got, err := IdempotencyKey("portfolio", instant)
	if err != nil || got != "portfolio|2026-07-23T08:00:00.000000123Z" {
		t.Fatalf("IdempotencyKey() = %q, %v", got, err)
	}
}

func TestRunOnceSuccessRetryAndPermanentFailure(t *testing.T) {
	scheduler, store, clock, _ := openScheduler(t)
	defer store.Close()
	ctx := context.Background()

	success, err := scheduler.RunOnce(ctx, "success", clock.Now(), func(context.Context) ([]controlstore.NewOutbox, error) { return nil, nil })
	if err != nil || success.Job.Status != controlstore.JobSucceeded || success.Step.Status != controlstore.StepSucceeded {
		t.Fatalf("success result = %+v, %v", success, err)
	}
	runs := 0
	replayed, err := scheduler.RunOnce(ctx, "success", clock.Now(), func(context.Context) ([]controlstore.NewOutbox, error) {
		runs++
		return nil, nil
	})
	if err != nil || runs != 0 || replayed.Job.ID != success.Job.ID {
		t.Fatalf("success replay = %+v, runs %d, %v", replayed, runs, err)
	}

	retryErr := &TaskError{Code: "TEMP", Err: errors.New("dependency down"), Retryable: true}
	retry, err := scheduler.RunOnce(ctx, "retry", clock.Now(), func(context.Context) ([]controlstore.NewOutbox, error) { return nil, retryErr })
	if !errors.Is(err, retryErr) || retry.Job.Status != controlstore.JobRunning || retry.Step.Status != controlstore.StepPending || retry.Step.Failure == nil || !retry.Step.Failure.Retryable {
		t.Fatalf("retry result = %+v, %v", retry, err)
	}
	clock.Advance(time.Minute)
	retry, err = scheduler.RunOnce(ctx, "retry", clock.Now().Add(-time.Minute), func(context.Context) ([]controlstore.NewOutbox, error) { return nil, nil })
	if err != nil || retry.Job.Status != controlstore.JobSucceeded || retry.Step.Attempt != 2 {
		t.Fatalf("retry completion = %+v, %v", retry, err)
	}

	permanentErr := &TaskError{Code: "BAD_INPUT", Err: errors.New("invalid"), Retryable: false}
	failed, err := scheduler.RunOnce(ctx, "failed", clock.Now(), func(context.Context) ([]controlstore.NewOutbox, error) { return nil, permanentErr })
	if !errors.Is(err, permanentErr) || failed.Job.Status != controlstore.JobFailed || failed.Step.Status != controlstore.StepFailed {
		t.Fatalf("failed result = %+v, %v", failed, err)
	}
}

func TestRunOnceExpiredLeaseRecoveryAcrossReopen(t *testing.T) {
	scheduler, store, clock, path := openScheduler(t)
	ctx := context.Background()
	scheduledAt := clock.Now()
	created, err := scheduler.Create(ctx, "recover", scheduledAt)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	job, err := store.StartJob(ctx, created.Job.ID)
	if err != nil {
		t.Fatalf("StartJob() error = %v", err)
	}
	first, err := store.StartStep(ctx, job.ID, created.Step.ID, "crashed-worker", time.Minute)
	if err != nil {
		t.Fatalf("StartStep() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	clock.Advance(time.Minute)
	store, err = controlstore.Open(path, controlstore.Options{Now: clock.Now})
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	defer store.Close()
	scheduler, err = New(store, Options{Producer: "scheduler", Worker: "recovery-worker", Lease: time.Minute, RetryDelay: time.Minute, Now: clock.Now})
	if err != nil {
		t.Fatalf("New() after reopen error = %v", err)
	}
	recovered, err := scheduler.RunOnce(ctx, "recover", scheduledAt, func(context.Context) ([]controlstore.NewOutbox, error) { return nil, nil })
	if err != nil || recovered.Job.Status != controlstore.JobSucceeded || recovered.Step.Attempt != 2 || recovered.Step.LeaseToken != "" || first.LeaseToken == "" {
		t.Fatalf("recovered result = %+v, %v", recovered, err)
	}
}
