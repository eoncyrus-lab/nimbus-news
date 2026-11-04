package alert

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/internal/controlstore"
	"github.com/Ricaardo/nimbus-os/datasources/internal/delivery"
)

type clock struct{ now time.Time }

func (clock *clock) Now() time.Time              { return clock.now }
func (clock *clock) Advance(value time.Duration) { clock.now = clock.now.Add(value) }

type sender struct{ messages []delivery.Message }

func (sender *sender) Send(_ context.Context, message delivery.Message) error {
	sender.messages = append(sender.messages, message)
	return nil
}

func setup(t *testing.T) (*Service, *controlstore.Store, *clock) {
	t.Helper()
	clock := &clock{now: time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC)}
	store, err := controlstore.Open(filepath.Join(t.TempDir(), "control.db"), controlstore.Options{Now: clock.Now})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	service, err := New(store, Options{Worker: "alert", Lease: time.Minute, Now: clock.Now})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return service, store, clock
}

func testRequest(clock *clock) Request {
	return Request{
		Event: AlertEvent{
			Version: Version, Producer: "price-monitor", Kind: "price-drop", Symbol: "US:AAPL",
			Severity: SeverityCritical, ObservedAt: clock.Now(), RetrievedAt: clock.Now(),
			Provenance: []string{"yahoo"}, TraceID: "trace-alert", Data: []byte(`{"change":"-10.0"}`),
		},
		Destination: "test", CooldownKey: "price-drop:US:AAPL", CooldownUntil: clock.Now().Add(time.Hour),
	}
}

func TestCooldownOnlyAfterAck(t *testing.T) {
	service, store, clock := setup(t)
	defer store.Close()
	ctx := context.Background()
	request := testRequest(clock)
	created, err := service.Emit(ctx, request)
	if err != nil || !created.Created || created.Job.Status != controlstore.JobRunning || created.Step.Status != controlstore.StepSucceeded {
		t.Fatalf("Emit() = %+v, %v", created, err)
	}
	if _, err := store.GetCooldown(ctx, request.CooldownKey); !errors.Is(err, controlstore.ErrNotFound) {
		t.Fatalf("cooldown before ack error = %v, want ErrNotFound", err)
	}
	replayRequest := request
	replayRequest.Event.Enrichment = []byte(`not-json-from-llm`)
	replayRequest.Event.TraceID = "trace-alert-replay"
	replayRequest.Event.RetrievedAt = replayRequest.Event.RetrievedAt.Add(time.Second)
	replayed, err := service.Emit(ctx, replayRequest)
	if err != nil || replayed.Created || replayed.Key != created.Key || replayed.Job.ID != created.Job.ID {
		t.Fatalf("enriched replay = %+v, %v", replayed, err)
	}

	sent := &sender{}
	worker, err := delivery.NewWorker(store, map[string]delivery.Sender{"test": sent}, delivery.Options{
		Worker: "delivery", BatchSize: 10, Lease: time.Minute,
		Retry: delivery.ExponentialRetry{Base: time.Minute}, Now: clock.Now,
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	delivered, err := worker.RunOnce(ctx)
	if err != nil || delivered.Acked != 1 || len(sent.messages) != 1 || sent.messages[0].IdempotencyKey != created.Key {
		t.Fatalf("delivery = %+v, messages %+v, %v", delivered, sent.messages, err)
	}
	cooldown, err := store.GetCooldown(ctx, request.CooldownKey)
	if err != nil || !cooldown.CommitUntil.Equal(request.CooldownUntil) {
		t.Fatalf("cooldown after ack = %+v, %v", cooldown, err)
	}
	job, err := store.GetJob(ctx, created.Job.ID)
	if err != nil || job.Status != controlstore.JobSucceeded {
		t.Fatalf("job after ack = %+v, %v", job, err)
	}
	suppressedRequest := request
	suppressedRequest.Event.ObservedAt = clock.Now().Add(time.Minute)
	suppressedRequest.Event.Data = []byte(`{"change":"-11.0"}`)
	suppressed, err := service.Emit(ctx, suppressedRequest)
	if err != nil || !suppressed.Suppressed {
		t.Fatalf("Emit during cooldown = %+v, %v", suppressed, err)
	}
}

func TestDeterministicKeyRejectsDuplicateFieldsTrailingAndInvalidNumbers(t *testing.T) {
	clock := &clock{now: time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC)}
	event := testRequest(clock).Event
	cases := []json.RawMessage{
		[]byte(`{"value":1,"value":2}`),
		[]byte(`{"outer":{"value":1,"value":2}}`),
		[]byte(`{"items":[{"value":1,"value":2}]}`),
		[]byte(`{} {}`),
		[]byte(`{"value":01}`),
		[]byte(`1e999`),
		[]byte(`{"value":1e999}`),
		[]byte(`[1e999]`),
	}
	for index, payload := range cases {
		event.Data = payload
		if _, err := DeterministicKey(event); err == nil {
			t.Fatalf("strict JSON case %d unexpectedly succeeded", index)
		}
	}
}

func TestRawIdentityIgnoresJSONOrderRetrievalAndEnrichment(t *testing.T) {
	clock := &clock{now: time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC)}
	left := testRequest(clock).Event
	left.Data = []byte(`{"change":"-10.0","price":"90.0"}`)
	right := left
	right.ID = "ignored"
	right.TraceID = "other-trace"
	right.RetrievedAt = right.RetrievedAt.Add(time.Second)
	right.Provenance = []string{"yahoo", "yahoo"}
	right.Data = []byte(`{"price":"90.0","change":"-10.0"}`)
	right.Enrichment = []byte(`{"summary":"anything"}`)
	leftKey, err := DeterministicKey(left)
	if err != nil {
		t.Fatalf("left DeterministicKey() error = %v", err)
	}
	rightKey, err := DeterministicKey(right)
	if err != nil {
		t.Fatalf("right DeterministicKey() error = %v", err)
	}
	if leftKey != rightKey {
		t.Fatalf("keys differ: %q and %q", leftKey, rightKey)
	}
}
