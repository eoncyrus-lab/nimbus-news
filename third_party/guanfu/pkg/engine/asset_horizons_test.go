// B5 integration test: each asset's BuildForecast must apply its
// per-asset default horizons when caller passes empty opts.Horizons.
//
// TestHorizonsForAsset (in pkg/forecast) locks the helper map; this
// test exercises the wiring through the asset interface so a future
// refactor that reverts opts.Horizons = forecast.HorizonsForAsset(...)
// to opts = forecast.DefaultOptions() would fail here.

package engine

import (
	"math"
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

// syntheticPriceSeries builds n daily points with a deterministic
// trend + sinusoid so kNN finds analogs and forecast.Build doesn't
// short-circuit on degenerate data.
func syntheticPriceSeries(start time.Time, n int, base float64) []store.PricePoint {
	pts := make([]store.PricePoint, n)
	for i := 0; i < n; i++ {
		// gentle drift + cycle so realized variance > 0 across windows
		v := base + float64(i)*0.05 + 8*math.Sin(float64(i)*0.04)
		pts[i] = store.PricePoint{
			Date:   start.AddDate(0, 0, i).Format("2006-01-02"),
			Close:  v,
			Source: "synthetic",
		}
	}
	return pts
}

func setupQQAssetWithStore(t *testing.T, days int) *QQAsset {
	t.Helper()
	dir := t.TempDir()
	s := &store.PriceStore{Dir: dir}
	pts := syntheticPriceSeries(time.Date(2014, 1, 1, 0, 0, 0, 0, time.UTC), days, 80)
	if err := s.Save("qqq", pts); err != nil {
		t.Fatalf("save qqq: %v", err)
	}
	return &QQAsset{store: s}
}

func setupGoldAssetWithStore(t *testing.T, days int) *GoldAsset {
	t.Helper()
	dir := t.TempDir()
	s := &store.PriceStore{Dir: dir}
	pts := syntheticPriceSeries(time.Date(2014, 1, 1, 0, 0, 0, 0, time.UTC), days, 1500)
	if err := s.Save("gold", pts); err != nil {
		t.Fatalf("save gold: %v", err)
	}
	return &GoldAsset{store: s}
}

func TestQQAssetBuildForecastUsesPerAssetHorizons(t *testing.T) {
	a := setupQQAssetWithStore(t, 1500)
	fc, err := a.BuildForecast(nil, forecast.Options{}) // empty Horizons
	if err != nil {
		t.Fatalf("QQAsset.BuildForecast: %v", err)
	}
	// QQQ default per B5: {30, 63, 90, 180, 252}
	want := []int{30, 63, 90, 180, 252}
	if len(fc.Horizons) != len(want) {
		t.Fatalf("len(fc.Horizons) = %d, want %d (per-asset default did not fire)", len(fc.Horizons), len(want))
	}
	for i, h := range fc.Horizons {
		if h.Days != want[i] {
			t.Fatalf("fc.Horizons[%d].Days = %d, want %d", i, h.Days, want[i])
		}
	}
}

func TestGoldAssetBuildForecastUsesPerAssetHorizons(t *testing.T) {
	a := setupGoldAssetWithStore(t, 1500)
	fc, err := a.BuildForecast(nil, forecast.Options{}) // empty Horizons
	if err != nil {
		t.Fatalf("GoldAsset.BuildForecast: %v", err)
	}
	// Gold default per B5 (revised 2026-05-09): {30, 60, 90, 120}.
	// 180d was dropped after post-refresh backtest showed 49% dir_hit
	// on n=51, no better than random.
	want := []int{30, 60, 90, 120}
	if len(fc.Horizons) != len(want) {
		t.Fatalf("len(fc.Horizons) = %d, want %d (per-asset default did not fire)", len(fc.Horizons), len(want))
	}
	for i, h := range fc.Horizons {
		if h.Days != want[i] {
			t.Fatalf("fc.Horizons[%d].Days = %d, want %d", i, h.Days, want[i])
		}
	}
}

func TestQQAssetBuildForecastRespectsCallerHorizons(t *testing.T) {
	a := setupQQAssetWithStore(t, 1500)
	caller := []int{45, 100}
	fc, err := a.BuildForecast(nil, forecast.Options{Horizons: caller})
	if err != nil {
		t.Fatalf("QQAsset.BuildForecast: %v", err)
	}
	if len(fc.Horizons) != len(caller) {
		t.Fatalf("caller horizons not respected: got %d, want %d", len(fc.Horizons), len(caller))
	}
	for i, h := range fc.Horizons {
		if h.Days != caller[i] {
			t.Fatalf("fc.Horizons[%d].Days = %d, want %d", i, h.Days, caller[i])
		}
	}
}
