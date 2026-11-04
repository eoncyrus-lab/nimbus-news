package forecast

import (
	"math"
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/shopspring/decimal"
)

func TestBuildForecastFromSyntheticHistory(t *testing.T) {
	points := syntheticPoints(3200)

	fc, err := Build(points, testOpts())
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	if fc.Method != "historical_analogue_knn_v2" {
		t.Fatalf("unexpected method: %s", fc.Method)
	}
	if fc.Coverage.SelectedAnalogs < minSelectedAnalogs {
		t.Fatalf("selected analogs = %d, want >= %d", fc.Coverage.SelectedAnalogs, minSelectedAnalogs)
	}
	if len(fc.Horizons) != 3 {
		t.Fatalf("horizons = %d, want 3", len(fc.Horizons))
	}
	for _, h := range fc.Horizons {
		sum := h.ProbabilityUpsideContinuation + h.ProbabilityRange + h.ProbabilityDownsidePressure
		if math.Abs(sum-1) > 0.0001 {
			t.Fatalf("scenario probabilities for %dd sum to %.6f", h.Days, sum)
		}
		if h.SampleSize == 0 || h.MedianPrice <= 0 || h.P10Price <= 0 || h.P90Price <= 0 {
			t.Fatalf("invalid horizon summary: %+v", h)
		}
	}
	if len(fc.CurrentFeatures) < minSharedFeatures {
		t.Fatalf("current features = %d, want >= %d", len(fc.CurrentFeatures), minSharedFeatures)
	}
}

func TestParseHorizonsNormalizesDays(t *testing.T) {
	got, err := ParseHorizons("180d,30,90,30")
	if err != nil {
		t.Fatalf("ParseHorizons returned error: %v", err)
	}
	want := []int{30, 90, 180}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("horizons = %v, want %v", got, want)
		}
	}
}

func TestPointsFromSnapshotReconstructsOldestFirstDates(t *testing.T) {
	snap := &model.MarketSnapshot{
		Date:         time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC),
		BTCPriceAsOf: "2026-05-03",
		BTCPriceHistory: []decimal.Decimal{
			decimal.NewFromInt(103),
			decimal.NewFromInt(102),
			decimal.NewFromInt(101),
		},
	}

	points, err := PointsFromSnapshot(snap)
	if err != nil {
		t.Fatalf("PointsFromSnapshot returned error: %v", err)
	}
	if len(points) != 3 {
		t.Fatalf("len = %d, want 3", len(points))
	}
	if points[0].Date != "2026-05-01" || points[0].Close != 101 {
		t.Fatalf("oldest point mismatch: %+v", points[0])
	}
	if points[2].Date != "2026-05-03" || points[2].Close != 103 {
		t.Fatalf("latest point mismatch: %+v", points[2])
	}
}

func TestBuildRejectsShortHistory(t *testing.T) {
	_, err := Build(syntheticPoints(250), testOpts())
	if err == nil {
		t.Fatal("expected short history error")
	}
}

func TestHorizonsForAsset(t *testing.T) {
	cases := []struct {
		asset string
		want  []int
	}{
		{"qqq", []int{30, 63, 90, 180, 252}},
		{"QQQ", []int{30, 63, 90, 180, 252}},
		{"spy", []int{30, 63, 90, 180, 252}},
		{"gold", []int{30, 60, 90, 120}}, // 180d remains opt-in because history is regime-dependent
		{"stock_aapl", []int{30, 90, 180}},
		{"btc", []int{30, 90, 180}},
		{"AAPL", []int{30, 90, 180}}, // arbitrary stock falls back to default
		{"", []int{30, 90, 180}},
	}
	for _, tc := range cases {
		got := HorizonsForAsset(tc.asset)
		if len(got) != len(tc.want) {
			t.Fatalf("%s: len = %d, want %d", tc.asset, len(got), len(tc.want))
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("%s: got %v, want %v", tc.asset, got, tc.want)
			}
		}
	}

	// HorizonsForAsset must return a fresh slice (no aliasing of internal map).
	h := HorizonsForAsset("qqq")
	h[0] = 999
	if HorizonsForAsset("qqq")[0] == 999 {
		t.Fatal("HorizonsForAsset returned aliased slice; mutation leaked back to map")
	}
}

func TestBuildAnnotatesProfileMetadataWhenAssetSet(t *testing.T) {
	opts := testOpts()
	opts.Asset = "qqq"
	opts.Horizons = []int{30}
	fc, err := Build(syntheticPoints(500), opts)
	if err != nil {
		t.Fatal(err)
	}
	if fc.ProfileKey != "qqq" || fc.AssetClass != "equity_index" || fc.FeatureBundle == "" || fc.ProfileVersion == "" {
		t.Fatalf("profile metadata not populated: %+v", fc)
	}
}

func testOpts() Options {
	opts := DefaultOptions()
	opts.Extractors = []FeatureExtractor{
		func(points []Point, i int) ([]FeatureValue, bool) {
			if i < 30 {
				return nil, false
			}
			r := points[i].Close/points[i-30].Close - 1
			return []FeatureValue{{Name: "return_30d", Value: r * 100, Normalized: r / 0.30, Weight: 1.10}}, true
		},
		func(points []Point, i int) ([]FeatureValue, bool) {
			if i < 90 {
				return nil, false
			}
			r := points[i].Close/points[i-90].Close - 1
			return []FeatureValue{{Name: "return_90d", Value: r * 100, Normalized: r / 0.60, Weight: 1.00}}, true
		},
		func(points []Point, i int) ([]FeatureValue, bool) {
			if i < 180 {
				return nil, false
			}
			r := points[i].Close/points[i-180].Close - 1
			return []FeatureValue{{Name: "return_180d", Value: r * 100, Normalized: r / 1.00, Weight: 0.80}}, true
		},
		func(points []Point, i int) ([]FeatureValue, bool) {
			if i < 14 {
				return nil, false
			}
			gains, losses := 0.0, 0.0
			for j := i - 13; j <= i; j++ {
				diff := points[j].Close - points[j-1].Close
				if diff > 0 {
					gains += diff
				} else {
					losses -= diff
				}
			}
			if losses == 0 {
				return []FeatureValue{{Name: "rsi_14", Value: 100, Normalized: 2.0, Weight: 0.80}}, true
			}
			rsi := 100 - 100/(1+(gains/14)/(losses/14))
			return []FeatureValue{{Name: "rsi_14", Value: rsi, Normalized: (rsi - 50) / 25, Weight: 0.80}}, true
		},
		func(points []Point, i int) ([]FeatureValue, bool) {
			if i < 200 {
				return nil, false
			}
			sum := 0.0
			for j := i - 199; j <= i; j++ {
				sum += points[j].Close
			}
			mayer := points[i].Close / (sum / 200)
			return []FeatureValue{{Name: "mayer_multiple", Value: mayer, Normalized: mayer / 2.4, Weight: 1.20}}, true
		},
		func(points []Point, i int) ([]FeatureValue, bool) {
			if i < 200 {
				return nil, false
			}
			sum := 0.0
			for j := i - 199; j <= i; j++ {
				sum += points[j].Close
			}
			sma := sum / 200
			dev := points[i].Close/sma - 1
			return []FeatureValue{{Name: "sma_200_dev", Value: dev * 100, Normalized: dev / 0.50, Weight: 0.70}}, true
		},
	}
	return opts
}

func syntheticPoints(n int) []Point {
	start := time.Date(2015, 1, 1, 0, 0, 0, 0, time.UTC)
	points := make([]Point, 0, n)
	for i := 0; i < n; i++ {
		x := float64(i)
		price := 250 * math.Exp(0.0018*x+0.28*math.Sin(x/95)+0.08*math.Sin(x/17))
		points = append(points, Point{
			Date:   start.AddDate(0, 0, i).Format("2006-01-02"),
			Close:  price,
			Source: "test",
		})
	}
	return points
}
