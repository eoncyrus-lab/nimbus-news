package main

import (
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

func TestNormalizeBacktestAssetRejectsUnsupportedAsset(t *testing.T) {
	for _, asset := range []string{"", "btc", "QQQ", "spy", "gold"} {
		if got, ok := normalizeBacktestAsset(asset); !ok || got == "" {
			t.Fatalf("expected supported asset %q to normalize, got %q ok=%v", asset, got, ok)
		}
	}

	removedAsset := "hs" + "300"
	if got, ok := normalizeBacktestAsset(removedAsset); ok || got != removedAsset {
		t.Fatalf("removed asset should be rejected, got %q ok=%v", got, ok)
	}
}

func TestBacktestExtractorsUseAssetSpecificBundles(t *testing.T) {
	ps := &store.PriceStore{Dir: t.TempDir()}
	points := syntheticBacktestPoints(1500)

	btcNames := featureNamesForTest(backtestExtractorsForAsset("btc", ps), points)
	for _, name := range []string{"ahr999_compressed", "halving_cycle_sin", "halving_cycle_cos"} {
		if !btcNames[name] {
			t.Fatalf("btc backtest bundle missing %s: %v", name, btcNames)
		}
	}

	for _, asset := range []string{"qqq", "spy", "gold"} {
		names := featureNamesForTest(backtestExtractorsForAsset(asset, ps), points)
		for _, name := range []string{"ahr999_compressed", "halving_cycle_sin", "halving_cycle_cos"} {
			if names[name] {
				t.Fatalf("%s backtest bundle should not include BTC-only %s: %v", asset, name, names)
			}
		}
	}
}

func syntheticBacktestPoints(n int) []forecast.Point {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	points := make([]forecast.Point, n)
	for i := range points {
		points[i] = forecast.Point{
			Date:  start.AddDate(0, 0, i).Format("2006-01-02"),
			Close: 100 + float64(i)*0.2,
		}
	}
	return points
}

func featureNamesForTest(extractors []forecast.FeatureExtractor, points []forecast.Point) map[string]bool {
	names := make(map[string]bool)
	i := len(points) - 1
	for _, extractor := range extractors {
		values, ok := extractor(points, i)
		if !ok {
			continue
		}
		for _, value := range values {
			names[value.Name] = true
		}
	}
	return names
}
