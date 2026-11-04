// Backtest 引擎 — 把 verdict 引擎拿到历史日期上回放，统计前向收益与命中率。
//
// 设计原则：
//   - 不假设所有指标都可回放；缺数据指标自动 Missing → verdict 跳过 → coverage 下降
//   - 只用已经有完整历史的源：CoinMetrics PriceUSD 2010+ + Binance 最新日线
//     （mayer / sma_200w / RSI 等）+ history.db
//     里实际采集过的 15 个指标 + CoinMetrics 全历史 MVRV/NUPL（需调用方传入）
//   - Lookahead bias 防御：每个采样点只能看到 ≤ 该日期的数据
//
// 输出三层：
//   1. 单点结果 SamplePoint：日期、verdict、前向收益
//   2. 按 stance 分桶 StanceStats：每个 stance 标签的样本数、命中率、avg fwd 收益
//   3. 按顶/底接近度分桶 ProximityStats：验证 top/bottom proximity 的预测力
//
// 用法见 cmd/guanfu-backtest/main.go。

package engine

import (
	"fmt"
	"sort"
	"time"
)

// PriceProvider — 注入 BTC 历史价格的接口。回测器只问 "date 那天的收盘价是多少"。
type PriceProvider interface {
	PriceAt(date string) (float64, bool)
}

// SamplePoint — 单个回测采样点的结果。
type SamplePoint struct {
	Date            string  `json:"date"`
	Price           float64 `json:"price"`
	Stance          string  `json:"stance"`
	Regime          string  `json:"regime"`
	NetDirection    int     `json:"net_direction"`
	Coverage        float64 `json:"coverage"`
	TopProximity    float64 `json:"top_proximity"`
	BottomProximity float64 `json:"bottom_proximity"`
	Fwd30dPct       float64 `json:"fwd_30d_pct"`
	Fwd90dPct       float64 `json:"fwd_90d_pct"`
	Fwd180dPct      float64 `json:"fwd_180d_pct"`
	HasFwd30        bool    `json:"has_fwd_30"`
	HasFwd90        bool    `json:"has_fwd_90"`
	HasFwd180       bool    `json:"has_fwd_180"`
}

// StanceStats — 按 verdict.Stance 聚合的统计。
type StanceStats struct {
	Stance     string  `json:"stance"`
	N          int     `json:"n"`
	AvgFwd30   float64 `json:"avg_fwd_30_pct"`
	AvgFwd90   float64 `json:"avg_fwd_90_pct"`
	AvgFwd180  float64 `json:"avg_fwd_180_pct"`
	HitRate30  float64 `json:"hit_rate_30"`
	HitRate90  float64 `json:"hit_rate_90"`
	HitRate180 float64 `json:"hit_rate_180"`
}

// ProximityStats — 按 top/bottom proximity 区间聚合（验证接近度的预测力）。
type ProximityStats struct {
	Bucket    string  `json:"bucket"`
	N         int     `json:"n"`
	AvgFwd30  float64 `json:"avg_fwd_30_pct"`
	AvgFwd90  float64 `json:"avg_fwd_90_pct"`
	AvgFwd180 float64 `json:"avg_fwd_180_pct"`
}

// BacktestReport — 完整报告。
type BacktestReport struct {
	From            string           `json:"from"`
	To              string           `json:"to"`
	NumSamples      int              `json:"num_samples"`
	AvgCoverage     float64          `json:"avg_coverage"`
	Samples         []SamplePoint    `json:"samples,omitempty"`
	StanceStats     []StanceStats    `json:"stance_stats"`
	TopProximity    []ProximityStats `json:"top_proximity_stats"`
	BottomProximity []ProximityStats `json:"bottom_proximity_stats"`
}

// AggregateBacktest — 输入采样点列表，产出聚合报告。纯逻辑无 I/O。
func AggregateBacktest(samples []SamplePoint, includeRaw bool) *BacktestReport {
	if len(samples) == 0 {
		return &BacktestReport{}
	}
	report := &BacktestReport{
		From:       samples[0].Date,
		To:         samples[len(samples)-1].Date,
		NumSamples: len(samples),
	}
	if includeRaw {
		report.Samples = samples
	}

	sumCov := 0.0
	for _, s := range samples {
		sumCov += s.Coverage
	}
	report.AvgCoverage = sumCov / float64(len(samples))

	byStance := map[string][]SamplePoint{}
	for _, s := range samples {
		byStance[s.Stance] = append(byStance[s.Stance], s)
	}
	for stance, pts := range byStance {
		report.StanceStats = append(report.StanceStats, statsForStance(stance, pts))
	}
	sort.Slice(report.StanceStats, func(i, j int) bool {
		return stanceOrder(report.StanceStats[i].Stance) < stanceOrder(report.StanceStats[j].Stance)
	})

	report.TopProximity = proximityBuckets(samples, "top")
	report.BottomProximity = proximityBuckets(samples, "bottom")

	return report
}

func statsForStance(stance string, pts []SamplePoint) StanceStats {
	s := StanceStats{Stance: stance, N: len(pts)}
	var sum30, sum90, sum180 float64
	var n30, n90, n180 int
	var hit30, hit90, hit180 int
	bullish := stanceIsBull(stance)
	bearish := stanceIsBear(stance)
	for _, p := range pts {
		if p.HasFwd30 {
			sum30 += p.Fwd30dPct
			n30++
			if (bullish && p.Fwd30dPct > 0) || (bearish && p.Fwd30dPct < 0) {
				hit30++
			}
		}
		if p.HasFwd90 {
			sum90 += p.Fwd90dPct
			n90++
			if (bullish && p.Fwd90dPct > 0) || (bearish && p.Fwd90dPct < 0) {
				hit90++
			}
		}
		if p.HasFwd180 {
			sum180 += p.Fwd180dPct
			n180++
			if (bullish && p.Fwd180dPct > 0) || (bearish && p.Fwd180dPct < 0) {
				hit180++
			}
		}
	}
	if n30 > 0 {
		s.AvgFwd30 = sum30 / float64(n30)
		if bullish || bearish {
			s.HitRate30 = float64(hit30) / float64(n30)
		}
	}
	if n90 > 0 {
		s.AvgFwd90 = sum90 / float64(n90)
		if bullish || bearish {
			s.HitRate90 = float64(hit90) / float64(n90)
		}
	}
	if n180 > 0 {
		s.AvgFwd180 = sum180 / float64(n180)
		if bullish || bearish {
			s.HitRate180 = float64(hit180) / float64(n180)
		}
	}
	return s
}

func proximityBuckets(samples []SamplePoint, kind string) []ProximityStats {
	thresholds := []float64{0.3, 0.5, 0.7}
	labels := []string{"<0.3", "0.3-0.5", "0.5-0.7", ">0.7"}
	buckets := make([][]SamplePoint, 4)
	for _, s := range samples {
		v := s.TopProximity
		if kind == "bottom" {
			v = s.BottomProximity
		}
		switch {
		case v < thresholds[0]:
			buckets[0] = append(buckets[0], s)
		case v < thresholds[1]:
			buckets[1] = append(buckets[1], s)
		case v < thresholds[2]:
			buckets[2] = append(buckets[2], s)
		default:
			buckets[3] = append(buckets[3], s)
		}
	}
	prefix := "top_proximity"
	if kind == "bottom" {
		prefix = "bottom_proximity"
	}
	out := []ProximityStats{}
	for i, lbl := range labels {
		if len(buckets[i]) == 0 {
			continue
		}
		var sum30, sum90, sum180 float64
		var n30, n90, n180 int
		for _, p := range buckets[i] {
			if p.HasFwd30 {
				sum30 += p.Fwd30dPct
				n30++
			}
			if p.HasFwd90 {
				sum90 += p.Fwd90dPct
				n90++
			}
			if p.HasFwd180 {
				sum180 += p.Fwd180dPct
				n180++
			}
		}
		ps := ProximityStats{
			Bucket: fmt.Sprintf("%s %s", prefix, lbl),
			N:      len(buckets[i]),
		}
		if n30 > 0 {
			ps.AvgFwd30 = sum30 / float64(n30)
		}
		if n90 > 0 {
			ps.AvgFwd90 = sum90 / float64(n90)
		}
		if n180 > 0 {
			ps.AvgFwd180 = sum180 / float64(n180)
		}
		out = append(out, ps)
	}
	return out
}

func stanceIsBull(s string) bool {
	switch s {
	case "强积累倾向", "偏积累倾向", "持有观察倾向":
		return true
	}
	return false
}

func stanceIsBear(s string) bool {
	switch s {
	case "防守倾向", "高防守倾向", "分配 / 避险风险":
		return true
	}
	return false
}

func stanceOrder(s string) int {
	order := map[string]int{
		"强积累倾向":     1,
		"偏积累倾向":     2,
		"持有观察倾向":    3,
		"等待":        4,
		"防守倾向":      5,
		"高防守倾向":     6,
		"分配 / 避险风险": 7,
	}
	if v, ok := order[s]; ok {
		return v
	}
	return 99
}

// ForwardReturn — utility: 给定 PriceProvider 与日期，计算 date+N 天的 % 收益。
// 节假日 / 缺数据时返回 (_, false)，由调用方决定是否跳过。
func ForwardReturn(prov PriceProvider, date string, days int) (float64, bool) {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return 0, false
	}
	cur, ok := prov.PriceAt(date)
	if !ok || cur <= 0 {
		return 0, false
	}
	fwdDate := t.AddDate(0, 0, days).Format("2006-01-02")
	fwd, ok := prov.PriceAt(fwdDate)
	if !ok || fwd <= 0 {
		return 0, false
	}
	return (fwd/cur - 1) * 100, true
}
