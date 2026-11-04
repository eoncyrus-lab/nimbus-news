package controlstore

import (
	"database/sql"
	"fmt"
	"time"
)

const jobColumns = `
	id, version, kind, producer, trace_id, idempotency_key, status,
	error_code, error_message, error_retryable,
	created_at, started_at, finished_at, updated_at`

const stepColumns = `
	id, version, job_id, name, status, attempt, available_at,
	lease_owner, lease_token, lease_until,
	error_code, error_message, error_retryable,
	created_at, started_at, finished_at, updated_at`

const inboxColumns = `
	id, version, producer, message_type, idempotency_key, payload, payload_hash,
	status, lease_owner, lease_token, lease_until, attempt, available_at,
	last_error, created_at, updated_at`

const outboxColumns = `
	id, job_id, step_id, version, destination, idempotency_key, payload, payload_hash,
	status, lease_owner, lease_token, lease_until, attempt, available_at,
	last_error, created_at, updated_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanJob(row rowScanner) (JobRun, error) {
	var job JobRun
	var status string
	var errorCode, errorMessage, createdAt, startedAt, finishedAt, updatedAt sql.NullString
	var errorRetryable sql.NullInt64
	if err := row.Scan(
		&job.ID, &job.Version, &job.Kind, &job.Producer, &job.TraceID, &job.IdempotencyKey, &status,
		&errorCode, &errorMessage, &errorRetryable,
		&createdAt, &startedAt, &finishedAt, &updatedAt,
	); err != nil {
		return JobRun{}, err
	}
	job.Status = JobStatus(status)
	var err error
	if job.CreatedAt, err = parseRequiredTime("job created_at", createdAt); err != nil {
		return JobRun{}, err
	}
	if job.StartedAt, err = parseOptionalTime("job started_at", startedAt); err != nil {
		return JobRun{}, err
	}
	if job.FinishedAt, err = parseOptionalTime("job finished_at", finishedAt); err != nil {
		return JobRun{}, err
	}
	if job.UpdatedAt, err = parseRequiredTime("job updated_at", updatedAt); err != nil {
		return JobRun{}, err
	}
	job.Failure = scanFailure(errorCode, errorMessage, errorRetryable)
	return job, nil
}

func scanStep(row rowScanner) (StepRun, error) {
	var step StepRun
	var status string
	var availableAt, createdAt, startedAt, finishedAt, updatedAt sql.NullString
	var leaseOwner, leaseToken, leaseUntil sql.NullString
	var errorCode, errorMessage sql.NullString
	var errorRetryable sql.NullInt64
	if err := row.Scan(
		&step.ID, &step.Version, &step.JobID, &step.Name, &status, &step.Attempt, &availableAt,
		&leaseOwner, &leaseToken, &leaseUntil,
		&errorCode, &errorMessage, &errorRetryable,
		&createdAt, &startedAt, &finishedAt, &updatedAt,
	); err != nil {
		return StepRun{}, err
	}
	step.Status = StepStatus(status)
	step.LeaseOwner = leaseOwner.String
	step.LeaseToken = leaseToken.String
	var err error
	if step.AvailableAt, err = parseRequiredTime("step available_at", availableAt); err != nil {
		return StepRun{}, err
	}
	if step.LeaseUntil, err = parseOptionalTime("step lease_until", leaseUntil); err != nil {
		return StepRun{}, err
	}
	if step.CreatedAt, err = parseRequiredTime("step created_at", createdAt); err != nil {
		return StepRun{}, err
	}
	if step.StartedAt, err = parseOptionalTime("step started_at", startedAt); err != nil {
		return StepRun{}, err
	}
	if step.FinishedAt, err = parseOptionalTime("step finished_at", finishedAt); err != nil {
		return StepRun{}, err
	}
	if step.UpdatedAt, err = parseRequiredTime("step updated_at", updatedAt); err != nil {
		return StepRun{}, err
	}
	step.Failure = scanFailure(errorCode, errorMessage, errorRetryable)
	return step, nil
}

func scanInbox(row rowScanner) (InboxMessage, error) {
	var message InboxMessage
	var status string
	var leaseOwner, leaseToken, leaseUntil, lastError sql.NullString
	var availableAt, createdAt, updatedAt sql.NullString
	if err := row.Scan(
		&message.ID, &message.Version, &message.Producer, &message.MessageType,
		&message.IdempotencyKey, &message.Payload, &message.PayloadHash,
		&status, &leaseOwner, &leaseToken, &leaseUntil, &message.Attempt, &availableAt,
		&lastError, &createdAt, &updatedAt,
	); err != nil {
		return InboxMessage{}, err
	}
	message.Status = InboxStatus(status)
	message.LeaseOwner = leaseOwner.String
	message.LeaseToken = leaseToken.String
	message.LastError = lastError.String
	var err error
	if message.LeaseUntil, err = parseOptionalTime("inbox lease_until", leaseUntil); err != nil {
		return InboxMessage{}, err
	}
	if message.AvailableAt, err = parseRequiredTime("inbox available_at", availableAt); err != nil {
		return InboxMessage{}, err
	}
	if message.CreatedAt, err = parseRequiredTime("inbox created_at", createdAt); err != nil {
		return InboxMessage{}, err
	}
	if message.UpdatedAt, err = parseRequiredTime("inbox updated_at", updatedAt); err != nil {
		return InboxMessage{}, err
	}
	message.Payload = append([]byte(nil), message.Payload...)
	return message, nil
}

func scanOutbox(row rowScanner) (OutboxMessage, error) {
	var message OutboxMessage
	var jobID, stepID sql.NullString
	var status string
	var leaseOwner, leaseToken, leaseUntil, lastError sql.NullString
	var availableAt, createdAt, updatedAt sql.NullString
	if err := row.Scan(
		&message.ID, &jobID, &stepID, &message.Version, &message.Destination,
		&message.IdempotencyKey, &message.Payload, &message.PayloadHash,
		&status, &leaseOwner, &leaseToken, &leaseUntil, &message.Attempt, &availableAt,
		&lastError, &createdAt, &updatedAt,
	); err != nil {
		return OutboxMessage{}, err
	}
	message.JobID = jobID.String
	message.StepID = stepID.String
	message.Status = OutboxStatus(status)
	message.LeaseOwner = leaseOwner.String
	message.LeaseToken = leaseToken.String
	message.LastError = lastError.String
	var err error
	if message.LeaseUntil, err = parseOptionalTime("outbox lease_until", leaseUntil); err != nil {
		return OutboxMessage{}, err
	}
	if message.AvailableAt, err = parseRequiredTime("outbox available_at", availableAt); err != nil {
		return OutboxMessage{}, err
	}
	if message.CreatedAt, err = parseRequiredTime("outbox created_at", createdAt); err != nil {
		return OutboxMessage{}, err
	}
	if message.UpdatedAt, err = parseRequiredTime("outbox updated_at", updatedAt); err != nil {
		return OutboxMessage{}, err
	}
	message.Payload = append([]byte(nil), message.Payload...)
	return message, nil
}

func parseRequiredTime(field string, value sql.NullString) (time.Time, error) {
	if !value.Valid {
		return time.Time{}, fmt.Errorf("controlstore: %s is null", field)
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return time.Time{}, fmt.Errorf("controlstore: %s: %w", field, err)
	}
	return parsed, nil
}

func parseOptionalTime(field string, value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil, fmt.Errorf("controlstore: %s: %w", field, err)
	}
	return &parsed, nil
}

func scanFailure(code, message sql.NullString, retryable sql.NullInt64) *Failure {
	if !code.Valid && !message.Valid && !retryable.Valid {
		return nil
	}
	return &Failure{
		Code:      code.String,
		Message:   message.String,
		Retryable: retryable.Valid && retryable.Int64 != 0,
	}
}
