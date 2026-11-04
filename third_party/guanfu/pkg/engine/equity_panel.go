// equity_panel.go — shared market panel helpers plus the QQQ/SPY equity wrapper.
//
// Shared domains:
//   - technical: RSI(14), MACD, SMA(50), SMA(200), BB_width, volatility_30d
//   - macro: vix_level, dxy_trend, tlt_trend (10Y proxy)
//   - sentiment: fear_greed (when available)
//   - valuation: pe, pb when an equity caller supplies valuation snapshots
//
// BuildMarketPanel is asset-neutral. BuildEquityPanel remains a compatibility
// wrapper for QQQ/SPY callers so equity semantics stay explicit at the call site.

package engine

import (
	"fmt"
	"math"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/assetprofile"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
)

// MarketPanelInput is the data needed to build a shared technical/macro panel.
// The caller owns asset-specific semantics and any extra valuation domains.
type MarketPanelInput struct {
	Asset     string
	Date      string
	Price     float64
	PriceAsOf string

	// Price history (newest-first, at least 200 data points for SMA200)
	PriceHistory []float64

	// Cross-asset context
	VIX  float64 // VIXY or VIX level
	DXY  float64 // UUP or DXY
	TLT  float64 // long-end Treasury proxy
	Gold float64 // gold price (optional)

	// Sentiment
	FearGreed float64 // 0-100

	// Valuation (Phase 3)
	PE float64
	PB float64
}

// EquityPanelInput is retained for source compatibility with existing QQQ/SPY
// code. New non-equity assets should use MarketPanelInput directly.
type EquityPanelInput = MarketPanelInput

// BuildEquityPanel computes technical + macro indicators for an equity ETF.
func BuildEquityPanel(in *EquityPanelInput) *model.IndicatorPanel {
	return BuildMarketPanel(in)
}

// BuildMarketPanel computes shared technical + macro indicators without
// assigning equity-specific reading semantics.
func BuildMarketPanel(in *MarketPanelInput) *model.IndicatorPanel {
	date := in.Date
	if date == "" {
		date = time.Now().UTC().Format("2006-01-02")
	}

	snap := model.SnapshotData{
		GoldPrice: in.Gold,
		DataDate:  date,
		FearGreed: in.FearGreed,
	}
	switch in.Asset {
	case "qqq":
		snap.QQQPrice = in.Price
	case "spy":
		snap.SPYPrice = in.Price
	case "gold":
		snap.GoldPrice = in.Price
	}

	panel := &model.IndicatorPanel{
		Asset:       in.Asset,
		Date:        date,
		Snapshot:    snap,
		Technical:   make(map[string]model.Indicator),
		Macro:       make(map[string]model.Indicator),
		Positioning: make(map[string]model.Indicator),
		Valuation:   make(map[string]model.Indicator),
	}

	// ── Technical domain ──
	if len(in.PriceHistory) >= 14 {
		panel.Technical["rsi_14"] = computeRSI(in.PriceHistory, 14)
	}
	if len(in.PriceHistory) >= 26 {
		panel.Technical["macd"] = computeMACDIndicator(in.PriceHistory)
	}
	if len(in.PriceHistory) >= 50 {
		panel.Technical["sma_50"] = computeSMAIndicator(in.PriceHistory, 50, in.Price)
	}
	if len(in.PriceHistory) >= 200 {
		panel.Technical["sma_200"] = computeSMAIndicator(in.PriceHistory, 200, in.Price)
		panel.Technical["sma_200_dev"] = computeSMADev(in.PriceHistory, 200, in.Price)
	}
	if len(in.PriceHistory) >= 20 {
		panel.Technical["bb_width"] = computeBBWidth(in.PriceHistory, 20)
	}
	if len(in.PriceHistory) >= 30 {
		panel.Technical["volatility_30d"] = computeVol30d(in.PriceHistory)
	}
	// Price momentum
	if len(in.PriceHistory) >= 90 {
		panel.Technical["momentum_90d"] = computeMomentum(in.PriceHistory, 90)
	}
	if len(in.PriceHistory) >= 200 {
		panel.Technical["drawdown_200d"] = computeDrawdown(in.PriceHistory, in.Price)
	}

	// ── Macro domain ──
	if in.VIX > 0 {
		panel.Macro["vix_level"] = model.Indicator{
			Value:  in.VIX,
			Label:  vixLabel(in.VIX),
			Source: "price_store:vixy",
		}
	}
	if in.DXY > 0 {
		panel.Macro["dxy_proxy"] = model.Indicator{
			Value:  in.DXY,
			Label:  "UUP (DXY proxy)",
			Source: "price_store:uup",
		}
	}
	if in.TLT > 0 {
		panel.Macro["tlt_proxy"] = model.Indicator{
			Value:  in.TLT,
			Label:  "TLT (long-end Treasury proxy)",
			Source: "price_store:tlt",
		}
	}

	// ── Sentiment domain (via positioning) ──
	if in.FearGreed > 0 {
		panel.Positioning["fear_greed"] = model.Indicator{
			Value:  in.FearGreed,
			Label:  fgLabel(in.FearGreed),
			Source: "alternative.me",
		}
	}

	// ── Valuation domain ──
	if in.PE > 0 {
		panel.Valuation["pe"] = model.Indicator{
			Value:  in.PE,
			Label:  peLabel(in.PE),
			Source: "futu:snapshot",
		}
	}
	if in.PB > 0 {
		panel.Valuation["pb"] = model.Indicator{
			Value:  in.PB,
			Label:  fmt.Sprintf("PB %.2f", in.PB),
			Source: "futu:snapshot",
		}
	}

	AnnotatePanelProfile(panel, in.Asset)
	return panel
}

// ─── Indicator computation helpers ──────────────────────

func computeRSI(history []float64, period int) model.Indicator {
	rsi := calcRSI(history, period)
	return model.Indicator{
		Value:  rsi,
		Label:  rsiLabel(rsi),
		Source: "price_store",
	}
}

func calcRSI(closes []float64, period int) float64 {
	if len(closes) < period+1 {
		return 0
	}
	var gains, losses float64
	for i := 1; i <= period; i++ {
		diff := closes[len(closes)-1-i] - closes[len(closes)-i]
		if diff > 0 {
			gains += diff
		} else {
			losses -= diff
		}
	}
	avgGain := gains / float64(period)
	avgLoss := losses / float64(period)
	if avgLoss == 0 {
		return 100
	}
	rs := avgGain / avgLoss
	return 100 - (100 / (1 + rs))
}

func computeMACDIndicator(history []float64) model.Indicator {
	macd, signal, hist := calcMACD(history)
	return model.Indicator{
		Value:  macd,
		Label:  fmt.Sprintf("MACD=%.4f sig=%.4f hist=%.4f", macd, signal, hist),
		Source: "price_store",
	}
}

func calcMACD(closes []float64) (macd, signal, histogram float64) {
	if len(closes) < 26 {
		return 0, 0, 0
	}
	ema12 := calcEMA(closes, 12)
	ema26 := calcEMA(closes, 26)
	macd = ema12 - ema26

	// Signal line: 9-period EMA of MACD
	// Simplified: use a single-point approximation
	signal = macd * 0.9 // approximation
	histogram = macd - signal
	return
}

func calcEMA(closes []float64, period int) float64 {
	if len(closes) < period {
		return 0
	}
	// Start with SMA for initial EMA
	var sum float64
	for i := 0; i < period; i++ {
		sum += closes[len(closes)-1-i]
	}
	ema := sum / float64(period)
	multiplier := 2.0 / float64(period+1)

	// Calculate EMA for remaining points
	for i := period; i < len(closes); i++ {
		price := closes[len(closes)-1-i]
		ema = (price-ema)*multiplier + ema
	}
	return ema
}

func computeSMAIndicator(history []float64, period int, currentPrice float64) model.Indicator {
	if len(history) < period {
		return model.Indicator{Missing: true}
	}
	var sum float64
	for i := 0; i < period && i < len(history); i++ {
		sum += history[i]
	}
	sma := sum / float64(period)
	dev := (currentPrice - sma) / sma * 100
	return model.Indicator{
		Value:    sma,
		Quantile: 0,
		Label:    fmt.Sprintf("$%.2f (price %+.1f%%)", sma, dev),
		Source:   "price_store",
	}
}

func computeSMADev(history []float64, period int, currentPrice float64) model.Indicator {
	if len(history) < period {
		return model.Indicator{Missing: true}
	}
	var sum float64
	for i := 0; i < period && i < len(history); i++ {
		sum += history[i]
	}
	sma := sum / float64(period)
	dev := (currentPrice - sma) / sma * 100
	return model.Indicator{
		Value:  dev,
		Label:  smaDevLabel(dev),
		Source: "price_store",
	}
}

func smaDevLabel(dev float64) string {
	switch {
	case dev > 20:
		return "极度偏离上方"
	case dev > 10:
		return "偏高"
	case dev < -20:
		return "极度偏离下方"
	case dev < -10:
		return "偏低"
	default:
		return "正常范围"
	}
}

func computeBBWidth(history []float64, period int) model.Indicator {
	if len(history) < period {
		return model.Indicator{Missing: true}
	}
	// Compute SMA
	var sum float64
	start := len(history) - period
	if start < 0 {
		start = 0
	}
	n := len(history) - start
	if n == 0 {
		return model.Indicator{Missing: true}
	}
	for i := start; i < len(history); i++ {
		sum += history[i]
	}
	sma := sum / float64(n)

	// Compute std dev
	var variance float64
	for i := start; i < len(history); i++ {
		variance += (history[i] - sma) * (history[i] - sma)
	}
	std := math.Sqrt(variance / float64(n))
	bbWidth := (2 * std) / sma * 100

	return model.Indicator{
		Value:  bbWidth,
		Label:  fmt.Sprintf("BB宽度 %.2f%%", bbWidth),
		Source: "price_store",
	}
}

func computeVol30d(history []float64) model.Indicator {
	if len(history) < 30 {
		return model.Indicator{Missing: true}
	}
	// Daily returns
	returns := make([]float64, 29)
	for i := 0; i < 29; i++ {
		returns[i] = (history[i] - history[i+1]) / history[i+1]
	}
	// Mean
	var mean float64
	for _, r := range returns {
		mean += r
	}
	mean /= float64(len(returns))

	// Std dev
	var variance float64
	for _, r := range returns {
		variance += (r - mean) * (r - mean)
	}
	std := math.Sqrt(variance / float64(len(returns)))
	annualVol := std * math.Sqrt(252) * 100

	return model.Indicator{
		Value:  annualVol,
		Label:  fmt.Sprintf("%.1f%% 年化", annualVol),
		Source: "price_store",
	}
}

func computeMomentum(history []float64, days int) model.Indicator {
	if len(history) < days {
		return model.Indicator{Missing: true}
	}
	pct := (history[0] - history[days-1]) / history[days-1] * 100
	return model.Indicator{
		Value:  pct,
		Label:  fmt.Sprintf("%+.1f%% (%dd)", pct, days),
		Source: "price_store",
	}
}

func computeDrawdown(history []float64, currentPrice float64) model.Indicator {
	if len(history) < 2 {
		return model.Indicator{Missing: true}
	}
	peak := history[0]
	for _, p := range history {
		if p > peak {
			peak = p
		}
	}
	dd := (currentPrice - peak) / peak * 100
	return model.Indicator{
		Value:  dd,
		Label:  fmt.Sprintf("%+.1f%% from 200d high $%.2f", dd, peak),
		Source: "price_store",
	}
}

// ─── Label helpers ──────────────────────────────────────

func vixLabel(vix float64) string {
	switch {
	case vix > 30:
		return "恐慌"
	case vix > 20:
		return "偏高"
	case vix > 12:
		return "正常"
	default:
		return "极低波动"
	}
}

func fgLabel(fg float64) string {
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

func peLabel(pe float64) string {
	switch {
	case pe > 30:
		return "偏高"
	case pe > 20:
		return "中性偏高"
	case pe > 15:
		return "中性"
	default:
		return "偏低"
	}
}

// ─── Equity verdict ─────────────────────────────────────

// BuildEquityVerdict produces a simple multi-domain verdict for equity ETFs.
func BuildEquityVerdict(panel *model.IndicatorPanel) *Verdict {
	return BuildProfiledMarketVerdict(panel)
}

// BuildProfiledMarketVerdict scores shared market-panel domains using the
// active asset profile's verdict policy. Feature interpretation still lives in
// engine scoring helpers; domain order, thresholds, and stance language live in
// pkg/assetprofile.
func BuildProfiledMarketVerdict(panel *model.IndicatorPanel) *Verdict {
	policy := marketVerdictPolicy(panel)
	v := &Verdict{
		Date:            panel.Date,
		Confidence:      "中",
		Domains:         make([]DomainVote, 0, len(policy.DomainOrder)),
		Reasons:         make([]string, 0),
		CounterEvidence: make([]string, 0),
		KillCriteria:    make([]string, 0),
	}

	totalCoverage := 0.0
	for _, domain := range policy.DomainOrder {
		vote, bull, bear, coverage := scoreProfiledMarketDomain(panel, domain)
		v.NetDirection += vote
		totalCoverage += coverage
		v.Domains = append(v.Domains, DomainVote{
			Domain:   domain,
			Vote:     vote,
			Bullish:  bull,
			Bearish:  bear,
			Coverage: coverage,
		})
	}
	if len(v.Domains) > 0 {
		v.Coverage = totalCoverage / float64(len(v.Domains))
	}

	applyVerdictPolicy(v, policy)
	v.Reasons, v.CounterEvidence = pickEvidenceFromVotes(v.Domains, v.NetDirection)
	v.TopProximity = topProximityFromPanel(panel)
	v.BottomProximity = bottomProximityFromPanel(panel)

	return v
}

func marketVerdictPolicy(panel *model.IndicatorPanel) assetprofile.VerdictPolicy {
	asset := ""
	if panel != nil {
		asset = panel.Asset
		if asset == "" {
			asset = panel.ProfileKey
		}
	}
	if asset != "" {
		if policy, ok := assetprofile.VerdictPolicyFor(asset); ok && len(policy.DomainOrder) > 0 {
			return policy
		}
	}
	policy, _ := assetprofile.VerdictPolicyFor("qqq")
	if len(policy.DomainOrder) > 0 {
		return policy
	}
	return assetprofile.VerdictPolicy{
		DomainOrder:          []string{"technical", "macro", "positioning"},
		BullThreshold:        3,
		BearThreshold:        -3,
		BullRegime:           "趋势偏强",
		NeutralRegime:        "震荡/不确定",
		BearRegime:           "趋势偏弱",
		BullStance:           "技术面偏多，宏观配合",
		NeutralStance:        "方向不明确，需等待信号确认",
		BearStance:           "技术面偏空，需关注宏观转折",
		LowCoverageThreshold: 0.5,
	}
}

func scoreProfiledMarketDomain(panel *model.IndicatorPanel, domain string) (vote int, bull, bear []string, coverage float64) {
	asset := panel.Asset
	if asset == "" {
		asset = panel.ProfileKey
	}
	if asset == "" {
		asset = "qqq" // default equity policy when asset is unknown
	}
	switch domain {
	case "technical":
		vote, bull, bear = scoreEquityTechnicalForAsset(panel.Technical, asset)
		coverage = coverageScore(panel.Technical)
	case "macro":
		vote, bull, bear = scoreEquityMacroForAsset(panel.Macro, asset)
		coverage = coverageScore(panel.Macro)
	case "positioning":
		vote, bull, bear = scoreEquityPositioningForAsset(panel.Positioning, asset)
		coverage = coverageScore(panel.Positioning)
	default:
		coverage = 0
	}
	return vote, bull, bear, coverage
}

func applyVerdictPolicy(v *Verdict, policy assetprofile.VerdictPolicy) {
	switch {
	case v.NetDirection >= policy.BullThreshold:
		v.Regime = policy.BullRegime
		v.Stance = policy.BullStance
	case v.NetDirection <= policy.BearThreshold:
		v.Regime = policy.BearRegime
		v.Stance = policy.BearStance
	default:
		v.Regime = policy.NeutralRegime
		v.Stance = policy.NeutralStance
	}
	if v.Regime == "" {
		v.Regime = "中性"
	}
	if v.Stance == "" {
		v.Stance = "方向不明确，需等待信号确认"
	}
	lowCoverage := policy.LowCoverageThreshold
	if lowCoverage <= 0 {
		lowCoverage = 0.5
	}
	if v.Coverage < lowCoverage {
		v.Confidence = "低"
		v.Stance += " (低覆盖)"
	}
}

func scoreEquityTechnicalForAsset(tech map[string]model.Indicator, asset string) (vote int, bull, bear []string) {
	rules := assetprofile.ScoringRulesFor(asset, "technical")
	if rsi, ok := tech["rsi_14"]; ok && rsi.IsAvailable() {
		if applyRule(rules, "rsi_14", rsi.Value) > 0 {
			bull = append(bull, fmt.Sprintf("RSI超卖(%.0f)", rsi.Value))
			vote++
		} else if applyRule(rules, "rsi_14", rsi.Value) < 0 {
			bear = append(bear, fmt.Sprintf("RSI超买(%.0f)", rsi.Value))
			vote--
		}
	}
	if smaDev, ok := tech["sma_200_dev"]; ok && smaDev.IsAvailable() {
		if applyRule(rules, "sma_200_dev", smaDev.Value) > 0 {
			bull = append(bull, "价格高于200SMA 10%+")
			vote++
		} else if applyRule(rules, "sma_200_dev", smaDev.Value) < 0 {
			bear = append(bear, "价格低于200SMA 10%+")
			vote--
		}
	}
	if mom, ok := tech["momentum_90d"]; ok && mom.IsAvailable() {
		if applyRule(rules, "momentum_90d", mom.Value) > 0 {
			bull = append(bull, fmt.Sprintf("90d动量强(+%.1f%%)", mom.Value))
			vote++
		} else if applyRule(rules, "momentum_90d", mom.Value) < 0 {
			bear = append(bear, fmt.Sprintf("90d动量弱(%.1f%%)", mom.Value))
			vote--
		}
	}
	return
}

func scoreEquityMacroForAsset(macro map[string]model.Indicator, asset string) (vote int, bull, bear []string) {
	rules := assetprofile.ScoringRulesFor(asset, "macro")
	if vix, ok := macro["vix_level"]; ok && vix.IsAvailable() {
		if applyRule(rules, "vix_level", vix.Value) > 0 {
			bull = append(bull, fmt.Sprintf("VIX低波动(%.0f)", vix.Value))
			vote++
		} else if applyRule(rules, "vix_level", vix.Value) < 0 {
			bear = append(bear, fmt.Sprintf("VIX恐慌(%.0f)", vix.Value))
			vote--
		}
	}
	return
}

func scoreEquityPositioningForAsset(pos map[string]model.Indicator, asset string) (vote int, bull, bear []string) {
	rules := assetprofile.ScoringRulesFor(asset, "positioning")
	if fg, ok := pos["fear_greed"]; ok && fg.IsAvailable() {
		if applyRule(rules, "fear_greed", fg.Value) > 0 {
			bull = append(bull, fmt.Sprintf("极度恐惧(%.0f)", fg.Value))
			vote++
		} else if applyRule(rules, "fear_greed", fg.Value) < 0 {
			bear = append(bear, fmt.Sprintf("极度贪婪(%.0f)", fg.Value))
			vote--
		}
	}
	if pc, ok := pos["put_call_ratio"]; ok && pc.IsAvailable() {
		if applyRule(rules, "put_call_ratio", pc.Value) > 0 {
			bull = append(bull, fmt.Sprintf("Put/Call恐惧(%.2f)", pc.Value))
			vote++
		} else if applyRule(rules, "put_call_ratio", pc.Value) < 0 {
			bear = append(bear, fmt.Sprintf("Put/Call追涨(%.2f)", pc.Value))
			vote--
		}
	}
	return
}

// applyRule evaluates an IndicatorRule against a value.
// Returns +1 (bull), -1 (bear), or 0 (neutral/no rule).
// When rules map is nil or the key is absent, falls back to 0 (no signal).
func applyRule(rules map[string]assetprofile.IndicatorRule, key string, value float64) int {
	if rules == nil {
		return 0
	}
	r, ok := rules[key]
	if !ok {
		return 0
	}
	bull := (r.BullBelow != nil && value < *r.BullBelow) || (r.BullAbove != nil && value > *r.BullAbove)
	bear := (r.BearBelow != nil && value < *r.BearBelow) || (r.BearAbove != nil && value > *r.BearAbove)
	switch {
	case bull && !bear:
		return +1
	case bear && !bull:
		return -1
	default:
		return 0
	}
}

func coverageScore(m map[string]model.Indicator) float64 {
	if len(m) == 0 {
		return 0
	}
	available := 0
	for _, ind := range m {
		if ind.IsAvailable() {
			available++
		}
	}
	return float64(available) / float64(len(m))
}

func topProximityFromPanel(p *model.IndicatorPanel) float64 {
	if rsi, ok := p.Technical["rsi_14"]; ok && rsi.IsAvailable() && rsi.Value > 70 {
		return math.Min(1.0, (rsi.Value-70)/30)
	}
	if pc, ok := p.Positioning["put_call_ratio"]; ok && pc.IsAvailable() && pc.Value < 0.7 {
		return math.Min(1.0, (0.7-pc.Value)/0.3)
	}
	return 0
}

func bottomProximityFromPanel(p *model.IndicatorPanel) float64 {
	if rsi, ok := p.Technical["rsi_14"]; ok && rsi.IsAvailable() && rsi.Value < 30 {
		return math.Min(1.0, (30-rsi.Value)/30)
	}
	if pc, ok := p.Positioning["put_call_ratio"]; ok && pc.IsAvailable() && pc.Value > 1.2 {
		return math.Min(1.0, (pc.Value-1.2)/0.6)
	}
	return 0
}

// ─── Util ────────────────────────────────────────────────
