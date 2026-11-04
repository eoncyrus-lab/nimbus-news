package mathutil

import (
	"github.com/shopspring/decimal"
)

// CalculateMA 计算移动平均线
// data[0] 是最新数据，data[n] 是 n 天前的数据
// window 是窗口大小
func CalculateMA(data []decimal.Decimal, window int) decimal.Decimal {
	if len(data) < window || window <= 0 {
		return decimal.Zero
	}

	sum := decimal.Zero
	for i := 0; i < window; i++ {
		sum = sum.Add(data[i])
	}

	return sum.Div(decimal.NewFromInt(int64(window)))
}

// CalculateEMA 计算指数移动平均
// EMA_today = Price_today * alpha + EMA_yesterday * (1-alpha)
// alpha = 2 / (N+1)
func CalculateEMA(data []decimal.Decimal, window int) decimal.Decimal {
	if len(data) < window || window <= 0 {
		return decimal.Zero
	}

	// Start from the end (oldest) to calc correctly
	// But we only need the EMA at index 0 (today).
	// We need to initialize with SMA at some point?
	// Standard practice: Initialize with SMA of first 'window' elements (furthest back), then iterate.
	// data: [0: Today, 1: Yest, ... N]
	// Let's iterate from window*2 back if possible for precision, or just 'window'.
	// Usually EMA needs ~3.5*N data points to converge.

	lookback := window * 3
	if lookback > len(data) {
		lookback = len(data)
	}

	// Initial SMA at the start of lookback
	// Index: lookback-1
	// We need at least 'window' points to start SMA
	if lookback < window {
		return CalculateMA(data, len(data))
	}

	// Calculate SMA for the oldest window
	// Data indices: [lookback-1 ... lookback-window]
	smaSum := decimal.Zero
	for i := 0; i < window; i++ {
		smaSum = smaSum.Add(data[lookback-1-i])
	}
	ema := smaSum.Div(decimal.NewFromInt(int64(window)))

	// Multiplier
	k := decimal.NewFromFloat(2.0 / float64(window+1))

	// Iterate forward to 0
	// Start from lookback - window - 1 down to 0
	for i := lookback - window - 1; i >= 0; i-- {
		price := data[i]
		// EMA = Price * k + PrevEMA * (1-k)
		ema = price.Mul(k).Add(ema.Mul(decimal.NewFromInt(1).Sub(k)))
	}

	return ema
}

// CalculateMACD
// Returns diff (MACD Line - Signal Line) aka Histogram
// Standard: 12, 26, 9
func CalculateMACD(data []decimal.Decimal) decimal.Decimal {
	if len(data) < 40 { // Need enough data for 26 EMA convergence
		return decimal.Zero
	}

	ema12 := CalculateEMA(data, 12)
	ema26 := CalculateEMA(data, 26)

	// macdLine := ema12.Sub(ema26) // Not used directly, we calculate history
	_ = ema12
	_ = ema26

	// Signal Line is EMA 9 of MACD Line.
	// We can't easily calculate EMA9 of MACD without the history of MACD values.
	// This is tricky stateless.
	// We need to calculate MACD line for the past 9 days.

	// Let's generate MACD history for last 9+ points
	// We need about 9*3 = 27 points of MACD history for Signal EMA.
	// So we need to calculate EMA12/26 for each of those days.

	// Simplified: just return MACD Line for now? No, Histogram is key.
	// Let's calculate MACD values for last 15 days.

	macdHistory := make([]decimal.Decimal, 15)
	for i := 0; i < 15; i++ {
		// Slice data starting from i
		subData := data[i:]
		if len(subData) < 30 {
			break
		}
		e12 := CalculateEMA(subData, 12)
		e26 := CalculateEMA(subData, 26)
		macdHistory[i] = e12.Sub(e26)
	}

	// Signal = EMA9 of macdHistory
	signal := CalculateEMA(macdHistory, 9)

	// Histogram = MACD - Signal
	return macdHistory[0].Sub(signal)
}

// CalculateStdDev
func CalculateStdDev(data []decimal.Decimal, window int) decimal.Decimal {
	if len(data) < window || window <= 0 {
		return decimal.Zero
	}

	mean := CalculateMA(data, window)

	sumSqDiff := decimal.Zero
	for i := 0; i < window; i++ {
		diff := data[i].Sub(mean)
		sumSqDiff = sumSqDiff.Add(diff.Mul(diff))
	}

	variance := sumSqDiff.Div(decimal.NewFromInt(int64(window)))
	// Sqrt
	return variance.Pow(decimal.NewFromFloat(0.5))
}

// CalculateRSI 计算相对强弱指数
// data[0] is latest. We need data[window] (e.g. 14 days) + 1 for changes.
func CalculateRSI(data []decimal.Decimal, window int) decimal.Decimal {
	if len(data) <= window || window <= 0 {
		return decimal.NewFromInt(50) // Default neutral
	}

	var gains, losses decimal.Decimal

	for i := 0; i < window; i++ {
		// Price[i] vs Price[i+1]
		// Price[i] is newer.
		diff := data[i].Sub(data[i+1])

		if diff.GreaterThan(decimal.Zero) {
			gains = gains.Add(diff)
		} else {
			losses = losses.Add(diff.Abs())
		}
	}

	if losses.IsZero() {
		if gains.IsZero() {
			return decimal.NewFromInt(50)
		}
		return decimal.NewFromInt(100)
	}

	avgGain := gains.Div(decimal.NewFromInt(int64(window)))
	avgLoss := losses.Div(decimal.NewFromInt(int64(window)))

	rs := avgGain.Div(avgLoss)

	// 100 - 100 / (1 + RS)
	den := decimal.NewFromInt(1).Add(rs)
	sub := decimal.NewFromInt(100).Div(den)

	return decimal.NewFromInt(100).Sub(sub)
}

// LinearInterpolate 线性插值
// 将 value 从 [inMin, inMax] 映射到 [outMin, outMax]
func LinearInterpolate(value, inMin, inMax, outMin, outMax decimal.Decimal) decimal.Decimal {
	if inMax.Equal(inMin) {
		return outMin
	}
	// (value - inMin) / (inMax - inMin) * (outMax - outMin) + outMin
	ratio := value.Sub(inMin).Div(inMax.Sub(inMin))
	return ratio.Mul(outMax.Sub(outMin)).Add(outMin)
}
