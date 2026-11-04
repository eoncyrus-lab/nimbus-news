package controlstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"modernc.org/sqlite"
)

const (
	maxSnapshotBytes  = 256 << 20
	snapshotStepPages = 128
	snapshotWriteSize = 64 << 10
)

// SnapshotMeta describes a complete standalone SQLite snapshot.
type SnapshotMeta struct {
	Size          int64
	SHA256        string
	SchemaVersion int
}

type onlineBackuper interface {
	NewBackup(string) (*sqlite.Backup, error)
}

type databaseSerializer interface {
	Serialize() ([]byte, error)
}

// Snapshot writes a consistent, standalone image of the control database.
// The online backup is sourced from a connection owned by Store and never
// reopens the source database by path. Snapshots are limited to 256 MiB because
// modernc's serialization API materializes the complete image in memory.
func (s *Store) Snapshot(ctx context.Context, destination io.Writer) (SnapshotMeta, error) {
	if ctx == nil {
		return SnapshotMeta{}, fmt.Errorf("controlstore: snapshot context is required")
	}
	if destination == nil {
		return SnapshotMeta{}, fmt.Errorf("controlstore: snapshot destination is required")
	}
	if err := ctx.Err(); err != nil {
		return SnapshotMeta{}, fmt.Errorf("controlstore: snapshot: %w", err)
	}

	select {
	case s.snapshotGate <- struct{}{}:
		defer func() { <-s.snapshotGate }()
	case <-ctx.Done():
		return SnapshotMeta{}, fmt.Errorf("controlstore: snapshot: %w", ctx.Err())
	}

	var image []byte
	var schemaVersion int
	var err error
	func() {
		s.db.SetMaxOpenConns(2)
		defer s.db.SetMaxOpenConns(1)
		image, schemaVersion, err = s.captureSnapshot(ctx)
	}()
	if err != nil {
		return SnapshotMeta{}, err
	}
	if err := writeSnapshot(ctx, destination, image); err != nil {
		return SnapshotMeta{}, err
	}

	sum := sha256.Sum256(image)
	return SnapshotMeta{
		Size:          int64(len(image)),
		SHA256:        "sha256:" + hex.EncodeToString(sum[:]),
		SchemaVersion: schemaVersion,
	}, nil
}

func (s *Store) captureSnapshot(ctx context.Context) (image []byte, schemaVersion int, finalErr error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("controlstore: snapshot: acquire owner connection: %w", err)
	}
	defer func() {
		finalErr = errors.Join(finalErr, conn.Close())
	}()

	var pageCount, pageSize int64
	if err := conn.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pageCount); err != nil {
		return nil, 0, fmt.Errorf("controlstore: snapshot: read page count: %w", err)
	}
	if err := conn.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		return nil, 0, fmt.Errorf("controlstore: snapshot: read page size: %w", err)
	}
	if err := validateSnapshotPageSize(pageCount, pageSize); err != nil {
		return nil, 0, err
	}
	if err := conn.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&schemaVersion); err != nil {
		return nil, 0, fmt.Errorf("controlstore: snapshot: read schema version: %w", err)
	}

	err = conn.Raw(func(driverConn any) (rawErr error) {
		backuper, ok := driverConn.(onlineBackuper)
		if !ok {
			return fmt.Errorf("sqlite driver does not support online backup")
		}
		backup, startErr := backuper.NewBackup(":memory:")
		if startErr != nil {
			return fmt.Errorf("start online backup: %w", startErr)
		}
		backupConsumed := false
		defer func() {
			if !backupConsumed {
				rawErr = errors.Join(rawErr, backup.Finish())
			}
		}()

		for more := true; more; {
			if err := ctx.Err(); err != nil {
				return err
			}
			var stepErr error
			more, stepErr = backup.Step(snapshotStepPages)
			if stepErr != nil {
				return fmt.Errorf("step online backup: %w", stepErr)
			}
			if more && s.afterSnapshotStep != nil {
				s.afterSnapshotStep()
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		backupConsumed = true
		memoryConn, commitErr := backup.Commit()
		if commitErr != nil {
			return fmt.Errorf("finish online backup: %w", commitErr)
		}
		defer func() {
			rawErr = errors.Join(rawErr, memoryConn.Close())
		}()

		serializer, ok := memoryConn.(databaseSerializer)
		if !ok {
			return fmt.Errorf("sqlite driver does not support serialization")
		}
		image, rawErr = serializer.Serialize()
		if rawErr != nil {
			return fmt.Errorf("serialize online backup: %w", rawErr)
		}
		if len(image) > maxSnapshotBytes {
			return fmt.Errorf("snapshot exceeds %d-byte limit", maxSnapshotBytes)
		}
		return nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("controlstore: snapshot: %w", err)
	}
	return image, schemaVersion, nil
}

func validateSnapshotPageSize(pageCount, pageSize int64) error {
	if pageCount < 0 || pageSize <= 0 {
		return fmt.Errorf("controlstore: snapshot has invalid page count or size")
	}
	if pageCount > 0 && pageSize > int64(maxSnapshotBytes)/pageCount {
		return fmt.Errorf("controlstore: snapshot exceeds %d-byte limit", maxSnapshotBytes)
	}
	return nil
}

func writeSnapshot(ctx context.Context, destination io.Writer, image []byte) error {
	for offset := 0; offset < len(image); {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("controlstore: snapshot: %w", err)
		}
		end := min(offset+snapshotWriteSize, len(image))
		written, err := destination.Write(image[offset:end])
		if err != nil {
			return fmt.Errorf("controlstore: snapshot: write: %w", err)
		}
		if written != end-offset {
			return fmt.Errorf("controlstore: snapshot: write: %w", io.ErrShortWrite)
		}
		offset = end
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("controlstore: snapshot: %w", err)
	}
	return nil
}
