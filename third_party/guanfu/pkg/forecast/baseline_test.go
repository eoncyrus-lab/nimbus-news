package forecast

import (
	"math"
	"testing"
)

func TestFlatRateBaselineCompoundsToHorizon(t *testing.T) {
	fn := FlatRateBaseline(4.5, 6.0)
	tb, pa, ok, note := fn(90)
	if !ok {
		t.Fatal("90d should be ok")
	}
	// 4.5% annual, 90/365 yr: (1.045)^(90/365) - 1 ≈ 1.095%
	want := (math.Pow(1.045, 90.0/365) - 1) * 100
	if math.Abs(tb-want) > 0.01 {
		t.Errorf("tbill = %.4f, want %.4f", tb, want)
	}
	if pa <= tb {
		t.Errorf("passive (%.2f) should be above tbill (%.2f) for higher annual rate", pa, tb)
	}
	if note == "" {
		t.Error("note must include rate hint")
	}

	// days<=0 must return ok=false
	if _, _, ok, _ := fn(0); ok {
		t.Error("0 days must not be ok")
	}
}

func TestAnnotateBaselinesFillsFields(t *testing.T) {
	fc := &Forecast{
		Horizons: []HorizonForecast{
			{Days: 90, MedianReturnPct: 5.0},
			{Days: 180, MedianReturnPct: -1.0},
		},
	}
	AnnotateBaselines(fc, FlatRateBaseline(4.5, 6.0))
	h90 := fc.Horizons[0]
	if h90.RiskFreeReturnPct <= 0 || h90.PassiveReturnPct <= 0 {
		t.Errorf("baselines not filled: %+v", h90)
	}
	// Delta = median(5%) - max(tbill, passive) — which for 6% annual
	// compounded to 90d is ~1.45%, so delta ≈ 5 - 1.45 ≈ 3.55
	if h90.RiskAdjustedDeltaPct <= 0 {
		t.Errorf("90d expected positive delta vs baseline; got %.2f", h90.RiskAdjustedDeltaPct)
	}
	// 180d median is -1%, baseline is positive → delta must be negative
	h180 := fc.Horizons[1]
	if h180.RiskAdjustedDeltaPct >= 0 {
		t.Errorf("180d expected negative delta vs baseline; got %.2f", h180.RiskAdjustedDeltaPct)
	}
}

func TestAnnotateBaselinesNoOpOnNil(t *testing.T) {
	AnnotateBaselines(nil, FlatRateBaseline(1, 1)) // no panic
	fc := &Forecast{Horizons: []HorizonForecast{{Days: 30, MedianReturnPct: 1}}}
	AnnotateBaselines(fc, nil) // no panic; no mutation
	if fc.Horizons[0].RiskFreeReturnPct != 0 {
		t.Error("nil baseline fn should leave fields untouched")
	}
}

func TestAnnotateBaselinesSkipsNotOkHorizons(t *testing.T) {
	fn := BaselineFn(func(days int) (float64, float64, bool, string) {
		if days == 90 {
			return 1.0, 1.5, true, "test"
		}
		return 0, 0, false, ""
	})
	fc := &Forecast{Horizons: []HorizonForecast{
		{Days: 30, MedianReturnPct: 2},
		{Days: 90, MedianReturnPct: 2},
	}}
	AnnotateBaselines(fc, fn)
	if fc.Horizons[0].RiskFreeReturnPct != 0 {
		t.Error("30d returned !ok, must not be filled")
	}
	if fc.Horizons[1].RiskFreeReturnPct == 0 {
		t.Error("90d returned ok, must be filled")
	}
}
