// K5: ledger integrity tests. These guard:
//   - schema version applied
//   - AsOf filled when zero
//   - ID generation time-sortable
//   - filename encodes date + asset + horizon + id suffix
//   - List*() returns records sorted by AsOf
//   - Corrupt records are skipped, not panicked
//   - Disabled env flag is honored by caller paths (we only test the helper)

package claim

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestLedger(t *testing.T) *Ledger {
	t.Helper()
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	return l
}

func TestRecordClaimFillsIdentityFields(t *testing.T) {
	l := newTestLedger(t)
	path, err := l.RecordClaim(Claim{
		Asset:          "btc",
		Horizon:        90,
		PriceAtClaim:   78000,
		IntervalLow:    -0.05,
		IntervalHigh:   0.18,
		ExpectedReturn: 0.05,
	})
	if err != nil {
		t.Fatalf("RecordClaim: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got Claim
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", got.SchemaVersion, SchemaVersion)
	}
	if got.ID == "" {
		t.Error("ID must be filled")
	}
	if got.AsOf.IsZero() {
		t.Error("AsOf must be filled")
	}
	if !strings.HasSuffix(path, ".json") {
		t.Errorf("path should end .json: %s", path)
	}
	// filename must encode date + asset + horizon
	base := filepath.Base(path)
	if !strings.Contains(base, "-btc-90-") {
		t.Errorf("filename should contain asset+horizon: %s", base)
	}
}

func TestRecordClaimRequiresAssetAndHorizon(t *testing.T) {
	l := newTestLedger(t)
	if _, err := l.RecordClaim(Claim{Horizon: 30}); err == nil {
		t.Error("empty Asset should fail")
	}
	if _, err := l.RecordClaim(Claim{Asset: "btc"}); err == nil {
		t.Error("zero Horizon should fail")
	}
}

func TestNewIDIsTimeSortable(t *testing.T) {
	t1 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	id1 := NewID(t1)
	id2 := NewID(t2)
	if id1 >= id2 {
		t.Errorf("IDs not time-sortable: %s !< %s", id1, id2)
	}
	if !strings.HasPrefix(id1, "20260101T") {
		t.Errorf("id1 prefix wrong: %s", id1)
	}
}

func TestListClaimsSortedByAsOf(t *testing.T) {
	l := newTestLedger(t)
	dates := []time.Time{
		time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC),
	}
	for _, d := range dates {
		if _, err := l.RecordClaim(Claim{
			Asset: "btc", Horizon: 30, AsOf: d, PriceAtClaim: 1000,
		}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := l.ListClaims(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 claims, got %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].AsOf.Before(got[i-1].AsOf) {
			t.Errorf("claims not sorted: %v before %v", got[i].AsOf, got[i-1].AsOf)
		}
	}
}

func TestListClaimsAppliesFilter(t *testing.T) {
	l := newTestLedger(t)
	for _, a := range []string{"btc", "qqq", "spy"} {
		if _, err := l.RecordClaim(Claim{
			Asset: a, Horizon: 30, PriceAtClaim: 100,
		}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := l.ListClaims(func(c Claim) bool { return c.Asset == "qqq" })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Asset != "qqq" {
		t.Errorf("filter failed: %#v", got)
	}
}

func TestListClaimsSkipsCorruptFiles(t *testing.T) {
	l := newTestLedger(t)
	if _, err := l.RecordClaim(Claim{Asset: "btc", Horizon: 30, PriceAtClaim: 100}); err != nil {
		t.Fatal(err)
	}
	// Drop a garbage file into the same month directory.
	monthDir := filepath.Join(l.root, "claims", time.Now().UTC().Format("2006-01"))
	if err := os.WriteFile(filepath.Join(monthDir, "2026-99-99-garbage.json"),
		[]byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Swap warnOut to silence during test.
	origWarn := warnOut
	warnOut = func(format string, args ...any) {}
	defer func() { warnOut = origWarn }()

	got, err := l.ListClaims(nil)
	if err != nil {
		t.Fatalf("list errored on corrupt file: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("corrupt file not skipped: got %d claims", len(got))
	}
}

func TestListClaimsEmptyLedgerDoesNotError(t *testing.T) {
	l := newTestLedger(t)
	got, err := l.ListClaims(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("fresh ledger should be empty, got %d", len(got))
	}
}

func TestSanitizeAssetForFilenames(t *testing.T) {
	cases := map[string]string{
		"BTC":         "btc",
		"stock_AAPL":  "stock_aapl",
		"spooky/path": "spooky_path",
		"  qqq  ":     "qqq",
	}
	for in, want := range cases {
		if got := sanitizeAsset(in); got != want {
			t.Errorf("sanitizeAsset(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDisabledEnv(t *testing.T) {
	t.Setenv("GUANFU_NO_CLAIMS", "1")
	if !Disabled() {
		t.Error("GUANFU_NO_CLAIMS=1 should disable")
	}
	t.Setenv("GUANFU_NO_CLAIMS", "")
	if Disabled() {
		t.Error("empty env should not disable")
	}
}
