// CoinMetrics client for BTC on-chain valuation.
//
// Community API exposes CapMVRVCur and CapMrktCurUSD for BTC. CapRealUSD may
// require paid access, so the client computes realized cap as market cap / MVRV
// when direct CapRealUSD is unavailable. This still keeps the realized-cap
// source auditable because CapMVRVCur is CoinMetrics' own realized-cap ratio.
package client

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/coinmetrics"
)

const coinMetricsLookbackDays = 6000 // covers BTC daily history since 2010 with buffer

type OnchainValuationData struct {
	MVRV            float64
	MVRVZScore      float64
	NUPL            float64
	MVRVQuantile    float64
	NUPLQuantile    float64
	MarketCapUSD    float64
	RealizedCapUSD  float64
	RealizedCapMode string
	LatestDate      time.Time
	StaleDays       int
	Warnings        []string
}

type coinMetricsPoint struct {
	Asset         string `json:"asset"`
	Time          string `json:"time"`
	CapMrktCurUSD string `json:"CapMrktCurUSD"`
	CapMVRVCur    string `json:"CapMVRVCur"`
	CapRealUSD    string `json:"CapRealUSD"`
}

func FetchBTCOnchainValuation(ctx context.Context) (*OnchainValuationData, error) {
	apiKey := os.Getenv("COINMETRICS_API_KEY")

	points, err := fetchCoinMetricsValuation(ctx, apiKey, apiKey != "")
	if err != nil && apiKey != "" {
		points, err = fetchCoinMetricsValuation(ctx, "", false)
		if err == nil {
			out, buildErr := buildOnchainValuation(points)
			if buildErr != nil {
				return nil, buildErr
			}
			out.Warnings = append(out.Warnings, "CoinMetrics paid endpoint failed; fell back to community CapMVRVCur/CapMrktCurUSD")
			return out, nil
		}
	}
	if err != nil {
		return nil, err
	}
	return buildOnchainValuation(points)
}

func fetchCoinMetricsValuation(ctx context.Context, apiKey string, includeRealized bool) ([]coinMetricsPoint, error) {
	metrics := []string{"CapMrktCurUSD", "CapMVRVCur"}
	if includeRealized {
		metrics = append(metrics, "CapRealUSD")
	}

	pts, err := coinmetrics.AssetMetrics(ctx, coinmetrics.Query{
		Asset:   "btc",
		Metrics: metrics,
		Limit:   coinMetricsLookbackDays,
		APIKey:  apiKey,
	})
	if err != nil {
		return nil, err
	}

	out := make([]coinMetricsPoint, 0, len(pts))
	for _, p := range pts {
		out = append(out, coinMetricsPoint{
			Asset:         p.Asset,
			Time:          p.Time.Format(time.RFC3339Nano),
			CapMrktCurUSD: p.Metrics["CapMrktCurUSD"],
			CapMVRVCur:    p.Metrics["CapMVRVCur"],
			CapRealUSD:    p.Metrics["CapRealUSD"],
		})
	}
	return out, nil
}

func buildOnchainValuation(points []coinMetricsPoint) (*OnchainValuationData, error) {
	sort.Slice(points, func(i, j int) bool { return points[i].Time < points[j].Time })

	type sample struct {
		t              time.Time
		marketCap      float64
		realizedCap    float64
		mvrv           float64
		realizedDirect bool
	}

	samples := make([]sample, 0, len(points))
	for _, p := range points {
		t, err := time.Parse(time.RFC3339Nano, p.Time)
		if err != nil {
			continue
		}
		marketCap := parseMetricFloat(p.CapMrktCurUSD)
		mvrv := parseMetricFloat(p.CapMVRVCur)
		realizedCap := parseMetricFloat(p.CapRealUSD)
		realizedDirect := isFinitePositive(realizedCap)
		if !isFinitePositive(realizedCap) && isFinitePositive(marketCap) && isFinitePositive(mvrv) {
			realizedCap = marketCap / mvrv
		}
		if !isFinitePositive(marketCap) || !isFinitePositive(realizedCap) || !isFinitePositive(mvrv) {
			continue
		}
		samples = append(samples, sample{
			t:              t,
			marketCap:      marketCap,
			realizedCap:    realizedCap,
			mvrv:           mvrv,
			realizedDirect: realizedDirect,
		})
	}
	if len(samples) < 30 {
		return nil, fmt.Errorf("coinmetrics usable samples too few: %d", len(samples))
	}

	latest := samples[len(samples)-1]
	marketCaps := make([]float64, len(samples))
	mvrvs := make([]float64, len(samples))
	nupls := make([]float64, len(samples))
	mcRealDiffs := make([]float64, len(samples))
	for i, s := range samples {
		marketCaps[i] = s.marketCap
		mvrvs[i] = s.mvrv
		nupls[i] = (s.marketCap - s.realizedCap) / s.marketCap
		mcRealDiffs[i] = s.marketCap - s.realizedCap
	}

	// Standard MVRV Z-Score uses rolling 1-year std of (market_cap - realized_cap),
	// not population std over all samples.
	rollWindow := 365
	if len(mcRealDiffs) < rollWindow {
		rollWindow = len(mcRealDiffs)
	}
	rollDiffs := mcRealDiffs[len(mcRealDiffs)-rollWindow:]
	std := stddev(rollDiffs)
	if std <= 0 {
		return nil, fmt.Errorf("coinmetrics rolling 1y stddev of (mcap - rcap) is zero")
	}

	nupl := (latest.marketCap - latest.realizedCap) / latest.marketCap
	mode := "implied_from_CapMVRVCur"
	if latest.realizedDirect {
		mode = "direct_CapRealUSD"
	}

	return &OnchainValuationData{
		MVRV:            latest.mvrv,
		MVRVZScore:      (latest.marketCap - latest.realizedCap) / std,
		NUPL:            nupl,
		MVRVQuantile:    quantileRankFloat(mvrvs, latest.mvrv),
		NUPLQuantile:    quantileRankFloat(nupls, nupl),
		MarketCapUSD:    latest.marketCap,
		RealizedCapUSD:  latest.realizedCap,
		RealizedCapMode: mode,
		LatestDate:      latest.t,
		StaleDays:       staleDays(latest.t),
	}, nil
}

func parseMetricFloat(s string) float64 {
	if s == "" || s == "." {
		return math.NaN()
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return math.NaN()
	}
	return v
}

func isFinitePositive(v float64) bool {
	return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0)
}

func stddev(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	mean := sum / float64(len(values))
	var ss float64
	for _, v := range values {
		d := v - mean
		ss += d * d
	}
	return math.Sqrt(ss / float64(len(values)))
}

func quantileRankFloat(samples []float64, current float64) float64 {
	if len(samples) == 0 || math.IsNaN(current) || math.IsInf(current, 0) {
		return 0
	}
	sorted := make([]float64, 0, len(samples))
	for _, v := range samples {
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			sorted = append(sorted, v)
		}
	}
	if len(sorted) == 0 {
		return 0
	}
	sort.Float64s(sorted)
	idx := sort.Search(len(sorted), func(i int) bool { return sorted[i] > current })
	return float64(idx) / float64(len(sorted))
}

func staleDays(t time.Time) int {
	now := time.Now().UTC().Truncate(24 * time.Hour)
	date := t.UTC().Truncate(24 * time.Hour)
	if date.After(now) {
		return 0
	}
	return int(now.Sub(date).Hours() / 24)
}
