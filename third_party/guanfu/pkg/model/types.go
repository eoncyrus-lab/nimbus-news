package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// CurrentMarketSnapshotSchemaVersion guards the on-disk market cache contract.
// Bump this when MarketSnapshot gains fields that materially change panel output.
const CurrentMarketSnapshotSchemaVersion = 8

// MarketSnapshot 包含了计算宏观评分所需的所有市场数据
type MarketSnapshot struct {
	Date                  time.Time
	SnapshotSchemaVersion int // on-disk cache schema version
	FetchedAt             string

	// BTC 数据
	BTCPrice         decimal.Decimal   // 当前价格
	BTCVolume24h     decimal.Decimal   // 24h 成交量
	BTCPriceHistory  []decimal.Decimal // 历史价格 (用于计算 MA)
	BTCVolumeHistory []decimal.Decimal // 历史成交量 (用于计算 MA)
	BTCPriceAsOf     string            // BTC 最新 K 线日期/时间

	// ETH 数据 (New)
	ETHPrice         decimal.Decimal
	ETHVolume24h     decimal.Decimal
	ETHPriceHistory  []decimal.Decimal
	ETHVolumeHistory []decimal.Decimal
	ETHPriceAsOf     string

	// 市场结构数据
	Top50Coins   []CoinSnapshot // Top50 代币快照 (排除稳定币)
	Top50Fetched bool

	// 资金数据
	TotalMarketCap        decimal.Decimal   // 总市值
	TotalMarketCapHistory []decimal.Decimal // 历史总市值 (用于计算变化率)

	// 稳定币流动性 (New)
	StablecoinMarketCap        decimal.Decimal
	StablecoinMarketCapHistory []decimal.Decimal
	StablecoinMarketCapFetched bool

	USDTPriceCNY decimal.Decimal // USDT/CNY 价格
	USDPriceCNY  decimal.Decimal // USD/CNY 汇率

	// 衍生品与链上数据 (新增)
	BTCFundingRate  decimal.Decimal // 资金费率
	BTCOpenInterest decimal.Decimal // 合约持仓量 (USDT)
	FuturesFetched  bool

	// 网络数据 (mempool.space, v2 新增)
	HashRateEHs         decimal.Decimal // 当前哈希率 (EH/s)
	HashRibbonsLabel    string          // "上行(扩张)"/"下行(投降)"/"交叉中"
	DifficultyChangePct decimal.Decimal // 最近一次难度调整 %
	MempoolMB           decimal.Decimal // mempool 拥堵 (MB)
	MempoolAsOf         string
	MempoolFetched      bool

	// ETF 数据 (SoSoValue, v2 新增)
	ETFNetFlow7dUSD  decimal.Decimal // 7 日累计净流入
	ETFNetFlow30dUSD decimal.Decimal // 30 日累计净流入
	ETFTotalAssetUSD decimal.Decimal // 总持仓
	ETFStaleDays     int             // 数据距今天数
	ETFAsOf          string

	// CoinMetrics 链上估值（v2 新增）
	CapMVRVCur              decimal.Decimal // Market Cap / Realized Cap
	MVRVZScore              decimal.Decimal // (market cap - realized cap) / std(market cap)
	NUPL                    decimal.Decimal // (market cap - realized cap) / market cap
	MVRVQuantile            decimal.Decimal // MVRV 历史分位
	NUPLQuantile            decimal.Decimal // NUPL 历史分位
	MarketCapCurUSD         decimal.Decimal // CoinMetrics current market cap
	RealizedCapUSD          decimal.Decimal // CoinMetrics realized cap, direct or implied by MVRV
	OnchainValuationAsOf    string
	OnchainValuationFetched bool

	// FRED 宏观 (Phase 2 新增)
	DXY60dTrendPct     decimal.Decimal // DTWEXBGS 60 日变化 %
	DXYLatest          decimal.Decimal // DTWEXBGS 最新值
	DXYAsOf            string
	RealYield10YPct    decimal.Decimal // DFII10 最新 %
	RealYield10YAsOf   string
	M2YoYPct           decimal.Decimal // M2SL 同比 %
	M2LatestB          decimal.Decimal // M2SL 最新（十亿美元）
	M2AsOf             string
	SPXCorrelation30d  decimal.Decimal // BTC vs SPX 30 日收益 Pearson
	SPXAsOf            string
	HYSpreadBps        decimal.Decimal // ICE BofA US High Yield OAS (bp)
	HYSpreadAsOf       string
	YieldCurve10Y2YBps decimal.Decimal // 10Y-2Y Treasury spread (bp)
	YieldCurveAsOf     string
	// US Liquidity (Priority: Fed assets, RRP, TGA)
	FedAssetsB           decimal.Decimal // WALCL: Fed total assets (billions)
	FedAssetsAsOf        string
	RRPB                 decimal.Decimal // RRPONTSYD: ON reverse repo (billions)
	RRPAsOf              string
	TGA_B                decimal.Decimal // WTREGEN: Treasury cash balance (billions)
	TGAAsOf              string
	NetLiquidityB        decimal.Decimal // FedAssets - RRP - TGA
	FedAssets60dTrendPct decimal.Decimal
	RRP60dTrendPct       decimal.Decimal
	TGA60dTrendPct       decimal.Decimal
	NetLiq60dTrendPct    decimal.Decimal
	MacroFetched         bool // FRED 是否成功拉取

	// 情绪数据
	BTCDominance           decimal.Decimal // BTC 市占率 (0.00-1.00)
	FearGreedIndex         decimal.Decimal // 恐慌贪婪指数 (0-100)
	AltcoinSeasonIndex     decimal.Decimal // 山寨季指数 (0-100)
	GlobalMarketFetched    bool
	FearGreedFetched       bool
	AltcoinSeasonAvailable bool
	FearGreedAsOf          string

	// Deribit 期权 (v4 新增) — DVOL + 25-delta skew
	DVOL            decimal.Decimal // 当前 BTC IV 指数
	DVOL60dTrendPct decimal.Decimal // (now / 60d 前 - 1) × 100
	DVOLAvailable   bool
	DVOLAsOf        string
	DVOLHistory     []decimal.Decimal // 用于历史分位

	Skew25dNearTermPct decimal.Decimal // IV(25Δ put) - IV(25Δ call)，pp
	SkewAvailable      bool
	SkewAsOf           string
	SkewExpiry         string

	// Cross-asset prices (v3 新增)
	GoldPriceUSD  decimal.Decimal // 黄金现货 USD/oz (Binance PAXG)
	QQQPrice      decimal.Decimal // QQQ ETF
	SPYPrice      decimal.Decimal // SPY ETF
	GoldPriceAsOf string
	QQQPriceAsOf  string
	SPYPriceAsOf  string
	GoldHistory   []decimal.Decimal
	QQQHistory    []decimal.Decimal
	SPYHistory    []decimal.Decimal
	// Extended (v3.1, Futu)
	GoldETFPriceUSD   decimal.Decimal // GLD 实物黄金 ETF
	GoldETFHistory    []decimal.Decimal
	GoldETFAsOf       string
	WTIPrice          decimal.Decimal // Oil price/proxy (Futu USO ETF > Yahoo CL=F WTI futures)
	WTIHistory        []decimal.Decimal
	WTIPriceAsOf      string
	OilPriceSource    string          // "futu:US.USO" (ETF proxy) or "yahoo:CL=F" (WTI futures)
	UUPPrice          decimal.Decimal // 做多美元 ETF (DXY proxy)
	UUPHistory        []decimal.Decimal
	UUPPriceAsOf      string
	VIXYPrice         decimal.Decimal // VIX 波动率 ETF
	VIXYHistory       []decimal.Decimal
	VIXYPriceAsOf     string
	TLTPrice          decimal.Decimal // iShares 20+ Year Treasury Bond ETF (long-end Treasury proxy)
	TLTHistory        []decimal.Decimal
	TLTPriceAsOf      string
	CrossAssetFetched bool

	// 非致命数据源问题。BuildPanel 会透传到 stale_warnings。
	SourceWarnings []string
}

// CoinSnapshot 单个代币快照
type CoinSnapshot struct {
	Symbol       string
	Price        decimal.Decimal
	PriceHistory []decimal.Decimal // 历史价格 (至少需要 MA120 数据)
}

// === CoinMan v2: IndicatorPanel ===
//
// 投资盘面：纯指标，不做评分聚合，不输出 action。
// 由 Claude/skill 文档完成解读 + 决策。
//
// 8 个 domain 对应投资决策的不同视角：
//   - cycle: 在 4 年减半周期 / 长期均线意义上的位置
//   - valuation: 估值（MVRV / NUPL / AHR / Mayer / SOPR）
//   - network: 矿工 / 哈希率 / mempool 网络健康
//   - positioning: 杠杆 / 资金费率 / 情绪
//   - macro: 美元 / 实际利率 / 流动性 / 风险资产相关性
//   - flow: ETF 净流入 / 稳定币供应 / ETH/BTC 资金偏好
//   - technical: RSI / MACD / EMA / MA / Bollinger / 波动率
//   - cross_asset: 黄金 / QQQ / SPY / UUP / VIXY 相对强弱

// Indicator 单个指标项
//
// Missing=true 时该指标视为"缺数据 / placeholder"——verdict 引擎、域级一致性
// 计票、簇级去重必须自动跳过，不影响其他指标。回测同理：缺数据点不计入命中率。
//
// 默认 Missing=false（即 available）。这样既有指标构造无需改动；只在数据源失败
// 或值是哨兵时显式设 Missing=true。
//
// IsAvailable() 是首选的查询接口（额外做 NaN/Inf 健全性检查）。
type Indicator struct {
	Value     float64 `json:"value"`                // 原始数值（无 sigmoid / scaling）
	Quantile  float64 `json:"q,omitempty"`          // 历史分位 [0,1]，越高表示当前值在历史上越高
	Label     string  `json:"label,omitempty"`      // 简短解读标签（"中性偏高", "极端低估"），仅参考
	Source    string  `json:"source,omitempty"`     // 数据源（"binance", "coinmetrics", "mempool.space", ...）
	UpdatedAt string  `json:"updated_at,omitempty"` // 数据更新时间（RFC3339）
	Note      string  `json:"note,omitempty"`       // 计算备注（如 "200d MA 历史不足，使用 100d"）
	Missing   bool    `json:"missing,omitempty"`    // true = 数据缺失 / placeholder，verdict / 回测应跳过
}

// IsAvailable 判断指标是否可参与 verdict / 回测。
// false 表示：被显式标注 missing，或值是 NaN/Inf。零值不视为缺失（可能是真值）。
func (i Indicator) IsAvailable() bool {
	if i.Missing {
		return false
	}
	if i.Value != i.Value { // NaN
		return false
	}
	if i.Value > 1e308 || i.Value < -1e308 { // Inf
		return false
	}
	return true
}

// SnapshotData 当前快照基础数据
type SnapshotData struct {
	BTCPrice            float64 `json:"btc_price"`
	ETHPrice            float64 `json:"eth_price,omitempty"`
	GoldPrice           float64 `json:"gold_price,omitempty"`
	QQQPrice            float64 `json:"qqq_price,omitempty"`
	SPYPrice            float64 `json:"spy_price,omitempty"`
	BTCDominance        float64 `json:"btc_dominance"`
	TotalMarketCap      float64 `json:"total_market_cap"`
	StablecoinMarketCap float64 `json:"stablecoin_market_cap"`
	FearGreed           float64 `json:"fear_greed"`
	DataDate            string  `json:"data_date"` // 主数据快照日期
}

// SourceHealth summarizes whether a data source contributed usable data to the panel.
type SourceHealth struct {
	Source       string   `json:"source"`
	Status       string   `json:"status"` // ok, partial, stale, missing, warning
	AsOf         string   `json:"as_of,omitempty"`
	FallbackUsed bool     `json:"fallback_used,omitempty"`
	Impact       string   `json:"impact,omitempty"` // forecast, market_reading, both, optional
	Note         string   `json:"note,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
}

// PanelDomainMeta describes the asset-specific reading lens for a rendered
// panel. It is additive metadata; the indicator maps below remain stable for
// backward-compatible JSON consumers.
type PanelDomainMeta struct {
	Key     string `json:"key"`
	Title   string `json:"title,omitempty"`
	Icon    string `json:"icon,omitempty"`
	Purpose string `json:"purpose,omitempty"`
}

// IndicatorPanel CoinMan v2 主输出
type IndicatorPanel struct {
	Asset           string            `json:"asset,omitempty"` // qqq/spy/gold/btc/stock_*; printers and verdict logic dispatch on this
	ProfileKey      string            `json:"profile_key,omitempty"`
	ProfileVersion  string            `json:"profile_version,omitempty"`
	AssetClass      string            `json:"asset_class,omitempty"`
	SkillProfileURI string            `json:"skill_profile_uri,omitempty"`
	DomainMeta      []PanelDomainMeta `json:"domain_meta,omitempty"`
	Date            string            `json:"date"`
	Snapshot        SnapshotData      `json:"snapshot"`

	// 8 个 domain（每个 domain 是 indicator name → Indicator 的 map）
	Cycle       map[string]Indicator `json:"cycle"`
	Valuation   map[string]Indicator `json:"valuation"`
	Network     map[string]Indicator `json:"network"`
	Positioning map[string]Indicator `json:"positioning"`
	Macro       map[string]Indicator `json:"macro"`
	Flow        map[string]Indicator `json:"flow"`
	Technical   map[string]Indicator `json:"technical"`
	CrossAsset  map[string]Indicator `json:"cross_asset"`

	// 元数据
	StaleWarnings []string       `json:"stale_warnings,omitempty"` // 数据过时/缺失警告
	SourceHealth  []SourceHealth `json:"source_health,omitempty"`  // 数据源健康 / fallback 状态
}

// Config 系统配置
type Config struct {
	Weights    Weights    `mapstructure:"weights"`
	Thresholds Thresholds `mapstructure:"thresholds"`
	API        APIConfig  `mapstructure:"api"`
}

type Weights struct {
	Trend     float64 `mapstructure:"trend"`     // 趋势层权重 (默认0.30)
	Reversal  float64 `mapstructure:"reversal"`  // 反转层权重 (默认0.25)
	Valuation float64 `mapstructure:"valuation"` // 估值层权重 (默认0.25)
	Structure float64 `mapstructure:"structure"` // 结构层权重 (默认0.20)
}

type Thresholds struct {
	BTCMAFast       int     `mapstructure:"btc_ma_fast"` // e.g. 120
	BTCMASlow       int     `mapstructure:"btc_ma_slow"` // e.g. 200
	TopCoinCount    int     `mapstructure:"top_coin_count"`
	DominanceHigh   float64 `mapstructure:"dominance_high"`
	DominanceLow    float64 `mapstructure:"dominance_low"`
	AHRHalfLifeDays int     `mapstructure:"ahr_half_life_days"` // 拟合权重半衰期，默认 365*4。短窗口对近期快牛快熊更敏感。
}

type APIConfig struct {
	Timeout string `mapstructure:"timeout"`
	Retries int    `mapstructure:"retries"`
	Mock    bool   `mapstructure:"mock"`
}

// ScreenedCandidate is a single stock that passed a screener strategy.
// It flows from coarse screening (Longbridge, Uncle Stock) through fine
// screening (valuation/quality/potential/risk) into the guanfu panel.
type ScreenedCandidate struct {
	Symbol     string             `json:"symbol"`               // e.g. "700.HK", "AAPL.US"
	Name       string             `json:"name"`                 // e.g. "腾讯控股"
	Market     string             `json:"market"`               // "HK", "US", "A"
	Industry   string             `json:"industry,omitempty"`   // from Longbridge
	Sector     string             `json:"sector,omitempty"`     // broader sector grouping
	Price      float64            `json:"price"`                // latest close
	Indicators map[string]float64 `json:"indicators"`           // PE, PB, ROE, growth, margin, etc.
	Source     string             `json:"source"`               // "longbridge", "unclestock"

	// Theme and heat (from Longbridge rank_categories + concept tags)
	Themes      []string `json:"themes,omitempty"`       // concept/theme tags (AI, EV, 高股息...)
	HeatRank    int      `json:"heat_rank,omitempty"`    // position on hot_all leaderboard (0 = not on list)
	HeatRising  bool     `json:"heat_rising,omitempty"`  // on hot_up list
	DiscussHot  bool     `json:"discuss_hot,omitempty"`  // on discuss_heat list
	WatchCount  int      `json:"watch_count,omitempty"`  // watchlist popularity

	// Fine scores (populated by screener/fine/)
	ValScore    float64  `json:"val_score,omitempty"`    // 估值精筛分 0-100
	QualScore   float64  `json:"qual_score,omitempty"`   // 质量精筛分
	PotScore    float64  `json:"pot_score,omitempty"`    // 潜力精筛分
	TechScore   float64  `json:"tech_score,omitempty"`   // 技术面精筛分
	FlowScore   float64  `json:"flow_score,omitempty"`   // 资金面精筛分
	RiskScore   float64  `json:"risk_score,omitempty"`   // 风险精筛分
	FinalScore  float64  `json:"final_score,omitempty"`  // 复合加权分
	Reasons     []string `json:"reasons,omitempty"`      // 入选理由
	RiskFlags   []string `json:"risk_flags,omitempty"`   // 风险红旗
}

// ScoreWeights defines the relative importance of each screening dimension.
// Market-specific: A股偏质量+潜力(政策驱动), 港股偏估值+股息, 美股偏估值+质量.
// Every weight is a HAND-SET PRIOR until calibrated against forward returns.
type ScoreWeights struct {
	Valuation  float64 `json:"valuation" yaml:"valuation"`   // PE/PB/FCF yield
	Quality    float64 `json:"quality"   yaml:"quality"`     // ROE稳定性/现金流/负债
	Potential  float64 `json:"potential" yaml:"potential"`   // 拐点/催化/增长
	Technical  float64 `json:"technical" yaml:"technical"`   // MA/MACD/RSI/价量形态
	Flow       float64 `json:"flow"      yaml:"flow"`        // 资金流向/成交量/换手率
	Risk       float64 `json:"risk"      yaml:"risk"`        // 红旗扣除
}

// StrategyProfile defines a complete screening strategy for one market+style combination.
// It maps to a Longbridge preset strategy for coarse filtering, then applies market-specific
// fine screening weights, sector preferences, and heat requirements.
type StrategyProfile struct {
	Name        string   `json:"name" yaml:"name"`                 // "港股价值", "US Growth"
	Market      string   `json:"market" yaml:"market"`             // "HK", "US", "A"
	Style       string   `json:"style" yaml:"style"`               // "value", "growth", "dividend", "potential", "balanced"
	Description string   `json:"description,omitempty" yaml:"description"`

	// Coarse layer
	CoarseStrategyID string   `json:"coarse_strategy_id" yaml:"coarse_strategy_id"` // Longbridge preset ID ("16"=Buffett) or "" for Mode B
	CoarseConditions []string `json:"coarse_conditions,omitempty" yaml:"coarse_conditions"` // Mode B custom conditions (key:min:max)
	MinMarketCap     float64  `json:"min_market_cap,omitempty" yaml:"min_market_cap"` // 市值下限(亿)
	MaxMarketCap     float64  `json:"max_market_cap,omitempty" yaml:"max_market_cap"`

	// Sector / industry preferences
	FocusSectors   []string `json:"focus_sectors,omitempty" yaml:"focus_sectors"`     // 优先板块
	ExcludeSectors []string `json:"exclude_sectors,omitempty" yaml:"exclude_sectors"` // 排除板块
	ExcludeThemes  []string `json:"exclude_themes,omitempty" yaml:"exclude_themes"`   // 排除概念标签

	// Heat / popularity
	MinHeatRank  int  `json:"min_heat_rank,omitempty" yaml:"min_heat_rank"`  // 热度排名下限(0=不限)
	RequireRising bool `json:"require_rising,omitempty" yaml:"require_rising"` // 必须热度上升中

	// Fine screening weights (market-specific defaults)
	Weights ScoreWeights `json:"weights" yaml:"weights"`

	// Decision thresholds
	CoreCandidateMin float64 `json:"core_candidate_min" yaml:"core_candidate_min"` // 核心标的线 (default 65)
	WatchlistMin     float64 `json:"watchlist_min" yaml:"watchlist_min"`           // 观察名单线 (default 50)
}

// DefaultScoreWeights returns the canonical value-investing weights.
// Sum = 1.0. UNVALIDATED PRIOR — calibrate with walk-forward backtest.
func DefaultScoreWeights() ScoreWeights {
	return ScoreWeights{
		Valuation: 0.25, Quality: 0.25, Potential: 0.18,
		Technical: 0.12, Flow: 0.10, Risk: 0.10,
	}
}

// Market-specific default weights.
// HK: 深度价值市场,估值权重大; A股: 政策+资金驱动,潜力权重大; US: 成熟市场,质量+估值均衡.

func AShareWeights() ScoreWeights {
	return ScoreWeights{Valuation: 0.18, Quality: 0.22, Potential: 0.25, Technical: 0.15, Flow: 0.10, Risk: 0.10}
}

func HKWeights() ScoreWeights {
	return ScoreWeights{Valuation: 0.28, Quality: 0.22, Potential: 0.13, Technical: 0.12, Flow: 0.10, Risk: 0.15}
}

func USWeights() ScoreWeights {
	return ScoreWeights{Valuation: 0.22, Quality: 0.25, Potential: 0.14, Technical: 0.14, Flow: 0.10, Risk: 0.15}
}

// Style-specific variants (applied on top of market base).

func GrowthWeights() ScoreWeights {
	return ScoreWeights{Valuation: 0.15, Quality: 0.22, Potential: 0.28, Technical: 0.15, Flow: 0.10, Risk: 0.10}
}

func DividendWeights() ScoreWeights {
	return ScoreWeights{Valuation: 0.18, Quality: 0.30, Potential: 0.07, Technical: 0.10, Flow: 0.10, Risk: 0.25}
}

func TurnaroundWeights() ScoreWeights {
	return ScoreWeights{Valuation: 0.20, Quality: 0.12, Potential: 0.35, Technical: 0.18, Flow: 0.10, Risk: 0.05}
}

func QualityWeights() ScoreWeights {
	return ScoreWeights{Valuation: 0.15, Quality: 0.35, Potential: 0.12, Technical: 0.13, Flow: 0.10, Risk: 0.15}
}

func BalancedWeights() ScoreWeights {
	return ScoreWeights{Valuation: 0.20, Quality: 0.20, Potential: 0.18, Technical: 0.15, Flow: 0.12, Risk: 0.15}
}

// BuiltinStrategyProfiles returns the default strategy profiles.
// 13 strategies across 3 markets + 1 cross-market.
// Every profile is a HAND-SET PRIOR — calibrate with walk-forward backtest.
func BuiltinStrategyProfiles() []StrategyProfile {
	return []StrategyProfile{
		// ── HK (5 strategies) ──
		{
			Name: "港股价值", Market: "HK", Style: "value",
			Description:      "深度价值: 低PE+低PB+高股息+AH溢价,格雷厄姆框架",
			CoarseStrategyID: "18",
			MinMarketCap:     50,
			Weights:          HKWeights(),
			CoreCandidateMin: 65, WatchlistMin: 50,
		},
		{
			Name: "港股成长", Market: "HK", Style: "growth",
			Description:      "盈利高成长+合理估值: 双增长>80%,避开老千股",
			CoarseStrategyID: "7",
			MinMarketCap:     10,
			MinHeatRank:      50,
			Weights:          GrowthWeights(),
			CoreCandidateMin: 65, WatchlistMin: 50,
		},
		{
			Name: "港股高股息", Market: "HK", Style: "dividend",
			Description:      "稳定高息+低负债: 收息策略,质量优先",
			CoarseStrategyID: "11",
			MinMarketCap:     50,
			Weights:          DividendWeights(),
			CoreCandidateMin: 68, WatchlistMin: 52,
		},
		{
			Name: "港股拐点", Market: "HK", Style: "turnaround",
			Description:      "盈利拐点+估值修复: 盈利从负转正或加速,低PB提供安全边际",
			CoarseStrategyID: "8", // 业绩上涨: netincomegrowth>50% + salesgrowth>50%
			MinMarketCap:     5,
			ExcludeSectors:   []string{"资产管理和托管银行"},
			Weights:          TurnaroundWeights(),
			CoreCandidateMin: 60, WatchlistMin: 45,
		},
		{
			Name: "港股小盘价值", Market: "HK", Style: "small_value",
			Description:      "小盘深度价值: 低PB+低负债+盈利改善,PE<15,mktcap 5-50亿",
			CoarseStrategyID: "14", // 小盘轻资产快速成长
			MinMarketCap:     2,
			MaxMarketCap:     80,
			Weights:          ScoreWeights{Valuation: 0.30, Quality: 0.18, Potential: 0.20, Technical: 0.12, Flow: 0.10, Risk: 0.10},
			CoreCandidateMin: 60, WatchlistMin: 45,
		},

		// ── US (5 strategies) ──
		{
			Name: "美股价值", Market: "US", Style: "value",
			Description:      "FCF yield+低EV/EBIT+回购: Buffett框架",
			CoarseStrategyID: "16",
			MinMarketCap:     50,
			Weights:          USWeights(),
			CoreCandidateMin: 65, WatchlistMin: 50,
		},
		{
			Name: "美股成长", Market: "US", Style: "growth",
			Description:      "收入+利润双增长+行业地位: CANSLIM框架",
			CoarseStrategyID: "15",
			MinMarketCap:     10,
			MinHeatRank:      100,
			Weights:          GrowthWeights(),
			CoreCandidateMin: 65, WatchlistMin: 50,
		},
		{
			Name: "美股质量", Market: "US", Style: "quality",
			Description:      "高ROIC+稳定利润率+低应计: 质量优先,护城河筛选",
			CoarseStrategyID: "17", // 高质量盈利
			MinMarketCap:     100,
			Weights:          QualityWeights(),
			CoreCandidateMin: 68, WatchlistMin: 52,
		},
		{
			Name: "美股股息成长", Market: "US", Style: "dividend",
			Description:      "持续增息+合理估值: 股息贵族策略,10年+连续增息",
			CoarseStrategyID: "11", // 高股息
			MinMarketCap:     100,
			Weights:          DividendWeights(),
			CoreCandidateMin: 65, WatchlistMin: 50,
		},
		{
			Name: "美股均衡", Market: "US", Style: "balanced",
			Description:      "GARP: 合理估值+稳健成长+质量,均衡框架",
			CoarseStrategyID: "17", // 高质量盈利 (closest to balanced)
			MinMarketCap:     50,
			Weights:          BalancedWeights(),
			CoreCandidateMin: 63, WatchlistMin: 48,
		},

		// ── A-shares (4 strategies) ──
		{
			Name: "A股价值", Market: "A", Style: "value",
			Description:      "低PE分位+ROE稳定+北向增持: 政策催化加持,排除ST",
			CoarseStrategyID: "9",
			MinMarketCap:     20,
			ExcludeThemes:    []string{"ST", "*ST"},
			Weights:          AShareWeights(),
			CoreCandidateMin: 65, WatchlistMin: 50,
		},
		{
			Name: "A股成长", Market: "A", Style: "growth",
			Description:      "收入+利润双增+行业地位+主题催化: 科技/新能源/医药",
			CoarseStrategyID: "7",
			MinMarketCap:     30,
			ExcludeThemes:    []string{"ST", "*ST"},
			FocusSectors:     []string{"电子", "计算机", "医药生物", "电力设备", "汽车"},
			MinHeatRank:      80,
			Weights:          GrowthWeights(),
			CoreCandidateMin: 65, WatchlistMin: 50,
		},
		{
			Name: "A股高股息", Market: "A", Style: "dividend",
			Description:      "高股息+低波动+央企/国企: 防御配置,分红再投资",
			CoarseStrategyID: "11",
			MinMarketCap:     100,
			ExcludeThemes:    []string{"ST", "*ST"},
			Weights:          DividendWeights(),
			CoreCandidateMin: 65, WatchlistMin: 50,
		},
		{
			Name: "A股潜力", Market: "A", Style: "potential",
			Description:      "盈利拐点+政策催化+主题共振: 超额收益来源",
			CoarseStrategyID: "8", // 业绩上涨
			MinMarketCap:     10,
			ExcludeThemes:    []string{"ST", "*ST"},
			RequireRising:    true,
			Weights:          ScoreWeights{Valuation: 0.12, Quality: 0.16, Potential: 0.32, Technical: 0.18, Flow: 0.12, Risk: 0.10},
			CoreCandidateMin: 60, WatchlistMin: 45,
		},

		// ── Cross-market (1 strategy) ──
		{
			Name: "跨市场价值", Market: "ALL", Style: "value",
			Description:      "A+H+US三市场价值标的: AH溢价套利+跨市场估值对比",
			CoarseStrategyID: "", // Mode B: combine multiple markets manually
			MinMarketCap:     50,
			Weights:          ScoreWeights{Valuation: 0.25, Quality: 0.22, Potential: 0.15, Technical: 0.13, Flow: 0.10, Risk: 0.15},
			CoreCandidateMin: 65, WatchlistMin: 50,
		},
	}
}
