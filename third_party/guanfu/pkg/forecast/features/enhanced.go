// Enhanced feature extractors: CAPE valuation, daily macro, cross-asset lead-lag.
// All use PriceStore for date-aligned data lookup.

package features

import (
	"math"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

const (
	capeMaxAgeDays    = 45
	fredMaxAgeDays    = 7
	cotMaxAgeDays     = 10
	etfMaxAgeDays     = 5
	putCallMaxAgeDays = 5
)

// ─── CAPE (Shiller PE) valuation ────────────────────────

// CAPEExtractor returns a feature that looks up Shiller CAPE from PriceStore.
// CAPE values: typical range 10-45, median ~20. Higher = more expensive.
func CAPEExtractor(s *store.PriceStore) forecast.FeatureExtractor {
	capePts, err := s.Load("spx_cape")
	if err != nil || len(capePts) < 10 {
		return nil
	}
	// Build sorted lookup array
	capeByDate := makeDateMap(capePts)

	return func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
		v := lookupDateMaxAge(capePts, capeByDate, points[i].Date, capeMaxAgeDays)
		// Sanity bound: real CAPE has been 5-50 since 1871. Anything outside
		// signals corrupted source data — drop the feature rather than feed garbage.
		if v < 5 || v > 80 {
			return nil, false
		}
		// Log-normalize: log(CAPE/20) / 0.6
		normalized := (math.Log(v) - math.Log(20)) / 0.6
		return []forecast.FeatureValue{{
			Name: "cape", Value: math.Round(v*100) / 100,
			Normalized: math.Round(clipE(normalized, 3)*1000) / 1000,
			Weight:     0.80, Note: "Shiller CAPE (monthly)",
		}}, true
	}
}

// ─── Daily Macro: DGS10 + DXY ───────────────────────────

// DGS10Extractor returns 10Y Treasury rate 30d change.
func DGS10Extractor(s *store.PriceStore) forecast.FeatureExtractor {
	pts, err := s.Load("fred_dgs10")
	if err != nil || len(pts) < 60 {
		return nil
	}
	byDate := makeDateMap(pts)

	return func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
		if i < 30 {
			return nil, false
		}
		now := lookupDateMaxAge(pts, byDate, points[i].Date, fredMaxAgeDays)
		past := lookupDateMaxAge(pts, byDate, points[i-30].Date, fredMaxAgeDays)
		if now == 0 || past == 0 {
			return nil, false
		}
		chg := (now - past) / 0.5
		return []forecast.FeatureValue{{
			Name: "dgs10_30d", Value: math.Round((now-past)*10000) / 100,
			Normalized: math.Round(clipE(chg, 3)*1000) / 1000,
			Weight:     0.45, Note: "10Y Treasury 30d change",
		}}, true
	}
}

// DXYExtractor returns Trade-Weighted USD 30d trend.
func DXYExtractor(s *store.PriceStore) forecast.FeatureExtractor {
	pts, err := s.Load("fred_dxy")
	if err != nil || len(pts) < 60 {
		return nil
	}
	byDate := makeDateMap(pts)

	return func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
		if i < 30 {
			return nil, false
		}
		now := lookupDateMaxAge(pts, byDate, points[i].Date, fredMaxAgeDays)
		past := lookupDateMaxAge(pts, byDate, points[i-30].Date, fredMaxAgeDays)
		if now == 0 || past == 0 {
			return nil, false
		}
		chg := (now - past) / past / 0.03
		return []forecast.FeatureValue{{
			Name: "dxy_30d", Value: math.Round((now/past-1)*10000) / 100,
			Normalized: math.Round(clipE(-chg, 3)*1000) / 1000,
			Weight:     0.45, Note: "USD 30d trend (inverted)",
		}}, true
	}
}

// ─── Equity macro: credit + curve ───────────────────────

// HYSpreadExtractor returns the credit spread 30d change (inverted).
// Uses BAA10Y (Moody's BAA - 10Y Treasury, 1986+) for long history,
// with ICE BofA HY OAS overlay (fred_hy_spread, 2023+) when available.
func HYSpreadExtractor(s *store.PriceStore) forecast.FeatureExtractor {
	hyPts, _ := s.Load("fred_hy_spread")
	baaPts, _ := s.Load("baa10y")
	if len(baaPts) < 60 && len(hyPts) < 60 {
		return nil
	}
	var hyByDate map[string]float64
	if len(hyPts) >= 60 {
		hyByDate = makeDateMap(hyPts)
	}
	baaByDate := makeDateMap(baaPts)
	minHYDate := ""
	if len(hyPts) > 0 {
		minHYDate = hyPts[0].Date
	}

	return func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
		if i < 30 {
			return nil, false
		}
		target := points[i].Date
		pastTarget := points[i-30].Date

		// Prefer HY OAS if it covers this date
		var now, past float64
		label := "BAA10Y spread 30d change (inverted)"
		normalizer := 1.5
		if minHYDate != "" && target >= minHYDate {
			now = lookupDateMaxAge(hyPts, hyByDate, target, fredMaxAgeDays)
			past = lookupDateMaxAge(hyPts, hyByDate, pastTarget, fredMaxAgeDays)
			label = "HY spread 30d change (inverted)"
			normalizer = 2.0
		}
		if now <= 0 || past <= 0 {
			now = lookupDateMaxAge(baaPts, baaByDate, target, fredMaxAgeDays)
			past = lookupDateMaxAge(baaPts, baaByDate, pastTarget, fredMaxAgeDays)
		}
		if now <= 0 || past <= 0 {
			return nil, false
		}
		chg := -(now - past) / normalizer
		return []forecast.FeatureValue{{
			Name: "hy_spread_30d", Value: math.Round((now-past)*100) / 100,
			Normalized: math.Round(clipE(chg, 3)*1000) / 1000,
			Weight:     0.55, Note: label,
		}}, true
	}
}

// YieldCurveExtractor returns the 10Y-2Y spread level. Negative = inversion.
func YieldCurveExtractor(s *store.PriceStore) forecast.FeatureExtractor {
	pts, err := s.Load("fred_yield_curve")
	if err != nil || len(pts) < 60 {
		return nil
	}
	byDate := makeDateMap(pts)
	return func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
		v := lookupDateMaxAge(pts, byDate, points[i].Date, fredMaxAgeDays)
		if v == 0 {
			return nil, false
		}
		// Normalize around 0.5 with ±2% range
		return []forecast.FeatureValue{{
			Name: "yield_curve", Value: math.Round(v*100) / 100,
			Normalized: math.Round(clipE(v/2.0, 3)*1000) / 1000,
			Weight:     0.45, Note: "10Y-2Y Treasury spread",
		}}, true
	}
}

// ─── Gold macro ────────────────────────────────────────

// RealYield10YExtractor returns DFII10 (10Y TIPS real yield) level.
// Real yield rising = bearish gold; falling = bullish gold.
func RealYield10YExtractor(s *store.PriceStore) forecast.FeatureExtractor {
	pts, err := s.Load("fred_dfii10")
	if err != nil || len(pts) < 60 {
		return nil
	}
	byDate := makeDateMap(pts)
	return func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
		v := lookupDateMaxAge(pts, byDate, points[i].Date, fredMaxAgeDays)
		if v == 0 {
			return nil, false
		}
		// Range typically -1.5 to +2.5; center 0.5
		return []forecast.FeatureValue{{
			Name: "real_yield_10y", Value: math.Round(v*100) / 100,
			Normalized: math.Round(clipE((v-0.5)/2.0, 3)*1000) / 1000,
			Weight:     0.65, Note: "10Y TIPS real yield",
		}}, true
	}
}

// BreakevenExtractor returns 10Y breakeven inflation expectations.
// Higher breakeven = inflation hedge bid for gold.
func BreakevenExtractor(s *store.PriceStore) forecast.FeatureExtractor {
	pts, err := s.Load("fred_breakeven")
	if err != nil || len(pts) < 60 {
		return nil
	}
	byDate := makeDateMap(pts)
	return func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
		v := lookupDateMaxAge(pts, byDate, points[i].Date, fredMaxAgeDays)
		if v == 0 {
			return nil, false
		}
		// Center around 2.2%, range 1-3.5
		return []forecast.FeatureValue{{
			Name: "breakeven_10y", Value: math.Round(v*100) / 100,
			Normalized: math.Round(clipE((v-2.2)/1.5, 3)*1000) / 1000,
			Weight:     0.45, Note: "10Y breakeven inflation",
		}}, true
	}
}

// GoldCOTExtractor returns Managed Money net long contracts in gold futures.
// Extreme net long = crowded; extreme net short = washout.
func GoldCOTExtractor(s *store.PriceStore) forecast.FeatureExtractor {
	pts, err := s.Load("gold_cot")
	if err != nil || len(pts) < 60 {
		return nil
	}
	byDate := makeDateMap(pts)
	return func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
		v := lookupDateMaxAge(pts, byDate, points[i].Date, cotMaxAgeDays)
		if v == 0 {
			return nil, false
		}
		// Typical 0-300k contracts; normalize against 150k
		return []forecast.FeatureValue{{
			Name: "gold_cot_net", Value: math.Round(v),
			Normalized: math.Round(clipE((v-150000)/100000, 3)*1000) / 1000,
			Weight:     0.40, Note: "Gold COT managed-money net long",
		}}, true
	}
}

// ─── Generic risk: VIX ─────────────────────────────────

// VIXExtractor returns the VIX level via VIXY ETF proxy.
// VIX > 30 = risk-off (benefits gold, hurts equities).
// VIX < 15 = complacency (hurts gold, benefits equities).
func VIXExtractor(s *store.PriceStore) forecast.FeatureExtractor {
	pts, err := s.Load("vixy")
	if err != nil || len(pts) < 60 {
		return nil
	}
	byDate := makeDateMap(pts)
	return func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
		v := lookupDateMaxAge(pts, byDate, points[i].Date, etfMaxAgeDays)
		if v == 0 {
			return nil, false
		}
		return []forecast.FeatureValue{{
			Name: "vixy_level", Value: math.Round(v*100) / 100,
			Normalized: math.Round(clipE((v-20)/15, 3)*1000) / 1000,
			Weight:     0.55, Note: "VIX proxy level (VIXY ETF)",
		}}, true
	}
}

type PutCallFeatureMode int

const (
	PutCallNone PutCallFeatureMode = iota
	PutCallRatioOnly
	PutCallRatioAndChange
	PutCallAll
)

// PutCallRatioExtractorWithMode supports ablation tests for the equity
// options feature family.
func PutCallRatioExtractorWithMode(s *store.PriceStore, mode PutCallFeatureMode) forecast.FeatureExtractor {
	if mode == PutCallNone {
		return nil
	}
	pts, err := s.Load("stooq_putcall")
	if err != nil || len(pts) < 60 {
		return nil
	}
	byDate := makeDateMap(pts)
	return func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
		if i < 30 {
			return nil, false
		}
		now := lookupDateMaxAge(pts, byDate, points[i].Date, putCallMaxAgeDays)
		past := lookupDateMaxAge(pts, byDate, points[i-30].Date, putCallMaxAgeDays)
		if now <= 0 || past <= 0 {
			return nil, false
		}
		change := now - past
		out := []forecast.FeatureValue{{
			Name: "put_call_ratio", Value: math.Round(now*1000) / 1000,
			Normalized: math.Round(clipE((now-0.95)/0.35, 3)*1000) / 1000,
			Weight:     0.15, Note: "CBOE total put/call ratio (conservative weight: short history)",
		}}
		if mode >= PutCallRatioAndChange {
			out = append(out, forecast.FeatureValue{
				Name: "put_call_30d_change", Value: math.Round(change*1000) / 1000,
				Normalized: math.Round(clipE(change/0.25, 3)*1000) / 1000,
				Weight:     0.10, Note: "CBOE total put/call 30d change",
			})
		}
		if mode >= PutCallAll {
			pct, ok := trailingPercentileMaxAge(pts, points[i].Date, 252, now, putCallMaxAgeDays)
			if !ok {
				return out, true
			}
			out = append(out, forecast.FeatureValue{
				Name: "put_call_252d_percentile", Value: math.Round(pct*1000) / 10,
				Normalized: math.Round(clipE((pct-0.5)/0.25, 3)*1000) / 1000,
				Weight:     0.12, Note: "CBOE total put/call trailing 252-observation percentile",
			})
		}
		return out, true
	}
}

// ─── Helpers ─────────────────────────────────────────────

func makeDateMap(pts []store.PricePoint) map[string]float64 {
	m := make(map[string]float64, len(pts))
	for _, p := range pts {
		m[p.Date] = p.Close
	}
	return m
}

func lookupDate(pts []store.PricePoint, dateMap map[string]float64, date string) float64 {
	// Fast path: exact match
	if v, ok := dateMap[date]; ok {
		return v
	}
	// Forward-fill: find most recent before date
	for i := len(pts) - 1; i >= 0; i-- {
		if pts[i].Date <= date {
			return pts[i].Close
		}
	}
	return 0
}

func lookupDateMaxAge(pts []store.PricePoint, dateMap map[string]float64, date string, maxAgeDays int) float64 {
	if maxAgeDays <= 0 {
		return lookupDate(pts, dateMap, date)
	}
	target, err := time.Parse("2006-01-02", date)
	if err != nil {
		return 0
	}
	if v, ok := dateMap[date]; ok {
		return v
	}
	for i := len(pts) - 1; i >= 0; i-- {
		if pts[i].Date > date {
			continue
		}
		asOf, err := time.Parse("2006-01-02", pts[i].Date)
		if err != nil {
			return 0
		}
		if target.Sub(asOf).Hours()/24 > float64(maxAgeDays) {
			return 0
		}
		return pts[i].Close
	}
	return 0
}

func trailingPercentileMaxAge(pts []store.PricePoint, date string, window int, value float64, maxAgeDays int) (float64, bool) {
	if len(pts) == 0 || value <= 0 {
		return 0, false
	}
	target, err := time.Parse("2006-01-02", date)
	if err != nil {
		return 0, false
	}
	end := -1
	for i := len(pts) - 1; i >= 0; i-- {
		if pts[i].Date <= date {
			end = i
			break
		}
	}
	if end < 0 {
		return 0, false
	}
	if maxAgeDays > 0 {
		asOf, err := time.Parse("2006-01-02", pts[end].Date)
		if err != nil || target.Sub(asOf).Hours()/24 > float64(maxAgeDays) {
			return 0, false
		}
	}
	start := 0
	if window > 0 && end+1 > window {
		start = end + 1 - window
	}
	total := 0
	le := 0
	for _, p := range pts[start : end+1] {
		if p.Close <= 0 {
			continue
		}
		total++
		if p.Close <= value {
			le++
		}
	}
	if total < 60 {
		return 0, false
	}
	return float64(le) / float64(total), true
}

func clipE(v, mx float64) float64 {
	if v > mx {
		return mx
	}
	if v < -mx {
		return -mx
	}
	return v
}
