// Package alerts persists condition-triggered notifications to
// ~/.guanfu/alerts/ (Track L5, MVP).
//
// Philosophy: MVP is a one-shot evaluate-then-record. `guanfu watch` is
// intended to be driven by cron / launchd; the CLI does not run a
// background daemon. That keeps the operational surface minimal and
// matches how serious traders already schedule stuff.
//
// An Alert is an append-only record. We never mutate past files; if a
// condition fires repeatedly, it generates multiple records with
// distinct timestamps.

package alerts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const SchemaVersion = 1

// Alert is one condition-trigger record.
type Alert struct {
	ID            string    `json:"id"`
	Triggered     time.Time `json:"triggered"`
	Asset         string    `json:"asset"`
	Expression    string    `json:"expression"`       // the raw --when clause
	Metric        string    `json:"metric,omitempty"` // parsed operand (for filter/grouping)
	Operator      string    `json:"operator,omitempty"`
	Threshold     float64   `json:"threshold,omitempty"`
	ObservedValue float64   `json:"observed_value"`
	Dispatched    []string  `json:"dispatched,omitempty"` // e.g. ["osascript"]
	Note          string    `json:"note,omitempty"`
	SchemaVersion int       `json:"schema_version"`
}

// Store owns the on-disk alerts directory.
type Store struct {
	root string
}

// Open returns a Store rooted at the given dir. Empty arg resolves via
// GUANFU_ALERTS_DIR or ~/.guanfu/alerts.
func Open(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		if env := os.Getenv("GUANFU_ALERTS_DIR"); env != "" {
			root = env
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("resolve home: %w", err)
			}
			root = filepath.Join(home, ".guanfu", "alerts")
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}
	return &Store{root: abs}, nil
}

// Record writes an Alert. Fills in SchemaVersion/ID/Triggered if zero.
// Returns the written path.
func (s *Store) Record(a Alert) (string, error) {
	if a.Triggered.IsZero() {
		a.Triggered = time.Now().UTC()
	}
	if a.ID == "" {
		a.ID = a.Triggered.Format("20060102T150405")
	}
	if a.SchemaVersion == 0 {
		a.SchemaVersion = SchemaVersion
	}
	if a.Asset == "" {
		return "", fmt.Errorf("alert: Asset required")
	}
	monthDir := filepath.Join(s.root, a.Triggered.Format("2006-01"))
	if err := os.MkdirAll(monthDir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s-%s-%s.json",
		a.Triggered.Format("2006-01-02"),
		sanitize(a.Asset),
		a.ID)
	path := filepath.Join(monthDir, name)
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, b, 0o644)
}

// List returns all alerts filtered by sinceTime (inclusive). sinceTime
// zero returns everything. Sorted by Triggered ascending.
func (s *Store) List(since time.Time) ([]Alert, error) {
	var out []Alert
	err := filepath.WalkDir(s.root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		var a Alert
		if jerr := json.Unmarshal(data, &a); jerr != nil {
			return nil
		}
		if !since.IsZero() && a.Triggered.Before(since) {
			return nil
		}
		out = append(out, a)
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Triggered.Before(out[j].Triggered) })
	return out, err
}

// sanitize normalizes asset-as-filename. Same rule as pkg/claim.
func sanitize(a string) string {
	a = strings.ToLower(strings.TrimSpace(a))
	b := make([]rune, 0, len(a))
	for _, r := range a {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b = append(b, r)
		default:
			b = append(b, '_')
		}
	}
	return string(b)
}
