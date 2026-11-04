package controlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

func (s *Store) CreateJob(ctx context.Context, input NewJob, steps []NewStep) (JobRun, bool, error) {
	if err := validateNewJob(input, steps); err != nil {
		return JobRun{}, false, err
	}

	now := s.now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return JobRun{}, false, fmt.Errorf("controlstore: create job: begin transaction: %w", err)
	}
	defer tx.Rollback()

	existing, err := getJobByIdentity(ctx, tx, input.Kind, input.Producer, input.IdempotencyKey)
	if err == nil {
		if err := verifyJobReplay(ctx, tx, existing, input, steps); err != nil {
			return JobRun{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return JobRun{}, false, fmt.Errorf("controlstore: create job: commit replay: %w", err)
		}
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return JobRun{}, false, fmt.Errorf("controlstore: create job: find replay: %w", err)
	}

	jobID, err := randomID("job")
	if err != nil {
		return JobRun{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO job_runs(
			id, version, kind, producer, trace_id, idempotency_key, status, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		jobID, schemaVersion, input.Kind, input.Producer, input.TraceID, input.IdempotencyKey,
		JobPending, formatTime(now), formatTime(now),
	); err != nil {
		tx.Rollback()
		return s.resolveConcurrentJobCreate(ctx, input, steps, err)
	}
	for _, item := range steps {
		stepID, err := randomID("step")
		if err != nil {
			return JobRun{}, false, err
		}
		availableAt := item.AvailableAt
		if availableAt.IsZero() {
			availableAt = now
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO step_runs(
				id, version, job_id, name, status, attempt, available_at, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?)`,
			stepID, schemaVersion, jobID, item.Name, StepPending, formatTime(availableAt), formatTime(now), formatTime(now),
		); err != nil {
			return JobRun{}, false, fmt.Errorf("controlstore: create job %q: insert step %q: %w", jobID, item.Name, err)
		}
	}
	job, err := getJobByID(ctx, tx, jobID)
	if err != nil {
		return JobRun{}, false, fmt.Errorf("controlstore: create job %q: read inserted job: %w", jobID, err)
	}
	if err := tx.Commit(); err != nil {
		return JobRun{}, false, fmt.Errorf("controlstore: create job %q: commit: %w", jobID, err)
	}
	return job, true, nil
}

func (s *Store) resolveConcurrentJobCreate(ctx context.Context, input NewJob, steps []NewStep, insertErr error) (JobRun, bool, error) {
	existing, err := getJobByIdentity(ctx, s.db, input.Kind, input.Producer, input.IdempotencyKey)
	if err != nil {
		return JobRun{}, false, fmt.Errorf("controlstore: create job: insert: %w", insertErr)
	}
	if err := verifyJobReplay(ctx, s.db, existing, input, steps); err != nil {
		return JobRun{}, false, err
	}
	return existing, false, nil
}

func (s *Store) StartJob(ctx context.Context, jobID string) (JobRun, error) {
	if err := requireText("job id", jobID); err != nil {
		return JobRun{}, err
	}
	now := s.now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return JobRun{}, fmt.Errorf("controlstore: start job %q: begin transaction: %w", jobID, err)
	}
	defer tx.Rollback()
	job, err := getJobByID(ctx, tx, jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return JobRun{}, fmt.Errorf("%w: job %q", ErrNotFound, jobID)
	}
	if err != nil {
		return JobRun{}, fmt.Errorf("controlstore: start job %q: read: %w", jobID, err)
	}
	if job.Status != JobPending {
		return JobRun{}, fmt.Errorf("%w: job %q cannot move from %s to running", ErrInvalidTransition, jobID, job.Status)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE job_runs
		SET status = ?, started_at = ?, updated_at = ?
		WHERE id = ? AND status = ?`,
		JobRunning, formatTime(now), formatTime(now), jobID, JobPending,
	); err != nil {
		return JobRun{}, fmt.Errorf("controlstore: start job %q: update: %w", jobID, err)
	}
	job, err = getJobByID(ctx, tx, jobID)
	if err != nil {
		return JobRun{}, fmt.Errorf("controlstore: start job %q: read result: %w", jobID, err)
	}
	if err := tx.Commit(); err != nil {
		return JobRun{}, fmt.Errorf("controlstore: start job %q: commit: %w", jobID, err)
	}
	return job, nil
}

func (s *Store) StartStep(ctx context.Context, jobID, stepID, worker string, lease time.Duration) (StepRun, error) {
	if err := requireText("job id", jobID); err != nil {
		return StepRun{}, err
	}
	if err := requireText("step id", stepID); err != nil {
		return StepRun{}, err
	}
	if err := requireText("worker", worker); err != nil {
		return StepRun{}, err
	}
	if lease <= 0 {
		return StepRun{}, fmt.Errorf("controlstore: lease must be positive")
	}
	now := s.now()
	token, err := randomID("lease")
	if err != nil {
		return StepRun{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StepRun{}, fmt.Errorf("controlstore: start step %q: begin transaction: %w", stepID, err)
	}
	defer tx.Rollback()
	job, err := getJobByID(ctx, tx, jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return StepRun{}, fmt.Errorf("%w: job %q", ErrNotFound, jobID)
	}
	if err != nil {
		return StepRun{}, fmt.Errorf("controlstore: start step %q: read job: %w", stepID, err)
	}
	if job.Status != JobRunning {
		return StepRun{}, fmt.Errorf("%w: step %q requires running job, got %s", ErrInvalidTransition, stepID, job.Status)
	}
	step, err := getStepByID(ctx, tx, stepID)
	if errors.Is(err, sql.ErrNoRows) {
		return StepRun{}, fmt.Errorf("%w: step %q", ErrNotFound, stepID)
	}
	if err != nil {
		return StepRun{}, fmt.Errorf("controlstore: start step %q: read: %w", stepID, err)
	}
	if step.JobID != jobID {
		return StepRun{}, fmt.Errorf("%w: step %q does not belong to job %q", ErrNotFound, stepID, jobID)
	}
	due := step.Status == StepPending && !step.AvailableAt.After(now)
	expired := step.Status == StepRunning && step.LeaseUntil != nil && !step.LeaseUntil.After(now)
	if !due && !expired {
		return StepRun{}, fmt.Errorf("%w: step %q cannot be started from %s", ErrInvalidTransition, stepID, step.Status)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE step_runs
		SET status = ?, attempt = attempt + 1, lease_owner = ?, lease_token = ?, lease_until = ?,
			error_code = NULL, error_message = NULL, error_retryable = NULL,
			started_at = COALESCE(started_at, ?), finished_at = NULL, updated_at = ?
		WHERE id = ? AND job_id = ?
			AND EXISTS (SELECT 1 FROM job_runs WHERE id = ? AND status = ?)
			AND (
			(status = ? AND available_at <= ?)
			OR (status = ? AND lease_until <= ?)
		)`,
		StepRunning, worker, token, formatTime(now.Add(lease)), formatTime(now), formatTime(now),
		stepID, jobID, jobID, JobRunning, StepPending, formatTime(now), StepRunning, formatTime(now),
	)
	if err != nil {
		return StepRun{}, fmt.Errorf("controlstore: start step %q: update: %w", stepID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return StepRun{}, fmt.Errorf("controlstore: start step %q: rows affected: %w", stepID, err)
	}
	if affected != 1 {
		return StepRun{}, fmt.Errorf("%w: step %q start race", ErrInvalidTransition, stepID)
	}
	step, err = getStepByID(ctx, tx, stepID)
	if err != nil {
		return StepRun{}, fmt.Errorf("controlstore: start step %q: read result: %w", stepID, err)
	}
	if err := tx.Commit(); err != nil {
		return StepRun{}, fmt.Errorf("controlstore: start step %q: commit: %w", stepID, err)
	}
	return step, nil
}

func (s *Store) RetryStep(ctx context.Context, stepID, leaseToken string, failure Failure, retryAt time.Time) (StepRun, error) {
	if err := requireText("step id", stepID); err != nil {
		return StepRun{}, err
	}
	if err := requireText("lease token", leaseToken); err != nil {
		return StepRun{}, err
	}
	if retryAt.IsZero() {
		return StepRun{}, fmt.Errorf("controlstore: retry_at is required")
	}
	now := s.now()
	code, message, retryable := failureValues(failure)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StepRun{}, fmt.Errorf("controlstore: retry step %q: begin transaction: %w", stepID, err)
	}
	defer tx.Rollback()
	step, err := getStepByID(ctx, tx, stepID)
	if errors.Is(err, sql.ErrNoRows) {
		return StepRun{}, fmt.Errorf("%w: step %q", ErrNotFound, stepID)
	}
	if err != nil {
		return StepRun{}, fmt.Errorf("controlstore: retry step %q: read: %w", stepID, err)
	}
	job, err := getJobByID(ctx, tx, step.JobID)
	if err != nil {
		return StepRun{}, fmt.Errorf("controlstore: retry step %q: read job: %w", stepID, err)
	}
	if job.Status != JobRunning {
		return StepRun{}, fmt.Errorf("%w: step %q requires running job, got %s", ErrInvalidTransition, stepID, job.Status)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE step_runs
		SET status = ?, available_at = ?, lease_owner = NULL, lease_token = NULL, lease_until = NULL,
			error_code = ?, error_message = ?, error_retryable = ?, updated_at = ?
		WHERE id = ? AND status = ? AND lease_token = ? AND lease_until > ?
			AND EXISTS (SELECT 1 FROM job_runs WHERE id = step_runs.job_id AND status = ?)`,
		StepPending, formatTime(retryAt), code, message, retryable, formatTime(now),
		stepID, StepRunning, leaseToken, formatTime(now), JobRunning,
	)
	if err != nil {
		return StepRun{}, fmt.Errorf("controlstore: retry step %q: update: %w", stepID, err)
	}
	if err := requireLeaseAffected(result, "step", stepID); err != nil {
		return StepRun{}, err
	}
	step, err = getStepByID(ctx, tx, stepID)
	if err != nil {
		return StepRun{}, fmt.Errorf("controlstore: retry step %q: read result: %w", stepID, err)
	}
	if err := tx.Commit(); err != nil {
		return StepRun{}, fmt.Errorf("controlstore: retry step %q: commit: %w", stepID, err)
	}
	return step, nil
}

func (s *Store) CompleteStepAndEnqueue(ctx context.Context, stepID, leaseToken string, messages []NewOutbox) (StepRun, error) {
	if err := requireText("step id", stepID); err != nil {
		return StepRun{}, err
	}
	if err := requireText("lease token", leaseToken); err != nil {
		return StepRun{}, err
	}
	if err := validateNewOutboxes(messages); err != nil {
		return StepRun{}, err
	}
	now := s.now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StepRun{}, fmt.Errorf("controlstore: complete step %q: begin transaction: %w", stepID, err)
	}
	defer tx.Rollback()
	step, err := getStepByID(ctx, tx, stepID)
	if errors.Is(err, sql.ErrNoRows) {
		return StepRun{}, fmt.Errorf("%w: step %q", ErrNotFound, stepID)
	}
	if err != nil {
		return StepRun{}, fmt.Errorf("controlstore: complete step %q: read: %w", stepID, err)
	}
	job, err := getJobByID(ctx, tx, step.JobID)
	if err != nil {
		return StepRun{}, fmt.Errorf("controlstore: complete step %q: read job: %w", stepID, err)
	}
	if job.Status != JobRunning {
		return StepRun{}, fmt.Errorf("%w: step %q requires running job, got %s", ErrInvalidTransition, stepID, job.Status)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE step_runs
		SET status = ?, lease_owner = NULL, lease_token = NULL, lease_until = NULL,
			error_code = NULL, error_message = NULL, error_retryable = NULL,
			finished_at = ?, updated_at = ?
		WHERE id = ? AND status = ? AND lease_token = ? AND lease_until > ?
			AND EXISTS (SELECT 1 FROM job_runs WHERE id = step_runs.job_id AND status = ?)`,
		StepSucceeded, formatTime(now), formatTime(now), stepID, StepRunning, leaseToken, formatTime(now), JobRunning,
	)
	if err != nil {
		return StepRun{}, fmt.Errorf("controlstore: complete step %q: update: %w", stepID, err)
	}
	if err := requireLeaseAffected(result, "step", stepID); err != nil {
		return StepRun{}, err
	}
	for _, message := range messages {
		if _, err := insertOutboxTx(ctx, tx, step.JobID, step.ID, message, now, false); err != nil {
			return StepRun{}, fmt.Errorf("controlstore: complete step %q: %w", stepID, err)
		}
	}
	step, err = getStepByID(ctx, tx, stepID)
	if err != nil {
		return StepRun{}, fmt.Errorf("controlstore: complete step %q: read result: %w", stepID, err)
	}
	if err := tx.Commit(); err != nil {
		return StepRun{}, fmt.Errorf("controlstore: complete step %q: commit: %w", stepID, err)
	}
	return step, nil
}

func (s *Store) FailStepAndJob(ctx context.Context, stepID, leaseToken string, failure Failure) (StepRun, error) {
	if err := requireText("step id", stepID); err != nil {
		return StepRun{}, err
	}
	if err := requireText("lease token", leaseToken); err != nil {
		return StepRun{}, err
	}
	now := s.now()
	code, message, retryable := failureValues(failure)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StepRun{}, fmt.Errorf("controlstore: fail step %q: begin transaction: %w", stepID, err)
	}
	defer tx.Rollback()
	step, err := getStepByID(ctx, tx, stepID)
	if errors.Is(err, sql.ErrNoRows) {
		return StepRun{}, fmt.Errorf("%w: step %q", ErrNotFound, stepID)
	}
	if err != nil {
		return StepRun{}, fmt.Errorf("controlstore: fail step %q: read: %w", stepID, err)
	}
	job, err := getJobByID(ctx, tx, step.JobID)
	if err != nil {
		return StepRun{}, fmt.Errorf("controlstore: fail step %q: read job: %w", stepID, err)
	}
	if job.Status != JobRunning {
		return StepRun{}, fmt.Errorf("%w: job %q cannot fail from %s", ErrInvalidTransition, job.ID, job.Status)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE step_runs
		SET status = ?, lease_owner = NULL, lease_token = NULL, lease_until = NULL,
			error_code = ?, error_message = ?, error_retryable = ?, finished_at = ?, updated_at = ?
		WHERE id = ? AND status = ? AND lease_token = ? AND lease_until > ?`,
		StepFailed, code, message, retryable, formatTime(now), formatTime(now),
		stepID, StepRunning, leaseToken, formatTime(now),
	)
	if err != nil {
		return StepRun{}, fmt.Errorf("controlstore: fail step %q: update: %w", stepID, err)
	}
	if err := requireLeaseAffected(result, "step", stepID); err != nil {
		return StepRun{}, err
	}
	if err := failJobTx(ctx, tx, job.ID, failure, now); err != nil {
		return StepRun{}, fmt.Errorf("controlstore: fail step %q: %w", stepID, err)
	}
	step, err = getStepByID(ctx, tx, stepID)
	if err != nil {
		return StepRun{}, fmt.Errorf("controlstore: fail step %q: read result: %w", stepID, err)
	}
	if err := tx.Commit(); err != nil {
		return StepRun{}, fmt.Errorf("controlstore: fail step %q: commit: %w", stepID, err)
	}
	return step, nil
}

func failJobTx(ctx context.Context, tx *sql.Tx, jobID string, failure Failure, now time.Time) error {
	code, message, retryable := failureValues(failure)
	result, err := tx.ExecContext(ctx, `
		UPDATE job_runs
		SET status = ?, error_code = ?, error_message = ?, error_retryable = ?, finished_at = ?, updated_at = ?
		WHERE id = ? AND status = ?`,
		JobFailed, code, message, retryable, formatTime(now), formatTime(now), jobID, JobRunning,
	)
	if err != nil {
		return fmt.Errorf("fail job %q: %w", jobID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("fail job %q: rows affected: %w", jobID, err)
	}
	if affected != 1 {
		return fmt.Errorf("%w: job %q cannot fail unless running", ErrInvalidTransition, jobID)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE step_runs
		SET status = ?, lease_owner = NULL, lease_token = NULL, lease_until = NULL,
			error_code = ?, error_message = ?, error_retryable = ?, finished_at = ?, updated_at = ?
		WHERE job_id = ? AND status IN (?, ?)`,
		StepCancelled, code, message, retryable, formatTime(now), formatTime(now),
		jobID, StepPending, StepRunning,
	); err != nil {
		return fmt.Errorf("cancel unfinished steps for job %q: %w", jobID, err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE outbox_messages
		SET status = ?, lease_owner = NULL, lease_token = NULL, lease_until = NULL,
			last_error = ?, updated_at = ?
		WHERE job_id = ? AND status IN (?, ?)`,
		OutboxDead, failureText(failure), formatTime(now), jobID, OutboxPending, OutboxClaimed,
	); err != nil {
		return fmt.Errorf("dead-letter unfinished outbox for job %q: %w", jobID, err)
	}
	return nil
}

func (s *Store) TrySucceedJob(ctx context.Context, jobID string) (JobRun, error) {
	if err := requireText("job id", jobID); err != nil {
		return JobRun{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return JobRun{}, fmt.Errorf("controlstore: succeed job %q: begin transaction: %w", jobID, err)
	}
	defer tx.Rollback()
	changed, err := s.trySucceedJobTx(ctx, tx, jobID, true)
	if err != nil {
		return JobRun{}, err
	}
	if !changed {
		return JobRun{}, fmt.Errorf("%w: job %q has incomplete steps or outbox", ErrInvalidTransition, jobID)
	}
	job, err := getJobByID(ctx, tx, jobID)
	if err != nil {
		return JobRun{}, fmt.Errorf("controlstore: succeed job %q: read result: %w", jobID, err)
	}
	if err := tx.Commit(); err != nil {
		return JobRun{}, fmt.Errorf("controlstore: succeed job %q: commit: %w", jobID, err)
	}
	return job, nil
}

func (s *Store) GetJob(ctx context.Context, jobID string) (JobRun, error) {
	if err := requireText("job id", jobID); err != nil {
		return JobRun{}, err
	}
	job, err := getJobByID(ctx, s.db, jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return JobRun{}, fmt.Errorf("%w: job %q", ErrNotFound, jobID)
	}
	if err != nil {
		return JobRun{}, fmt.Errorf("controlstore: get job %q: %w", jobID, err)
	}
	return job, nil
}

func (s *Store) GetStepByName(ctx context.Context, jobID, name string) (StepRun, error) {
	if err := requireText("job id", jobID); err != nil {
		return StepRun{}, err
	}
	if err := requireText("step name", name); err != nil {
		return StepRun{}, err
	}
	step, err := getStepByName(ctx, s.db, jobID, name)
	if errors.Is(err, sql.ErrNoRows) {
		return StepRun{}, fmt.Errorf("%w: step %q in job %q", ErrNotFound, name, jobID)
	}
	if err != nil {
		return StepRun{}, fmt.Errorf("controlstore: get step %q in job %q: %w", name, jobID, err)
	}
	return step, nil
}

func (s *Store) trySucceedJobTx(ctx context.Context, tx *sql.Tx, jobID string, strict bool) (bool, error) {
	job, err := getJobByID(ctx, tx, jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("%w: job %q", ErrNotFound, jobID)
	}
	if err != nil {
		return false, fmt.Errorf("controlstore: succeed job %q: read: %w", jobID, err)
	}
	if job.Status != JobRunning {
		if strict {
			return false, fmt.Errorf("%w: job %q cannot succeed from %s", ErrInvalidTransition, jobID, job.Status)
		}
		return false, nil
	}
	var blockers int
	if err := tx.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM step_runs WHERE job_id = ? AND status <> ?)
			+
			(SELECT COUNT(*) FROM outbox_messages WHERE job_id = ? AND status <> ?)`,
		jobID, StepSucceeded, jobID, OutboxAcked,
	).Scan(&blockers); err != nil {
		return false, fmt.Errorf("controlstore: succeed job %q: check gate: %w", jobID, err)
	}
	if blockers != 0 {
		return false, nil
	}
	now := s.now()
	result, err := tx.ExecContext(ctx, `
		UPDATE job_runs
		SET status = ?, error_code = NULL, error_message = NULL, error_retryable = NULL,
			finished_at = ?, updated_at = ?
		WHERE id = ? AND status = ?`,
		JobSucceeded, formatTime(now), formatTime(now), jobID, JobRunning,
	)
	if err != nil {
		return false, fmt.Errorf("controlstore: succeed job %q: update: %w", jobID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("controlstore: succeed job %q: rows affected: %w", jobID, err)
	}
	return affected == 1, nil
}

func validateNewJob(input NewJob, steps []NewStep) error {
	for field, value := range map[string]string{
		"job kind": input.Kind, "job producer": input.Producer,
		"job trace id": input.TraceID, "job idempotency key": input.IdempotencyKey,
	} {
		if err := requireText(field, value); err != nil {
			return err
		}
	}
	seen := make(map[string]struct{}, len(steps))
	for _, step := range steps {
		if err := requireText("step name", step.Name); err != nil {
			return err
		}
		if _, ok := seen[step.Name]; ok {
			return fmt.Errorf("%w: duplicate step name %q", ErrIdempotencyConflict, step.Name)
		}
		seen[step.Name] = struct{}{}
	}
	return nil
}

func validateNewOutboxes(messages []NewOutbox) error {
	for _, message := range messages {
		if err := validateNewOutbox(message, false); err != nil {
			return err
		}
	}
	return nil
}

func validateNewOutbox(message NewOutbox, allowDecisionEffect bool) error {
	if err := requireText("outbox destination", message.Destination); err != nil {
		return err
	}
	if err := requireText("outbox idempotency key", message.IdempotencyKey); err != nil {
		return err
	}
	if !json.Valid(message.Payload) {
		return fmt.Errorf("controlstore: outbox payload must be valid JSON")
	}
	if err := validateOutboxEffect(message.Effect); err != nil {
		return err
	}
	if message.Effect != nil && message.Effect.DecisionID != "" && !allowDecisionEffect {
		return fmt.Errorf("controlstore: decision outbox effects are managed by CreateDecisionNotification")
	}
	return nil
}

func verifyJobReplay(ctx context.Context, queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, existing JobRun, input NewJob, steps []NewStep) error {
	if existing.TraceID != input.TraceID {
		return fmt.Errorf("%w: job key reused with a different trace id", ErrIdempotencyConflict)
	}
	existingNames, err := listStepNames(ctx, queryer, existing.ID)
	if err != nil {
		return fmt.Errorf("controlstore: verify job replay %q: %w", existing.ID, err)
	}
	wantedNames := make([]string, 0, len(steps))
	for _, step := range steps {
		wantedNames = append(wantedNames, step.Name)
	}
	sort.Strings(wantedNames)
	if len(existingNames) != len(wantedNames) {
		return fmt.Errorf("%w: job key reused with a different step set", ErrIdempotencyConflict)
	}
	for index := range existingNames {
		if existingNames[index] != wantedNames[index] {
			return fmt.Errorf("%w: job key reused with a different step set", ErrIdempotencyConflict)
		}
	}
	return nil
}

func listStepNames(ctx context.Context, queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, jobID string) ([]string, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT name FROM step_runs WHERE job_id = ? ORDER BY name`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return names, nil
}

func getJobByID(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, jobID string) (JobRun, error) {
	return scanJob(queryer.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM job_runs WHERE id = ?`, jobID))
}

func getJobByIdentity(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, kind, producer, key string) (JobRun, error) {
	return scanJob(queryer.QueryRowContext(ctx, `SELECT `+jobColumns+`
		FROM job_runs WHERE kind = ? AND producer = ? AND idempotency_key = ?`, kind, producer, key))
}

func getStepByID(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, stepID string) (StepRun, error) {
	return scanStep(queryer.QueryRowContext(ctx, `SELECT `+stepColumns+` FROM step_runs WHERE id = ?`, stepID))
}

func getStepByName(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, jobID, name string) (StepRun, error) {
	return scanStep(queryer.QueryRowContext(ctx, `SELECT `+stepColumns+`
		FROM step_runs WHERE job_id = ? AND name = ?`, jobID, name))
}

func requireLeaseAffected(result sql.Result, kind, id string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("controlstore: %s %q: rows affected: %w", kind, id, err)
	}
	if affected != 1 {
		return fmt.Errorf("%w: %s %q token is stale or expired", ErrLeaseLost, kind, id)
	}
	return nil
}
