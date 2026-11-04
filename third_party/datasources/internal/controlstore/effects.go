package controlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const decisionColumns = `
	id, version, idempotency_key, proposal, proposal_hash, settlement, settlement_hash,
	status, created_at, closed_at, updated_at`

func insertOutboxTx(ctx context.Context, tx *sql.Tx, jobID, stepID string, message NewOutbox, now time.Time, allowDecisionEffect bool) (OutboxMessage, error) {
	if err := validateNewOutbox(message, allowDecisionEffect); err != nil {
		return OutboxMessage{}, err
	}
	messageID, err := randomID("outbox")
	if err != nil {
		return OutboxMessage{}, err
	}
	availableAt := message.AvailableAt
	if availableAt.IsZero() {
		availableAt = now
	}
	var nullableJobID, nullableStepID any
	if jobID != "" {
		nullableJobID = jobID
	}
	if stepID != "" {
		nullableStepID = stepID
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO outbox_messages(
			id, job_id, step_id, version, destination, idempotency_key,
			payload, payload_hash, status, attempt, available_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?)`,
		messageID, nullableJobID, nullableStepID, schemaVersion, message.Destination, message.IdempotencyKey,
		message.Payload, hashPayload(message.Payload), OutboxPending,
		formatTime(availableAt), formatTime(now), formatTime(now),
	); err != nil {
		var exists int
		lookupErr := tx.QueryRowContext(ctx,
			`SELECT 1 FROM outbox_messages WHERE destination = ? AND idempotency_key = ?`,
			message.Destination, message.IdempotencyKey,
		).Scan(&exists)
		if lookupErr == nil {
			return OutboxMessage{}, fmt.Errorf("%w: outbox destination=%q key=%q", ErrIdempotencyConflict, message.Destination, message.IdempotencyKey)
		}
		return OutboxMessage{}, fmt.Errorf("insert outbox: %w", err)
	}
	if message.Effect != nil {
		var cooldownKey, cooldownUntil, decisionID any
		if message.Effect.DecisionID != "" {
			decisionID = message.Effect.DecisionID
		} else {
			cooldownKey = message.Effect.CooldownKey
			cooldownUntil = formatTime(message.Effect.CooldownCommitUntil)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO outbox_effects(
				outbox_id, version, cooldown_key, cooldown_commit_until, decision_id, created_at
			) VALUES (?, ?, ?, ?, ?, ?)`,
			messageID, schemaVersion, cooldownKey, cooldownUntil, decisionID, formatTime(now),
		); err != nil {
			return OutboxMessage{}, fmt.Errorf("insert outbox effect: %w", err)
		}
	}
	result, err := getOutboxByID(ctx, tx, messageID)
	if err != nil {
		return OutboxMessage{}, fmt.Errorf("read inserted outbox %q: %w", messageID, err)
	}
	result.Effect = cloneOutboxEffect(message.Effect)
	return result, nil
}

func validateOutboxEffect(effect *OutboxEffect) error {
	if effect == nil {
		return nil
	}
	cooldown := effect.CooldownKey != "" || !effect.CooldownCommitUntil.IsZero()
	decision := effect.DecisionID != ""
	if cooldown == decision {
		return fmt.Errorf("controlstore: outbox effect must contain exactly one cooldown or decision")
	}
	if cooldown {
		if err := requireText("cooldown key", effect.CooldownKey); err != nil {
			return err
		}
		if effect.CooldownCommitUntil.IsZero() {
			return fmt.Errorf("controlstore: cooldown commit_until is required")
		}
	}
	return nil
}

func cloneOutboxEffect(effect *OutboxEffect) *OutboxEffect {
	if effect == nil {
		return nil
	}
	copy := *effect
	copy.CooldownCommitUntil = copy.CooldownCommitUntil.UTC()
	return &copy
}

func getOutboxEffect(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, outboxID string) (*OutboxEffect, error) {
	var cooldownKey, cooldownUntil, decisionID sql.NullString
	err := queryer.QueryRowContext(ctx, `
		SELECT cooldown_key, cooldown_commit_until, decision_id
		FROM outbox_effects WHERE outbox_id = ?`, outboxID,
	).Scan(&cooldownKey, &cooldownUntil, &decisionID)
	if err != nil {
		return nil, err
	}
	effect := &OutboxEffect{CooldownKey: cooldownKey.String, DecisionID: decisionID.String}
	if cooldownUntil.Valid {
		parsed, err := parseTime(cooldownUntil.String)
		if err != nil {
			return nil, fmt.Errorf("controlstore: scan cooldown effect: %w", err)
		}
		effect.CooldownCommitUntil = parsed
	}
	return effect, nil
}

func attachOutboxEffect(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, message OutboxMessage) (OutboxMessage, error) {
	effect, err := getOutboxEffect(ctx, queryer, message.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return message, nil
	}
	if err != nil {
		return OutboxMessage{}, err
	}
	message.Effect = effect
	return message, nil
}

func (s *Store) GetCooldown(ctx context.Context, key string) (Cooldown, error) {
	if err := requireText("cooldown key", key); err != nil {
		return Cooldown{}, err
	}
	var cooldown Cooldown
	var commitUntil, createdAt, updatedAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT key, version, commit_until, created_at, updated_at
		FROM cooldowns WHERE key = ?`, key,
	).Scan(&cooldown.Key, &cooldown.Version, &commitUntil, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Cooldown{}, fmt.Errorf("%w: cooldown %q", ErrNotFound, key)
	}
	if err != nil {
		return Cooldown{}, fmt.Errorf("controlstore: get cooldown %q: %w", key, err)
	}
	if cooldown.CommitUntil, err = parseTime(commitUntil); err != nil {
		return Cooldown{}, err
	}
	if cooldown.CreatedAt, err = parseTime(createdAt); err != nil {
		return Cooldown{}, err
	}
	if cooldown.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return Cooldown{}, err
	}
	return cooldown, nil
}

func (s *Store) GetDecision(ctx context.Context, decisionID string) (DecisionRecord, error) {
	if err := requireText("decision id", decisionID); err != nil {
		return DecisionRecord{}, err
	}
	record, err := getDecisionByID(ctx, s.db, decisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return DecisionRecord{}, fmt.Errorf("%w: decision %q", ErrNotFound, decisionID)
	}
	if err != nil {
		return DecisionRecord{}, fmt.Errorf("controlstore: get decision %q: %w", decisionID, err)
	}
	return record, nil
}

func (s *Store) CreateDecisionNotification(ctx context.Context, input NewDecisionNotification) (DecisionRecord, OutboxMessage, bool, error) {
	if err := validateNewDecisionNotification(input); err != nil {
		return DecisionRecord{}, OutboxMessage{}, false, err
	}
	now := s.now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DecisionRecord{}, OutboxMessage{}, false, fmt.Errorf("controlstore: create decision notification: begin transaction: %w", err)
	}
	defer tx.Rollback()

	existing, err := getDecisionByIdentity(ctx, tx, input.Decision.IdempotencyKey)
	if err == nil {
		outbox, verifyErr := verifyDecisionReplay(ctx, tx, existing, input)
		if verifyErr != nil {
			return DecisionRecord{}, OutboxMessage{}, false, verifyErr
		}
		if err := tx.Commit(); err != nil {
			return DecisionRecord{}, OutboxMessage{}, false, fmt.Errorf("controlstore: create decision notification: commit replay: %w", err)
		}
		return existing, outbox, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return DecisionRecord{}, OutboxMessage{}, false, fmt.Errorf("controlstore: create decision notification: find replay: %w", err)
	}
	if err := validateOutboxLink(ctx, tx, input.JobID, input.StepID); err != nil {
		return DecisionRecord{}, OutboxMessage{}, false, err
	}
	decisionID, err := randomID("decision")
	if err != nil {
		return DecisionRecord{}, OutboxMessage{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO decision_records(
			id, version, idempotency_key, proposal, proposal_hash, settlement, settlement_hash,
			status, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		decisionID, schemaVersion, input.Decision.IdempotencyKey,
		input.Decision.Proposal, hashPayload(input.Decision.Proposal),
		input.Decision.Settlement, hashPayload(input.Decision.Settlement),
		DecisionOpen, formatTime(now), formatTime(now),
	); err != nil {
		return DecisionRecord{}, OutboxMessage{}, false, fmt.Errorf("controlstore: create decision notification: insert decision: %w", err)
	}
	input.Notification.Effect = &OutboxEffect{DecisionID: decisionID}
	outbox, err := insertOutboxTx(ctx, tx, input.JobID, input.StepID, input.Notification, now, true)
	if err != nil {
		return DecisionRecord{}, OutboxMessage{}, false, fmt.Errorf("controlstore: create decision notification: %w", err)
	}
	record, err := getDecisionByID(ctx, tx, decisionID)
	if err != nil {
		return DecisionRecord{}, OutboxMessage{}, false, fmt.Errorf("controlstore: create decision notification: read decision: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return DecisionRecord{}, OutboxMessage{}, false, fmt.Errorf("controlstore: create decision notification: commit: %w", err)
	}
	return record, outbox, true, nil
}

func validateNewDecisionNotification(input NewDecisionNotification) error {
	if err := requireText("decision idempotency key", input.Decision.IdempotencyKey); err != nil {
		return err
	}
	if !json.Valid(input.Decision.Proposal) {
		return fmt.Errorf("controlstore: decision proposal must be valid JSON")
	}
	if !json.Valid(input.Decision.Settlement) {
		return fmt.Errorf("controlstore: decision settlement must be valid JSON")
	}
	if input.Notification.Effect != nil {
		return fmt.Errorf("controlstore: decision notification effect is managed by controlstore")
	}
	if err := validateNewOutboxes([]NewOutbox{input.Notification}); err != nil {
		return err
	}
	if (input.JobID == "") != (input.StepID == "") {
		return fmt.Errorf("controlstore: decision notification job id and step id must both be set or both be empty")
	}
	return nil
}

func validateOutboxLink(ctx context.Context, tx *sql.Tx, jobID, stepID string) error {
	if jobID == "" {
		return nil
	}
	job, err := getJobByID(ctx, tx, jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: job %q", ErrNotFound, jobID)
	}
	if err != nil {
		return fmt.Errorf("controlstore: validate outbox link: read job: %w", err)
	}
	if job.Status != JobRunning {
		return fmt.Errorf("%w: decision notification requires running job %q, got %s", ErrInvalidTransition, jobID, job.Status)
	}
	step, err := getStepByID(ctx, tx, stepID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && step.JobID != jobID) {
		return fmt.Errorf("%w: step %q in job %q", ErrNotFound, stepID, jobID)
	}
	if err != nil {
		return fmt.Errorf("controlstore: validate outbox link: %w", err)
	}
	if step.Status != StepRunning && step.Status != StepSucceeded {
		return fmt.Errorf("%w: decision notification step %q cannot be %s", ErrInvalidTransition, stepID, step.Status)
	}
	return nil
}

func verifyDecisionReplay(ctx context.Context, tx *sql.Tx, existing DecisionRecord, input NewDecisionNotification) (OutboxMessage, error) {
	if existing.ProposalHash != hashPayload(input.Decision.Proposal) || existing.SettlementHash != hashPayload(input.Decision.Settlement) {
		return OutboxMessage{}, fmt.Errorf("%w: decision key reused with different content", ErrIdempotencyConflict)
	}
	outbox, err := scanOutbox(tx.QueryRowContext(ctx, `SELECT `+outboxColumns+`
		FROM outbox_messages
		WHERE id = (SELECT outbox_id FROM outbox_effects WHERE decision_id = ?)`, existing.ID))
	if err != nil {
		return OutboxMessage{}, fmt.Errorf("controlstore: verify decision replay: read notification: %w", err)
	}
	if outbox.Destination != input.Notification.Destination || outbox.IdempotencyKey != input.Notification.IdempotencyKey ||
		outbox.PayloadHash != hashPayload(input.Notification.Payload) || outbox.JobID != input.JobID || outbox.StepID != input.StepID ||
		(!input.Notification.AvailableAt.IsZero() && !outbox.AvailableAt.Equal(input.Notification.AvailableAt.UTC())) {
		return OutboxMessage{}, fmt.Errorf("%w: decision notification key reused with different content", ErrIdempotencyConflict)
	}
	outbox.Effect = &OutboxEffect{DecisionID: existing.ID}
	return outbox, nil
}

func (s *Store) AckOutboxAndCommitCooldown(ctx context.Context, messageID, leaseToken string) (OutboxMessage, error) {
	return s.ackOutboxEffect(ctx, messageID, leaseToken, true)
}

func (s *Store) AckOutboxAndCloseDecision(ctx context.Context, messageID, leaseToken string) (OutboxMessage, error) {
	return s.ackOutboxEffect(ctx, messageID, leaseToken, false)
}

func (s *Store) ackOutboxEffect(ctx context.Context, messageID, leaseToken string, cooldown bool) (OutboxMessage, error) {
	if err := requireText("outbox id", messageID); err != nil {
		return OutboxMessage{}, err
	}
	if err := requireText("lease token", leaseToken); err != nil {
		return OutboxMessage{}, err
	}
	now := s.now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OutboxMessage{}, fmt.Errorf("controlstore: ack effect outbox %q: begin transaction: %w", messageID, err)
	}
	defer tx.Rollback()
	effect, err := getOutboxEffect(ctx, tx, messageID)
	if errors.Is(err, sql.ErrNoRows) {
		return OutboxMessage{}, fmt.Errorf("%w: outbox %q has no effect", ErrInvalidTransition, messageID)
	}
	if err != nil {
		return OutboxMessage{}, fmt.Errorf("controlstore: ack effect outbox %q: read effect: %w", messageID, err)
	}
	if cooldown != (effect.DecisionID == "") {
		return OutboxMessage{}, fmt.Errorf("%w: outbox %q effect requires a different acknowledgement", ErrInvalidTransition, messageID)
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE outbox_messages
		SET status = ?, lease_owner = NULL, lease_token = NULL, lease_until = NULL,
			last_error = NULL, updated_at = ?
		WHERE id = ? AND status = ? AND lease_token = ? AND lease_until > ?
			AND (job_id IS NULL OR EXISTS (
				SELECT 1 FROM job_runs WHERE id = outbox_messages.job_id AND status = ?
			))`,
		OutboxAcked, formatTime(now), messageID, OutboxClaimed, leaseToken, formatTime(now), JobRunning,
	)
	if err != nil {
		return OutboxMessage{}, fmt.Errorf("controlstore: ack effect outbox %q: update: %w", messageID, err)
	}
	if err := requireTxMessageLease(ctx, tx, result, "outbox", messageID, "outbox_messages"); err != nil {
		return OutboxMessage{}, err
	}
	if cooldown {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO cooldowns(key, version, commit_until, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(key) DO UPDATE SET
				commit_until = MAX(cooldowns.commit_until, excluded.commit_until),
				updated_at = excluded.updated_at`,
			effect.CooldownKey, schemaVersion, formatTime(effect.CooldownCommitUntil), formatTime(now), formatTime(now),
		); err != nil {
			return OutboxMessage{}, fmt.Errorf("controlstore: ack effect outbox %q: commit cooldown: %w", messageID, err)
		}
	} else {
		result, err := tx.ExecContext(ctx, `
			UPDATE decision_records SET status = ?, closed_at = ?, updated_at = ?
			WHERE id = ? AND status = ?`,
			DecisionClosed, formatTime(now), formatTime(now), effect.DecisionID, DecisionOpen,
		)
		if err != nil {
			return OutboxMessage{}, fmt.Errorf("controlstore: ack effect outbox %q: close decision: %w", messageID, err)
		}
		affected, err := result.RowsAffected()
		if err != nil || affected != 1 {
			return OutboxMessage{}, fmt.Errorf("%w: decision %q is not open", ErrInvalidTransition, effect.DecisionID)
		}
	}
	message, err := getOutboxByID(ctx, tx, messageID)
	if err != nil {
		return OutboxMessage{}, fmt.Errorf("controlstore: ack effect outbox %q: read result: %w", messageID, err)
	}
	message.Effect = effect
	if message.JobID != "" {
		if _, err := s.trySucceedJobTx(ctx, tx, message.JobID, false); err != nil {
			return OutboxMessage{}, fmt.Errorf("controlstore: ack effect outbox %q: try succeed job %q: %w", messageID, message.JobID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return OutboxMessage{}, fmt.Errorf("controlstore: ack effect outbox %q: commit: %w", messageID, err)
	}
	return message, nil
}

func getDecisionByID(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, decisionID string) (DecisionRecord, error) {
	return scanDecision(queryer.QueryRowContext(ctx, `SELECT `+decisionColumns+`
		FROM decision_records WHERE id = ?`, decisionID))
}

func getDecisionByIdentity(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, key string) (DecisionRecord, error) {
	return scanDecision(queryer.QueryRowContext(ctx, `SELECT `+decisionColumns+`
		FROM decision_records WHERE idempotency_key = ?`, key))
}

func scanDecision(row rowScanner) (DecisionRecord, error) {
	var record DecisionRecord
	var status, createdAt, updatedAt string
	var closedAt sql.NullString
	if err := row.Scan(
		&record.ID, &record.Version, &record.IdempotencyKey,
		&record.Proposal, &record.ProposalHash, &record.Settlement, &record.SettlementHash,
		&status, &createdAt, &closedAt, &updatedAt,
	); err != nil {
		return DecisionRecord{}, err
	}
	record.Status = DecisionStatus(status)
	var err error
	if record.CreatedAt, err = parseTime(createdAt); err != nil {
		return DecisionRecord{}, err
	}
	if closedAt.Valid {
		parsed, err := parseTime(closedAt.String)
		if err != nil {
			return DecisionRecord{}, err
		}
		record.ClosedAt = &parsed
	}
	if record.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return DecisionRecord{}, err
	}
	record.Proposal = append([]byte(nil), record.Proposal...)
	record.Settlement = append([]byte(nil), record.Settlement...)
	return record, nil
}
