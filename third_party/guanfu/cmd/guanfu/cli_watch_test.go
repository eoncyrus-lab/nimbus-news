package main

import (
	"testing"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
)

func TestLookupMetricFindsAcrossDomains(t *testing.T) {
	p := &model.IndicatorPanel{
		Cycle: map[string]model.Indicator{
			"mayer_multiple": {Value: 0.92},
		},
		Technical: map[string]model.Indicator{
			"rsi_14": {Value: 34.2},
		},
		Macro: map[string]model.Indicator{
			"real_yield_10y_pct": {Value: 1.8},
		},
	}
	cases := map[string]float64{
		"mayer_multiple":     0.92,
		"Mayer_Multiple":     0.92, // case-insensitive
		"rsi_14":             34.2,
		"real_yield_10y_pct": 1.8,
	}
	for k, want := range cases {
		got, ok := lookupMetric(p, k)
		if !ok {
			t.Errorf("lookupMetric(%q) not found", k)
			continue
		}
		if got != want {
			t.Errorf("lookupMetric(%q) = %v, want %v", k, got, want)
		}
	}
	if _, ok := lookupMetric(p, "nonexistent"); ok {
		t.Error("nonexistent metric should not be found")
	}
	if _, ok := lookupMetric(nil, "mayer"); ok {
		t.Error("nil panel should not find anything")
	}
}
