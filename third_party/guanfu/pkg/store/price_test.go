package store

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func tempStore(t *testing.T) *PriceStore {
	t.Helper()
	dir := t.TempDir()
	return &PriceStore{Dir: dir}
}

func TestSaveAndLoad(t *testing.T) {
	s := tempStore(t)
	points := []PricePoint{
		{Date: "2026-05-01", Close: 100, Source: "test"},
		{Date: "2026-05-02", Close: 101, Source: "test"},
		{Date: "2026-05-03", Close: 102, Source: "test"},
	}

	if err := s.Save("btc", points); err != nil {
		t.Fatal(err)
	}

	loaded, err := s.Load("btc")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 3 {
		t.Fatalf("expected 3 points, got %d", len(loaded))
	}
	if loaded[0].Date != "2026-05-01" || loaded[0].Close != 100 {
		t.Fatalf("oldest first assertion failed: %+v", loaded[0])
	}
}

func TestAppendAndDedup(t *testing.T) {
	s := tempStore(t)
	points := []PricePoint{
		{Date: "2026-05-01", Close: 100, Source: "test"},
		{Date: "2026-05-02", Close: 101, Source: "test"},
	}

	if err := s.Save("btc", points); err != nil {
		t.Fatal(err)
	}

	// Append with overlap + new data
	newPoints := []PricePoint{
		{Date: "2026-05-02", Close: 999, Source: "updated"}, // should overwrite
		{Date: "2026-05-03", Close: 103, Source: "test"},
	}
	if err := s.Append("btc", newPoints); err != nil {
		t.Fatal(err)
	}

	loaded, _ := s.Load("btc")
	if len(loaded) != 3 {
		t.Fatalf("expected 3 points, got %d", len(loaded))
	}
	// 2026-05-02 should have the updated value
	for _, p := range loaded {
		if p.Date == "2026-05-02" && p.Close != 999 {
			t.Fatalf("expected 05-02 to be overwritten with 999, got %f", p.Close)
		}
	}
}

func TestLoadNonExistent(t *testing.T) {
	s := tempStore(t)
	points, err := s.Load("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if points != nil {
		t.Fatal("expected nil points for nonexistent asset")
	}
}

func TestLastDate(t *testing.T) {
	s := tempStore(t)
	points := []PricePoint{
		{Date: "2026-01-01", Close: 100, Source: "test"},
		{Date: "2026-05-05", Close: 200, Source: "test"},
	}
	s.Save("btc", points)

	lastDate, err := s.LastDate("btc")
	if err != nil {
		t.Fatal(err)
	}
	if lastDate != "2026-05-05" {
		t.Fatalf("expected 2026-05-05, got %s", lastDate)
	}
}

func TestLastDateEmpty(t *testing.T) {
	s := tempStore(t)
	lastDate, err := s.LastDate("empty")
	if err != nil || lastDate != "" {
		t.Fatalf("expected empty last date, got %s err=%v", lastDate, err)
	}
}

func TestCount(t *testing.T) {
	s := tempStore(t)
	points := makePointsDistinct(100)
	s.Save("btc", points)

	count, _ := s.Count("btc")
	if count != 100 {
		t.Fatalf("expected 100, got %d", count)
	}
}

func TestLoadHistory(t *testing.T) {
	s := tempStore(t)
	points := []PricePoint{
		{Date: "2026-05-01", Close: 100, Source: "test"},
		{Date: "2026-05-02", Close: 200, Source: "test"},
		{Date: "2026-05-03", Close: 300, Source: "test"},
	}
	s.Save("btc", points)

	history, err := s.LoadHistory("btc")
	if err != nil {
		t.Fatal(err)
	}
	// Should be newest-first: [300, 200, 100]
	if len(history) != 3 {
		t.Fatalf("expected 3, got %d", len(history))
	}
	if history[0] != 300 || history[1] != 200 || history[2] != 100 {
		t.Fatalf("expected newest-first [300, 200, 100], got %v", history)
	}
}

func TestMeta(t *testing.T) {
	s := tempStore(t)
	points := []PricePoint{
		{Date: "2026-05-01", Close: 100, Source: "test"},
	}
	s.Save("btc", points)

	meta, ok := s.GetMeta("btc")
	if !ok {
		t.Fatal("expected meta to exist")
	}
	if meta.Count != 1 {
		t.Fatalf("expected count 1, got %d", meta.Count)
	}
	if meta.LastDate != "2026-05-01" {
		t.Fatalf("expected last_date 2026-05-01, got %s", meta.LastDate)
	}
}

func TestListAssets(t *testing.T) {
	s := tempStore(t)
	s.Save("btc", []PricePoint{{Date: "2026-01-01", Close: 100}})
	s.Save("qqq", []PricePoint{{Date: "2026-01-01", Close: 300}})

	assets, err := s.ListAssets()
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 2 {
		t.Fatalf("expected 2 assets, got %d", len(assets))
	}
}

func TestInvalidCloseFiltered(t *testing.T) {
	s := tempStore(t)
	points := []PricePoint{
		{Date: "2026-05-01", Close: 100},
		{Date: "2026-05-02", Close: 0},   // should be filtered
		{Date: "2026-05-03", Close: -10}, // should be filtered
		{Date: "2026-05-04", Close: 200},
	}
	s.Save("btc", points)

	loaded, _ := s.Load("btc")
	if len(loaded) != 2 {
		t.Fatalf("expected 2 valid points, got %d", len(loaded))
	}
}

func TestSignedSeriesPreservesNegativeAndZeroValues(t *testing.T) {
	s := tempStore(t)
	points := []PricePoint{
		{Date: "2026-05-01", Close: 1.2, Source: "test"},
		{Date: "2026-05-02", Close: 0, Source: "test"},
		{Date: "2026-05-03", Close: -2.5, Source: "test"},
	}
	if err := s.SaveSeries("deribit_skew_25d_pct", points); err != nil {
		t.Fatal(err)
	}

	loaded, err := s.LoadSeries("deribit_skew_25d_pct")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 3 {
		t.Fatalf("expected 3 signed points, got %d: %+v", len(loaded), loaded)
	}
	if loaded[1].Close != 0 || loaded[2].Close != -2.5 {
		t.Fatalf("signed values not preserved: %+v", loaded)
	}

	latest, ok := s.LatestSeries("deribit_skew_25d_pct")
	if !ok || latest.Close != -2.5 {
		t.Fatalf("LatestSeries = %+v ok=%v, want -2.5", latest, ok)
	}
}

func TestIncrementalFetchDays(t *testing.T) {
	s := tempStore(t)
	// No data → full import needed
	days := s.IncrementalFetchDays("btc", 3000)
	if days != 3000 {
		t.Fatalf("expected 3000 for empty asset, got %d", days)
	}

	// Use today's date so the test isn't sensitive to wall-clock drift
	// between when it was written and when it runs. Previously hard-coded
	// "2026-05-05" failed as soon as now() drifted more than 1 day past it.
	today := time.Now().UTC().Format("2006-01-02")
	s.Save("btc", []PricePoint{{Date: today, Close: 100, Source: "test"}})
	days = s.IncrementalFetchDays("btc", 3000)
	if days != 0 {
		t.Fatalf("expected 0 for fresh data (today), got %d", days)
	}
}

func TestNeedsFullImport(t *testing.T) {
	s := tempStore(t)
	if !s.NeedsFullImport("btc", 100) {
		t.Fatal("expected true for empty asset")
	}

	s.Save("btc", makePointsDistinct(200))
	if s.NeedsFullImport("btc", 100) {
		t.Fatal("expected false when count >= min")
	}
}

func makePointsDistinct(n int) []PricePoint {
	points := make([]PricePoint, n)
	for i := 0; i < n; i++ {
		m := (i%365)/30 + 1
		day := i%30 + 1
		points[i] = PricePoint{
			Date:  fmt.Sprintf("2026-%02d-%02d", m, day),
			Close: float64(i + 1),
		}
	}
	return points
}

func TestFilePersistence(t *testing.T) {
	dir := t.TempDir()
	s := &PriceStore{Dir: dir}

	s.Save("btc", []PricePoint{{Date: "2026-05-01", Close: 100}})

	// Verify file exists
	path := filepath.Join(dir, "btc.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("btc.json not created")
	}

	// New store instance should load same data
	s2 := &PriceStore{Dir: dir}
	loaded, _ := s2.Load("btc")
	if len(loaded) != 1 || loaded[0].Close != 100 {
		t.Fatal("data not persisted correctly")
	}
}

func TestNormalizeDuplicateEntries(t *testing.T) {
	points := []PricePoint{
		{Date: "2026-05-01", Close: 100},
		{Date: "2026-05-01", Close: 200},
		{Date: "2026-05-02", Close: 300},
	}
	normalized := NormalizePricePoints(points)
	if len(normalized) != 2 {
		t.Fatalf("expected 2 after dedup, got %d", len(normalized))
	}
	// Last write wins for same date
	for _, p := range normalized {
		if p.Date == "2026-05-01" && p.Close != 200 {
			t.Fatalf("expected 05-01 to be 200 (last write wins), got %f", p.Close)
		}
	}
}
