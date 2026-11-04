package controlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

func (s *Store) PutInbox(ctx context.Context, input NewInbox) (InboxMessage, bool, error) {
	if err := validateNewInbox(input); err != nil {
		return InboxMessage{}, false, err
	}
	payloadHash := hashPayload(input.Payload)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return InboxMessage{}, false, fmt.Errorf("controlstore: put inbox: begin transaction: %w", err)
	}
	defer tx.Rollback()
	existing, err := getInboxByIdentity(ctx, tx, input.Producer, input.IdempotencyKey)
	if err == nil {
		if err := verifyInboxReplay(existing, input, payloadHash); err != nil {
			return InboxMessage{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return InboxMessage{}, false, fmt.Errorf("controlstore: put inbox: commit replay: %w", err)
		}
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return InboxMessage{}, false, fmt.Errorf("controlstore: put inbox: find replay: %w", err)
	}

	now := s.now()
	availableAt := input.AvailableAt
	if availableAt.IsZero() {
		availableAt = now
	}
	messageID, err := randomID("inbox")
	if err != nil {
		return InboxMessage{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO inbox_messages(
			id, version, producer, message_type, idempotency_key, payload, payload_hash,
			status, attempt, available_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?)`,
		messageID, schemaVersion, input.Producer, input.MessageType, input.IdempotencyKey,
		input.Payload, payloadHash, InboxPending, formatTime(availableAt), formatTime(now), formatTime(now),
	); err != nil {
		tx.Rollback()
		existing, lookupErr := getInboxByIdentity(ctx, s.db, input.Producer, input.IdempotencyKey)
		if lookupErr != nil {
			return InboxMessage{}, false, fmt.Errorf("controlstore: put inbox: insert: %w", err)
		}
		if err := verifyInboxReplay(existing, input, payloadHash); err != nil {
			return InboxMessage{}, false, err
		}
		return existing, false, nil
	}
	message, err := getInboxByID(ctx, tx, messageID)
	if err != nil {
		return InboxMessage{}, false, fmt.Errorf("controlstore: put inbox %q: read result: %w", messageID, err)
	}
	if err := tx.Commit(); err != nil {
		return InboxMessage{}, false, fmt.Errorf("controlstore: put inbox %q: commit: %w", messageID, err)
	}
	return message, true, nil
}

func (s *Store) ClaimInbox(ctx context.Context, limit int, worker string, lease time.Duration) ([]InboxMessage, error) {
	if err := validateClaim(limit, worker, lease); err != nil {
		return nil, err
	}
	now := s.now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("controlstore: claim inbox: begin transaction: %w", err)
	}
	defer tx.Rollback()
	ids, err := selectDueIDs(ctx, tx, "inbox_messages", string(InboxPending), string(InboxClaimed), now, limit)
	if err != nil {
		return nil, fmt.Errorf("controlstore: claim inbox: select due: %w", err)
	}
	messages := make([]InboxMessage, 0, len(ids))
	for _, id := range ids {
		token, err := randomID("lease")
		if err != nil {
			return nil, err
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE inbox_messages
			SET status = ?, lease_owner = ?, lease_token = ?, lease_until = ?,
				attempt = attempt + 1, updated_at = ?
			WHERE id = ? AND (
				(status = ? AND available_at <= ?)
				OR (status = ? AND lease_until <= ?)
			)`,
			InboxClaimed, worker, token, formatTime(now.Add(lease)), formatTime(now), id,
			InboxPending, formatTime(now), InboxClaimed, formatTime(now),
		)
		if err != nil {
			return nil, fmt.Errorf("controlstore: claim inbox %q: update: %w", id, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("controlstore: claim inbox %q: rows affected: %w", id, err)
		}
		if affected == 0 {
			continue
		}
		message, err := getInboxByID(ctx, tx, id)
		if err != nil {
			return nil, fmt.Errorf("controlstore: claim inbox %q: read result: %w", id, err)
		}
		messages = append(messages, message)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("controlstore: claim inbox: commit: %w", err)
	}
	return messages, nil
}

func (s *Store) AckInbox(ctx context.Context, messageID, leaseToken string) (InboxMessage, error) {
	return s.finishInbox(ctx, "ack", messageID, leaseToken, InboxProcessed, Failure{}, time.Time{})
}

func (s *Store) NackInbox(ctx context.Context, messageID, leaseToken string, failure Failure, retryAt time.Time) (InboxMessage, error) {
	if retryAt.IsZero() {
		return InboxMessage{}, fmt.Errorf("controlstore: retry_at is required")
	}
	return s.finishInbox(ctx, "nack", messageID, leaseToken, InboxPending, failure, retryAt)
}

func (s *Store) DeadLetterInbox(ctx context.Context, messageID, leaseToken string, failure Failure) (InboxMessage, error) {
	return s.finishInbox(ctx, "dead-letter", messageID, leaseToken, InboxDead, failure, time.Time{})
}

func (s *Store) finishInbox(ctx context.Context, operation, messageID, leaseToken string, status InboxStatus, failure Failure, retryAt time.Time) (InboxMessage, error) {
	if err := requireText("inbox id", messageID); err != nil {
		return InboxMessage{}, err
	}
	if err := requireText("lease token", leaseToken); err != nil {
		return InboxMessage{}, err
	}
	now := s.now()
	availableAt := now
	if !retryAt.IsZero() {
		availableAt = retryAt
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE inbox_messages
		SET status = ?, lease_owner = NULL, lease_token = NULL, lease_until = NULL,
			available_at = ?, last_error = ?, updated_at = ?
		WHERE id = ? AND status = ? AND lease_token = ? AND lease_until > ?`,
		status, formatTime(availableAt), nullableFailureText(status == InboxProcessed, failure), formatTime(now),
		messageID, InboxClaimed, leaseToken, formatTime(now),
	)
	if err != nil {
		return InboxMessage{}, fmt.Errorf("controlstore: %s inbox %q: update: %w", operation, messageID, err)
	}
	if err := s.requireMessageLease(ctx, result, "inbox", messageID, "inbox_messages"); err != nil {
		return InboxMessage{}, err
	}
	message, err := getInboxByID(ctx, s.db, messageID)
	if err != nil {
		return InboxMessage{}, fmt.Errorf("controlstore: %s inbox %q: read result: %w", operation, messageID, err)
	}
	return message, nil
}

func (s *Store) ClaimOutbox(ctx context.Context, limit int, worker string, lease time.Duration) ([]OutboxMessage, error) {
	if err := validateClaim(limit, worker, lease); err != nil {
		return nil, err
	}
	now := s.now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("controlstore: claim outbox: begin transaction: %w", err)
	}
	defer tx.Rollback()
	ids, err := selectDueOutboxIDs(ctx, tx, now, limit)
	if err != nil {
		return nil, fmt.Errorf("controlstore: claim outbox: select due: %w", err)
	}
	messages := make([]OutboxMessage, 0, len(ids))
	for _, id := range ids {
		token, err := randomID("lease")
		if err != nil {
			return nil, err
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE outbox_messages
			SET status = ?, lease_owner = ?, lease_token = ?, lease_until = ?,
				attempt = attempt + 1, updated_at = ?
			WHERE id = ?
				AND (job_id IS NULL OR EXISTS (
					SELECT 1 FROM job_runs WHERE id = outbox_messages.job_id AND status = ?
				))
				AND (
				(status = ? AND available_at <= ?)
				OR (status = ? AND lease_until <= ?)
			)`,
			OutboxClaimed, worker, token, formatTime(now.Add(lease)), formatTime(now), id,
			JobRunning, OutboxPending, formatTime(now), OutboxClaimed, formatTime(now),
		)
		if err != nil {
			return nil, fmt.Errorf("controlstore: claim outbox %q: update: %w", id, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("controlstore: claim outbox %q: rows affected: %w", id, err)
		}
		if affected == 0 {
			continue
		}
		message, err := getOutboxByID(ctx, tx, id)
		if err != nil {
			return nil, fmt.Errorf("controlstore: claim outbox %q: read result: %w", id, err)
		}
		message, err = attachOutboxEffect(ctx, tx, message)
		if err != nil {
			return nil, fmt.Errorf("controlstore: claim outbox %q: read effect: %w", id, err)
		}
		messages = append(messages, message)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("controlstore: claim outbox: commit: %w", err)
	}
	return messages, nil
}

func (s *Store) AckOutbox(ctx context.Context, messageID, leaseToken string) (OutboxMessage, error) {
	if err := requireText("outbox id", messageID); err != nil {
		return OutboxMessage{}, err
	}
	if err := requireText("lease token", leaseToken); err != nil {
		return OutboxMessage{}, err
	}
	now := s.now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OutboxMessage{}, fmt.Errorf("controlstore: ack outbox %q: begin transaction: %w", messageID, err)
	}
	defer tx.Rollback()
	if _, err := getOutboxEffect(ctx, tx, messageID); err == nil {
		return OutboxMessage{}, fmt.Errorf("%w: outbox %q has an effect and requires specialized acknowledgement", ErrInvalidTransition, messageID)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return OutboxMessage{}, fmt.Errorf("controlstore: ack outbox %q: read effect: %w", messageID, err)
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
		return OutboxMessage{}, fmt.Errorf("controlstore: ack outbox %q: update: %w", messageID, err)
	}
	if err := requireTxMessageLease(ctx, tx, result, "outbox", messageID, "outbox_messages"); err != nil {
		return OutboxMessage{}, err
	}
	message, err := getOutboxByID(ctx, tx, messageID)
	if err != nil {
		return OutboxMessage{}, fmt.Errorf("controlstore: ack outbox %q: read result: %w", messageID, err)
	}
	if message.JobID != "" {
		if _, err := s.trySucceedJobTx(ctx, tx, message.JobID, false); err != nil {
			return OutboxMessage{}, fmt.Errorf("controlstore: ack outbox %q: try succeed job %q: %w", messageID, message.JobID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return OutboxMessage{}, fmt.Errorf("controlstore: ack outbox %q: commit: %w", messageID, err)
	}
	return message, nil
}

func (s *Store) NackOutbox(ctx context.Context, messageID, leaseToken string, failure Failure, retryAt time.Time) (OutboxMessage, error) {
	if retryAt.IsZero() {
		return OutboxMessage{}, fmt.Errorf("controlstore: retry_at is required")
	}
	return s.finishOutbox(ctx, "nack", messageID, leaseToken, OutboxPending, failure, retryAt)
}

func (s *Store) DeadLetterOutbox(ctx context.Context, messageID, leaseToken string, failure Failure) (OutboxMessage, error) {
	if err := requireText("outbox id", messageID); err != nil {
		return OutboxMessage{}, err
	}
	if err := requireText("lease token", leaseToken); err != nil {
		return OutboxMessage{}, err
	}
	now := s.now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OutboxMessage{}, fmt.Errorf("controlstore: dead-letter outbox %q: begin transaction: %w", messageID, err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE outbox_messages
		SET status = ?, lease_owner = NULL, lease_token = NULL, lease_until = NULL,
			last_error = ?, updated_at = ?
		WHERE id = ? AND status = ? AND lease_token = ? AND lease_until > ?`,
		OutboxDead, failureText(failure), formatTime(now),
		messageID, OutboxClaimed, leaseToken, formatTime(now),
	)
	if err != nil {
		return OutboxMessage{}, fmt.Errorf("controlstore: dead-letter outbox %q: update: %w", messageID, err)
	}
	if err := requireTxMessageLease(ctx, tx, result, "outbox", messageID, "outbox_messages"); err != nil {
		return OutboxMessage{}, err
	}
	message, err := getOutboxByID(ctx, tx, messageID)
	if err != nil {
		return OutboxMessage{}, fmt.Errorf("controlstore: dead-letter outbox %q: read result: %w", messageID, err)
	}
	message, err = attachOutboxEffect(ctx, tx, message)
	if err != nil {
		return OutboxMessage{}, fmt.Errorf("controlstore: dead-letter outbox %q: read effect: %w", messageID, err)
	}
	if message.JobID != "" {
		if err := failJobTx(ctx, tx, message.JobID, failure, now); err != nil {
			return OutboxMessage{}, fmt.Errorf("controlstore: dead-letter outbox %q: %w", messageID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return OutboxMessage{}, fmt.Errorf("controlstore: dead-letter outbox %q: commit: %w", messageID, err)
	}
	return message, nil
}

func (s *Store) finishOutbox(ctx context.Context, operation, messageID, leaseToken string, status OutboxStatus, failure Failure, retryAt time.Time) (OutboxMessage, error) {
	if err := requireText("outbox id", messageID); err != nil {
		return OutboxMessage{}, err
	}
	if err := requireText("lease token", leaseToken); err != nil {
		return OutboxMessage{}, err
	}
	now := s.now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE outbox_messages
		SET status = ?, lease_owner = NULL, lease_token = NULL, lease_until = NULL,
			available_at = ?, last_error = ?, updated_at = ?
		WHERE id = ? AND status = ? AND lease_token = ? AND lease_until > ?
			AND (job_id IS NULL OR EXISTS (
				SELECT 1 FROM job_runs WHERE id = outbox_messages.job_id AND status = ?
			))`,
		status, formatTime(retryAt), failureText(failure), formatTime(now),
		messageID, OutboxClaimed, leaseToken, formatTime(now), JobRunning,
	)
	if err != nil {
		return OutboxMessage{}, fmt.Errorf("controlstore: %s outbox %q: update: %w", operation, messageID, err)
	}
	if err := s.requireMessageLease(ctx, result, "outbox", messageID, "outbox_messages"); err != nil {
		return OutboxMessage{}, err
	}
	message, err := getOutboxByID(ctx, s.db, messageID)
	if err != nil {
		return OutboxMessage{}, fmt.Errorf("controlstore: %s outbox %q: read result: %w", operation, messageID, err)
	}
	message, err = attachOutboxEffect(ctx, s.db, message)
	if err != nil {
		return OutboxMessage{}, fmt.Errorf("controlstore: %s outbox %q: read effect: %w", operation, messageID, err)
	}
	return message, nil
}

func selectDueIDs(ctx context.Context, tx *sql.Tx, table, pending, claimed string, now time.Time, limit int) ([]string, error) {
	query := fmt.Sprintf(`
		SELECT id FROM %s
		WHERE (status = ? AND available_at <= ?)
			OR (status = ? AND lease_until <= ?)
		ORDER BY available_at, created_at, id
		LIMIT ?`, table)
	rows, err := tx.QueryContext(ctx, query, pending, formatTime(now), claimed, formatTime(now), limit)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

func selectDueOutboxIDs(ctx context.Context, tx *sql.Tx, now time.Time, limit int) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id FROM outbox_messages
		WHERE (job_id IS NULL OR EXISTS (
			SELECT 1 FROM job_runs WHERE id = outbox_messages.job_id AND status = ?
		))
		AND (
			(status = ? AND available_at <= ?)
			OR (status = ? AND lease_until <= ?)
		)
		ORDER BY available_at, created_at, id
		LIMIT ?`,
		JobRunning, OutboxPending, formatTime(now), OutboxClaimed, formatTime(now), limit,
	)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}

func (s *Store) requireMessageLease(ctx context.Context, result sql.Result, kind, id, table string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("controlstore: %s %q: rows affected: %w", kind, id, err)
	}
	if affected == 1 {
		return nil
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`SELECT 1 FROM %s WHERE id = ?`, table), id).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s %q", ErrNotFound, kind, id)
	} else if err != nil {
		return fmt.Errorf("controlstore: %s %q: verify existence: %w", kind, id, err)
	}
	return fmt.Errorf("%w: %s %q token is stale or expired", ErrLeaseLost, kind, id)
}

func requireTxMessageLease(ctx context.Context, tx *sql.Tx, result sql.Result, kind, id, table string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("controlstore: %s %q: rows affected: %w", kind, id, err)
	}
	if affected == 1 {
		return nil
	}
	var exists int
	if err := tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT 1 FROM %s WHERE id = ?`, table), id).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s %q", ErrNotFound, kind, id)
	} else if err != nil {
		return fmt.Errorf("controlstore: %s %q: verify existence: %w", kind, id, err)
	}
	return fmt.Errorf("%w: %s %q token is stale or expired", ErrLeaseLost, kind, id)
}

func getInboxByID(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, messageID string) (InboxMessage, error) {
	return scanInbox(queryer.QueryRowContext(ctx, `SELECT `+inboxColumns+` FROM inbox_messages WHERE id = ?`, messageID))
}

func getInboxByIdentity(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, producer, key string) (InboxMessage, error) {
	return scanInbox(queryer.QueryRowContext(ctx, `SELECT `+inboxColumns+`
		FROM inbox_messages WHERE producer = ? AND idempotency_key = ?`, producer, key))
}

func getOutboxByID(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, messageID string) (OutboxMessage, error) {
	return scanOutbox(queryer.QueryRowContext(ctx, `SELECT `+outboxColumns+` FROM outbox_messages WHERE id = ?`, messageID))
}

func validateNewInbox(input NewInbox) error {
	if err := requireText("inbox producer", input.Producer); err != nil {
		return err
	}
	if err := requireText("inbox message type", input.MessageType); err != nil {
		return err
	}
	if err := requireText("inbox idempotency key", input.IdempotencyKey); err != nil {
		return err
	}
	if !json.Valid(input.Payload) {
		return fmt.Errorf("controlstore: inbox payload must be valid JSON")
	}
	return nil
}

func verifyInboxReplay(existing InboxMessage, input NewInbox, payloadHash string) error {
	identity := fmt.Sprintf("producer=%q key=%q", input.Producer, input.IdempotencyKey)
	if existing.PayloadHash != payloadHash {
		return fmt.Errorf("%w: inbox %s payload hash differs", ErrIdempotencyConflict, identity)
	}
	if existing.MessageType != input.MessageType {
		return fmt.Errorf("%w: inbox %s message type differs", ErrIdempotencyConflict, identity)
	}
	if !input.AvailableAt.IsZero() && !existing.AvailableAt.Equal(input.AvailableAt.UTC()) {
		return fmt.Errorf("%w: inbox %s available_at differs", ErrIdempotencyConflict, identity)
	}
	return nil
}

func nullableFailureText(clear bool, failure Failure) any {
	if clear {
		return nil
	}
	return failureText(failure)
}
