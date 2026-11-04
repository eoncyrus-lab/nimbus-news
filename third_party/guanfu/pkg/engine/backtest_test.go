package engine

import (
	"testing"
)

type mockPrices map[string]float64

func (m mockPrices) PriceAt(date string) (float64, bool) {
	v, ok := m[date]
	return v, ok
}

func TestForwardReturn(t *testing.T) {
	prov := mockPrices{
		"2025-01-01": 100,
		"2025-01-31": 110,
	}
	r, ok := ForwardReturn(prov, "2025-01-01", 30)
	if !ok {
		t.Fatal("expected ok")
	}
	if r < 9.99 || r > 10.01 {
		t.Fatalf("expected ~10%%, got %.2f", r)
	}
}

func TestAggregateBacktest(t *testing.T) {
	samples := []SamplePoint{
		// 强积累 + 后续涨 → hit
		{Date: "2024-01-01", Price: 50000, Stance: "强积累倾向", Fwd30dPct: 15, HasFwd30: true,
			Fwd90dPct: 30, HasFwd90: true, BottomProximity: 0.8, Coverage: 0.6},
		{Date: "2024-01-15", Price: 52000, Stance: "强积累倾向", Fwd30dPct: 10, HasFwd30: true,
			Fwd90dPct: 25, HasFwd90: true, BottomProximity: 0.75, Coverage: 0.6},
		// 防守 + 后续跌 → hit
		{Date: "2024-03-01", Price: 70000, Stance: "防守倾向", Fwd30dPct: -8, HasFwd30: true,
			Fwd90dPct: -12, HasFwd90: true, TopProximity: 0.65, Coverage: 0.5},
		// 等待 + 横盘
		{Date: "2024-04-01", Price: 65000, Stance: "等待", Fwd30dPct: 1, HasFwd30: true,
			TopProximity: 0.4, Coverage: 0.55},
	}
	r := AggregateBacktest(samples, false)
	if r.NumSamples != 4 {
		t.Fatalf("expected 4 samples, got %d", r.NumSamples)
	}
	// 强积累 hit_rate_30 应该是 100%
	for _, s := range r.StanceStats {
		if s.Stance == "强积累倾向" {
			if s.HitRate30 != 1.0 {
				t.Errorf("强积累 hit_rate_30 should be 1.0, got %.2f", s.HitRate30)
			}
			if s.AvgFwd30 != 12.5 {
				t.Errorf("强积累 avg_fwd_30 should be 12.5, got %.2f", s.AvgFwd30)
			}
		}
		if s.Stance == "防守倾向" && s.HitRate30 != 1.0 {
			t.Errorf("防守 hit_rate_30 should be 1.0, got %.2f", s.HitRate30)
		}
	}
	// proximity buckets
	if len(r.BottomProximity) == 0 {
		t.Error("expected bottom proximity buckets")
	}
}
