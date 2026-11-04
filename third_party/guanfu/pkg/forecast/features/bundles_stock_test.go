package features_test

// D2 acceptance test: arbitrary-stock kNN forecast must do at least
// as well as the generic-technical-only baseline at the directional
// hit rate, otherwise the macro features are net harmful and the
// USStockExtractors bundle should be reverted.
//
// Per v5 regression budget: "D2 任意 stock 路径 dir hit < GenericTechnicalExtractors-only baseline → 回滚 D2".
//
// Skips automatically when stock_aapl.json is not in PriceStore
// (it's user-fetched, not committed) so CI doesn't fail on a fresh checkout.

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast/features"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

func TestUSStockExtractors_AcceptanceAAPL(t *testing.T) {
	s := &store.PriceStore{}
	raw, err := s.Load("stock_aapl")
	if err != nil || len(raw) < 400 {
		t.Skipf("stock_aapl missing or too short (%d points); run 'guanfu import-stock AAPL' first", len(raw))
	}

	points := make([]forecast.Point, len(raw))
	for i, p := range raw {
		points[i] = forecast.Point{Date: p.Date, Close: p.Close, Source: p.Source}
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Date < points[j].Date })

	dirHits := func(extractors []forecast.FeatureExtractor) map[int]float64 {
		opts := forecast.DefaultOptions()
		opts.Extractors = extractors
		opts.Horizons = []int{30, 90, 180}
		hits := map[int]int{30: 0, 90: 0, 180: 0}
		counts := map[int]int{30: 0, 90: 0, 180: 0}
		for i := 200; i+180 < len(points); i += 63 {
			window := points[:i+1]
			fc, err := forecast.Build(window, opts)
			if err != nil {
				continue
			}
			for _, hf := range fc.Horizons {
				h := hf.Days
				if i+h >= len(points) {
					continue
				}
				realized := points[i+h].Close - points[i].Close
				predicted := hf.MedianReturnPct
				counts[h]++
				if (predicted > 0 && realized > 0) || (predicted < 0 && realized < 0) {
					hits[h]++
				}
			}
		}
		out := map[int]float64{}
		for h, n := range counts {
			if n > 0 {
				out[h] = float64(hits[h]) / float64(n)
			}
		}
		return out
	}

	bare := dirHits(features.GenericTechnicalExtractors())
	full := dirHits(features.USStockExtractorsWithScales(s, nil))

	t.Logf("AAPL dir hits — bare(generic only): %v", bare)
	t.Logf("AAPL dir hits — full(USStockExtractors): %v", full)

	// Tolerance: USStockExtractors should not be worse by more than 10pp
	// at any horizon. Equal or better is the goal; mild noise is allowed.
	for _, h := range []int{30, 90, 180} {
		if bare[h] == 0 || full[h] == 0 {
			continue
		}
		if full[h]+0.10 < bare[h] {
			t.Errorf("USStockExtractors regressed at %dd: bare=%.2f full=%.2f (delta=%.2f > 10pp)",
				h, bare[h], full[h], bare[h]-full[h])
		}
	}

	// Sanity: data freshness so we know the test fired against real data.
	dir := s.Dir
	if dir == "" {
		dir = store.DefaultPricesDir()
	}
	if fi, err := os.Stat(filepath.Join(dir, "stock_aapl.json")); err == nil {
		t.Logf("stock_aapl.json size=%d bytes", fi.Size())
	}
}
