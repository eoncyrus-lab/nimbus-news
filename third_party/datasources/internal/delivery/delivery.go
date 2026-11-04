// Package delivery drains the durable outbox with at-least-once semantics.
// A sender may observe the same Message more than once when a process crashes
// after Send and before Ack. Senders must therefore deduplicate on
// Message.IdempotencyKey and may return ErrAlreadyDelivered for a replay.
package delivery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/internal/controlstore"
)

const Version = 1

var ErrAlreadyDelivered = errors.New("delivery: already delivered")

type Message struct {
	Version        int
	ID             string
	Destination    string
	IdempotencyKey string
	Payload        []byte
}

type Sender interface {
	Send(context.Context, Message) error
}

type RetryPolicy interface {
	NextRetry(attempt int, now time.Time) time.Time
}

type ExponentialRetry struct {
	Base time.Duration
	Max  time.Duration
}

func (policy ExponentialRetry) NextRetry(attempt int, now time.Time) time.Time {
	delay := policy.Base
	if delay <= 0 {
		delay = time.Second
	}
	if attempt < 1 {
		attempt = 1
	}
	for count := 1; count < attempt; count++ {
		if policy.Max > 0 && delay >= policy.Max/2 {
			delay = policy.Max
			break
		}
		delay *= 2
	}
	if policy.Max > 0 && delay > policy.Max {
		delay = policy.Max
	}
	return now.UTC().Add(delay)
}

type SendError struct {
	Code      string
	Err       error
	Retryable bool
}

func (e *SendError) Error() string {
	if e == nil || e.Err == nil {
		return e.Code
	}
	if e.Code == "" {
		return e.Err.Error()
	}
	return e.Code + ": " + e.Err.Error()
}

func (e *SendError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type Options struct {
	Worker    string
	BatchSize int
	Lease     time.Duration
	Retry     RetryPolicy
	Now       func() time.Time
}

type Worker struct {
	store     *controlstore.Store
	senders   map[string]Sender
	worker    string
	batchSize int
	lease     time.Duration
	retry     RetryPolicy
	now       func() time.Time
}

type Result struct {
	Claimed int
	Acked   int
	Retried int
	Dead    int
}

func NewWorker(store *controlstore.Store, senders map[string]Sender, options Options) (*Worker, error) {
	if store == nil {
		return nil, fmt.Errorf("delivery: control store is required")
	}
	if strings.TrimSpace(options.Worker) == "" {
		return nil, fmt.Errorf("delivery: worker is required")
	}
	if options.BatchSize < 1 || options.BatchSize > 1000 {
		return nil, fmt.Errorf("delivery: batch size must be between 1 and 1000")
	}
	if options.Lease <= 0 {
		return nil, fmt.Errorf("delivery: lease must be positive")
	}
	if options.Retry == nil {
		return nil, fmt.Errorf("delivery: retry policy is required")
	}
	if len(senders) == 0 {
		return nil, fmt.Errorf("delivery: at least one sender is required")
	}
	copySenders := make(map[string]Sender, len(senders))
	for destination, sender := range senders {
		if strings.TrimSpace(destination) == "" || sender == nil {
			return nil, fmt.Errorf("delivery: sender destinations and values are required")
		}
		copySenders[destination] = sender
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Worker{
		store: store, senders: copySenders, worker: options.Worker,
		batchSize: options.BatchSize, lease: options.Lease, retry: options.Retry, now: now,
	}, nil
}

// RunOnce claims at most BatchSize messages. If ctx is cancelled during Send,
// the lease is left claimed for crash-style recovery rather than recording a
// delivery failure that did not necessarily occur.
func (worker *Worker) RunOnce(ctx context.Context) (Result, error) {
	messages, err := worker.store.ClaimOutbox(ctx, worker.batchSize, worker.worker, worker.lease)
	if err != nil {
		return Result{}, fmt.Errorf("delivery: claim outbox: %w", err)
	}
	result := Result{Claimed: len(messages)}
	for _, outbox := range messages {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		sender, ok := worker.senders[outbox.Destination]
		if !ok {
			if err := worker.dead(ctx, outbox, &SendError{Code: "NO_SENDER", Err: fmt.Errorf("no sender for %q", outbox.Destination)}); err != nil {
				return result, err
			}
			result.Dead++
			continue
		}
		message := Message{
			Version: Version, ID: outbox.ID, Destination: outbox.Destination,
			IdempotencyKey: outbox.IdempotencyKey, Payload: append([]byte(nil), outbox.Payload...),
		}
		sendErr := sender.Send(ctx, message)
		if sendErr == nil || errors.Is(sendErr, ErrAlreadyDelivered) {
			if err := worker.ack(ctx, outbox); err != nil {
				return result, err
			}
			result.Acked++
			continue
		}
		if errors.Is(sendErr, context.Canceled) || errors.Is(sendErr, context.DeadlineExceeded) || ctx.Err() != nil {
			return result, sendErr
		}
		var classified *SendError
		if errors.As(sendErr, &classified) && classified.Retryable {
			failure := controlstore.Failure{Code: classified.Code, Message: sendErr.Error(), Retryable: true}
			retryAt := worker.retry.NextRetry(outbox.Attempt, worker.now().UTC())
			if _, err := worker.store.NackOutbox(ctx, outbox.ID, outbox.LeaseToken, failure, retryAt); err != nil {
				return result, fmt.Errorf("delivery: nack outbox %q: %w", outbox.ID, err)
			}
			result.Retried++
			continue
		}
		if err := worker.dead(ctx, outbox, sendErr); err != nil {
			return result, err
		}
		result.Dead++
	}
	return result, nil
}

func (worker *Worker) ack(ctx context.Context, message controlstore.OutboxMessage) error {
	var err error
	switch {
	case message.Effect == nil:
		_, err = worker.store.AckOutbox(ctx, message.ID, message.LeaseToken)
	case message.Effect.DecisionID != "":
		_, err = worker.store.AckOutboxAndCloseDecision(ctx, message.ID, message.LeaseToken)
	case message.Effect.CooldownKey != "":
		_, err = worker.store.AckOutboxAndCommitCooldown(ctx, message.ID, message.LeaseToken)
	default:
		err = fmt.Errorf("%w: outbox %q has unsupported effect", controlstore.ErrInvalidTransition, message.ID)
	}
	if err != nil {
		return fmt.Errorf("delivery: ack outbox %q: %w", message.ID, err)
	}
	return nil
}

func (worker *Worker) dead(ctx context.Context, message controlstore.OutboxMessage, sendErr error) error {
	failure := controlstore.Failure{Code: "DELIVERY_FAILED", Message: sendErr.Error()}
	var classified *SendError
	if errors.As(sendErr, &classified) && classified.Code != "" {
		failure.Code = classified.Code
	}
	if _, err := worker.store.DeadLetterOutbox(ctx, message.ID, message.LeaseToken, failure); err != nil {
		return fmt.Errorf("delivery: dead-letter outbox %q: %w", message.ID, err)
	}
	return nil
}
