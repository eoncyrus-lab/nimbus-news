// Package assetprofile is the single source of asset-class policy.
//
// It deliberately avoids importing forecast or engine packages so lower-level
// packages can depend on profile data without creating import cycles.
package assetprofile

import "strings"

type AssetClass string

const (
	ClassBTC         AssetClass = "btc"
	ClassEquityIndex AssetClass = "equity_index"
	ClassGold        AssetClass = "gold"
	ClassUSStock     AssetClass = "us_stock"
)

type ReliabilityCell struct {
	DirHit float64
	NTests int
	AsOf   string
}

type Profile struct {
	Key              string
	Class            AssetClass
	DisplayName      string
	Version          string
	ReadingDomains   []DomainSpec
	VerdictPolicy    VerdictPolicy
	Horizons         []int
	Reliability      map[int]ReliabilityCell
	ConformalScale   map[int]float64
	HorizonWeights   []HorizonWeightBand
	FeatureBundle    string
	ExpectedFeatures []string
	SkillProfileURI  string
	// FeatureScales maps feature name → normalization divisor.
	// Extractor clips raw/divisor to [-3, 3]. Absent keys fall back to
	// the BTC defaults baked into core.go.
	FeatureScales map[string]float64
	// ScoringRules maps domain → indicator key → thresholds for verdict scoring.
	// Engine scoring functions use these instead of hardcoded magic numbers.
	// Absent entries fall back to the engine's built-in defaults.
	ScoringRules map[string]map[string]IndicatorRule
}

// IndicatorRule defines bull/bear thresholds for a single indicator in a domain.
// A nil pointer means "no threshold in this direction".
type IndicatorRule struct {
	BullBelow *float64 // value < threshold → bull signal
	BullAbove *float64 // value > threshold → bull signal
	BearBelow *float64 // value < threshold → bear signal
	BearAbove *float64 // value > threshold → bear signal
}

type DomainSpec struct {
	Key     string
	Title   string
	Icon    string
	Purpose string
}

type VerdictPolicy struct {
	Key                  string
	DomainOrder          []string
	BullThreshold        int
	BearThreshold        int
	BullRegime           string
	NeutralRegime        string
	BearRegime           string
	BullStance           string
	NeutralStance        string
	BearStance           string
	LowCoverageThreshold float64
}

type HorizonWeightBand struct {
	MaxDays    int
	Multiplier map[string]float64
}

const version20260512 = "2026-05-12"

var profiles = map[string]Profile{
	"btc": {
		Key:              "btc",
		Class:            ClassBTC,
		DisplayName:      "Bitcoin",
		Version:          version20260512,
		ReadingDomains:   btcReadingDomains(),
		VerdictPolicy:    btcVerdictPolicy(),
		Horizons:         []int{30, 90, 180},
		FeatureBundle:    "btc_core",
		ExpectedFeatures: btcExpectedFeatures(),
		SkillProfileURI:  "guanfu://skill/profiles/btc",
		Reliability: map[int]ReliabilityCell{
			30:  {DirHit: 0.645, NTests: 62, AsOf: "2026-05-12"},
			90:  {DirHit: 0.645, NTests: 62, AsOf: "2026-05-12"},
			180: {DirHit: 0.693, NTests: 62, AsOf: "2026-05-12"},
		},
		HorizonWeights: defaultHorizonWeights(),
		// BTC scales are the defaults baked into core.go; listed here for
		// documentation and to allow explicit override if needed.
		FeatureScales: map[string]float64{
			"return_30d":       0.30,
			"return_90d":       0.60,
			"return_180d":      1.00,
			"drawdown_90d":     0.40,
			"realized_vol_30d": 0.50, // offset 0.60 handled in extractor
		},
	},
	"qqq": {
		Key:              "qqq",
		Class:            ClassEquityIndex,
		DisplayName:      "Nasdaq-100 ETF",
		Version:          version20260512,
		ReadingDomains:   equityReadingDomains(),
		VerdictPolicy:    equityVerdictPolicy(),
		Horizons:         []int{30, 63, 90, 180, 252},
		FeatureBundle:    "equity_index",
		ExpectedFeatures: equityExpectedFeatures(),
		SkillProfileURI:  "guanfu://skill/profiles/equity_index",
		Reliability: map[int]ReliabilityCell{
			30:  {DirHit: 0.643, NTests: 28, AsOf: "2026-05-12"},
			90:  {DirHit: 0.857, NTests: 28, AsOf: "2026-05-12"},
			180: {DirHit: 0.786, NTests: 28, AsOf: "2026-05-12"},
		},
		ConformalScale: map[int]float64{30: 1.80, 63: 1.80, 90: 1.80, 180: 1.80, 252: 1.80},
		HorizonWeights: defaultHorizonWeights(),
		// Equity indices have ~3-5× lower volatility than BTC.
		FeatureScales: equityFeatureScales(),
		ScoringRules:  equityScoringRules(),
	},
	"spy": {
		Key:              "spy",
		Class:            ClassEquityIndex,
		DisplayName:      "S&P 500 ETF",
		Version:          version20260512,
		ReadingDomains:   equityReadingDomains(),
		VerdictPolicy:    equityVerdictPolicy(),
		Horizons:         []int{30, 63, 90, 180, 252},
		FeatureBundle:    "equity_index",
		ExpectedFeatures: equityExpectedFeatures(),
		SkillProfileURI:  "guanfu://skill/profiles/equity_index",
		Reliability: map[int]ReliabilityCell{
			30:  {DirHit: 0.679, NTests: 28, AsOf: "2026-05-12"},
			90:  {DirHit: 0.786, NTests: 28, AsOf: "2026-05-12"},
			180: {DirHit: 0.821, NTests: 28, AsOf: "2026-05-12"},
		},
		ConformalScale: map[int]float64{30: 1.60, 63: 1.60, 90: 1.60, 180: 1.90, 252: 1.90},
		HorizonWeights: defaultHorizonWeights(),
		FeatureScales:  equityFeatureScales(),
		ScoringRules:   equityScoringRules(),
	},
	"gold": {
		Key:              "gold",
		Class:            ClassGold,
		DisplayName:      "London Gold (XAU/USD)",
		Version:          version20260512,
		ReadingDomains:   goldReadingDomains(),
		VerdictPolicy:    goldVerdictPolicy(),
		Horizons:         []int{30, 60, 90, 120},
		FeatureBundle:    "gold",
		ExpectedFeatures: goldExpectedFeatures(),
		SkillProfileURI:  "guanfu://skill/profiles/gold",
		Reliability: map[int]ReliabilityCell{
			30: {DirHit: 0.609, NTests: 69, AsOf: "2026-05-12"},
			90: {DirHit: 0.667, NTests: 69, AsOf: "2026-05-12"},
			// 180d retained for explicit opt-in queries even though absent from default Gold horizon set.
			180: {DirHit: 0.623, NTests: 69, AsOf: "2026-05-12"},
		},
		ConformalScale: map[int]float64{120: 1.20, 180: 1.20},
		HorizonWeights: defaultHorizonWeights(),
		// Gold vol is ~15-20% annualized; lower than equity, much lower than BTC.
		FeatureScales: goldFeatureScales(),
		ScoringRules:  goldScoringRules(),
	},
	"us_stock": {
		Key:              "us_stock",
		Class:            ClassUSStock,
		DisplayName:      "US Stock",
		Version:          version20260512,
		ReadingDomains:   usStockReadingDomains(),
		VerdictPolicy:    usStockVerdictPolicy(),
		Horizons:         []int{30, 90, 180},
		FeatureBundle:    "us_stock",
		ExpectedFeatures: usStockExpectedFeatures(),
		SkillProfileURI:  "guanfu://skill/profiles/us_stock",
		HorizonWeights:   defaultHorizonWeights(),
		// Single stocks can vary widely; use equity-index scales as a
		// conservative default (avoids BTC's extreme vol assumptions).
		FeatureScales: equityFeatureScales(),
		ScoringRules:  equityScoringRules(),
	},
}

func btcVerdictPolicy() VerdictPolicy {
	return VerdictPolicy{
		Key:                  "btc",
		DomainOrder:          []string{"cycle", "valuation", "network", "positioning", "macro", "flow", "technical", "cross_asset"},
		BullThreshold:        4,
		BearThreshold:        -4,
		BullRegime:           "风险偏多",
		NeutralRegime:        "过渡 / 震荡",
		BearRegime:           "风险偏空",
		BullStance:           "偏积累倾向",
		NeutralStance:        "等待",
		BearStance:           "高防守倾向",
		LowCoverageThreshold: 0.5,
	}
}

func equityVerdictPolicy() VerdictPolicy {
	return VerdictPolicy{
		Key:                  "equity_index",
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

func goldVerdictPolicy() VerdictPolicy {
	return VerdictPolicy{
		Key:                  "gold",
		DomainOrder:          []string{"technical", "macro", "valuation"},
		BullThreshold:        3,
		BearThreshold:        -3,
		BullRegime:           "偏强积累区",
		NeutralRegime:        "中性",
		BearRegime:           "偏弱谨慎区",
		BullStance:           "实际利率/美元/技术面合力偏多",
		NeutralStance:        "黄金驱动不一致，等待实际利率/美元/风险偏好确认",
		BearStance:           "美元或实际利率压力偏强，黄金信号偏弱",
		LowCoverageThreshold: 0.5,
	}
}

func usStockVerdictPolicy() VerdictPolicy {
	return VerdictPolicy{
		Key:                  "us_stock",
		DomainOrder:          []string{"technical", "macro", "positioning"},
		BullThreshold:        3,
		BearThreshold:        -3,
		BullRegime:           "单股环境偏强",
		NeutralRegime:        "单股环境分歧",
		BearRegime:           "单股环境偏弱",
		BullStance:           "技术/宏观/情绪偏多，但仍需核对财报和事件风险",
		NeutralStance:        "信号不一致；单股结论需等待财报、行业和事件确认",
		BearStance:           "技术或宏观压力偏强；单股需优先排查基本面和事件风险",
		LowCoverageThreshold: 0.5,
	}
}

func btcReadingDomains() []DomainSpec {
	return []DomainSpec{
		{Key: "cycle", Title: "Cycle 周期定位", Icon: "🌊", Purpose: "halving cycle, long-cycle trend, miner-cycle context"},
		{Key: "valuation", Title: "Valuation 估值", Icon: "💰", Purpose: "BTC-native valuation and on-chain valuation"},
		{Key: "network", Title: "Network 网络", Icon: "⛏️", Purpose: "hashrate, difficulty, usage, and miner stress"},
		{Key: "positioning", Title: "Positioning 杠杆 & 情绪", Icon: "📊", Purpose: "derivatives leverage, options skew, and sentiment"},
		{Key: "macro", Title: "Macro 宏观", Icon: "🌍", Purpose: "rates, dollar, liquidity, and broad risk backdrop"},
		{Key: "flow", Title: "Flow 资金流", Icon: "💸", Purpose: "ETF, stablecoin, exchange, and liquidity flow"},
		{Key: "technical", Title: "Technical 技术指标", Icon: "📈", Purpose: "trend, momentum, volatility, and drawdown state"},
		{Key: "cross_asset", Title: "CrossAsset 跨资产", Icon: "🔗", Purpose: "BTC versus gold, equities, dollar, and rates"},
	}
}

func equityReadingDomains() []DomainSpec {
	return []DomainSpec{
		{Key: "valuation", Title: "Valuation 估值", Icon: "💰", Purpose: "CAPE and broad valuation context when historical series exist"},
		{Key: "macro", Title: "Macro 利率/信用", Icon: "🌍", Purpose: "rates, dollar, credit, liquidity, and volatility backdrop"},
		{Key: "positioning", Title: "Options/Sentiment 期权情绪", Icon: "📊", Purpose: "put/call, fear-greed, and crowding context"},
		{Key: "technical", Title: "Technical 技术指标", Icon: "📈", Purpose: "trend, momentum, volatility, and drawdown state"},
	}
}

func goldReadingDomains() []DomainSpec {
	return []DomainSpec{
		{Key: "valuation", Title: "RealYield/USD 黄金估值", Icon: "💰", Purpose: "real-yield proxy, dollar pressure, and safe-haven valuation"},
		{Key: "macro", Title: "RiskOff/Macro 避险宏观", Icon: "🌍", Purpose: "VIX risk-off demand, dollar, and long-duration rate proxy"},
		{Key: "technical", Title: "Technical 技术指标", Icon: "📈", Purpose: "gold trend, momentum, volatility, and drawdown state"},
	}
}

func usStockReadingDomains() []DomainSpec {
	return []DomainSpec{
		{Key: "valuation", Title: "Valuation 估值", Icon: "💰", Purpose: "per-name valuation when historical or snapshot data exists"},
		{Key: "macro", Title: "Macro 宏观", Icon: "🌍", Purpose: "rates, dollar, credit, and broad risk backdrop"},
		{Key: "positioning", Title: "Flow/Sentiment 流向情绪", Icon: "📊", Purpose: "volume, options, short interest, and event-risk context when available"},
		{Key: "technical", Title: "Technical 技术指标", Icon: "📈", Purpose: "single-name trend, momentum, volatility, and drawdown state"},
	}
}

func genericTechnicalFeatures() []string {
	return []string{
		"return_30d", "return_90d", "return_180d", "drawdown_90d",
		"mayer_multiple", "realized_vol_30d", "rsi_14",
	}
}

// equityFeatureScales returns normalization divisors for equity-index assets.
// Equity indices have ~3-5× lower volatility than BTC, so the same raw return
// would be clipped to near-zero with BTC scales.
func equityFeatureScales() map[string]float64 {
	return map[string]float64{
		"return_30d":       0.08, // SPY/QQQ typical 30d range ±8%
		"return_90d":       0.15, // typical 90d range ±15%
		"return_180d":      0.25, // typical 180d range ±25%
		"drawdown_90d":     0.20, // typical 90d drawdown ±20%
		"realized_vol_30d": 0.15, // equity vol center ~15%, range ±15%
	}
}

// goldFeatureScales returns normalization divisors for gold.
// Gold annualized vol ~15-20%; lower than equity, much lower than BTC.
func goldFeatureScales() map[string]float64 {
	return map[string]float64{
		"return_30d":       0.06, // gold typical 30d range ±6%
		"return_90d":       0.12, // typical 90d range ±12%
		"return_180d":      0.20, // typical 180d range ±20%
		"drawdown_90d":     0.15, // typical 90d drawdown ±15%
		"realized_vol_30d": 0.10, // gold vol center ~12%, range ±10%
	}
}

// equityScoringRules returns indicator thresholds for equity-index verdict scoring.
// These replace the hardcoded magic numbers in engine/equity_panel.go scoreXxx functions.
func equityScoringRules() map[string]map[string]IndicatorRule {
	return map[string]map[string]IndicatorRule{
		"technical": {
			"rsi_14":         {BullBelow: ptr(30), BearAbove: ptr(70)},
			"sma_200_dev":    {BullAbove: ptr(10), BearBelow: ptr(-10)},
			"momentum_90d":   {BullAbove: ptr(10), BearBelow: ptr(-10)},
			"drawdown_200d":  {BullBelow: ptr(-15)}, // deep drawdown → mean-reversion bull
			"volatility_30d": {BearAbove: ptr(30)},  // high vol → bear
		},
		"macro": {
			"vix_level": {BullBelow: ptr(15), BearAbove: ptr(30)},
		},
		"positioning": {
			"fear_greed":     {BullBelow: ptr(25), BearAbove: ptr(75)},
			"put_call_ratio": {BullAbove: ptr(1.2), BearBelow: ptr(0.7)},
		},
		"valuation": {
			// sma_200_dev in valuation domain (dashboard path): low = cheap, high = expensive
			"sma_200_dev": {BullBelow: ptr(-10.0), BearAbove: ptr(15.0)},
			"pe":          {BearAbove: ptr(30.0)},
		},
	}
}

// goldScoringRules returns indicator thresholds for gold verdict scoring.
func goldScoringRules() map[string]map[string]IndicatorRule {
	return map[string]map[string]IndicatorRule{
		"technical": {
			// Gold technical uses same RSI thresholds as equity
			"rsi_14":       {BullBelow: ptr(30), BearAbove: ptr(70)},
			"sma_200_dev":  {BullAbove: ptr(10), BearBelow: ptr(-10)},
			"momentum_90d": {BullAbove: ptr(8), BearBelow: ptr(-8)},
		},
		"macro": {
			// VIX: high VIX = risk-off = gold bullish (opposite of equity)
			"vix_level": {BullAbove: ptr(30), BearBelow: ptr(12)},
		},
		"valuation": {
			// real_yield_proxy: TLT price proxy; >100 = real yields falling = gold bullish
			"real_yield_proxy": {BullAbove: ptr(100), BearBelow: ptr(85)},
		},
	}
}

func btcExpectedFeatures() []string {
	return append(genericTechnicalFeatures(),
		"sma_200w_dev", "ahr999_compressed", "halving_cycle_sin", "halving_cycle_cos")
}

func equityExpectedFeatures() []string {
	return append(genericTechnicalFeatures(),
		"cape", "dgs10_30d", "dxy_30d", "hy_spread_30d", "yield_curve", "vixy_level",
		"put_call_ratio", "put_call_30d_change", "put_call_252d_percentile")
}

func goldExpectedFeatures() []string {
	return append(genericTechnicalFeatures(),
		"real_yield_10y", "breakeven_10y", "dxy_30d", "dgs10_30d", "gold_cot_net", "vixy_level")
}

func usStockExpectedFeatures() []string {
	return append(genericTechnicalFeatures(),
		"dgs10_30d", "dxy_30d", "hy_spread_30d", "yield_curve", "vixy_level")
}

func defaultHorizonWeights() []HorizonWeightBand {
	return []HorizonWeightBand{
		{
			MaxDays: 45,
			Multiplier: map[string]float64{
				"return_30d":               1.25,
				"drawdown_90d":             1.25,
				"realized_vol_30d":         1.25,
				"rsi_14":                   1.25,
				"vixy_level":               1.25,
				"put_call_30d_change":      1.25,
				"return_180d":              0.75,
				"cape":                     0.75,
				"ahr999_compressed":        0.75,
				"sma_200w_dev":             0.75,
				"yield_curve":              0.75,
				"put_call_252d_percentile": 0.75,
			},
		},
		{
			MaxDays: 120,
			Multiplier: map[string]float64{
				"return_90d":          1.15,
				"drawdown_90d":        1.15,
				"dgs10_30d":           1.15,
				"dxy_30d":             1.15,
				"hy_spread_30d":       1.15,
				"put_call_30d_change": 1.15,
				"return_30d":          0.90,
			},
		},
		{
			MaxDays: 0,
			Multiplier: map[string]float64{
				"return_180d":              1.25,
				"mayer_multiple":           1.25,
				"sma_200w_dev":             1.25,
				"ahr999_compressed":        1.25,
				"cape":                     1.25,
				"real_yield_10y":           1.25,
				"gold_cot_net":             1.25,
				"yield_curve":              1.25,
				"put_call_252d_percentile": 1.25,
				"return_30d":               0.75,
				"rsi_14":                   0.75,
			},
		},
	}
}

func For(asset string) (Profile, bool) {
	key := normalizeKey(asset)
	p, ok := profiles[key]
	if !ok && strings.HasPrefix(key, "stock_") {
		p, ok = profiles["us_stock"]
	}
	if !ok {
		return Profile{}, false
	}
	return cloneProfile(p), true
}

func VerdictPolicyFor(asset string) (VerdictPolicy, bool) {
	p, ok := For(asset)
	if !ok || p.VerdictPolicy.Key == "" {
		return VerdictPolicy{}, false
	}
	return cloneVerdictPolicy(p.VerdictPolicy), true
}

func ReliabilityFor(asset string, days int) (ReliabilityCell, bool) {
	if strings.TrimSpace(asset) == "" {
		return ReliabilityCell{}, false
	}
	p, ok := For(asset)
	if !ok {
		return ReliabilityCell{}, false
	}
	r, ok := p.Reliability[days]
	return r, ok
}

func ConformalScale(asset string, days int) float64 {
	p, ok := For(asset)
	if !ok {
		return 1
	}
	if scale := p.ConformalScale[days]; scale > 0 {
		return scale
	}
	return 1
}

func ExpectedFeaturesFor(asset string) []string {
	if p, ok := For(asset); ok && len(p.ExpectedFeatures) > 0 {
		return append([]string(nil), p.ExpectedFeatures...)
	}
	return genericTechnicalFeatures()
}

func HorizonWeightMultiplier(asset, feature string, days int) float64 {
	p, ok := For(asset)
	if !ok {
		// Unknown assets still use the generic US-stock profile so callers
		// get a conservative non-BTC policy.
		p = profiles["us_stock"]
	}
	for _, band := range p.HorizonWeights {
		if band.MaxDays > 0 && days > band.MaxDays {
			continue
		}
		if v, ok := band.Multiplier[feature]; ok {
			return v
		}
		return 1
	}
	return 1
}

// ScoringRulesFor returns the indicator scoring rules for a domain in the given
// asset's profile. Returns nil when no rules are registered for that domain.
func ScoringRulesFor(asset, domain string) map[string]IndicatorRule {
	p, ok := For(asset)
	if !ok {
		return nil
	}
	return p.ScoringRules[domain]
}

// ptr is a convenience helper for creating *float64 threshold values.
func ptr(v float64) *float64 { return &v }

func normalizeKey(asset string) string {
	key := strings.ToLower(strings.TrimSpace(asset))
	if key == "" {
		return "btc"
	}
	return key
}

func cloneProfile(p Profile) Profile {
	p.ReadingDomains = cloneDomainSpecs(p.ReadingDomains)
	p.VerdictPolicy = cloneVerdictPolicy(p.VerdictPolicy)
	p.Horizons = append([]int(nil), p.Horizons...)
	p.Reliability = cloneReliability(p.Reliability)
	p.ConformalScale = cloneFloatMap(p.ConformalScale)
	p.HorizonWeights = cloneWeightBands(p.HorizonWeights)
	p.ExpectedFeatures = append([]string(nil), p.ExpectedFeatures...)
	p.FeatureScales = cloneStringFloatMap(p.FeatureScales)
	p.ScoringRules = cloneScoringRules(p.ScoringRules)
	return p
}

func cloneDomainSpecs(in []DomainSpec) []DomainSpec {
	if len(in) == 0 {
		return nil
	}
	return append([]DomainSpec(nil), in...)
}

func cloneVerdictPolicy(in VerdictPolicy) VerdictPolicy {
	in.DomainOrder = append([]string(nil), in.DomainOrder...)
	return in
}

func cloneReliability(in map[int]ReliabilityCell) map[int]ReliabilityCell {
	if len(in) == 0 {
		return nil
	}
	out := make(map[int]ReliabilityCell, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneFloatMap(in map[int]float64) map[int]float64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[int]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneStringFloatMap(in map[string]float64) map[string]float64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneScoringRules(in map[string]map[string]IndicatorRule) map[string]map[string]IndicatorRule {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]map[string]IndicatorRule, len(in))
	for domain, rules := range in {
		domainCopy := make(map[string]IndicatorRule, len(rules))
		for k, r := range rules {
			domainCopy[k] = r // IndicatorRule contains only *float64 pointers; shallow copy is safe (immutable after init)
		}
		out[domain] = domainCopy
	}
	return out
}

func cloneWeightBands(in []HorizonWeightBand) []HorizonWeightBand {
	out := make([]HorizonWeightBand, len(in))
	for i, band := range in {
		out[i] = HorizonWeightBand{MaxDays: band.MaxDays, Multiplier: map[string]float64{}}
		for k, v := range band.Multiplier {
			out[i].Multiplier[k] = v
		}
	}
	return out
}
