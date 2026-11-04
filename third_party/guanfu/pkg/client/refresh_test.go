package client

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

// fakeSource lets us drive RefreshAll without hitting the network.
type fakeSource struct {
	key, name string
	mode      string
	added     int
	err       error
}

func (f fakeSource) Key() string         { return f.key }
func (f fakeSource) DisplayName() string { return f.name }
func (f fakeSource) Refresh(_ context.Context, _ *store.PriceStore) (*RefreshResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &RefreshResult{
		Key: f.key, DisplayName: f.name,
		Mode: f.mode, Added: f.added, Total: 100, LastDate: "2026-05-09",
	}, nil
}

func TestRefreshAllReportsPerSourceOutcomes(t *testing.T) {
	s := &store.PriceStore{Dir: t.TempDir()}
	sources := []Source{
		fakeSource{key: "a", name: "Source A", mode: "incremental", added: 5},
		fakeSource{key: "b", name: "Source B", mode: "skip"},
		fakeSource{key: "c", name: "Source C", err: errors.New("boom")},
	}
	results := RefreshAll(context.Background(), s, sources)
	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}
	if results[0].Mode != "incremental" || results[0].Added != 5 {
		t.Errorf("source A: %+v", results[0])
	}
	if results[1].Mode != "skip" {
		t.Errorf("source B: %+v", results[1])
	}
	if results[2].Mode != "fail" || !strings.Contains(results[2].Error, "boom") {
		t.Errorf("source C: %+v", results[2])
	}
	for _, r := range results {
		if r.Duration == "" {
			t.Errorf("%s: missing duration", r.Key)
		}
	}
}

func TestStaleThresholdHonors24h(t *testing.T) {
	s := &store.PriceStore{Dir: t.TempDir()}
	// No data yet → stale
	stale, last := staleThreshold(s, "missing")
	if !stale || last != "" {
		t.Errorf("missing data: stale=%v last=%q, want stale=true last=\"\"", stale, last)
	}

	// Save fresh point — should be not stale
	today := time.Now().UTC().Format("2006-01-02")
	if err := s.Save("fresh", []store.PricePoint{{Date: today, Close: 100}}); err != nil {
		t.Fatal(err)
	}
	stale, last = staleThreshold(s, "fresh")
	if stale {
		t.Errorf("fresh data: stale=true, want false; last=%s", last)
	}

	// Save 3-day-old point — should be stale
	old := time.Now().AddDate(0, 0, -3).UTC().Format("2006-01-02")
	if err := s.Save("stale", []store.PricePoint{{Date: old, Close: 100}}); err != nil {
		t.Fatal(err)
	}
	stale, _ = staleThreshold(s, "stale")
	if !stale {
		t.Errorf("3d-old data: stale=false, want true")
	}
}

func TestFormatRefreshTableIncludesAllRows(t *testing.T) {
	results := []*RefreshResult{
		{Key: "btc", Mode: "skip", Total: 5775, LastDate: "2026-05-09"},
		{Key: "fred_dxy", Mode: "incremental", Added: 3, Total: 5100, LastDate: "2026-05-08"},
		{Key: "broken", Mode: "fail", Error: "DNS lookup failed"},
	}
	out := FormatRefreshTable(results)
	for _, want := range []string{"btc", "fred_dxy", "broken", "5775", "5100", "FAIL", "DNS lookup failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
}
