package source

import (
	"context"
	"fmt"
	"math"
	"time"
)

// A/H 没有可用的官方波动率指数（中国波指 2018 年起停发，恒指波指没有公开 K 线接口），
// 所以用 20 日已实现波动率自算一个"恐慌分"，口径固定、可解释：
//
//	RV20 = 日对数收益率标准差 × √252 × 100（年化 %）
//	恐慌分 = clamp((RV20 - 10) / 30 × 100, 0, 100)
//
// 15% 年化 ≈ 17 分（平静），25% ≈ 50 分（正常偏高），40% 以上 ≈ 100 分（恐慌）。
const panicWindow = 20

type panicReading struct {
	Label       string
	RV20        float64
	Score       int
	Level       string
	ChangePct   float64
	DrawdownPct float64
	OK          bool
}

func (s *MarketBriefingSource) fetchPanicReading(ctx context.Context, symbol, label string) *panicReading {
	if s.marketService == nil {
		return nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// 取 3mo：节假日多的月份 1mo 可能只有 20 根，凑不满 21 个收盘价。
	klines, err := s.marketService.GetHistoricalQuotes(lookupCtx, symbol, "1d", "3mo")
	if err != nil || len(klines) < panicWindow+1 {
		return nil
	}
	closes := make([]float64, 0, len(klines))
	for _, k := range klines {
		if k.Close > 0 {
			closes = append(closes, k.Close)
		}
	}
	if len(closes) < panicWindow+1 {
		return nil
	}
	closes = closes[len(closes)-(panicWindow+1):]

	returns := make([]float64, 0, panicWindow)
	for i := 1; i < len(closes); i++ {
		if closes[i-1] <= 0 || closes[i] <= 0 {
			continue
		}
		returns = append(returns, math.Log(closes[i]/closes[i-1]))
	}
	if len(returns) < 2 {
		return nil
	}
	mean := 0.0
	for _, r := range returns {
		mean += r
	}
	mean /= float64(len(returns))
	variance := 0.0
	for _, r := range returns {
		variance += (r - mean) * (r - mean)
	}
	variance /= float64(len(returns) - 1)
	rv := math.Sqrt(variance) * math.Sqrt(252) * 100

	score := int(math.Round((rv - 10) / 30 * 100))
	score = max(0, min(100, score))

	last := closes[len(closes)-1]
	prev := closes[len(closes)-2]
	change := 0.0
	if prev > 0 {
		change = (last/prev - 1) * 100
	}
	high := closes[0]
	for _, c := range closes {
		if c > high {
			high = c
		}
	}
	drawdown := 0.0
	if high > 0 {
		drawdown = (last/high - 1) * 100
	}

	return &panicReading{
		Label:       label,
		RV20:        rv,
		Score:       score,
		Level:       panicLevel(score),
		ChangePct:   change,
		DrawdownPct: drawdown,
		OK:          true,
	}
}

func panicLevel(score int) string {
	switch {
	case score < 25:
		return "平静"
	case score < 50:
		return "正常"
	case score < 75:
		return "紧张"
	default:
		return "恐慌"
	}
}

// renderPanic 生成 A/H 恐慌温度块；两条都取不到数据时不输出。
func (s *MarketBriefingSource) renderPanic(ctx context.Context) []string {
	readings := []*panicReading{
		s.fetchPanicReading(ctx, "000001.SS", "A股"),
		s.fetchPanicReading(ctx, "^HSI", "港股"),
	}
	lines := make([]string, 0, len(readings))
	for _, r := range readings {
		if r == nil || !r.OK {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s 恐慌分 %d/100（%s）| RV20 %.1f%% | 日涨跌 %+.2f%% | 距20日高 %+.1f%%",
			r.Label, r.Score, r.Level, r.RV20, r.ChangePct, r.DrawdownPct))
	}
	return lines
}
