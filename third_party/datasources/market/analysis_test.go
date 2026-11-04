package market

import (
	"math"
	"testing"
)

func TestTechnicalAnalyzer_CalculateSMA(t *testing.T) {
	analyzer := NewTechnicalAnalyzer()

	tests := []struct {
		name     string
		data     []float64
		period   int
		expected float64
	}{
		{
			name:     "simple 5-period SMA",
			data:     []float64{1, 2, 3, 4, 5},
			period:   5,
			expected: 3.0,
		},
		{
			name:     "10 values, 5-period SMA",
			data:     []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
			period:   5,
			expected: 8.0, // (6+7+8+9+10)/5
		},
		{
			name:     "insufficient data",
			data:     []float64{1, 2, 3},
			period:   5,
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := analyzer.CalculateSMA(tt.data, tt.period)
			if result != tt.expected {
				t.Errorf("CalculateSMA() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestTechnicalAnalyzer_CalculateEMA(t *testing.T) {
	analyzer := NewTechnicalAnalyzer()

	// EMA 需要更复杂的验证
	data := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	result := analyzer.CalculateEMA(data, 5)

	// EMA 应该在数据范围内且接近最近值
	if result <= 0 || result > 10 {
		t.Errorf("CalculateEMA() = %v, expected value between 0 and 10", result)
	}

	// 空数据测试
	emptyResult := analyzer.CalculateEMA([]float64{}, 5)
	if emptyResult != 0 {
		t.Errorf("CalculateEMA(empty) = %v, expected 0", emptyResult)
	}
}

func TestTechnicalAnalyzer_CalculateRSI(t *testing.T) {
	analyzer := NewTechnicalAnalyzer()

	// 持续上涨，RSI 应该很高
	upData := make([]float64, 20)
	for i := range upData {
		upData[i] = float64(i + 1)
	}
	upRSI := analyzer.CalculateRSI(upData, 14)
	if upRSI <= 70 {
		t.Errorf("RSI for uptrend = %v, expected > 70", upRSI)
	}

	// 持续下跌，RSI 应该很低
	downData := make([]float64, 20)
	for i := range downData {
		downData[i] = float64(20 - i)
	}
	downRSI := analyzer.CalculateRSI(downData, 14)
	if downRSI >= 30 {
		t.Errorf("RSI for downtrend = %v, expected < 30", downRSI)
	}

	// 数据不足返回默认值 50
	shortData := []float64{1, 2, 3}
	shortRSI := analyzer.CalculateRSI(shortData, 14)
	if shortRSI != 50.0 {
		t.Errorf("RSI for short data = %v, expected 50", shortRSI)
	}
}

func TestTechnicalAnalyzer_CalculateMACD(t *testing.T) {
	analyzer := NewTechnicalAnalyzer()

	// 生成测试数据
	data := make([]float64, 50)
	for i := range data {
		data[i] = float64(i) + 100
	}

	macd, signal, hist := analyzer.CalculateMACD(data, 12, 26, 9)

	// MACD 线应该是非零的（因为短期 EMA > 长期 EMA 在上涨趋势中）
	if macd == 0 {
		t.Error("MACD should not be zero for trending data")
	}

	// 柱状图应该等于 MACD - Signal
	expectedHist := macd - signal
	if math.Abs(hist-expectedHist) > 0.0001 {
		t.Errorf("MACD histogram = %v, expected %v", hist, expectedHist)
	}

	// 数据不足
	shortMacd, shortSignal, shortHist := analyzer.CalculateMACD([]float64{1, 2, 3}, 12, 26, 9)
	if shortMacd != 0 || shortSignal != 0 || shortHist != 0 {
		t.Error("MACD should be 0 for insufficient data")
	}
}

func TestTechnicalAnalyzer_CalculateKDJ(t *testing.T) {
	analyzer := NewTechnicalAnalyzer()

	// 生成测试数据
	n := 30
	highs := make([]float64, n)
	lows := make([]float64, n)
	closes := make([]float64, n)

	for i := 0; i < n; i++ {
		base := float64(100 + i)
		highs[i] = base + 5
		lows[i] = base - 5
		closes[i] = base
	}

	k, d, j := analyzer.CalculateKDJ(highs, lows, closes, 9, 3, 3)

	// KDJ 值应该在合理范围内
	if k < 0 || k > 100 {
		t.Errorf("K value %v is out of range [0, 100]", k)
	}
	if d < 0 || d > 100 {
		t.Errorf("D value %v is out of range [0, 100]", d)
	}

	// J 可以超出 0-100 范围
	if math.IsNaN(j) {
		t.Error("J value should not be NaN")
	}
}

func TestTechnicalAnalyzer_CalculateBollingerBands(t *testing.T) {
	analyzer := NewTechnicalAnalyzer()

	data := []float64{100, 102, 98, 101, 103, 99, 100, 102, 101, 100,
		98, 102, 103, 101, 100, 99, 101, 102, 100, 101}

	upper, middle, lower := analyzer.CalculateBollingerBands(data, 20, 2)

	// 中轨应该是 SMA
	expectedMiddle := analyzer.CalculateSMA(data, 20)
	if middle != expectedMiddle {
		t.Errorf("middle band = %v, expected SMA %v", middle, expectedMiddle)
	}

	// 上轨 > 中轨 > 下轨
	if upper <= middle {
		t.Errorf("upper band %v should be > middle band %v", upper, middle)
	}
	if middle <= lower {
		t.Errorf("middle band %v should be > lower band %v", middle, lower)
	}
}

func TestTechnicalAnalyzer_CalculateATR(t *testing.T) {
	analyzer := NewTechnicalAnalyzer()

	n := 20
	highs := make([]float64, n)
	lows := make([]float64, n)
	closes := make([]float64, n)

	for i := 0; i < n; i++ {
		highs[i] = float64(105 + i)
		lows[i] = float64(95 + i)
		closes[i] = float64(100 + i)
	}

	atr := analyzer.CalculateATR(highs, lows, closes, 14)

	// ATR 应该是正数
	if atr <= 0 {
		t.Errorf("ATR should be positive, got %v", atr)
	}

	// ATR 应该反映波动幅度 (high - low = 10)
	if atr < 5 || atr > 15 {
		t.Errorf("ATR %v seems unreasonable for high-low range of 10", atr)
	}
}

func TestTechnicalAnalyzer_DetermineTrend(t *testing.T) {
	analyzer := NewTechnicalAnalyzer()

	// 上涨趋势 (MA5 > MA10 > MA20)
	upTrend := make([]float64, 30)
	for i := range upTrend {
		upTrend[i] = float64(i + 1)
	}
	if analyzer.DetermineTrend(upTrend) != "bullish" {
		t.Error("expected bullish trend for upward data")
	}

	// 下跌趋势 (MA5 < MA10 < MA20)
	downTrend := make([]float64, 30)
	for i := range downTrend {
		downTrend[i] = float64(30 - i)
	}
	if analyzer.DetermineTrend(downTrend) != "bearish" {
		t.Error("expected bearish trend for downward data")
	}

	// 数据不足
	if analyzer.DetermineTrend([]float64{1, 2, 3}) != "neutral" {
		t.Error("expected neutral for insufficient data")
	}
}

func TestTechnicalAnalyzer_CalculateSupport(t *testing.T) {
	analyzer := NewTechnicalAnalyzer()

	lows := []float64{100, 98, 95, 97, 99, 96, 94, 98, 100, 95,
		93, 97, 98, 96, 95, 94, 92, 95, 96, 94}

	support := analyzer.CalculateSupport(lows, 20)

	// 支撑位应该是最低点
	if support != 92 {
		t.Errorf("support = %v, expected 92", support)
	}
}

func TestTechnicalAnalyzer_CalculateResistance(t *testing.T) {
	analyzer := NewTechnicalAnalyzer()

	highs := []float64{100, 102, 105, 103, 101, 104, 106, 102, 100, 105,
		107, 103, 102, 104, 105, 106, 108, 105, 104, 106}

	resistance := analyzer.CalculateResistance(highs, 20)

	// 阻力位应该是最高点
	if resistance != 108 {
		t.Errorf("resistance = %v, expected 108", resistance)
	}
}

func BenchmarkTechnicalAnalyzer_CalculateMACD(b *testing.B) {
	analyzer := NewTechnicalAnalyzer()
	data := make([]float64, 100)
	for i := range data {
		data[i] = float64(i) + 100
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		analyzer.CalculateMACD(data, 12, 26, 9)
	}
}

func BenchmarkTechnicalAnalyzer_CalculateRSI(b *testing.B) {
	analyzer := NewTechnicalAnalyzer()
	data := make([]float64, 100)
	for i := range data {
		data[i] = float64(i) + 100
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		analyzer.CalculateRSI(data, 14)
	}
}
