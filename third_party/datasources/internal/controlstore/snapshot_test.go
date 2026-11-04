package controlstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSnapshotEmptyDataAndConsecutiveImages(t *testing.T) {
	store, _, _ := openTestStore(t)

	firstImage, firstMeta := takeSnapshot(t, store)
	assertSnapshot(t, firstImage, 0)
	assertSnapshotMeta(t, firstImage, firstMeta)

	if _, created, err := store.CreateJob(context.Background(), NewJob{
		Kind: "snapshot", Producer: "snapshot-test", TraceID: "trace-1", IdempotencyKey: "snapshot-1",
	}, nil); err != nil || !created {
		t.Fatalf("CreateJob() created=%v error=%v", created, err)
	}
	secondImage, secondMeta := takeSnapshot(t, store)
	assertSnapshot(t, secondImage, 1)
	assertSnapshotMeta(t, secondImage, secondMeta)
	if bytes.Equal(firstImage, secondImage) {
		t.Fatal("consecutive snapshots did not reflect the committed row")
	}
}

func TestSnapshotWithConcurrentWALWriter(t *testing.T) {
	store, _, _ := openTestStore(t)
	if _, err := store.db.Exec(`
		CREATE TABLE snapshot_load(id INTEGER PRIMARY KEY, payload BLOB NOT NULL);
		WITH RECURSIVE counter(value) AS (
			SELECT 1 UNION ALL SELECT value + 1 FROM counter WHERE value < 2048
		)
		INSERT INTO snapshot_load(payload) SELECT randomblob(4096) FROM counter`); err != nil {
		t.Fatalf("seed snapshot load: %v", err)
	}

	var pageCount int
	if err := store.db.QueryRow(`PRAGMA page_count`).Scan(&pageCount); err != nil {
		t.Fatal(err)
	}
	if pageCount <= snapshotStepPages {
		t.Fatalf("seeded database has %d pages, need more than one backup step", pageCount)
	}

	captureStep := make(chan struct{})
	writerDone := make(chan struct{})
	var barrier sync.Once
	store.afterSnapshotStep = func() {
		barrier.Do(func() {
			close(captureStep)
			<-writerDone
		})
	}
	writerErr := make(chan error, 1)
	go func() {
		<-captureStep
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err := store.db.ExecContext(ctx, `INSERT INTO snapshot_load(payload) VALUES (randomblob(1024))`)
		writerErr <- err
		close(writerDone)
	}()

	image, _ := takeSnapshot(t, store)
	store.afterSnapshotStep = nil
	if err := <-writerErr; err != nil {
		t.Fatalf("concurrent WAL writer: %v", err)
	}
	assertSnapshot(t, image, 0)
	snapshotDB := openSnapshot(t, image)
	defer snapshotDB.Close()
	var rows int
	if err := snapshotDB.QueryRow(`SELECT COUNT(*) FROM snapshot_load`).Scan(&rows); err != nil {
		t.Fatalf("count snapshot_load: %v", err)
	}
	if rows < 2048 || rows > 2049 {
		t.Fatalf("snapshot_load row count = %d, want a transactionally valid point in [2048, 2049]", rows)
	}
}

func TestSnapshotContextCancellationAndWriterFailureLeaveStoreUsable(t *testing.T) {
	store, _, _ := openTestStore(t)
	if _, err := store.db.Exec(`CREATE TABLE snapshot_payload(value BLOB); INSERT INTO snapshot_payload VALUES (randomblob(1048576))`); err != nil {
		t.Fatal(err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Snapshot(cancelled, &bytes.Buffer{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Snapshot(cancelled) error = %v, want context.Canceled", err)
	}
	assertSingleConnectionPool(t, store)

	backupContext, cancelBackup := context.WithCancel(context.Background())
	backupStepReached := make(chan struct{})
	var cancelOnce sync.Once
	store.afterSnapshotStep = func() {
		cancelOnce.Do(func() {
			close(backupStepReached)
			cancelBackup()
		})
	}
	if _, err := store.Snapshot(backupContext, &bytes.Buffer{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Snapshot(cancel during backup) error = %v, want context.Canceled", err)
	}
	store.afterSnapshotStep = nil
	select {
	case <-backupStepReached:
	default:
		t.Fatal("Snapshot() did not reach an incomplete online backup step")
	}
	assertSingleConnectionPool(t, store)

	midWrite, cancelMidWrite := context.WithCancel(context.Background())
	cancelDestination := &cancelAfterWrite{cancel: cancelMidWrite}
	if _, err := store.Snapshot(midWrite, cancelDestination); !errors.Is(err, context.Canceled) {
		t.Fatalf("Snapshot(cancel during write) error = %v, want context.Canceled", err)
	}

	writeErr := errors.New("destination failed")
	if _, err := store.Snapshot(context.Background(), failingWriter{err: writeErr}); !errors.Is(err, writeErr) {
		t.Fatalf("Snapshot(failing writer) error = %v, want %v", err, writeErr)
	}
	assertSingleConnectionPool(t, store)
	if err := store.Probe(context.Background()); err != nil {
		t.Fatalf("Probe() after failed snapshots: %v", err)
	}
	image, _ := takeSnapshot(t, store)
	assertSnapshot(t, image, 0)
}

func TestValidateSnapshotPageSizeBoundaries(t *testing.T) {
	const pageSize int64 = 4096
	atLimit := int64(maxSnapshotBytes) / pageSize
	if err := validateSnapshotPageSize(atLimit, pageSize); err != nil {
		t.Fatalf("validateSnapshotPageSize(at limit) error = %v", err)
	}
	if err := validateSnapshotPageSize(atLimit+1, pageSize); err == nil {
		t.Fatal("validateSnapshotPageSize(over limit) succeeded")
	}
	maxInt64 := int64(^uint64(0) >> 1)
	if err := validateSnapshotPageSize(maxInt64, maxInt64); err == nil {
		t.Fatal("validateSnapshotPageSize(overflowing product) succeeded")
	}
}

func TestSnapshotDoesNotBlockStoreDuringDestinationWrite(t *testing.T) {
	store, _, _ := openTestStore(t)
	destination := &blockingWriter{entered: make(chan struct{}), release: make(chan struct{})}
	type result struct {
		meta SnapshotMeta
		err  error
	}
	resultCh := make(chan result, 1)
	go func() {
		meta, err := store.Snapshot(context.Background(), destination)
		resultCh <- result{meta: meta, err: err}
	}()

	select {
	case <-destination.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("Snapshot() did not reach destination write")
	}
	waitingContext, cancelWaiting := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelWaiting()
	if _, err := store.Snapshot(waitingContext, &bytes.Buffer{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting Snapshot() error = %v, want context.DeadlineExceeded", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, created, err := store.CreateJob(ctx, NewJob{
		Kind: "concurrent", Producer: "snapshot-test", TraceID: "trace-2", IdempotencyKey: "snapshot-concurrent",
	}, nil); err != nil || !created {
		t.Fatalf("CreateJob() during Snapshot() created=%v error=%v", created, err)
	}
	close(destination.release)
	snapshotResult := <-resultCh
	if snapshotResult.err != nil {
		t.Fatalf("Snapshot() error = %v", snapshotResult.err)
	}
	assertSnapshotMeta(t, destination.Bytes(), snapshotResult.meta)
	assertSnapshot(t, destination.Bytes(), 0)
}

func takeSnapshot(t *testing.T, store *Store) ([]byte, SnapshotMeta) {
	t.Helper()
	var destination bytes.Buffer
	meta, err := store.Snapshot(context.Background(), &destination)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	assertSingleConnectionPool(t, store)
	return destination.Bytes(), meta
}

func assertSingleConnectionPool(t *testing.T, store *Store) {
	t.Helper()
	if got := store.db.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1", got)
	}
}

func assertSnapshotMeta(t *testing.T, image []byte, meta SnapshotMeta) {
	t.Helper()
	sum := sha256.Sum256(image)
	wantHash := "sha256:" + hex.EncodeToString(sum[:])
	if meta.Size != int64(len(image)) || meta.SHA256 != wantHash || meta.SchemaVersion != 2 {
		t.Fatalf("SnapshotMeta = %+v, want size=%d sha256=%s schema=2", meta, len(image), wantHash)
	}
}

func assertSnapshot(t *testing.T, image []byte, wantJobs int) {
	t.Helper()
	db := openSnapshot(t, image)
	defer db.Close()
	var quickCheck string
	if err := db.QueryRow(`PRAGMA quick_check`).Scan(&quickCheck); err != nil {
		t.Fatalf("PRAGMA quick_check: %v", err)
	}
	if quickCheck != "ok" {
		t.Fatalf("PRAGMA quick_check = %q, want ok", quickCheck)
	}
	var jobs int
	if err := db.QueryRow(`SELECT COUNT(*) FROM job_runs`).Scan(&jobs); err != nil {
		t.Fatalf("count job_runs: %v", err)
	}
	if jobs != wantJobs {
		t.Fatalf("job_runs count = %d, want %d", jobs, wantJobs)
	}
}

func openSnapshot(t *testing.T, image []byte) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "snapshot.db")
	if err := os.WriteFile(path, image, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro", path))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Fatalf("open snapshot: %v", err)
	}
	return db
}

type failingWriter struct {
	err error
}

func (writer failingWriter) Write([]byte) (int, error) {
	return 0, writer.err
}

type cancelAfterWrite struct {
	cancel context.CancelFunc
}

func (writer *cancelAfterWrite) Write(value []byte) (int, error) {
	writer.cancel()
	return len(value), nil
}

type blockingWriter struct {
	bytes.Buffer
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (writer *blockingWriter) Write(value []byte) (int, error) {
	writer.once.Do(func() {
		close(writer.entered)
		<-writer.release
	})
	return writer.Buffer.Write(value)
}
