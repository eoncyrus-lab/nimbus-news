package controlstore

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type migration struct {
	version  int
	name     string
	contents string
	checksum string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("controlstore: read embedded migrations: %w", err)
	}

	migrations := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		var version int
		if _, err := fmt.Sscanf(entry.Name(), "%03d_", &version); err != nil || version <= 0 {
			return nil, fmt.Errorf("controlstore: invalid migration name %q", entry.Name())
		}
		contents, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("controlstore: read migration %q: %w", entry.Name(), err)
		}
		sum := sha256.Sum256(contents)
		migrations = append(migrations, migration{
			version:  version,
			name:     entry.Name(),
			contents: string(contents),
			checksum: "sha256:" + hex.EncodeToString(sum[:]),
		})
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })
	for i := 1; i < len(migrations); i++ {
		if migrations[i-1].version == migrations[i].version {
			return nil, fmt.Errorf("controlstore: duplicate migration version %d", migrations[i].version)
		}
	}
	return migrations, nil
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			checksum TEXT NOT NULL,
			applied_at TEXT NOT NULL
		)`); err != nil {
		return fmt.Errorf("controlstore: initialize schema migrations: %w", err)
	}

	migrations, err := loadMigrations()
	if err != nil {
		return err
	}
	known := make(map[int]migration, len(migrations))
	for _, item := range migrations {
		known[item.version] = item
	}

	rows, err := s.db.QueryContext(ctx, `SELECT version, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		return fmt.Errorf("controlstore: list applied migrations: %w", err)
	}
	applied := make(map[int]string)
	for rows.Next() {
		var version int
		var checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			rows.Close()
			return fmt.Errorf("controlstore: scan applied migration: %w", err)
		}
		item, ok := known[version]
		if !ok {
			rows.Close()
			return fmt.Errorf("controlstore: database contains unknown migration version %d", version)
		}
		if checksum != item.checksum {
			rows.Close()
			return fmt.Errorf("controlstore: migration %03d checksum mismatch: database=%s embedded=%s", version, checksum, item.checksum)
		}
		applied[version] = checksum
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("controlstore: close migration rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("controlstore: iterate applied migrations: %w", err)
	}

	for _, item := range migrations {
		if _, ok := applied[item.version]; ok {
			continue
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("controlstore: begin migration %03d: %w", item.version, err)
		}
		if _, err := tx.ExecContext(ctx, item.contents); err != nil {
			tx.Rollback()
			return fmt.Errorf("controlstore: apply migration %03d (%s): %w", item.version, item.name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations(version, checksum, applied_at) VALUES (?, ?, ?)`,
			item.version, item.checksum, formatTime(s.now()),
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("controlstore: record migration %03d: %w", item.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("controlstore: commit migration %03d: %w", item.version, err)
		}
	}
	return nil
}
