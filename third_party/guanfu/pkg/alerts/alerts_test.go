package alerts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseBasicConditions(t *testing.T) {
	cases := []struct {
		in     string
		metric string
		op     string
		thresh float64
	}{
		{"mayer_multiple < 0.8", "mayer_multiple", "<", 0.8},
		{"ahr999_compressed >= 3.344", "ahr999_compressed", ">=", 3.344},
		{"cycle.mayer_multiple < 0.6", "mayer_multiple", "<", 0.6}, // domain prefix stripped
		{"sma_200w_dev < -10", "sma_200w_dev", "<", -10},
		{"funding_rate_pct > 0.08", "funding_rate_pct", ">", 0.08},
		{"rsi_14 != 50", "rsi_14", "!=", 50},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if err != nil {
			t.Errorf("Parse(%q) error: %v", c.in, err)
			continue
		}
		if got.Metric != c.metric || got.Operator != c.op || got.Threshold != c.thresh {
			t.Errorf("Parse(%q) = {%s %s %v}, want {%s %s %v}",
				c.in, got.Metric, got.Operator, got.Threshold, c.metric, c.op, c.thresh)
		}
	}
}

func TestParseRejectsBadExpressions(t *testing.T) {
	for _, bad := range []string{"", "no_op", "< 5", "metric <", "metric < notanum"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) should have errored", bad)
		}
	}
}

func TestEvaluateAllOperators(t *testing.T) {
	c := &Condition{Metric: "x", Threshold: 10}
	cases := []struct {
		op       string
		observed float64
		want     bool
	}{
		{"<", 9, true}, {"<", 10, false}, {"<", 11, false},
		{"<=", 10, true}, {"<=", 10.01, false},
		{">", 11, true}, {">", 10, false},
		{">=", 10, true}, {">=", 9, false},
		{"=", 10, true}, {"=", 10.1, false},
		{"!=", 10, false}, {"!=", 9, true},
	}
	for _, tc := range cases {
		c.Operator = tc.op
		if got := c.Evaluate(tc.observed); got != tc.want {
			t.Errorf("x %s 10, observed %v → %v want %v", tc.op, tc.observed, got, tc.want)
		}
	}
}

func TestStoreRecordAndList(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 5, 9, 12, 0, 0, 0, time.UTC)
	path, err := s.Record(Alert{
		Triggered:     now,
		Asset:         "BTC",
		Expression:    "mayer < 0.8",
		Metric:        "mayer_multiple",
		Operator:      "<",
		Threshold:     0.8,
		ObservedValue: 0.77,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, ".json") {
		t.Errorf("path should end .json: %s", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file should exist: %v", err)
	}
	// filename should contain asset normalized to lowercase
	if !strings.Contains(filepath.Base(path), "-btc-") {
		t.Errorf("filename missing asset slug: %s", path)
	}

	got, err := s.List(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("list = %d, want 1", len(got))
	}
	if got[0].Asset != "BTC" || got[0].ObservedValue != 0.77 {
		t.Errorf("roundtrip lost data: %#v", got[0])
	}
}

func TestStoreListFilterSinceTime(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	s.Record(Alert{Triggered: old, Asset: "btc"})
	s.Record(Alert{Triggered: recent, Asset: "btc"})
	got, _ := s.List(time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	if len(got) != 1 {
		t.Fatalf("since filter failed: got %d", len(got))
	}
	if !got[0].Triggered.Equal(recent) {
		t.Errorf("wrong record kept: %v", got[0].Triggered)
	}
}

func TestRecordRequiresAsset(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	if _, err := s.Record(Alert{Asset: ""}); err == nil {
		t.Error("empty Asset should fail")
	}
}

func TestDispatchStdoutIsNoError(t *testing.T) {
	a := Alert{Asset: "btc", Metric: "m", Operator: "<", Threshold: 1, ObservedValue: 0.5}
	ch, err := Dispatch(a, "stdout")
	if err != nil || ch != "stdout" {
		t.Errorf("stdout dispatch failed: %v %v", ch, err)
	}
	// Empty channel defaults to stdout.
	ch, err = Dispatch(a, "")
	if err != nil || ch != "stdout" {
		t.Errorf("default dispatch failed: %v %v", ch, err)
	}
	// Unknown channel errors.
	if _, err := Dispatch(a, "carrier-pigeon"); err == nil {
		t.Error("unknown channel should error")
	}
}
