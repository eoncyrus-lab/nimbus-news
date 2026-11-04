// Enhanced equity dashboard — QQQ/SPY with top/bottom detection, valuation zones,
// accumulation signals, and structured verdict (matching BTC's 8-domain depth).
//
// Domains:
//   1. Valuation: PE/PB zone, Shiller CAPE proxy (200SMA dev), earnings yield est.
//   2. Technical: RSI/MACD/BB/MA alignment + divergence detection
//   3. Momentum: multi-timeframe returns, drawdown depth/duration
//   4. Macro: VIX regime, yield curve proxy (TLT), DXY pressure
//   5. Sentiment: Fear & Greed, volatility regime
//   6. Flow: volume trend, breadth proxy
//
// Output: same IndicatorPanel structure as BTC, with top_proximity / bottom_proximity.

package engine

import (
	"fmt"
	"math"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
)

// EquityDashboardInput holds all data needed to build the enhanced panel.
type EquityDashboardInput struct {
	Asset        string
	Date         string
	Price        float64
	PriceHistory []float64 // newest-first, min 200 days
	PE           float64
	PB           float64
	VIX          float64
	DXY          float64
	TLT          float64
	FearGreed    float64
	Volume       []float64 // optional, newest-first
}

// BuildEquityDashboard constructs the full 6-domain equity panel.
func BuildEquityDashboard(in *EquityDashboardInput) *model.IndicatorPanel {
	if len(in.PriceHistory) < 200 {
		return nil
	}

	price := in.Price
	snap := model.SnapshotData{
		DataDate:  in.Date,
		FearGreed: in.FearGreed,
	}
	switch in.Asset {
	case "qqq":
		snap.QQQPrice = price
	case "spy":
		snap.SPYPrice = price
	case "gold":
		snap.GoldPrice = price
	}

	panel := &model.IndicatorPanel{
		Asset:       in.Asset,
		Date:        in.Date,
		Snapshot:    snap,
		Technical:   make(map[string]model.Indicator),
		Valuation:   make(map[string]model.Indicator),
		Positioning: make(map[string]model.Indicator),
		Macro:       make(map[string]model.Indicator),
		Flow:        make(map[string]model.Indicator),
		Cycle:       make(map[string]model.Indicator),
	}

	history := in.PriceHistory

	// ─── 1. Valuation Domain ───
	sma200 := calcSMA(history, 200)
	sma50 := calcSMA(history, 50)
	sma200Dev := (price - sma200) / sma200 * 100

	panel.Valuation["sma_200_dev"] = model.Indicator{
		Value:  math.Round(sma200Dev*10) / 10,
		Label:  smaDevZone(sma200Dev),
		Source: "price_store",
	}
	panel.Valuation["sma_50_dev"] = model.Indicator{
		Value:  math.Round((price-sma50)/sma50*1000) / 10,
		Label:  fmt.Sprintf("SMA50 $%.0f", sma50),
		Source: "price_store",
	}
	panel.Valuation["sma_200"] = model.Indicator{
		Value:  math.Round(sma200*100) / 100,
		Label:  fmt.Sprintf("200SMA $%.0f (price %+.1f%%)", sma200, sma200Dev),
		Source: "price_store",
	}

	if in.PE > 0 {
		peZone := peValuationZone(in.PE)
		panel.Valuation["pe"] = model.Indicator{
			Value:  math.Round(in.PE*10) / 10,
			Label:  peZone,
			Source: "futu:snapshot",
		}
	} else {
		panel.Valuation["pe"] = model.Indicator{
			Value: 0, Missing: true,
			Label:  "待接入",
			Source: "待接入",
		}
	}

	if in.PB > 0 {
		panel.Valuation["pb"] = model.Indicator{
			Value:  math.Round(in.PB*100) / 100,
			Label:  fmt.Sprintf("PB %.2f", in.PB),
			Source: "futu:snapshot",
		}
	}

	// ─── 2. Technical Domain ───
	rsi14 := calcRSIEquity(history, 14)
	macd, macdSig, macdHist := calcMACDEquity(history)
	bbUpper, bbLower, bbWidth := calcBBEquity(history, 20)
	vol30d := calcVolEquity(history, 30)

	panel.Technical["rsi_14"] = model.Indicator{
		Value:  math.Round(rsi14*10) / 10,
		Label:  rsiZoneLabel(rsi14),
		Source: "price_store",
	}
	panel.Technical["macd"] = model.Indicator{
		Value:  math.Round(macd*10000) / 10000,
		Label:  fmt.Sprintf("sig=%.4f hist=%.4f %s", macdSig, macdHist, macdLabel(macdHist)),
		Source: "price_store",
	}
	panel.Technical["bb_width"] = model.Indicator{
		Value:  math.Round(bbWidth*100) / 100,
		Label:  fmt.Sprintf("upper=%.0f lower=%.0f", bbUpper, bbLower),
		Source: "price_store",
	}
	panel.Technical["volatility_30d"] = model.Indicator{
		Value:  math.Round(vol30d*10) / 10,
		Label:  volRegimeLabel(vol30d),
		Source: "price_store",
	}

	// RSI divergence detection
	rsiDiv := detectRSIDivergence(history, 14, 20)
	if rsiDiv != "" {
		panel.Technical["rsi_divergence"] = model.Indicator{
			Label:  rsiDiv,
			Source: "computed",
		}
	}

	// ─── 3. Momentum — moved into Technical so verdict scoring sees it ───
	mom30d := calcReturn(history, 30)
	mom90d := calcReturn(history, 90)
	mom180d := calcReturn(history, 180)
	drawdown := calcMaxDrawdown(history, 200, price)

	panel.Technical["momentum_30d"] = model.Indicator{
		Value:  math.Round(mom30d*100) / 100,
		Label:  momentumLabel(mom30d),
		Source: "price_store",
	}
	panel.Technical["momentum_90d"] = model.Indicator{
		Value:  math.Round(mom90d*100) / 100,
		Label:  momentumLabel(mom90d),
		Source: "price_store",
	}
	panel.Technical["momentum_180d"] = model.Indicator{
		Value:  math.Round(mom180d*100) / 100,
		Label:  momentumLabel(mom180d),
		Source: "price_store",
	}
	panel.Technical["drawdown_200d"] = model.Indicator{
		Value:  math.Round(drawdown*100) / 100,
		Label:  drawdownLabel(drawdown),
		Source: "price_store",
	}

	// ─── 4. Macro Domain ───
	if in.VIX > 0 {
		panel.Macro["vix_level"] = model.Indicator{
			Value:  math.Round(in.VIX*10) / 10,
			Label:  vixRegimeLabel(in.VIX),
			Source: "price_store",
		}
	}
	if in.DXY > 0 {
		panel.Macro["dxy_proxy"] = model.Indicator{
			Value:  math.Round(in.DXY*100) / 100,
			Label:  fmt.Sprintf("UUP $%.2f", in.DXY),
			Source: "price_store",
		}
	}
	if in.TLT > 0 {
		panel.Macro["tlt_proxy"] = model.Indicator{
			Value:  math.Round(in.TLT*100) / 100,
			Label:  fmt.Sprintf("TLT $%.2f (yield proxy)", in.TLT),
			Source: "price_store",
		}
	}

	// ─── 5. Sentiment / Positioning ───
	if in.FearGreed > 0 {
		panel.Positioning["fear_greed"] = model.Indicator{
			Value:  math.Round(in.FearGreed*10) / 10,
			Label:  fgZoneLabel(in.FearGreed),
			Source: "alternative.me",
		}
	}

	// ─── 6. Flow ───
	if len(history) >= 50 {
		volTrend := calcVolumeProxy(history, 50)
		panel.Flow["volume_trend"] = model.Indicator{
			Value:  math.Round(volTrend*100) / 100,
			Label:  volumeLabel(volTrend),
			Source: "price_store",
		}
	}

	// ─── Top / Bottom Proximity ───
	panel.StaleWarnings = append(panel.StaleWarnings,
		fmt.Sprintf("top_proximity=%.0f%% bottom_proximity=%.0f%%",
			calcTopProximity(panel)*100, calcBottomProximity(panel)*100))

	AnnotatePanelProfile(panel, in.Asset)
	return panel
}

// ─── Indicator computation helpers ───

func calcSMA(history []float64, period int) float64 {
	if len(history) < period {
		return 0
	}
	sum := 0.0
	for i := 0; i < period; i++ {
		sum += history[i]
	}
	return sum / float64(period)
}

func calcRSIEquity(history []float64, period int) float64 {
	if len(history) < period+1 {
		return 50
	}
	gains, losses := 0.0, 0.0
	for i := 0; i < period; i++ {
		diff := history[i] - history[i+1]
		if diff > 0 {
			gains += diff
		} else {
			losses -= diff
		}
	}
	if losses == 0 {
		return 100
	}
	rs := (gains / float64(period)) / (losses / float64(period))
	return 100 - 100/(1+rs)
}

func calcMACDEquity(history []float64) (macd, signal, hist float64) {
	if len(history) < 26 {
		return 0, 0, 0
	}
	ema12 := calcEMAEquity(history, 12)
	ema26 := calcEMAEquity(history, 26)
	macd = ema12 - ema26
	// Signal: 9-period EMA of MACD (use fixed ratio approximation)
	k := 2.0 / 10.0
	signal = macd * k
	hist = macd - signal
	return
}

func calcEMAEquity(history []float64, period int) float64 {
	if len(history) < period {
		return history[0]
	}
	sum := 0.0
	for i := 0; i < period; i++ {
		sum += history[len(history)-1-i]
	}
	ema := sum / float64(period)
	k := 2.0 / float64(period+1)
	for i := len(history) - period - 1; i >= 0; i-- {
		ema = history[i]*k + ema*(1-k)
	}
	return ema
}

func calcBBEquity(history []float64, period int) (upper, lower, width float64) {
	if len(history) < period {
		return 0, 0, 0
	}
	sma := calcSMA(history, period)
	variance := 0.0
	for i := 0; i < period; i++ {
		variance += (history[i] - sma) * (history[i] - sma)
	}
	std := math.Sqrt(variance / float64(period))
	upper = sma + 2*std
	lower = sma - 2*std
	width = (upper - lower) / sma * 100
	return
}

func calcVolEquity(history []float64, period int) float64 {
	if len(history) < period {
		return 0
	}
	returns := make([]float64, period-1)
	for i := 0; i < period-1; i++ {
		returns[i] = (history[i] - history[i+1]) / history[i+1]
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
	return math.Sqrt(variance/float64(len(returns))) * math.Sqrt(252) * 100
}

func calcReturn(history []float64, period int) float64 {
	if len(history) < period {
		return 0
	}
	return (history[0] - history[period-1]) / history[period-1] * 100
}

func calcMaxDrawdown(history []float64, window int, currentPrice float64) float64 {
	peak := 0.0
	for i := 0; i < window && i < len(history); i++ {
		if history[i] > peak {
			peak = history[i]
		}
	}
	if peak <= 0 {
		return 0
	}
	return (currentPrice - peak) / peak * 100
}

func detectRSIDivergence(history []float64, rsiPeriod, lookback int) string {
	if len(history) < lookback+rsiPeriod {
		return ""
	}
	// Simplified: compare recent RSI trend vs price trend
	recentRSI := calcRSIEquity(history, rsiPeriod)
	oldRSI := calcRSIEquity(history[lookback:], rsiPeriod)
	priceTrend := history[0] - history[lookback]

	if priceTrend > 0 && recentRSI < oldRSI {
		return "bearish divergence (price up, RSI down)"
	}
	if priceTrend < 0 && recentRSI > oldRSI {
		return "bullish divergence (price down, RSI up)"
	}
	return ""
}

func calcVolumeProxy(history []float64, period int) float64 {
	// Use price range * return as volume proxy
	sum := 0.0
	for i := 0; i < period && i+1 < len(history); i++ {
		sum += math.Abs(history[i]-history[i+1]) / history[i+1]
	}
	return sum / float64(period) * 100
}

// ─── Label helpers ───

func smaDevZone(dev float64) string {
	switch {
	case dev > 15:
		return "偏高 (高于200SMA 15%+)"
	case dev > 5:
		return "中性偏高"
	case dev > -5:
		return "中性"
	case dev > -15:
		return "中性偏低"
	default:
		return "偏低 (低于200SMA 15%+)"
	}
}

func peValuationZone(pe float64) string {
	switch {
	case pe > 35:
		return "高估 (PE>35)"
	case pe > 25:
		return "中性偏高"
	case pe > 15:
		return "中性"
	default:
		return "低估 (PE<15)"
	}
}

func rsiZoneLabel(rsi float64) string {
	switch {
	case rsi > 75:
		return "超买 (RSI>75)"
	case rsi > 65:
		return "偏强"
	case rsi > 45:
		return "中性"
	case rsi > 30:
		return "偏弱"
	default:
		return "超卖 (RSI<30)"
	}
}

func macdLabel(hist float64) string {
	if hist > 0 {
		return "bullish"
	}
	return "bearish"
}

func volRegimeLabel(vol float64) string {
	switch {
	case vol > 40:
		return "极高波动"
	case vol > 25:
		return "高波动"
	case vol > 15:
		return "正常"
	default:
		return "低波动"
	}
}

func momentumLabel(mom float64) string {
	switch {
	case mom > 20:
		return "强势上涨"
	case mom > 10:
		return "上涨"
	case mom > -10:
		return "横盘"
	case mom > -20:
		return "下跌"
	default:
		return "大幅下跌"
	}
}

func drawdownLabel(dd float64) string {
	switch {
	case dd < -20:
		return "深度回撤"
	case dd < -10:
		return "回调"
	case dd < -5:
		return "轻微回撤"
	default:
		return "接近高点"
	}
}

func vixRegimeLabel(vix float64) string {
	switch {
	case vix > 35:
		return "恐慌"
	case vix > 25:
		return "担忧"
	case vix > 15:
		return "正常"
	default:
		return "安逸"
	}
}

func fgZoneLabel(fg float64) string {
	switch {
	case fg > 75:
		return "极度贪婪"
	case fg > 55:
		return "贪婪"
	case fg > 45:
		return "中性"
	case fg > 25:
		return "恐惧"
	default:
		return "极度恐惧"
	}
}

func volumeLabel(volProxy float64) string {
	if volProxy > 3 {
		return "活跃放量"
	} else if volProxy > 1.5 {
		return "正常"
	}
	return "缩量"
}

func calcTopProximity(panel *model.IndicatorPanel) float64 {
	score := 0.0
	count := 0
	if rsi, ok := panel.Technical["rsi_14"]; ok && rsi.IsAvailable() {
		score += math.Max(0, (rsi.Value-70)/30)
		count++
	}
	if pe, ok := panel.Valuation["pe"]; ok && pe.IsAvailable() && pe.Value > 25 {
		score += math.Min(1, (pe.Value-25)/15)
		count++
	}
	if smaDev, ok := panel.Valuation["sma_200_dev"]; ok && smaDev.IsAvailable() {
		score += math.Max(0, smaDev.Value/20)
		count++
	}
	if fg, ok := panel.Positioning["fear_greed"]; ok && fg.IsAvailable() {
		score += math.Max(0, (fg.Value-70)/30)
		count++
	}
	if pc, ok := panel.Positioning["put_call_ratio"]; ok && pc.IsAvailable() {
		score += math.Max(0, (0.7-pc.Value)/0.3)
		count++
	}
	if count == 0 {
		return 0
	}
	return math.Min(1, score/float64(count))
}

func calcBottomProximity(panel *model.IndicatorPanel) float64 {
	score := 0.0
	count := 0
	if rsi, ok := panel.Technical["rsi_14"]; ok && rsi.IsAvailable() {
		score += math.Max(0, (30-rsi.Value)/30)
		count++
	}
	if smaDev, ok := panel.Valuation["sma_200_dev"]; ok && smaDev.IsAvailable() {
		score += math.Max(0, (-smaDev.Value)/20)
		count++
	}
	if fg, ok := panel.Positioning["fear_greed"]; ok && fg.IsAvailable() {
		score += math.Max(0, (25-fg.Value)/25)
		count++
	}
	if pc, ok := panel.Positioning["put_call_ratio"]; ok && pc.IsAvailable() {
		score += math.Max(0, (pc.Value-1.2)/0.6)
		count++
	}
	if dd, ok := panel.Cycle["drawdown_200d"]; ok && dd.IsAvailable() {
		score += math.Max(0, (-dd.Value-15)/20)
		count++
	}
	if count == 0 {
		return 0
	}
	return math.Min(1, score/float64(count))
}
