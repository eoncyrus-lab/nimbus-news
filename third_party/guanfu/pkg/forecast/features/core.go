// Core BTC price features — 11 extractors that work on full history (2010+).
//
// FeatureExtractor signature: func(points []Point, i int) ([]FeatureValue, bool)
// Points are oldest-first. Index i is the target date.
// Returns nil, false if insufficient data at index i.

package features

import (
	"math"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast"
)

const (
	ahrLegacyLogSlope      = 5.84
	ahrLegacyLogIntercept  = -17.01
	ahrCompressionExponent = 0.75
)

// CoreExtractors returns all 11 core BTC price feature extractors.
func CoreExtractors() []forecast.FeatureExtractor {
	return []forecast.FeatureExtractor{
		Return30d,
		Return90d,
		Return180d,
		Drawdown90d,
		MayerMultiple,
		SMA200WeekDev,
		RealizedVol30d,
		RSI14,
		CompressedAHR,
		HalvingPhaseSin,
		HalvingPhaseCos,
	}
}

// GenericTechnicalExtractors returns the asset-agnostic price features that
// work for any series with 200+ days of history. Excludes BTC-only quirks
// (AHR999 fair-price, halving cycle, 200-week SMA — designed for BTC's
// 13-year secular trend).
func GenericTechnicalExtractors() []forecast.FeatureExtractor {
	return GenericTechnicalExtractorsWithScales(nil)
}

// GenericTechnicalExtractorsWithScales is the scale-aware variant.
// scales maps feature name → normalization divisor; nil or missing keys
// fall back to BTC defaults (0.30/0.60/1.00/0.40 for returns/drawdown).
func GenericTechnicalExtractorsWithScales(scales map[string]float64) []forecast.FeatureExtractor {
	scaleOf := func(name string, btcDefault float64) float64 {
		if scales == nil {
			return btcDefault
		}
		if v, ok := scales[name]; ok && v > 0 {
			return v
		}
		return btcDefault
	}

	ret30Scale := scaleOf("return_30d", 0.30)
	ret90Scale := scaleOf("return_90d", 0.60)
	ret180Scale := scaleOf("return_180d", 1.00)
	dd90Scale := scaleOf("drawdown_90d", 0.40)
	volScale := scaleOf("realized_vol_30d", 0.50)

	return30d := func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
		r, ok := returnOver(points, i, 30)
		if !ok {
			return nil, false
		}
		return []forecast.FeatureValue{{
			Name: "return_30d", Value: round4(r * 100), Normalized: round4(clip(r/ret30Scale, 3)), Weight: 1.10,
		}}, true
	}
	return90d := func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
		r, ok := returnOver(points, i, 90)
		if !ok {
			return nil, false
		}
		return []forecast.FeatureValue{{
			Name: "return_90d", Value: round4(r * 100), Normalized: round4(clip(r/ret90Scale, 3)), Weight: 1.00,
		}}, true
	}
	return180d := func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
		r, ok := returnOver(points, i, 180)
		if !ok {
			return nil, false
		}
		return []forecast.FeatureValue{{
			Name: "return_180d", Value: round4(r * 100), Normalized: round4(clip(r/ret180Scale, 3)), Weight: 0.80,
		}}, true
	}
	drawdown90d := func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
		dd, ok := drawdown(points, i, 90)
		if !ok {
			return nil, false
		}
		return []forecast.FeatureValue{{
			Name: "drawdown_90d", Value: round4(dd * 100), Normalized: round4(clip(dd/dd90Scale, 3)), Weight: 1.10,
		}}, true
	}
	realizedVol30d := func(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
		vol, ok := realizedVol(points, i, 30)
		if !ok {
			return nil, false
		}
		// For BTC: center 0.60, scale 0.50. For equity: center ~0.15, scale 0.15.
		// We approximate by using volScale as both center and range.
		center := volScale * 1.2 // rough center (BTC: 0.60, equity: 0.18, gold: 0.12)
		if scales == nil {
			center = 0.60 // preserve exact BTC legacy behavior
		}
		return []forecast.FeatureValue{{
			Name: "realized_vol_30d", Value: round4(vol * 100), Normalized: round4(clip((vol-center)/volScale, 3)), Weight: 0.70,
		}}, true
	}

	return []forecast.FeatureExtractor{
		return30d,
		return90d,
		return180d,
		drawdown90d,
		MayerMultiple, // price/200d SMA — scale is asset-agnostic (ratio, not return)
		realizedVol30d,
		RSI14, // (rsi-50)/25 — scale is asset-agnostic
	}
}

func Return30d(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
	r, ok := returnOver(points, i, 30)
	if !ok {
		return nil, false
	}
	return []forecast.FeatureValue{{
		Name: "return_30d", Value: round4(r * 100), Normalized: round4(clip(r/0.30, 3)), Weight: 1.10,
	}}, true
}

func Return90d(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
	r, ok := returnOver(points, i, 90)
	if !ok {
		return nil, false
	}
	return []forecast.FeatureValue{{
		Name: "return_90d", Value: round4(r * 100), Normalized: round4(clip(r/0.60, 3)), Weight: 1.00,
	}}, true
}

func Return180d(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
	r, ok := returnOver(points, i, 180)
	if !ok {
		return nil, false
	}
	return []forecast.FeatureValue{{
		Name: "return_180d", Value: round4(r * 100), Normalized: round4(clip(r/1.00, 3)), Weight: 0.80,
	}}, true
}

func Drawdown90d(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
	dd, ok := drawdown(points, i, 90)
	if !ok {
		return nil, false
	}
	return []forecast.FeatureValue{{
		Name: "drawdown_90d", Value: round4(dd * 100), Normalized: round4(clip(dd/0.40, 3)), Weight: 1.10,
	}}, true
}

func MayerMultiple(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
	m, ok := mayerMult(points, i)
	if !ok {
		return nil, false
	}
	return []forecast.FeatureValue{{
		Name: "mayer_multiple", Value: round4(m), Normalized: round4(clip(math.Log(m)/math.Log(2.4), 3)), Weight: 1.20,
	}}, true
}

func SMA200WeekDev(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
	dev, ok := smaDev(points, i, 1400)
	if !ok {
		return nil, false
	}
	return []forecast.FeatureValue{{
		Name: "sma_200w_dev", Value: round4(dev * 100), Normalized: round4(clip(dev/1.50, 3)), Weight: 1.00,
	}}, true
}

func RealizedVol30d(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
	vol, ok := realizedVol(points, i, 30)
	if !ok {
		return nil, false
	}
	return []forecast.FeatureValue{{
		Name: "realized_vol_30d", Value: round4(vol * 100), Normalized: round4(clip((vol-0.60)/0.50, 3)), Weight: 0.70,
	}}, true
}

func RSI14(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
	rsi, ok := rsiVal(points, i, 14)
	if !ok {
		return nil, false
	}
	return []forecast.FeatureValue{{
		Name: "rsi_14", Value: round4(rsi), Normalized: round4(clip((rsi-50)/25, 3)), Weight: 0.80,
	}}, true
}

func CompressedAHR(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
	ahr, ok := compressedAHR(points, i)
	if !ok {
		return nil, false
	}
	return []forecast.FeatureValue{{
		Name: "ahr999_compressed", Value: round4(ahr), Normalized: round4(clip(math.Log(ahr)/math.Log(2.5), 3)), Weight: 1.40,
	}}, true
}

func HalvingPhaseSin(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
	progress, ok := halvingProgress(mustParseDate(points[i].Date))
	if !ok {
		return nil, false
	}
	v := math.Sin(2 * math.Pi * progress)
	return []forecast.FeatureValue{{
		Name: "halving_cycle_sin", Value: round4(v), Normalized: round4(v), Weight: 0.35,
	}}, true
}

func HalvingPhaseCos(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
	progress, ok := halvingProgress(mustParseDate(points[i].Date))
	if !ok {
		return nil, false
	}
	v := math.Cos(2 * math.Pi * progress)
	return []forecast.FeatureValue{{
		Name: "halving_cycle_cos", Value: round4(v), Normalized: round4(v), Weight: 0.35,
	}}, true
}

// ─── Shared helpers ─────────────────────────────────────

func returnOver(points []forecast.Point, i, days int) (float64, bool) {
	if i-days < 0 || !usablePositive(points[i-days].Close) {
		return 0, false
	}
	return points[i].Close/points[i-days].Close - 1, true
}

func drawdown(points []forecast.Point, i, window int) (float64, bool) {
	if i-window+1 < 0 {
		return 0, false
	}
	maxClose := 0.0
	for j := i - window + 1; j <= i; j++ {
		if points[j].Close > maxClose {
			maxClose = points[j].Close
		}
	}
	if !usablePositive(maxClose) {
		return 0, false
	}
	return points[i].Close/maxClose - 1, true
}

func mayerMult(points []forecast.Point, i int) (float64, bool) {
	ma, ok := sma(points, i, 200)
	if !ok || !usablePositive(ma) {
		return 0, false
	}
	return points[i].Close / ma, true
}

func smaDev(points []forecast.Point, i, window int) (float64, bool) {
	ma, ok := sma(points, i, window)
	if !ok || !usablePositive(ma) {
		return 0, false
	}
	return points[i].Close/ma - 1, true
}

func sma(points []forecast.Point, i, window int) (float64, bool) {
	if window <= 0 || i-window+1 < 0 {
		return 0, false
	}
	sum := 0.0
	for j := i - window + 1; j <= i; j++ {
		if !usablePositive(points[j].Close) {
			return 0, false
		}
		sum += points[j].Close
	}
	return sum / float64(window), true
}

func realizedVol(points []forecast.Point, i, window int) (float64, bool) {
	if window <= 1 || i-window < 0 {
		return 0, false
	}
	returns := make([]float64, 0, window)
	for j := i - window + 1; j <= i; j++ {
		if !usablePositive(points[j].Close) || !usablePositive(points[j-1].Close) {
			return 0, false
		}
		returns = append(returns, math.Log(points[j].Close/points[j-1].Close))
	}
	std := stddev(returns)
	if !usableFinite(std) {
		return 0, false
	}
	return std * math.Sqrt(365), true
}

func rsiVal(points []forecast.Point, i, window int) (float64, bool) {
	if window <= 0 || i-window < 0 {
		return 0, false
	}
	gains := 0.0
	losses := 0.0
	for j := i - window + 1; j <= i; j++ {
		diff := points[j].Close - points[j-1].Close
		if diff > 0 {
			gains += diff
		} else {
			losses -= diff
		}
	}
	if losses == 0 {
		if gains == 0 {
			return 50, true
		}
		return 100, true
	}
	rs := (gains / float64(window)) / (losses / float64(window))
	return 100 - 100/(1+rs), true
}

func compressedAHR(points []forecast.Point, i int) (float64, bool) {
	dca, ok := sma(points, i, 200)
	if !ok || !usablePositive(dca) {
		return 0, false
	}
	date := mustParseDate(points[i].Date)
	genesis := time.Date(2009, 1, 3, 0, 0, 0, 0, time.UTC)
	age := date.Sub(genesis).Hours() / 24
	if age <= 0 {
		return 0, false
	}
	fair := math.Pow(10, ahrLegacyLogSlope*math.Log10(age)+ahrLegacyLogIntercept)
	if !usablePositive(fair) {
		return 0, false
	}
	raw := (points[i].Close / dca) * (points[i].Close / fair)
	return math.Pow(raw, ahrCompressionExponent), true
}

func halvingProgress(date time.Time) (float64, bool) {
	halvings := []time.Time{
		time.Date(2012, 11, 28, 0, 0, 0, 0, time.UTC),
		time.Date(2016, 7, 9, 0, 0, 0, 0, time.UTC),
		time.Date(2020, 5, 11, 0, 0, 0, 0, time.UTC),
		time.Date(2024, 4, 20, 0, 0, 0, 0, time.UTC),
		time.Date(2028, 4, 20, 0, 0, 0, 0, time.UTC),
	}
	var prev, next time.Time
	for _, h := range halvings {
		if h.After(date) {
			next = h
			break
		}
		prev = h
	}
	if prev.IsZero() || next.IsZero() {
		return 0, false
	}
	total := next.Sub(prev).Hours() / 24
	elapsed := date.Sub(prev).Hours() / 24
	if total <= 0 {
		return 0, false
	}
	return clamp01(elapsed / total), true
}

// ─── Math helpers ───────────────────────────────────────

func stddev(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	avg := average(values)
	sum := 0.0
	for _, v := range values {
		diff := v - avg
		sum += diff * diff
	}
	return math.Sqrt(sum / float64(len(values)))
}

func average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func mustParseDate(date string) time.Time {
	t, _ := time.Parse("2006-01-02", date)
	return t.UTC()
}

func usablePositive(v float64) bool {
	return v > 0 && usableFinite(v)
}

func usableFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func clip(v, absMax float64) float64 {
	if v > absMax {
		return absMax
	}
	if v < -absMax {
		return -absMax
	}
	return v
}

func round4(v float64) float64 {
	return math.Round(v*10000) / 10000
}
