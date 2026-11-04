// guanfu-web-export — emit per-asset DCA JSON for the static web app.
//
// For each equity/gold asset it reads the PriceStore, runs the (reproducible)
// valuation-zone DCA replay from pkg/dca, and layers a consumer-grade
// score + DCA-intensity multiplier on top. The score/multiplier layer lives
// ONLY here, never in the core engine — guanfu's panels stay multi-dimensional
// and signal-free; this binary is the consumer compression for the website.
//
// Usage:
//
//	guanfu-web-export --out ../btcdca-web/public/data
//
// Output: ../btcdca-web/public/data/{qqq,spy,gold}.json + meta.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/dca"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

// tradingDaysPerYear: equities/gold are trading-day series (~252/yr), not the
// calendar-daily 365 that BTC uses. Drives the zone-replay window labels.
const tradingDaysPerYear = 252

type assetCfg struct {
	key      string
	name     string
	yearDays int
	useCAPE  bool // blend Shiller CAPE percentile into the score (broad-market valuation)
	capeNote string
}

var assets = []assetCfg{
	{key: "btc", name: "Bitcoin (BTC)", yearDays: 365},
	{key: "gold", name: "黄金 (GC=F)", yearDays: tradingDaysPerYear},
	{key: "qqq", name: "纳斯达克100 (QQQ)", yearDays: tradingDaysPerYear},
	{key: "spy", name: "标普500 (SPY)", yearDays: tradingDaysPerYear, useCAPE: true, capeNote: "Shiller CAPE（标普周期调整市盈率）"},
	{key: "hs300", name: "沪深300 (CSI300)", yearDays: tradingDaysPerYear},
}

const disclaimer = "数据与回测仅供参考，非投资建议、非交易指令。分数与倍数是基于历史估值分位的" +
	"「定投强度参考」，不预测短期涨跌；下方按估值区间的历史回测才是可复现的决策底料。" +
	"模拟收益未计手续费/税/汇率损耗。"

type valuationInput struct {
	Name       string  `json:"name"`
	Value      float64 `json:"value"`
	Percentile float64 `json:"percentile"` // 0-1 position in own history (high = expensive)
	Note       string  `json:"note"`
}

type histPoint struct {
	Date  string  `json:"date"`
	Close float64 `json:"close"`
	MA200 float64 `json:"ma200,omitempty"`
}

type dataHealth struct {
	Source       string `json:"source"`
	PointCount   int    `json:"point_count"`
	HistoryStart string `json:"history_start"`
	HistoryEnd   string `json:"history_end"`
	StaleDays    int    `json:"stale_days"`
	Stale        bool   `json:"stale"`
	Note         string `json:"note"`
}

type metricExport struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	Value  string `json:"value"`
	Detail string `json:"detail"`
	Tone   string `json:"tone"`
}

type assetExport struct {
	Asset         string                `json:"asset"`
	Name          string                `json:"name"`
	GeneratedAt   string                `json:"generated_at"`
	PriceDate     string                `json:"price_date"`
	Price         float64               `json:"price"`
	Score         int                   `json:"score"`      // 0-100, high = cheap = 偏积累
	Multiplier    float64               `json:"multiplier"` // DCA intensity guide
	ValuationZone string                `json:"valuation_zone"`
	Inputs        []valuationInput      `json:"inputs"`
	DataHealth    dataHealth            `json:"data_health"`
	Metrics       []metricExport        `json:"metrics"`
	ZoneReplay    *dca.ZoneReplayResult `json:"zone_replay"`
	History       []histPoint           `json:"history"`
	Disclaimer    string                `json:"disclaimer"`
}

type metaFile struct {
	GeneratedAt string            `json:"generated_at"`
	Assets      map[string]string `json:"assets"` // asset -> latest price date
}

func main() {
	outDir := flag.String("out", "../btcdca-web/public/data", "output directory for JSON (the btcdca-web repo)")
	flag.Parse()

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		fatal("mkdir %s: %v", *outDir, err)
	}

	s := &store.PriceStore{}
	capeSeries := loadCAPE(s) // date -> cape, may be empty
	meta := metaFile{GeneratedAt: nowUTC(), Assets: map[string]string{}}

	var failed []string
	for _, a := range assets {
		exp, err := buildAsset(s, a, capeSeries)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[%s] FAILED: %v\n", a.key, err)
			failed = append(failed, a.key)
			continue
		}
		path := filepath.Join(*outDir, a.key+".json")
		if err := writeJSON(path, exp); err != nil {
			fatal("write %s: %v", path, err)
		}
		meta.Assets[a.key] = exp.PriceDate
		fmt.Printf("wrote %s (score=%d mult=%.2fx zone=%s price=%.2f@%s)\n",
			path, exp.Score, exp.Multiplier, exp.ValuationZone, exp.Price, exp.PriceDate)
	}

	if err := writeJSON(filepath.Join(*outDir, "meta.json"), meta); err != nil {
		fatal("write meta: %v", err)
	}

	if len(failed) == len(assets) {
		fatal("all assets failed to export")
	}
	if len(failed) > 0 {
		os.Exit(1)
	}
}

func buildAsset(s *store.PriceStore, a assetCfg, capeSeries map[string]float64) (*assetExport, error) {
	yearDays := a.yearDays
	if yearDays <= 0 {
		yearDays = tradingDaysPerYear
	}
	raw, err := s.Load(a.key)
	if err != nil {
		return nil, fmt.Errorf("load store: %w", err)
	}
	points := toValidPoints(raw)
	if len(points) < yearDays*5+200 {
		return nil, fmt.Errorf("need >= %d valid points, got %d", yearDays*5+200, len(points))
	}

	closes := make([]float64, len(points))
	for i, p := range points {
		closes[i] = p.Close
	}
	cur := points[len(points)-1]

	// Valuation input 1: Mayer Multiple (price / 200d SMA) + its historical percentile.
	mayerSeries := mayerHistory(closes, 200)
	mayerNow := mayerSeries[len(mayerSeries)-1]
	mayerPct := percentileOf(mayerSeries, mayerNow)
	inputs := []valuationInput{{
		Name:       "Mayer Multiple",
		Value:      round(mayerNow, 3),
		Percentile: round(mayerPct, 3),
		Note:       "价格 / 200日均线；分位越高越贵",
	}}

	// expensiveness in [0,1]: 1 = most expensive vs own history.
	expensive := mayerPct

	// Valuation input 2 (SPY only): Shiller CAPE percentile.
	if a.useCAPE && len(capeSeries) > 0 {
		if capeNow, capePct, ok := capePercentile(capeSeries, cur.Date); ok {
			inputs = append(inputs, valuationInput{
				Name:       "Shiller CAPE",
				Value:      round(capeNow, 2),
				Percentile: round(capePct, 3),
				Note:       a.capeNote + "；分位越高越贵",
			})
			// blend equally with Mayer
			expensive = (expensive + capePct) / 2
		}
	}

	score := int(math.Round((1 - expensive) * 100)) // cheap -> high score
	score = clampInt(score, 0, 100)
	mult := multiplierFor(score)
	zone := zoneLabel(score)

	zr, err := dca.ZoneReplayYearLen(toDCAPoints(points), yearDays)
	if err != nil {
		return nil, fmt.Errorf("zone replay: %w", err)
	}

	return &assetExport{
		Asset:         a.key,
		Name:          a.name,
		GeneratedAt:   nowUTC(),
		PriceDate:     cur.Date,
		Price:         round(cur.Close, 2),
		Score:         score,
		Multiplier:    mult,
		ValuationZone: zone,
		Inputs:        inputs,
		DataHealth:    buildDataHealth(points, yearDays),
		Metrics:       buildMetrics(points, closes, mayerNow, mayerPct, yearDays),
		ZoneReplay:    zr,
		History:       buildHistory(points, closes, yearDays),
		Disclaimer:    disclaimer,
	}, nil
}

// multiplierFor maps a 0-100 cheapness score to a DCA-intensity multiplier via
// piecewise-linear anchors. This is an explicit heuristic intensity guide (cap
// 2.5x), NOT an optimized signal — the zone-replay table is the real evidence.
func multiplierFor(score int) float64 {
	anchors := []struct{ s, m float64 }{
		{0, 0.3}, {50, 1.0}, {75, 1.6}, {100, 2.5},
	}
	x := float64(score)
	for i := 1; i < len(anchors); i++ {
		if x <= anchors[i].s {
			a, b := anchors[i-1], anchors[i]
			t := (x - a.s) / (b.s - a.s)
			return round(a.m+t*(b.m-a.m), 2)
		}
	}
	return anchors[len(anchors)-1].m
}

func zoneLabel(score int) string {
	switch {
	case score >= 75:
		return "低估 — 偏积累区"
	case score >= 55:
		return "中性偏低"
	case score >= 40:
		return "中性"
	case score >= 25:
		return "偏高"
	default:
		return "高估 — 谨慎区"
	}
}

func buildDataHealth(points []store.PricePoint, yearDays int) dataHealth {
	if len(points) == 0 {
		return dataHealth{Source: "unknown", Stale: true, Note: "无可用价格点"}
	}
	if yearDays <= 0 {
		yearDays = tradingDaysPerYear
	}
	first := points[0]
	last := points[len(points)-1]
	source := last.Source
	if source == "" {
		for i := len(points) - 1; i >= 0; i-- {
			if points[i].Source != "" {
				source = points[i].Source
				break
			}
		}
	}
	if source == "" {
		source = "PriceStore"
	}

	staleDays := daysSince(last.Date)
	threshold := 4
	if yearDays >= 365 {
		threshold = 2
	}
	stale := staleDays > threshold
	note := "数据新鲜"
	if stale {
		note = fmt.Sprintf("最新价格距今 %d 天，超过 %d 天阈值", staleDays, threshold)
	}
	if staleDays < 0 {
		note = "价格日期晚于当前日期，请检查系统时区或源数据"
		stale = true
	}

	return dataHealth{
		Source:       source,
		PointCount:   len(points),
		HistoryStart: first.Date,
		HistoryEnd:   last.Date,
		StaleDays:    staleDays,
		Stale:        stale,
		Note:         note,
	}
}

func buildMetrics(points []store.PricePoint, closes []float64, mayerNow, mayerPct float64, yearDays int) []metricExport {
	out := []metricExport{{
		Key:    "mayer",
		Label:  "Mayer",
		Value:  fmt.Sprintf("%.3f", mayerNow),
		Detail: fmt.Sprintf("历史分位 %.0f%%", mayerPct*100),
		Tone:   toneForPercentile(mayerPct),
	}}
	if ma200, ok := movingAvg(closes, 200); ok && ma200 > 0 {
		dev := (closes[len(closes)-1]/ma200 - 1) * 100
		out = append(out, metricExport{
			Key:    "ma200_dev",
			Label:  "MA200 偏离",
			Value:  signedPct(dev, 1),
			Detail: fmt.Sprintf("MA200 %.2f", round(ma200, 2)),
			Tone:   toneForDeviation(dev),
		})
	}
	if ma50, ok := movingAvg(closes, 50); ok && ma50 > 0 {
		dev := (closes[len(closes)-1]/ma50 - 1) * 100
		out = append(out, metricExport{
			Key:    "ma50_dev",
			Label:  "MA50 偏离",
			Value:  signedPct(dev, 1),
			Detail: fmt.Sprintf("MA50 %.2f", round(ma50, 2)),
			Tone:   toneForTrend(dev),
		})
	}
	if ret, ok := pctReturn(closes, 30); ok {
		out = append(out, metricExport{
			Key:    "ret_30",
			Label:  "30期收益",
			Value:  signedPct(ret, 1),
			Detail: "近30个价格点",
			Tone:   toneForReturn(ret),
		})
	}
	if ret, ok := pctReturn(closes, 90); ok {
		out = append(out, metricExport{
			Key:    "ret_90",
			Label:  "90期收益",
			Value:  signedPct(ret, 1),
			Detail: "近90个价格点",
			Tone:   toneForReturn(ret),
		})
	}
	if ret, ok := pctReturn(closes, yearDays); ok {
		out = append(out, metricExport{
			Key:    "ret_1y",
			Label:  "1年收益",
			Value:  signedPct(ret, 1),
			Detail: fmt.Sprintf("近%d个价格点", yearDays),
			Tone:   toneForReturn(ret),
		})
	}
	if vol, ok := realizedVol30(closes, yearDays); ok {
		out = append(out, metricExport{
			Key:    "vol_30",
			Label:  "30期波动",
			Value:  fmt.Sprintf("%.1f%%", vol),
			Detail: "年化实现波动率",
			Tone:   toneForVol(vol),
		})
	}
	if len(points) > 0 {
		out = append(out, metricExport{
			Key:    "source_points",
			Label:  "样本数",
			Value:  fmt.Sprintf("%d", len(points)),
			Detail: points[0].Date + " → " + points[len(points)-1].Date,
			Tone:   "neutral",
		})
	}
	return out
}

func daysSince(date string) int {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return 9999
	}
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return int(today.Sub(t).Hours() / 24)
}

func movingAvg(closes []float64, window int) (float64, bool) {
	if window <= 0 || len(closes) < window {
		return 0, false
	}
	sum := 0.0
	for _, v := range closes[len(closes)-window:] {
		sum += v
	}
	return sum / float64(window), true
}

func pctReturn(closes []float64, lag int) (float64, bool) {
	if lag <= 0 || len(closes) <= lag {
		return 0, false
	}
	prev := closes[len(closes)-1-lag]
	if prev <= 0 {
		return 0, false
	}
	return (closes[len(closes)-1]/prev - 1) * 100, true
}

func realizedVol30(closes []float64, yearDays int) (float64, bool) {
	const window = 30
	if len(closes) <= window {
		return 0, false
	}
	if yearDays <= 0 {
		yearDays = tradingDaysPerYear
	}
	rets := make([]float64, 0, window)
	start := len(closes) - window
	for i := start; i < len(closes); i++ {
		prev := closes[i-1]
		if prev <= 0 {
			return 0, false
		}
		rets = append(rets, math.Log(closes[i]/prev))
	}
	mean := 0.0
	for _, r := range rets {
		mean += r
	}
	mean /= float64(len(rets))
	variance := 0.0
	for _, r := range rets {
		diff := r - mean
		variance += diff * diff
	}
	variance /= float64(len(rets))
	return math.Sqrt(variance) * math.Sqrt(float64(yearDays)) * 100, true
}

func signedPct(v float64, dp int) string {
	return fmt.Sprintf("%+.1f%%", round(v, dp))
}

func toneForPercentile(p float64) string {
	switch {
	case p <= 0.25:
		return "good"
	case p >= 0.8:
		return "bad"
	case p >= 0.65:
		return "warn"
	default:
		return "neutral"
	}
}

func toneForDeviation(v float64) string {
	switch {
	case v <= -10:
		return "good"
	case v >= 20:
		return "bad"
	case v >= 10:
		return "warn"
	default:
		return "neutral"
	}
}

func toneForTrend(v float64) string {
	switch {
	case v >= 0:
		return "good"
	case v <= -8:
		return "bad"
	default:
		return "warn"
	}
}

func toneForReturn(v float64) string {
	switch {
	case v > 0:
		return "good"
	case v < 0:
		return "bad"
	default:
		return "neutral"
	}
}

func toneForVol(v float64) string {
	switch {
	case v >= 45:
		return "bad"
	case v >= 25:
		return "warn"
	default:
		return "neutral"
	}
}

// mayerHistory returns price/SMA(window) at each index where the window exists.
func mayerHistory(closes []float64, window int) []float64 {
	out := make([]float64, 0, len(closes))
	if len(closes) < window {
		return out
	}
	sum := 0.0
	for i := 0; i < window; i++ {
		sum += closes[i]
	}
	for i := window - 1; i < len(closes); i++ {
		if i >= window {
			sum += closes[i] - closes[i-window]
		}
		sma := sum / float64(window)
		if sma > 0 {
			out = append(out, closes[i]/sma)
		}
	}
	return out
}

// percentileOf returns the fraction of samples <= v (0-1).
func percentileOf(samples []float64, v float64) float64 {
	if len(samples) == 0 {
		return 0.5
	}
	sorted := append([]float64(nil), samples...)
	sort.Float64s(sorted)
	le := sort.Search(len(sorted), func(i int) bool { return sorted[i] > v }) // count <= v
	return float64(le) / float64(len(sorted))
}

func capePercentile(cape map[string]float64, date string) (float64, float64, bool) {
	// nearest CAPE on or before date (monthly series)
	var dates []string
	for d := range cape {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	var pick string
	for _, d := range dates {
		if d <= date {
			pick = d
		} else {
			break
		}
	}
	if pick == "" {
		return 0, 0, false
	}
	now := cape[pick]
	vals := make([]float64, 0, len(dates))
	for _, d := range dates {
		vals = append(vals, cape[d])
	}
	return now, percentileOf(vals, now), true
}

// buildHistory downsamples the last ~5y to weekly points with a 200d MA overlay.
func buildHistory(points []store.PricePoint, closes []float64, yearDays int) []histPoint {
	n := len(points)
	if yearDays <= 0 {
		yearDays = tradingDaysPerYear
	}
	lookback := yearDays*5 + 200
	start := n - lookback
	if start < 0 {
		start = 0
	}
	out := make([]histPoint, 0, lookback/5)
	for i := start; i < n; i += 5 {
		hp := histPoint{Date: points[i].Date, Close: round(closes[i], 2)}
		if i >= 199 {
			sum := 0.0
			for j := i - 199; j <= i; j++ {
				sum += closes[j]
			}
			hp.MA200 = round(sum/200, 2)
		}
		out = append(out, hp)
	}
	// always include the very last point
	if last := n - 1; (last-start)%5 != 0 {
		hp := histPoint{Date: points[last].Date, Close: round(closes[last], 2)}
		if last >= 199 {
			sum := 0.0
			for j := last - 199; j <= last; j++ {
				sum += closes[j]
			}
			hp.MA200 = round(sum/200, 2)
		}
		out = append(out, hp)
	}
	return out
}

func loadCAPE(s *store.PriceStore) map[string]float64 {
	pts, err := s.LoadSeries("spx_cape")
	if err != nil || len(pts) == 0 {
		return nil
	}
	out := make(map[string]float64, len(pts))
	for _, p := range pts {
		if len(p.Date) >= 10 && p.Close > 0 {
			out[p.Date] = p.Close
		}
	}
	return out
}

// toValidPoints drops the empty-date / non-positive artifacts that survive
// store normalization, so the monthly DCA loop never slices a short date.
func toValidPoints(raw []store.PricePoint) []store.PricePoint {
	out := make([]store.PricePoint, 0, len(raw))
	for _, p := range raw {
		if len(p.Date) >= 10 && p.Close > 0 {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

func toDCAPoints(pts []store.PricePoint) []dca.Point {
	out := make([]dca.Point, len(pts))
	for i, p := range pts {
		out[i] = dca.Point{Date: p.Date, Close: p.Close}
	}
	return out
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

func round(v float64, dp int) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	p := math.Pow(10, float64(dp))
	return math.Round(v*p) / p
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
