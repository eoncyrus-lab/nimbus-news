package delivery

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/internal/controlstore"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *fakeClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *fakeClock) Advance(value time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = clock.now.Add(value)
}

type senderFunc func(context.Context, Message) error

func (function senderFunc) Send(ctx context.Context, message Message) error {
	return function(ctx, message)
}

func createOutbox(t *testing.T, store *controlstore.Store, suffix string, effect *controlstore.OutboxEffect) (controlstore.JobRun, controlstore.OutboxMessage) {
	t.Helper()
	ctx := context.Background()
	job, _, err := store.CreateJob(ctx, controlstore.NewJob{Kind: "delivery-test", Producer: "test", TraceID: suffix, IdempotencyKey: suffix}, []controlstore.NewStep{{Name: "send"}})
	if err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}
	if _, err := store.StartJob(ctx, job.ID); err != nil {
		t.Fatalf("StartJob() error = %v", err)
	}
	step, err := store.GetStepByName(ctx, job.ID, "send")
	if err != nil {
		t.Fatalf("GetStepByName() error = %v", err)
	}
	running, err := store.StartStep(ctx, job.ID, step.ID, "producer", time.Minute)
	if err != nil {
		t.Fatalf("StartStep() error = %v", err)
	}
	if _, err := store.CompleteStepAndEnqueue(ctx, step.ID, running.LeaseToken, []controlstore.NewOutbox{{
		Destination: "test", IdempotencyKey: suffix, Payload: []byte(`{"ok":true}`), Effect: effect,
	}}); err != nil {
		t.Fatalf("CompleteStepAndEnqueue() error = %v", err)
	}
	return job, controlstore.OutboxMessage{}
}

func newTestWorker(t *testing.T, store *controlstore.Store, clock *fakeClock, sender Sender) *Worker {
	t.Helper()
	worker, err := NewWorker(store, map[string]Sender{"test": sender}, Options{
		Worker: "delivery", BatchSize: 10, Lease: time.Minute,
		Retry: ExponentialRetry{Base: time.Minute, Max: 10 * time.Minute}, Now: clock.Now,
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	return worker
}

func TestDeliveryRetry(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC)}
	store, err := controlstore.Open(filepath.Join(t.TempDir(), "control.db"), controlstore.Options{Now: clock.Now})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	job, _ := createOutbox(t, store, "retry", nil)
	attempts := 0
	worker := newTestWorker(t, store, clock, senderFunc(func(context.Context, Message) error {
		attempts++
		if attempts == 1 {
			return &SendError{Code: "TEMP", Err: errors.New("down"), Retryable: true}
		}
		return nil
	}))
	result, err := worker.RunOnce(context.Background())
	if err != nil || result.Retried != 1 || attempts != 1 {
		t.Fatalf("first RunOnce() = %+v, attempts %d, %v", result, attempts, err)
	}
	result, err = worker.RunOnce(context.Background())
	if err != nil || result.Claimed != 0 {
		t.Fatalf("early RunOnce() = %+v, %v", result, err)
	}
	clock.Advance(time.Minute)
	result, err = worker.RunOnce(context.Background())
	if err != nil || result.Acked != 1 || attempts != 2 {
		t.Fatalf("retry RunOnce() = %+v, attempts %d, %v", result, attempts, err)
	}
	got, err := store.GetJob(context.Background(), job.ID)
	if err != nil || got.Status != controlstore.JobSucceeded {
		t.Fatalf("job after retry = %+v, %v", got, err)
	}
}

func TestDeliveryCrashRecovery(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC)}
	path := filepath.Join(t.TempDir(), "control.db")
	store, err := controlstore.Open(path, controlstore.Options{Now: clock.Now})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	job, _ := createOutbox(t, store, "crash", nil)
	seen := make(map[string]int)
	crashing := newTestWorker(t, store, clock, senderFunc(func(_ context.Context, message Message) error {
		seen[message.IdempotencyKey]++
		return context.Canceled
	}))
	if _, err := crashing.RunOnce(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("crashing RunOnce() error = %v, want context.Canceled", err)
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
	recovered := newTestWorker(t, store, clock, senderFunc(func(_ context.Context, message Message) error {
		seen[message.IdempotencyKey]++
		return ErrAlreadyDelivered
	}))
	result, err := recovered.RunOnce(context.Background())
	if err != nil || result.Acked != 1 || seen["crash"] != 2 {
		t.Fatalf("recovery RunOnce() = %+v, seen %+v, %v", result, seen, err)
	}
	got, err := store.GetJob(context.Background(), job.ID)
	if err != nil || got.Status != controlstore.JobSucceeded {
		t.Fatalf("job after recovery = %+v, %v", got, err)
	}
}

func TestDeliveryAckIdempotency(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC)}
	store, err := controlstore.Open(filepath.Join(t.TempDir(), "control.db"), controlstore.Options{Now: clock.Now})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	_, _ = createOutbox(t, store, "ack-idempotency", nil)
	delivered := false
	worker := newTestWorker(t, store, clock, senderFunc(func(_ context.Context, message Message) error {
		if delivered {
			return ErrAlreadyDelivered
		}
		delivered = true
		clock.Advance(time.Minute)
		return nil
	}))
	if _, err := worker.RunOnce(context.Background()); !errors.Is(err, controlstore.ErrLeaseLost) {
		t.Fatalf("first RunOnce() error = %v, want ErrLeaseLost", err)
	}
	result, err := worker.RunOnce(context.Background())
	if err != nil || result.Acked != 1 {
		t.Fatalf("idempotent replay RunOnce() = %+v, %v", result, err)
	}
}
