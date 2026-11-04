package controlstore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	busyTimeoutMilliseconds = 5000
	maxClaimLimit           = 1000
)

type Options struct {
	Now func() time.Time
}

// Store owns the sole writer lock for one control database. Separate Store
// instances must use different database paths.
type Store struct {
	db                *sql.DB
	nowFn             func() time.Time
	path              string
	writerLock        *writerLock
	closeMu           sync.Mutex
	snapshotGate      chan struct{}
	afterSnapshotStep func()
	closed            bool
}

func Open(path string, options Options) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("controlstore: open: path is required")
	}
	lock, canonicalPath, err := acquireWriterLock(path)
	if err != nil {
		return nil, err
	}
	dsnURL := url.URL{Scheme: "file", Path: canonicalPath}
	query := dsnURL.Query()
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMilliseconds))
	query.Add("_pragma", "synchronous(NORMAL)")
	dsnURL.RawQuery = query.Encode()

	db, err := sql.Open("sqlite", dsnURL.String())
	if err != nil {
		lock.release()
		return nil, fmt.Errorf("controlstore: open %q: %w", canonicalPath, err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	nowFn := options.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	store := &Store{
		db: db, nowFn: nowFn, path: canonicalPath, writerLock: lock,
		snapshotGate: make(chan struct{}, 1),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := store.initialize(ctx); err != nil {
		cleanupErr := errors.Join(db.Close(), lock.release())
		return nil, errors.Join(fmt.Errorf("controlstore: initialize %q: %w", canonicalPath, err), cleanupErr)
	}
	return store, nil
}

func (s *Store) initialize(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping sqlite: %w", err)
	}
	var journalMode string
	if err := s.db.QueryRowContext(ctx, `PRAGMA journal_mode = WAL`).Scan(&journalMode); err != nil {
		return fmt.Errorf("set journal_mode WAL: %w", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		return fmt.Errorf("verify journal_mode: got %q, want WAL", journalMode)
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA synchronous = NORMAL`); err != nil {
		return fmt.Errorf("set synchronous NORMAL: %w", err)
	}
	var synchronous int
	if err := s.db.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&synchronous); err != nil {
		return fmt.Errorf("verify synchronous: %w", err)
	}
	if synchronous != 1 {
		return fmt.Errorf("verify synchronous: got %d, want 1 (NORMAL)", synchronous)
	}
	var foreignKeys int
	if err := s.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		return fmt.Errorf("verify foreign_keys: %w", err)
	}
	if foreignKeys != 1 {
		return fmt.Errorf("verify foreign_keys: got %d, want 1", foreignKeys)
	}
	var busyTimeout int
	if err := s.db.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		return fmt.Errorf("verify busy_timeout: %w", err)
	}
	if busyTimeout != busyTimeoutMilliseconds {
		return fmt.Errorf("verify busy_timeout: got %d, want %d", busyTimeout, busyTimeoutMilliseconds)
	}
	if err := s.migrate(ctx); err != nil {
		return err
	}
	return nil
}

func (s *Store) Close() error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if err := errors.Join(s.db.Close(), s.writerLock.release()); err != nil {
		return fmt.Errorf("controlstore: close %q: %w", s.path, err)
	}
	return nil
}

func (s *Store) now() time.Time {
	return s.nowFn().UTC()
}

func formatTime(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000000000Z")
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse UTC timestamp %q: %w", value, err)
	}
	return parsed.UTC(), nil
}

func randomID(prefix string) (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate %s id: %w", prefix, err)
	}
	return prefix + "_" + hex.EncodeToString(bytes), nil
}

func hashPayload(payload []byte) string {
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func requireText(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("controlstore: %s is required", field)
	}
	return nil
}

func validateClaim(limit int, worker string, lease time.Duration) error {
	if limit < 1 || limit > maxClaimLimit {
		return fmt.Errorf("controlstore: claim limit must be between 1 and %d", maxClaimLimit)
	}
	if err := requireText("worker", worker); err != nil {
		return err
	}
	if lease <= 0 {
		return fmt.Errorf("controlstore: lease must be positive")
	}
	return nil
}

func failureValues(failure Failure) (string, string, int) {
	retryable := 0
	if failure.Retryable {
		retryable = 1
	}
	return failure.Code, failure.Message, retryable
}

func failureText(failure Failure) string {
	if failure.Code == "" {
		return failure.Message
	}
	if failure.Message == "" {
		return failure.Code
	}
	return failure.Code + ": " + failure.Message
}
