package client

import (
	"fmt"
	"testing"
	"time"
)

func TestBuildOnchainValuationUsesImpliedRealizedCap(t *testing.T) {
	points := make([]coinMetricsPoint, 40)
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	for i := range points {
		marketCap := 1_000_000_000_000.0 + float64(i)*10_000_000_000
		mvrv := 1.0 + float64(i)/100
		points[i] = coinMetricsPoint{
			Asset:         "btc",
			Time:          start.AddDate(0, 0, i).Format(time.RFC3339Nano),
			CapMrktCurUSD: fmt.Sprintf("%.0f", marketCap),
			CapMVRVCur:    fmt.Sprintf("%.6f", mvrv),
		}
	}

	got, err := buildOnchainValuation(points)
	if err != nil {
		t.Fatal(err)
	}

	if got.RealizedCapMode != "implied_from_CapMVRVCur" {
		t.Fatalf("RealizedCapMode = %s", got.RealizedCapMode)
	}
	if got.MVRV <= 1 || got.NUPL <= 0 || got.MVRVZScore <= 0 {
		t.Fatalf("unexpected valuation: mvrv=%f nupl=%f z=%f", got.MVRV, got.NUPL, got.MVRVZScore)
	}
	if got.MVRVQuantile <= 0.9 || got.NUPLQuantile <= 0.9 {
		t.Fatalf("expected latest values near top quantile, got mvrv q=%f nupl q=%f", got.MVRVQuantile, got.NUPLQuantile)
	}
}
