package channel

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/news/internal/model"
)

type managerTestChannel struct {
	BaseChannel
	err       error
	active    *atomic.Int32
	maxActive *atomic.Int32
}

func newManagerTestChannel(name string, err error, active, maxActive *atomic.Int32) *managerTestChannel {
	return &managerTestChannel{BaseChannel: NewBaseChannel(name, "test", ModePush), err: err, active: active, maxActive: maxActive}
}

func (channel *managerTestChannel) Send(context.Context, *model.Message) error {
	active := channel.active.Add(1)
	for {
		maximum := channel.maxActive.Load()
		if active <= maximum || channel.maxActive.CompareAndSwap(maximum, active) {
			break
		}
	}
	time.Sleep(10 * time.Millisecond)
	channel.active.Add(-1)
	return channel.err
}
func (channel *managerTestChannel) SendBatch(context.Context, []*model.Message) error {
	return channel.err
}
func (channel *managerTestChannel) Reply(context.Context, string, *model.Message) error {
	return channel.err
}
func (channel *managerTestChannel) Start(context.Context) error { return nil }
func (channel *managerTestChannel) Stop() error                 { return nil }

func TestBroadcastSendsConcurrentlyAndAggregatesErrors(t *testing.T) {
	firstErr := errors.New("first failed")
	secondErr := errors.New("second failed")
	var active, maxActive atomic.Int32
	manager := NewManager()
	manager.Add(newManagerTestChannel("first", firstErr, &active, &maxActive))
	manager.Add(newManagerTestChannel("second", secondErr, &active, &maxActive))
	manager.Add(newManagerTestChannel("success", nil, &active, &maxActive))

	err := manager.Broadcast(context.Background(), &model.Message{}, []string{"first", "second", "success"})
	if !errors.Is(err, firstErr) || !errors.Is(err, secondErr) {
		t.Fatalf("aggregated error=%v", err)
	}
	if maxActive.Load() < 2 {
		t.Fatalf("sends were not concurrent, max active=%d", maxActive.Load())
	}
}

func TestSendToPropagatesFailureAndMissingChannel(t *testing.T) {
	sendErr := errors.New("delivery failed")
	var active, maximum atomic.Int32
	manager := NewManager()
	manager.Add(newManagerTestChannel("failing", sendErr, &active, &maximum))
	if err := manager.SendTo(context.Background(), &model.Message{}, "failing"); !errors.Is(err, sendErr) {
		t.Fatalf("SendTo error=%v", err)
	}
	if err := manager.SendTo(context.Background(), &model.Message{}, "missing"); err == nil {
		t.Fatal("missing channel accepted")
	}
}

type countingIdempotentChannel struct {
	managerTestChannel
	sent atomic.Int32
	err  error
}

func (c *countingIdempotentChannel) Send(context.Context, *model.Message) error {
	c.sent.Add(1)
	return c.err
}

type memoryIdempotencyStore struct {
	keys map[string]time.Time
}

func (m *memoryIdempotencyStore) Exists(_, key string) (bool, error) {
	expire, ok := m.keys[key]
	return ok && expire.After(time.Now()), nil
}
func (m *memoryIdempotencyStore) Set(_, key string, ttl time.Duration) error {
	m.keys[key] = time.Now().Add(ttl)
	return nil
}
func (m *memoryIdempotencyStore) Delete(_, key string) error {
	delete(m.keys, key)
	return nil
}

func TestSendToIdempotentSkipsAlreadyDeliveredKey(t *testing.T) {
	var active, maxActive atomic.Int32
	ch := &countingIdempotentChannel{managerTestChannel: *newManagerTestChannel("main", nil, &active, &maxActive)}
	manager := NewManager()
	manager.Add(ch)
	manager.SetIdempotencyStore(&memoryIdempotencyStore{keys: map[string]time.Time{}})

	msg := &model.Message{ID: "m1", Content: "digest"}
	if err := manager.SendToIdempotent(context.Background(), msg, "main", "digest:d1:main"); err != nil {
		t.Fatal(err)
	}
	if err := manager.SendToIdempotent(context.Background(), msg, "main", "digest:d1:main"); err != nil {
		t.Fatal(err)
	}
	if got := ch.sent.Load(); got != 1 {
		t.Fatalf("sent=%d, want 1 (second call must be deduplicated)", got)
	}
}

func TestSendToIdempotentReleasesKeyOnFailure(t *testing.T) {
	var active, maxActive atomic.Int32
	ch := &countingIdempotentChannel{managerTestChannel: *newManagerTestChannel("main", nil, &active, &maxActive)}
	sendErr := errors.New("webhook down")
	ch.err = sendErr
	manager := NewManager()
	manager.Add(ch)
	manager.SetIdempotencyStore(&memoryIdempotencyStore{keys: map[string]time.Time{}})

	msg := &model.Message{ID: "m1"}
	if err := manager.SendToIdempotent(context.Background(), msg, "main", "digest:d2:main"); !errors.Is(err, sendErr) {
		t.Fatalf("err=%v", err)
	}
	ch.err = nil
	if err := manager.SendToIdempotent(context.Background(), msg, "main", "digest:d2:main"); err != nil {
		t.Fatalf("retry after failure must be allowed, err=%v", err)
	}
	if got := ch.sent.Load(); got != 2 {
		t.Fatalf("sent=%d, want 2", got)
	}
}

// R9：直推失败要能精确指出失败的渠道，补推队列只重投这几个，不能整条重发。
func TestBroadcastDetailedReportsFailedChannelsOnly(t *testing.T) {
	var active, maxActive atomic.Int32
	sendErr := errors.New("webhook down")
	manager := NewManager()
	manager.Add(newManagerTestChannel("good", nil, &active, &maxActive))
	manager.Add(newManagerTestChannel("bad", sendErr, &active, &maxActive))
	manager.Add(newManagerTestChannel("bad2", sendErr, &active, &maxActive))

	failed, err := manager.BroadcastDetailed(context.Background(), &model.Message{ID: "m"}, []string{"good", "bad", "bad2", "missing"})
	if !errors.Is(err, sendErr) {
		t.Fatalf("err=%v", err)
	}
	if len(failed) != 2 || failed[0] == "" {
		t.Fatalf("failed=%v, want exactly bad and bad2", failed)
	}
	seen := map[string]bool{}
	for _, name := range failed {
		seen[name] = true
	}
	if !seen["bad"] || !seen["bad2"] || seen["good"] {
		t.Fatalf("failed=%v", failed)
	}
}
