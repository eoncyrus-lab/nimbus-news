package history

import (
	"path/filepath"
	"testing"
)

func TestRecordAndQuantile(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Write 10 days, values 1..10 via RecordMany.
	dates := []string{
		"2026-04-23", "2026-04-24", "2026-04-25", "2026-04-26", "2026-04-27",
		"2026-04-28", "2026-04-29", "2026-04-30", "2026-05-01", "2026-05-02",
	}
	for i, d := range dates {
		if err := s.RecordMany(d, map[string]float64{"test_metric": float64(i + 1)}); err != nil {
			t.Fatal(err)
		}
	}

	// Overwrite the last day: value 999 should replace 10.
	if err := s.RecordMany("2026-05-02", map[string]float64{"test_metric": 999}); err != nil {
		t.Fatal(err)
	}

	// value=5 → 5/10 = 0.5 quantile
	q, count, err := s.QuantileAsOf("test_metric", 5, 730, "2026-05-02")
	if err != nil {
		t.Fatal(err)
	}
	if count != 10 {
		t.Fatalf("expected 10 count, got %d", count)
	}
	if q != 0.5 {
		t.Fatalf("expected q=0.5, got %f", q)
	}

	// value=11 below the overwritten 999 → 9/10 = 0.9
	q, _, _ = s.QuantileAsOf("test_metric", 11, 730, "2026-05-02")
	if q != 0.9 {
		t.Fatalf("expected q=0.9, got %f", q)
	}

	// value=0 → 0% quantile
	q, _, _ = s.QuantileAsOf("test_metric", 0, 730, "2026-05-02")
	if q != 0 {
		t.Fatalf("expected q=0, got %f", q)
	}
}

func TestQuantileAsOfExcludesFutureRows(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for _, row := range []struct {
		date  string
		value float64
	}{
		{date: "2026-05-01", value: 1},
		{date: "2026-05-02", value: 2},
		{date: "2026-05-03", value: 100},
	} {
		if err := s.RecordMany(row.date, map[string]float64{"test_metric": row.value}); err != nil {
			t.Fatal(err)
		}
	}

	q, count, err := s.QuantileAsOf("test_metric", 2, 730, "2026-05-02")
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected 2 samples as of 2026-05-02, got %d", count)
	}
	if q != 1 {
		t.Fatalf("expected q=1 without future row, got %f", q)
	}
}

func TestRecordMany(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.RecordMany("2026-05-02", map[string]float64{
		"a": 1, "b": 2, "c": 3,
	}); err != nil {
		t.Fatal(err)
	}

	// Verify each key was written: quantile of value+1 should be 1.0 (only one row)
	for _, k := range []string{"a", "b", "c"} {
		_, count, err := s.QuantileAsOf(k, 999, 730, "2026-05-02")
		if err != nil {
			t.Fatalf("key %s: %v", k, err)
		}
		if count != 1 {
			t.Fatalf("key %s: expected 1 sample, got %d", k, count)
		}
	}
}

func TestRecordManyOverwritesSameDayValues(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.RecordMany("2026-05-02", map[string]float64{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordMany("2026-05-02", map[string]float64{"a": 2}); err != nil {
		t.Fatal(err)
	}

	q, count, err := s.QuantileAsOf("a", 1.5, 730, "2026-05-02")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 sample, got %d", count)
	}
	if q != 0 {
		t.Fatalf("expected overwritten value 2 to be above 1.5, got q=%f", q)
	}
}
