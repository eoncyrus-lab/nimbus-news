// Package scheduler maps deterministic schedules onto durable controlstore
// jobs and leased steps. A task result is persisted as a state transition; a
// log line is never treated as completion.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/internal/controlstore"
)

const stepName = "run"

type Options struct {
	Producer   string
	Worker     string
	Lease      time.Duration
	RetryDelay time.Duration
	Now        func() time.Time
}

type Scheduler struct {
	store      *controlstore.Store
	producer   string
	worker     string
	lease      time.Duration
	retryDelay time.Duration
	now        func() time.Time
}

// Task returns any durable deliveries that must be acknowledged before the
// job can succeed.
type Task func(context.Context) ([]controlstore.NewOutbox, error)

type RunResult struct {
	Job     controlstore.JobRun
	Step    controlstore.StepRun
	Created bool
}

type TaskError struct {
	Code      string
	Err       error
	Retryable bool
}

func (e *TaskError) Error() string {
	if e == nil || e.Err == nil {
		return e.Code
	}
	if e.Code == "" {
		return e.Err.Error()
	}
	return e.Code + ": " + e.Err.Error()
}

func (e *TaskError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func New(store *controlstore.Store, options Options) (*Scheduler, error) {
	if store == nil {
		return nil, fmt.Errorf("scheduler: control store is required")
	}
	if strings.TrimSpace(options.Producer) == "" {
		return nil, fmt.Errorf("scheduler: producer is required")
	}
	if strings.TrimSpace(options.Worker) == "" {
		return nil, fmt.Errorf("scheduler: worker is required")
	}
	if options.Lease <= 0 {
		return nil, fmt.Errorf("scheduler: lease must be positive")
	}
	if options.RetryDelay <= 0 {
		return nil, fmt.Errorf("scheduler: retry delay must be positive")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Scheduler{
		store: store, producer: options.Producer, worker: options.Worker,
		lease: options.Lease, retryDelay: options.RetryDelay, now: now,
	}, nil
}

// IdempotencyKey is stable across time zones and process restarts.
func IdempotencyKey(kind string, scheduledAt time.Time) (string, error) {
	if strings.TrimSpace(kind) == "" {
		return "", fmt.Errorf("scheduler: kind is required")
	}
	if scheduledAt.IsZero() {
		return "", fmt.Errorf("scheduler: scheduled_at is required")
	}
	return kind + "|" + scheduledAt.UTC().Format(time.RFC3339Nano), nil
}

// Create ensures the schedule has exactly one durable job and step.
func (s *Scheduler) Create(ctx context.Context, kind string, scheduledAt time.Time) (RunResult, error) {
	key, err := IdempotencyKey(kind, scheduledAt)
	if err != nil {
		return RunResult{}, err
	}
	job, created, err := s.store.CreateJob(ctx, controlstore.NewJob{
		Kind: kind, Producer: s.producer, TraceID: key, IdempotencyKey: key,
	}, []controlstore.NewStep{{Name: stepName, AvailableAt: scheduledAt.UTC()}})
	if err != nil {
		return RunResult{}, fmt.Errorf("scheduler: create %s: %w", key, err)
	}
	step, err := s.store.GetStepByName(ctx, job.ID, stepName)
	if err != nil {
		return RunResult{}, fmt.Errorf("scheduler: read step for %s: %w", key, err)
	}
	return RunResult{Job: job, Step: step, Created: created}, nil
}

// RunOnce creates or resumes a schedule and executes one leased attempt. A
// live lease is returned as ErrInvalidTransition by controlstore; after expiry
// the same call recovers the step with a fresh token.
func (s *Scheduler) RunOnce(ctx context.Context, kind string, scheduledAt time.Time, task Task) (RunResult, error) {
	if task == nil {
		return RunResult{}, fmt.Errorf("scheduler: task is required")
	}
	result, err := s.Create(ctx, kind, scheduledAt)
	if err != nil {
		return RunResult{}, err
	}
	switch result.Job.Status {
	case controlstore.JobSucceeded:
		return result, nil
	case controlstore.JobPending:
		result.Job, err = s.store.StartJob(ctx, result.Job.ID)
		if err != nil {
			return result, fmt.Errorf("scheduler: start job %q: %w", result.Job.ID, err)
		}
	case controlstore.JobRunning:
	case controlstore.JobFailed, controlstore.JobCancelled:
		return result, fmt.Errorf("%w: scheduler job %q is %s", controlstore.ErrInvalidTransition, result.Job.ID, result.Job.Status)
	default:
		return result, fmt.Errorf("scheduler: job %q has unsupported status %q", result.Job.ID, result.Job.Status)
	}

	result.Step, err = s.store.GetStepByName(ctx, result.Job.ID, stepName)
	if err != nil {
		return result, fmt.Errorf("scheduler: read step %q: %w", result.Job.ID, err)
	}
	if result.Step.Status == controlstore.StepSucceeded {
		result.Job, err = s.store.GetJob(ctx, result.Job.ID)
		return result, err
	}
	running, err := s.store.StartStep(ctx, result.Job.ID, result.Step.ID, s.worker, s.lease)
	if err != nil {
		return result, fmt.Errorf("scheduler: start step %q: %w", result.Step.ID, err)
	}
	result.Step = running

	messages, taskErr := task(ctx)
	if taskErr == nil {
		result.Step, err = s.store.CompleteStepAndEnqueue(ctx, running.ID, running.LeaseToken, messages)
		if err != nil {
			return result, fmt.Errorf("scheduler: complete step %q: %w", running.ID, err)
		}
		if len(messages) == 0 {
			result.Job, err = s.store.TrySucceedJob(ctx, result.Job.ID)
			if err != nil {
				return result, fmt.Errorf("scheduler: succeed job %q: %w", result.Job.ID, err)
			}
		} else {
			result.Job, err = s.store.GetJob(ctx, result.Job.ID)
		}
		return result, err
	}
	if errors.Is(taskErr, context.Canceled) || errors.Is(taskErr, context.DeadlineExceeded) {
		return result, taskErr
	}

	failure := failureFromTaskError(taskErr)
	var classified *TaskError
	if errors.As(taskErr, &classified) && classified.Retryable {
		result.Step, err = s.store.RetryStep(ctx, running.ID, running.LeaseToken, failure, s.now().UTC().Add(s.retryDelay))
		if err != nil {
			return result, fmt.Errorf("scheduler: retry step %q: %w", running.ID, err)
		}
		result.Job, _ = s.store.GetJob(ctx, result.Job.ID)
		return result, taskErr
	}
	result.Step, err = s.store.FailStepAndJob(ctx, running.ID, running.LeaseToken, failure)
	if err != nil {
		return result, fmt.Errorf("scheduler: fail step %q: %w", running.ID, err)
	}
	result.Job, _ = s.store.GetJob(ctx, result.Job.ID)
	return result, taskErr
}

func failureFromTaskError(err error) controlstore.Failure {
	failure := controlstore.Failure{Code: "TASK_FAILED", Message: err.Error()}
	var classified *TaskError
	if errors.As(err, &classified) {
		if classified.Code != "" {
			failure.Code = classified.Code
		}
		failure.Retryable = classified.Retryable
	}
	return failure
}
