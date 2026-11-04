package market

import (
	"math"
)

// TechnicalAnalyzer 技术分析器
type TechnicalAnalyzer struct{}

// NewTechnicalAnalyzer 创建技术分析器
func NewTechnicalAnalyzer() *TechnicalAnalyzer {
	return &TechnicalAnalyzer{}
}

// CalculateSMA 计算简单移动平均
func (a *TechnicalAnalyzer) CalculateSMA(data []float64, period int) float64 {
	if len(data) < period {
		return 0
	}
	sum := 0.0
	for i := len(data) - period; i < len(data); i++ {
		sum += data[i]
	}
	return sum / float64(period)
}

// CalculateEMA 计算指数移动平均
func (a *TechnicalAnalyzer) CalculateEMA(data []float64, period int) float64 {
	if len(data) == 0 {
		return 0
	}
	multiplier := 2.0 / float64(period+1)
	ema := data[0]
	for i := 1; i < len(data); i++ {
		ema = (data[i]-ema)*multiplier + ema
	}
	return ema
}

// CalculateRSI 计算相对强弱指数
func (a *TechnicalAnalyzer) CalculateRSI(data []float64, period int) float64 {
	if len(data) < period+1 {
		return 50.0 // 默认中性
	}

	var gains, losses []float64
	for i := 1; i < len(data); i++ {
		change := data[i] - data[i-1]
		if change > 0 {
			gains = append(gains, change)
			losses = append(losses, 0)
		} else {
			gains = append(gains, 0)
			losses = append(losses, -change)
		}
	}

	if len(gains) < period {
		return 50.0
	}

	avgGain := a.CalculateSMA(gains[len(gains)-period:], period)
	avgLoss := a.CalculateSMA(losses[len(losses)-period:], period)

	if avgLoss == 0 {
		return 100.0
	}

	rs := avgGain / avgLoss
	rsi := 100 - (100 / (1 + rs))
	return rsi
}

// CalculateMACD 计算 MACD
func (a *TechnicalAnalyzer) CalculateMACD(data []float64, shortPeriod, longPeriod, signalPeriod int) (macd, signal, hist float64) {
	if len(data) < longPeriod {
		return 0, 0, 0
	}

	shortEMA := a.CalculateEMA(data, shortPeriod)
	longEMA := a.CalculateEMA(data, longPeriod)
	macd = shortEMA - longEMA

	// 计算 MACD 线的历史值用于计算信号线
	macdLine := make([]float64, 0)
	for i := longPeriod; i <= len(data); i++ {
		shortE := a.CalculateEMA(data[:i], shortPeriod)
		longE := a.CalculateEMA(data[:i], longPeriod)
		macdLine = append(macdLine, shortE-longE)
	}

	if len(macdLine) >= signalPeriod {
		signal = a.CalculateEMA(macdLine, signalPeriod)
	}

	hist = macd - signal
	return macd, signal, hist
}

// CalculateKDJ 计算 KDJ 指标
func (a *TechnicalAnalyzer) CalculateKDJ(highs, lows, closes []float64, nPeriod, kPeriod, dPeriod int) (k, d, j float64) {
	if len(closes) < nPeriod {
		return 50, 50, 50
	}

	// 计算 RSV
	var rsvList []float64
	for i := nPeriod - 1; i < len(closes); i++ {
		high := highs[i]
		low := lows[i]
		for j := i - nPeriod + 1; j < i; j++ {
			if highs[j] > high {
				high = highs[j]
			}
			if lows[j] < low {
				low = lows[j]
			}
		}
		if high == low {
			rsvList = append(rsvList, 50)
		} else {
			rsv := (closes[i] - low) / (high - low) * 100
			rsvList = append(rsvList, rsv)
		}
	}

	if len(rsvList) == 0 {
		return 50, 50, 50
	}

	// 计算 K 值 (RSV 的 EMA)
	k = rsvList[0]
	for i := 1; i < len(rsvList); i++ {
		k = (2*rsvList[i] + (float64(kPeriod)-1)*k) / float64(kPeriod+1)
	}

	// 计算 D 值 (K 的 EMA)
	kList := make([]float64, 0)
	tempK := rsvList[0]
	for i := 0; i < len(rsvList); i++ {
		tempK = (2*rsvList[i] + (float64(kPeriod)-1)*tempK) / float64(kPeriod+1)
		kList = append(kList, tempK)
	}

	d = kList[0]
	for i := 1; i < len(kList); i++ {
		d = (2*kList[i] + (float64(dPeriod)-1)*d) / float64(dPeriod+1)
	}

	// 计算 J 值
	j = 3*k - 2*d

	return k, d, j
}

// CalculateBollingerBands 计算布林带
func (a *TechnicalAnalyzer) CalculateBollingerBands(data []float64, period int, stdMultiplier float64) (upper, middle, lower float64) {
	if len(data) < period {
		return 0, 0, 0
	}

	middle = a.CalculateSMA(data, period)
	stdDev := a.CalculateStdDev(data, period)

	upper = middle + stdMultiplier*stdDev
	lower = middle - stdMultiplier*stdDev

	return upper, middle, lower
}

// CalculateStdDev 计算标准差
func (a *TechnicalAnalyzer) CalculateStdDev(data []float64, period int) float64 {
	if len(data) < period {
		return 0
	}

	recentData := data[len(data)-period:]
	mean := a.CalculateSMA(data, period)

	var sumSquares float64
	for _, v := range recentData {
		diff := v - mean
		sumSquares += diff * diff
	}

	variance := sumSquares / float64(period)
	return math.Sqrt(variance)
}

// CalculateATR 计算平均真实波幅
func (a *TechnicalAnalyzer) CalculateATR(highs, lows, closes []float64, period int) float64 {
	if len(closes) < period+1 {
		return 0
	}

	var trList []float64
	for i := 1; i < len(closes); i++ {
		tr := math.Max(
			highs[i]-lows[i],
			math.Max(
				math.Abs(highs[i]-closes[i-1]),
				math.Abs(lows[i]-closes[i-1]),
			),
		)
		trList = append(trList, tr)
	}

	if len(trList) < period {
		return 0
	}

	return a.CalculateSMA(trList[len(trList)-period:], period)
}

// DetermineTrend 判断趋势
func (a *TechnicalAnalyzer) DetermineTrend(closes []float64) string {
	if len(closes) < 20 {
		return "neutral"
	}

	ma5 := a.CalculateSMA(closes, 5)
	ma10 := a.CalculateSMA(closes, 10)
	ma20 := a.CalculateSMA(closes, 20)

	// 多头排列
	if ma5 > ma10 && ma10 > ma20 {
		return "bullish"
	}
	// 空头排列
	if ma5 < ma10 && ma10 < ma20 {
		return "bearish"
	}

	return "neutral"
}

// CalculateSupport 计算支撑位
func (a *TechnicalAnalyzer) CalculateSupport(lows []float64, period int) float64 {
	if len(lows) < period {
		return 0
	}

	recentLows := lows[len(lows)-period:]
	minVal := recentLows[0]
	for _, v := range recentLows {
		if v < minVal {
			minVal = v
		}
	}
	return minVal
}

// CalculateResistance 计算阻力位
func (a *TechnicalAnalyzer) CalculateResistance(highs []float64, period int) float64 {
	if len(highs) < period {
		return 0
	}

	recentHighs := highs[len(highs)-period:]
	maxVal := recentHighs[0]
	for _, v := range recentHighs {
		if v > maxVal {
			maxVal = v
		}
	}
	return maxVal
}
