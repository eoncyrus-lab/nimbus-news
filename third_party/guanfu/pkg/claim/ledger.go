// Ledger is the disk persistence layer for Claim records.
//
// Storage layout:
//
//   $GUANFU_CLAIMS_DIR/                 (default ~/.guanfu/claims)
//   ├── claims/
//   │   └── YYYY-MM/
//   │       └── YYYY-MM-DD-{asset}-{horizon}-{short_id}.json
//       └── YYYY-MM/
//           └── YYYY-MM-DD-{asset}-{short_id}.json
//
// Why a filesystem ledger and not SQLite:
//   - Transparency: user can `cat` / `grep` any record without a client.
//   - Resilience: a corrupt record cannot break the ledger — at worst we
//     skip it on List() and log a warning.
//   - Shardability: month directories keep listings O(1) for recent work.
//   - No schema migration: additive-only Claim fields never require
//     rewriting existing files.
//
// Concurrency: Record writes a new filename every call (timestamp + pid-ish
// suffix), so two processes won't collide. No file locking.

package claim

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Ledger owns the on-disk claim/intent directory tree.
type Ledger struct {
	root string // absolute path to the ledger root
}

// Open returns a Ledger rooted at the given directory, creating the
// claims/ subtree on first use. An empty root resolves to
// $GUANFU_CLAIMS_DIR (if set) or $HOME/.guanfu/claims.
func Open(root string) (*Ledger, error) {
	if strings.TrimSpace(root) == "" {
		if env := os.Getenv("GUANFU_CLAIMS_DIR"); env != "" {
			root = env
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("resolve home: %w", err)
			}
			root = filepath.Join(home, ".guanfu", "claims")
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	for _, sub := range []string{"claims"} {
		if err := os.MkdirAll(filepath.Join(abs, sub), 0o755); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", sub, err)
		}
	}
	return &Ledger{root: abs}, nil
}

// Disabled reports whether claim ledger writes are turned off via env.
func Disabled() bool {
	return os.Getenv("GUANFU_NO_CLAIMS") == "1"
}

// NewID returns a time-sortable, random-suffixed id suitable for Claim.ID
// Format: YYYYMMDDTHHMMSS-{8 hex} in UTC.
func NewID(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	return t.UTC().Format("20060102T150405") + "-" + hex.EncodeToString(b[:])
}

// RecordClaim writes a Claim to disk. The Claim's ID / SchemaVersion are
// filled in if empty. AsOf is used for directory sharding; if zero, now.
func (l *Ledger) RecordClaim(c Claim) (string, error) {
	if c.AsOf.IsZero() {
		c.AsOf = time.Now().UTC()
	}
	if c.ID == "" {
		c.ID = NewID(c.AsOf)
	}
	if c.SchemaVersion == 0 {
		c.SchemaVersion = SchemaVersion
	}
	if c.Asset == "" {
		return "", fmt.Errorf("claim: Asset is required")
	}
	if c.Horizon <= 0 {
		return "", fmt.Errorf("claim: Horizon must be > 0")
	}

	dateDir := c.AsOf.UTC().Format("2006-01")
	dir := filepath.Join(l.root, "claims", dateDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	short := shortID(c.ID)
	name := fmt.Sprintf("%s-%s-%d-%s.json",
		c.AsOf.UTC().Format("2006-01-02"),
		sanitizeAsset(c.Asset),
		c.Horizon,
		short)
	path := filepath.Join(dir, name)
	return path, writeJSON(path, c)
}

// ListClaims returns every Claim under the ledger root, sorted by AsOf
// ascending. Filter is applied in-memory. A corrupt JSON file is silently
// skipped (logged via warnOut).
func (l *Ledger) ListClaims(filter func(Claim) bool) ([]Claim, error) {
	var out []Claim
	err := walkJSON(filepath.Join(l.root, "claims"), func(p string, data []byte) {
		var c Claim
		if err := json.Unmarshal(data, &c); err != nil {
			warnOut("skip corrupt claim %s: %v\n", p, err)
			return
		}
		if filter != nil && !filter(c) {
			return
		}
		out = append(out, c)
	})
	sort.Slice(out, func(i, j int) bool { return out[i].AsOf.Before(out[j].AsOf) })
	return out, err
}

// ── internals ──────────────────────────────────────────

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func walkJSON(root string, fn func(path string, data []byte)) error {
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".json") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			warnOut("skip unreadable %s: %v\n", p, err)
			return nil
		}
		fn(p, data)
		return nil
	})
}

// shortID returns the last 8 chars of an ID for filenames.
func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[len(id)-8:]
}

// sanitizeAsset replaces characters that aren't portable filename
// components. stock_AAPL → stock_aapl; anything else → underscore.
func sanitizeAsset(a string) string {
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

// warnOut is the single place we emit a warning for corrupt records. Tests
// swap this via init() if they need silence; production sends to stderr.
var warnOut = func(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "claim ledger: "+format, args...)
}
