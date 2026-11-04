package decision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/internal/controlstore"
	"github.com/Ricaardo/nimbus-os/datasources/internal/delivery"
)

type clock struct{ now time.Time }

func (clock *clock) Now() time.Time { return clock.now }

type senderFunc func(context.Context, delivery.Message) error

func (function senderFunc) Send(ctx context.Context, message delivery.Message) error {
	return function(ctx, message)
}

func setup(t *testing.T) (*Service, *controlstore.Store, *clock) {
	t.Helper()
	clock := &clock{now: time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC)}
	store, err := controlstore.Open(filepath.Join(t.TempDir(), "control.db"), controlstore.Options{Now: clock.Now})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	service, err := New(store)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return service, store, clock
}

func request(clock *clock, id string) Request {
	return Request{
		Proposal: RecommendationProposal{
			Version: Version, ID: id, Producer: "research", Symbol: "US:AAPL",
			Recommendation: "review", AsOf: clock.Now(), Provenance: []string{"fundamentals"},
			TraceID: "trace-" + id, Payload: []byte(`{"score":0.8}`),
		},
		Settlement: Settlement{
			Version: Version, ProposalID: id, Outcome: OutcomeNeedsReview, SettledAt: clock.Now(),
			Reasons: []string{"human approval required"}, Payload: []byte(`{"policy":"manual"}`),
		},
		Destination: "test",
	}
}

func worker(t *testing.T, store *controlstore.Store, clock *clock, sender delivery.Sender) *delivery.Worker {
	t.Helper()
	worker, err := delivery.NewWorker(store, map[string]delivery.Sender{"test": sender}, delivery.Options{
		Worker: "delivery", BatchSize: 1, Lease: time.Minute,
		Retry: delivery.ExponentialRetry{Base: time.Minute}, Now: clock.Now,
	})
	if err != nil {
		t.Fatalf("NewWorker() error = %v", err)
	}
	return worker
}

func TestCloseOnlyAfterNotificationAck(t *testing.T) {
	service, store, clock := setup(t)
	defer store.Close()
	ctx := context.Background()
	created, err := service.Create(ctx, request(clock, "proposal-1"))
	if err != nil || !created.Created || created.Record.Status != controlstore.DecisionOpen || created.Outbox.Effect == nil {
		t.Fatalf("Create() = %+v, %v", created, err)
	}
	replayed, err := service.Create(ctx, request(clock, "proposal-1"))
	if err != nil || replayed.Created || replayed.Record.ID != created.Record.ID {
		t.Fatalf("Create() replay = %+v, %v", replayed, err)
	}
	failedWorker := worker(t, store, clock, senderFunc(func(context.Context, delivery.Message) error {
		return &delivery.SendError{Code: "REJECTED", Err: errors.New("bad destination")}
	}))
	result, err := failedWorker.RunOnce(ctx)
	if err != nil || result.Dead != 1 {
		t.Fatalf("failed delivery = %+v, %v", result, err)
	}
	open, err := store.GetDecision(ctx, created.Record.ID)
	if err != nil || open.Status != controlstore.DecisionOpen {
		t.Fatalf("decision after dead letter = %+v, %v", open, err)
	}

	second, err := service.Create(ctx, request(clock, "proposal-2"))
	if err != nil {
		t.Fatalf("create second decision: %v", err)
	}
	successWorker := worker(t, store, clock, senderFunc(func(context.Context, delivery.Message) error { return nil }))
	result, err = successWorker.RunOnce(ctx)
	if err != nil || result.Acked != 1 {
		t.Fatalf("successful delivery = %+v, %v", result, err)
	}
	closed, err := store.GetDecision(ctx, second.Record.ID)
	if err != nil || closed.Status != controlstore.DecisionClosed || closed.ClosedAt == nil {
		t.Fatalf("decision after ack = %+v, %v", closed, err)
	}
}

func TestValidatedProposalSettlementAndReplayConflict(t *testing.T) {
	service, store, clock := setup(t)
	defer store.Close()
	ctx := context.Background()
	invalid := request(clock, "invalid")
	invalid.Proposal.Symbol = "AAPL"
	if _, err := service.Create(ctx, invalid); err == nil {
		t.Fatal("non-canonical symbol unexpectedly succeeded")
	}
	invalid = request(clock, "invalid")
	invalid.Settlement.ProposalID = "other"
	if _, err := service.Create(ctx, invalid); err == nil {
		t.Fatal("mismatched settlement unexpectedly succeeded")
	}
	created, err := service.Create(ctx, request(clock, "conflict"))
	if err != nil || !created.Created {
		t.Fatalf("Create() = %+v, %v", created, err)
	}
	conflict := request(clock, "conflict")
	conflict.Proposal.Payload = []byte(`{"score":0.9}`)
	if _, err := service.Create(ctx, conflict); !errors.Is(err, controlstore.ErrIdempotencyConflict) {
		t.Fatalf("content conflict error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestCreateRejectsDuplicateFieldsTrailingAndInvalidNumbers(t *testing.T) {
	service, store, clock := setup(t)
	defer store.Close()
	ctx := context.Background()
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
		proposalRequest := request(clock, fmt.Sprintf("strict-proposal-%d", index))
		proposalRequest.Proposal.Payload = payload
		if _, err := service.Create(ctx, proposalRequest); err == nil {
			t.Fatalf("proposal strict JSON case %d unexpectedly succeeded", index)
		}
		settlementRequest := request(clock, fmt.Sprintf("strict-settlement-%d", index))
		settlementRequest.Settlement.Payload = payload
		if _, err := service.Create(ctx, settlementRequest); err == nil {
			t.Fatalf("settlement strict JSON case %d unexpectedly succeeded", index)
		}
	}
}
