package main

import (
	"math"
	"testing"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

func TestToValidPoints_DropsArtifacts(t *testing.T) {
	in := []store.PricePoint{
		{Date: "", Close: 714.51},        // empty-date artifact that survives store normalization
		{Date: "2026-05-22", Close: 717}, // good
		{Date: "2026-05-20", Close: 0},   // non-positive
		{Date: "2026", Close: 100},       // short date that would panic Date[:7]
		{Date: "2026-05-21", Close: 715}, // good, out of order
	}
	out := toValidPoints(in)
	if len(out) != 2 {
		t.Fatalf("expected 2 valid points, got %d: %+v", len(out), out)
	}
	// sorted ascending
	if out[0].Date != "2026-05-21" || out[1].Date != "2026-05-22" {
		t.Fatalf("expected sorted [2026-05-21, 2026-05-22], got %+v", out)
	}
	for _, p := range out {
		if len(p.Date) < 10 {
			t.Fatalf("invalid date survived: %q", p.Date)
		}
	}
}

func TestPercentileOf(t *testing.T) {
	s := []float64{1, 2, 3, 4, 5}
	cases := []struct {
		v    float64
		want float64
	}{
		{0, 0.0}, // below all
		{3, 0.6}, // 3 of 5 are <= 3
		{5, 1.0}, // all <= 5
		{6, 1.0}, // above all
	}
	for _, c := range cases {
		if got := percentileOf(s, c.v); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("percentileOf(%v)=%v want %v", c.v, got, c.want)
		}
	}
	if got := percentileOf(nil, 1); got != 0.5 {
		t.Errorf("empty samples should return 0.5, got %v", got)
	}
}

func TestMultiplierFor_MonotoneAndBounded(t *testing.T) {
	prev := -1.0
	for s := 0; s <= 100; s += 5 {
		m := multiplierFor(s)
		if m < 0.3-1e-9 || m > 2.5+1e-9 {
			t.Errorf("multiplier %v at score %d out of [0.3,2.5]", m, s)
		}
		if m < prev-1e-9 {
			t.Errorf("multiplier not monotone: score %d gave %v < prev %v", s, m, prev)
		}
		prev = m
	}
	// anchor checks: cheap=high score → bigger multiplier
	if multiplierFor(0) >= multiplierFor(100) {
		t.Error("score 0 should map to a smaller multiplier than score 100")
	}
	if m := multiplierFor(50); math.Abs(m-1.0) > 1e-9 {
		t.Errorf("score 50 should anchor at 1.0x, got %v", m)
	}
}
