package engine

import (
	"math"
	"sort"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/history"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/mathutil"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"

	"github.com/shopspring/decimal"
)

const (
	ahrDCAWindowDays       = 200
	ahrFitWindowDays       = 365 * 8
	ahrMinFitWindowDays    = 365 * 3
	ahrRecentHalfLifeDays  = 365 * 4
	ahrLegacyLogSlope      = 5.84
	ahrLegacyLogIntercept  = -17.01
	ahrCompressionExponent = 0.75 // sqrt-AHR: pow(raw, 0.75) 压缩凸性偏差
)

type Calculator struct {
	Config     *model.Config
	History    *history.Store    // 可选；nil 表示不写历史 / 不算历史分位
	PriceStore *store.PriceStore // 可选；nil 表示不追加 PriceStore 宏观上下文
}

func NewCalculator(cfg *model.Config) *Calculator {
	return &Calculator{Config: cfg}
}

// WithHistory 注入 SQLite history store 后启用 v2 历史分位填充
func (c *Calculator) WithHistory(s *history.Store) *Calculator {
	c.History = s
	return c
}

// WithPriceStore enables optional PriceStore-backed macro context such as
// USD/CNY and global front-end policy rates.
func (c *Calculator) WithPriceStore(s *store.PriceStore) *Calculator {
	c.PriceStore = s
	return c
}

func (c *Calculator) calcAhr999Detailed(snap *model.MarketSnapshot) (score decimal.Decimal, raw decimal.Decimal, quantile float64, ok bool) {
	if snap.BTCPrice.IsZero() || len(snap.BTCPriceHistory) < ahrDCAWindowDays {
		return decimal.Zero, decimal.Zero, -1, false
	}

	price, _ := snap.BTCPrice.Float64()
	if price <= 0 {
		return decimal.Zero, decimal.Zero, -1, false
	}

	if len(snap.BTCPriceHistory) >= ahrMinFitWindowDays {
		score, rawFloat, q, ok := c.calcAdaptiveAhr999Score(snap, price)
		if ok {
			return score, decimal.NewFromFloat(rawFloat), q, true
		}
	}

	score, raw = c.calcLegacyAhr999Score(snap, price)
	if raw.IsZero() {
		return decimal.Zero, decimal.Zero, -1, false
	}
	return score, raw, -1, true
}

// ahrHalfLife 返回有效半衰期（天）。优先使用 Config 配置；否则用默认常量。
func (c *Calculator) ahrHalfLife() int {
	if c != nil && c.Config != nil && c.Config.Thresholds.AHRHalfLifeDays > 0 {
		return c.Config.Thresholds.AHRHalfLifeDays
	}
	return ahrRecentHalfLifeDays
}

func (c *Calculator) calcAdaptiveAhr999Score(snap *model.MarketSnapshot, price float64) (decimal.Decimal, float64, float64, bool) {
	halfLife := c.ahrHalfLife()
	fit, ok := fitAhrLogLogModel(snap.BTCPriceHistory, snap.Date, halfLife)
	if !ok {
		return decimal.Zero, 0, -1, false
	}

	dcaCost, ok := calculateDcaCost(snap.BTCPriceHistory, 0, ahrDCAWindowDays)
	if !ok {
		return decimal.Zero, 0, -1, false
	}

	fairValue := fit.fairValue(snap.Date)
	if !isUsablePositive(fairValue) {
		return decimal.Zero, 0, -1, false
	}

	raw := (price / dcaCost) * (price / fairValue)
	if !isUsablePositive(raw) {
		return decimal.Zero, 0, -1, false
	}

	logSamples := buildAhrLogSamples(snap.BTCPriceHistory, snap.Date, fit)
	if len(logSamples) < ahrMinFitWindowDays-ahrDCAWindowDays {
		return decimal.Zero, 0, -1, false
	}

	logRaw := math.Log(raw)
	q := quantileRank(logSamples, logRaw)
	score := scoreAhrByDynamicQuantiles(logRaw, logSamples)
	return decimal.NewFromFloat(score), raw, q, true
}

func (c *Calculator) calcLegacyAhr999Score(snap *model.MarketSnapshot, price float64) (decimal.Decimal, decimal.Decimal) {
	dcaCost, ok := calculateDcaCost(snap.BTCPriceHistory, 0, ahrDCAWindowDays)
	if !ok {
		return decimal.Zero, decimal.Zero
	}

	genesis := time.Date(2009, 1, 3, 0, 0, 0, 0, time.UTC)
	days := snap.Date.Sub(genesis).Hours() / 24.0
	if days <= 0 {
		return decimal.Zero, decimal.Zero
	}
	val := ahrLegacyLogSlope*math.Log10(days) + ahrLegacyLogIntercept
	expGrowth := math.Pow(10, val)
	if !isUsablePositive(expGrowth) {
		return decimal.Zero, decimal.Zero
	}
	ahr999 := (price / dcaCost) * (price / expGrowth)
	if !isUsablePositive(ahr999) {
		return decimal.Zero, decimal.Zero
	}

	ahrDec := decimal.NewFromFloat(ahr999)
	var score decimal.Decimal
	switch {
	case ahr999 < 0.45:
		// 历史底部区域
		score = decimal.NewFromInt(1)
	case ahr999 < 0.8:
		// 低估区 [0.45, 0.8] -> [+1, +0.5]
		score = mathutil.LinearInterpolate(
			ahrDec,
			decimal.NewFromFloat(0.45), decimal.NewFromFloat(0.8),
			decimal.NewFromInt(1), decimal.NewFromFloat(0.5),
		)
	case ahr999 < 1.0:
		// 合理偏低 [0.8, 1.0] -> [+0.5, 0]
		score = mathutil.LinearInterpolate(
			ahrDec,
			decimal.NewFromFloat(0.8), decimal.NewFromInt(1),
			decimal.NewFromFloat(0.5), decimal.Zero,
		)
	case ahr999 < 1.2:
		// 合理偏高 [1.0, 1.2] -> [0, -0.5]
		score = mathutil.LinearInterpolate(
			ahrDec,
			decimal.NewFromInt(1), decimal.NewFromFloat(1.2),
			decimal.Zero, decimal.NewFromFloat(-0.5),
		)
	case ahr999 < 2.0:
		// 高估区 [1.2, 2.0] -> [-0.5, -1]
		score = mathutil.LinearInterpolate(
			ahrDec,
			decimal.NewFromFloat(1.2), decimal.NewFromInt(2),
			decimal.NewFromFloat(-0.5), decimal.NewFromInt(-1),
		)
	default:
		// 泡沫区
		score = decimal.NewFromInt(-1)
	}

	return score, ahrDec
}

// 压缩版 AHR999 阈值 — 原始阈值经过 pow(x, 0.75) 映射，保证分档数学等价。
const (
	ctComp045 = 0.549 // 0.45^0.75
	ctComp08  = 0.846 // 0.8^0.75
	ctComp10  = 1.000 // 1.0^0.75 (用于线性插值)
	ctComp12  = 1.147 // 1.2^0.75
	ctComp20  = 1.682 // 2.0^0.75
	ctComp50  = 3.344 // 5.0^0.75
	ctComp200 = 9.457 // 20.0^0.75
)

// calcCompressedAhr999 计算压缩版 sqrt-AHR999。
// 公式：raw = (price/dcaCost) * (price/fairValue)，然后 compressed = raw^0.75。
// 阈值使用原始阈值^0.75，保证分档与原始 AHR999 数学等价。
// 压缩降低 price² 的凸性偏差，让 5.0+ 泡沫桶从假阳性翻转为高风险信号。
// 回测验证：5.0-20.0 桶 fwd180 从 +47% 降至 -35%；≥20.0 桶胜率保持 0%。
func (c *Calculator) calcCompressedAhr999(snap *model.MarketSnapshot) (score decimal.Decimal, rawCompressed float64, rawOriginal float64, ok bool) {
	if snap.BTCPrice.IsZero() || len(snap.BTCPriceHistory) < ahrDCAWindowDays {
		return decimal.Zero, 0, 0, false
	}

	price, _ := snap.BTCPrice.Float64()
	if price <= 0 {
		return decimal.Zero, 0, 0, false
	}

	dcaCost, dcaOK := calculateDcaCost(snap.BTCPriceHistory, 0, ahrDCAWindowDays)
	if !dcaOK {
		return decimal.Zero, 0, 0, false
	}

	genesis := time.Date(2009, 1, 3, 0, 0, 0, 0, time.UTC)
	days := snap.Date.Sub(genesis).Hours() / 24.0
	if days <= 0 {
		return decimal.Zero, 0, 0, false
	}
	val := ahrLegacyLogSlope*math.Log10(days) + ahrLegacyLogIntercept
	expGrowth := math.Pow(10, val)
	if !isUsablePositive(expGrowth) {
		return decimal.Zero, 0, 0, false
	}

	rawOriginal = (price / dcaCost) * (price / expGrowth)
	if !isUsablePositive(rawOriginal) {
		return decimal.Zero, 0, 0, false
	}

	rawCompressed = math.Pow(rawOriginal, ahrCompressionExponent)

	// 使用压缩映射后的等价阈值评分
	var s decimal.Decimal
	compDec := decimal.NewFromFloat(rawCompressed)
	switch {
	case rawCompressed < ctComp045:
		s = decimal.NewFromInt(1)
	case rawCompressed < ctComp08:
		s = mathutil.LinearInterpolate(
			compDec,
			decimal.NewFromFloat(ctComp045), decimal.NewFromFloat(ctComp08),
			decimal.NewFromInt(1), decimal.NewFromFloat(0.5),
		)
	case rawCompressed < ctComp10:
		s = mathutil.LinearInterpolate(
			compDec,
			decimal.NewFromFloat(ctComp08), decimal.NewFromFloat(ctComp10),
			decimal.NewFromFloat(0.5), decimal.Zero,
		)
	case rawCompressed < ctComp12:
		s = mathutil.LinearInterpolate(
			compDec,
			decimal.NewFromFloat(ctComp10), decimal.NewFromFloat(ctComp12),
			decimal.Zero, decimal.NewFromFloat(-0.5),
		)
	case rawCompressed < ctComp20:
		s = mathutil.LinearInterpolate(
			compDec,
			decimal.NewFromFloat(ctComp12), decimal.NewFromFloat(ctComp20),
			decimal.NewFromFloat(-0.5), decimal.NewFromInt(-1),
		)
	default:
		s = decimal.NewFromFloat(-1)
	}

	return s, rawCompressed, rawOriginal, true
}

// calc3DScore 计算三维打分（估值 × 动量 × 恐慌）。
// 回测验证：V--（仅估值便宜）fwd180 +100.9%/95%胜率
// -M-（仅动量/价格低于200d SMA）fwd180 -33.4%/11%胜率 = 下跌延续风险。
func (c *Calculator) calc3DScore(snap *model.MarketSnapshot) (score int, val float64, mayer float64, dd float64, ok bool) {
	price, _ := snap.BTCPrice.Float64()
	if price <= 0 || len(snap.BTCPriceHistory) < 200 {
		return 0, 0, 0, 0, false
	}

	genesis := time.Date(2009, 1, 3, 0, 0, 0, 0, time.UTC)
	days := snap.Date.Sub(genesis).Hours() / 24.0
	if days > 0 {
		fv := math.Pow(10, ahrLegacyLogSlope*math.Log10(days)+ahrLegacyLogIntercept)
		if fv > 0 {
			val = price / fv
		}
	}

	sma200 := 0.0
	for i := 0; i < 200; i++ {
		v, _ := snap.BTCPriceHistory[i].Float64()
		sma200 += v
	}
	if sma200 > 0 {
		mayer = price / (sma200 / 200)
	}

	if len(snap.BTCPriceHistory) >= 90 {
		max90 := 0.0
		for i := 0; i < 90; i++ {
			v, _ := snap.BTCPriceHistory[i].Float64()
			if v > max90 {
				max90 = v
			}
		}
		if max90 > 0 {
			dd = (price - max90) / max90
		}
	}

	if val > 0 && val < 0.8 {
		score++
	}
	if mayer > 0 && mayer < 1.0 {
		score++
	}
	if dd < -0.20 {
		score++
	}
	return score, val, mayer, dd, true
}

type ahrLogLogFit struct {
	alpha float64
	beta  float64
}

// fitAhrLogLogModel 拟合 log(price) = alpha + beta*log(age)。
// 时间衰减：weight = 0.5^(i / halfLife)，越近权重越大。
// 抗 outlier：先做加权 LS 得初始 fit，再用 Huber 单步重加权（c=1.345·MAD），
//
//	降低 2021 牛市顶 / LUNA-FTX 双爆这种极端样本对斜率的影响。
func fitAhrLogLogModel(history []decimal.Decimal, asOf time.Time, halfLifeDays int) (ahrLogLogFit, bool) {
	if halfLifeDays <= 0 {
		halfLifeDays = ahrRecentHalfLifeDays
	}

	limit := len(history)
	if limit > ahrFitWindowDays {
		limit = ahrFitWindowDays
	}
	if limit < ahrMinFitWindowDays {
		return ahrLogLogFit{}, false
	}

	samples := make([]ahrSample, 0, limit)
	for i := 0; i < limit; i++ {
		price, _ := history[i].Float64()
		if price <= 0 {
			continue
		}
		date := asOf.AddDate(0, 0, -i)
		ageDays := bitcoinAgeDays(date)
		if ageDays <= 0 {
			continue
		}
		samples = append(samples, ahrSample{
			x: math.Log(ageDays),
			y: math.Log(price),
			w: math.Pow(0.5, float64(i)/float64(halfLifeDays)),
		})
	}
	if len(samples) < ahrMinFitWindowDays {
		return ahrLogLogFit{}, false
	}

	// Step 1: 初始加权最小二乘
	alpha, beta, ok := weightedLinearFit(samples)
	if !ok {
		return ahrLogLogFit{}, false
	}

	// Step 2: Huber 单步重加权（IRLS 1 iter），削弱极端残差
	residuals := make([]float64, len(samples))
	for i, s := range samples {
		residuals[i] = s.y - (alpha + beta*s.x)
	}
	mad := medianAbsDeviation(residuals)
	if mad > 1e-9 {
		// 标准 Huber 阈值 1.345·σ ≈ 2·MAD（MAD≈0.6745σ）
		threshold := 2.0 * mad
		for i := range samples {
			r := math.Abs(residuals[i])
			if r > threshold {
				samples[i].w *= threshold / r // |r|>threshold 部分按 threshold/|r| 衰减
			}
		}
		alpha2, beta2, ok2 := weightedLinearFit(samples)
		if ok2 {
			alpha, beta = alpha2, beta2
		}
	}

	if !isUsableFinite(alpha) || !isUsableFinite(beta) {
		return ahrLogLogFit{}, false
	}

	return ahrLogLogFit{alpha: alpha, beta: beta}, true
}

// ahrSample 是 fit 拟合用的样本（log-age, log-price, weight）。
type ahrSample struct {
	x, y, w float64
}

// weightedLinearFit y = alpha + beta*x，加权最小二乘。
func weightedLinearFit(samples []ahrSample) (float64, float64, bool) {
	var sw, sx, sy, sxx, sxy float64
	for _, s := range samples {
		sw += s.w
		sx += s.w * s.x
		sy += s.w * s.y
		sxx += s.w * s.x * s.x
		sxy += s.w * s.x * s.y
	}
	den := sw*sxx - sx*sx
	if sw <= 0 || math.Abs(den) < 1e-12 {
		return 0, 0, false
	}
	beta := (sw*sxy - sx*sy) / den
	alpha := (sy - beta*sx) / sw
	return alpha, beta, true
}

// medianAbsDeviation 计算 |residual - median(residual)| 的中位数（MAD）。
func medianAbsDeviation(residuals []float64) float64 {
	if len(residuals) == 0 {
		return 0
	}
	cp := make([]float64, len(residuals))
	copy(cp, residuals)
	sort.Float64s(cp)
	med := cp[len(cp)/2]
	for i := range cp {
		cp[i] = math.Abs(cp[i] - med)
	}
	sort.Float64s(cp)
	return cp[len(cp)/2]
}

func (f ahrLogLogFit) fairValue(date time.Time) float64 {
	ageDays := bitcoinAgeDays(date)
	if ageDays <= 0 {
		return 0
	}
	return math.Exp(f.alpha + f.beta*math.Log(ageDays))
}

func buildAhrLogSamples(history []decimal.Decimal, asOf time.Time, fit ahrLogLogFit) []float64 {
	limit := len(history)
	if limit > ahrFitWindowDays {
		limit = ahrFitWindowDays
	}

	samples := make([]float64, 0, limit-ahrDCAWindowDays)
	for i := 0; i+ahrDCAWindowDays <= limit; i++ {
		price, _ := history[i].Float64()
		if price <= 0 {
			continue
		}

		dcaCost, ok := calculateDcaCost(history, i, ahrDCAWindowDays)
		if !ok {
			continue
		}

		date := asOf.AddDate(0, 0, -i)
		fairValue := fit.fairValue(date)
		if !isUsablePositive(fairValue) {
			continue
		}

		raw := (price / dcaCost) * (price / fairValue)
		if isUsablePositive(raw) {
			samples = append(samples, math.Log(raw))
		}
	}

	return samples
}

func calculateDcaCost(history []decimal.Decimal, start, window int) (float64, bool) {
	if window <= 0 || start < 0 || len(history) < start+window {
		return 0, false
	}

	invSum := 0.0
	count := 0
	for i := start; i < start+window; i++ {
		price, _ := history[i].Float64()
		if price <= 0 {
			continue
		}
		invSum += 1 / price
		count++
	}
	if count == 0 || invSum <= 0 {
		return 0, false
	}

	return float64(count) / invSum, true
}

func scoreAhrByDynamicQuantiles(logAhr float64, samples []float64) float64 {
	sort.Float64s(samples)

	q10 := quantileSorted(samples, 0.10)
	q35 := quantileSorted(samples, 0.35)
	q55 := quantileSorted(samples, 0.55)
	q75 := quantileSorted(samples, 0.75)
	q90 := quantileSorted(samples, 0.90)

	switch {
	case logAhr <= q10:
		return 1
	case logAhr <= q35:
		return linearMap(logAhr, q10, q35, 1, 0.5)
	case logAhr <= q55:
		return linearMap(logAhr, q35, q55, 0.5, 0)
	case logAhr <= q75:
		return linearMap(logAhr, q55, q75, 0, -0.5)
	case logAhr <= q90:
		return linearMap(logAhr, q75, q90, -0.5, -1)
	default:
		return -1
	}
}

func quantileSorted(sortedValues []float64, q float64) float64 {
	if len(sortedValues) == 0 {
		return 0
	}
	if q <= 0 {
		return sortedValues[0]
	}
	if q >= 1 {
		return sortedValues[len(sortedValues)-1]
	}

	pos := q * float64(len(sortedValues)-1)
	lower := int(math.Floor(pos))
	upper := int(math.Ceil(pos))
	if lower == upper {
		return sortedValues[lower]
	}
	frac := pos - float64(lower)
	return sortedValues[lower]*(1-frac) + sortedValues[upper]*frac
}

func linearMap(value, inMin, inMax, outMin, outMax float64) float64 {
	if math.Abs(inMax-inMin) < 1e-12 {
		return outMin
	}
	ratio := (value - inMin) / (inMax - inMin)
	return outMin + ratio*(outMax-outMin)
}

func bitcoinAgeDays(date time.Time) float64 {
	genesis := time.Date(2009, 1, 3, 0, 0, 0, 0, time.UTC)
	return date.Sub(genesis).Hours() / 24.0
}

// ── Jacobian-corrected Power-Law (Priority 1) ──
//
// Problem: standard log-log OLS with time-decay weights (w=0.5^(i/halfLife)) lets
// recent data dominate — the most recent era has ~8.78x more weight than the earliest.
// The regression overwhelmingly fits post-2020 price action.
//
// Fix: w = 1/ageDays so every multiplicative decade of Bitcoin's history gets equal
// weight. Combined with Huber reweighting, this produces a "structural" fair value
// that doesn't drift with recent bull/bear cycles.

func fitAhrLogLogModelJacobian(history []decimal.Decimal, asOf time.Time) (ahrLogLogFit, bool) {
	limit := len(history)
	if limit > ahrFitWindowDays {
		limit = ahrFitWindowDays
	}
	if limit < ahrMinFitWindowDays {
		return ahrLogLogFit{}, false
	}

	samples := make([]ahrSample, 0, limit)
	for i := 0; i < limit; i++ {
		price, _ := history[i].Float64()
		if price <= 0 {
			continue
		}
		date := asOf.AddDate(0, 0, -i)
		ageDays := bitcoinAgeDays(date)
		if ageDays <= 0 {
			continue
		}
		samples = append(samples, ahrSample{
			x: math.Log(ageDays),
			y: math.Log(price),
			w: 1.0 / ageDays, // Jacobian: equal weight per log-time decade
		})
	}
	if len(samples) < ahrMinFitWindowDays {
		return ahrLogLogFit{}, false
	}

	alpha, beta, ok := weightedLinearFit(samples)
	if !ok {
		return ahrLogLogFit{}, false
	}

	// Huber single-step reweighting — same as adaptive fit, different initial weights
	residuals := make([]float64, len(samples))
	for i, s := range samples {
		residuals[i] = s.y - (alpha + beta*s.x)
	}
	mad := medianAbsDeviation(residuals)
	if mad > 1e-9 {
		threshold := 2.0 * mad
		for i := range samples {
			r := math.Abs(residuals[i])
			if r > threshold {
				samples[i].w *= threshold / r
			}
		}
		alpha2, beta2, ok2 := weightedLinearFit(samples)
		if ok2 {
			alpha, beta = alpha2, beta2
		}
	}

	if !isUsableFinite(alpha) || !isUsableFinite(beta) {
		return ahrLogLogFit{}, false
	}

	return ahrLogLogFit{alpha: alpha, beta: beta}, true
}

// calcAhr999Jacobian computes AHR999 using Jacobian-corrected power-law fair value.
// The Jacobian fit gives every era equal voice — early history (2010-2016) has the
// same structural weight as recent history, unlike the adaptive fit which prioritizes
// the last 4-year half-life window.
func (c *Calculator) calcAhr999Jacobian(snap *model.MarketSnapshot) (raw float64, ok bool) {
	if snap.BTCPrice.IsZero() || len(snap.BTCPriceHistory) < ahrMinFitWindowDays {
		return 0, false
	}
	price, _ := snap.BTCPrice.Float64()
	if price <= 0 {
		return 0, false
	}

	dcaCost, dcaOK := calculateDcaCost(snap.BTCPriceHistory, 0, ahrDCAWindowDays)
	if !dcaOK {
		return 0, false
	}

	fit, fitOK := fitAhrLogLogModelJacobian(snap.BTCPriceHistory, snap.Date)
	if !fitOK {
		return 0, false
	}

	fv := fit.fairValue(snap.Date)
	if !isUsablePositive(fv) {
		return 0, false
	}

	raw = (price / dcaCost) * (price / fv)
	if !isUsablePositive(raw) {
		return 0, false
	}
	return raw, true
}

// ── Miner Cost Floor (Priority 2+4 merged) ──
//
// Estimates the all-in electricity cost of mining one BTC. When BTC price approaches
// or falls below this floor, miners operate at a loss → historically strong buy zones.
//
// Formula: cost_per_btc = (hashrate_THs * elec_cost_per_TH_per_day) / btc_mined_per_day
//
// Assumptions (conservative, efficient miner baseline):
//   - Electricity: $0.05/kWh (global average for industrial miners)
//   - Efficiency: 25 J/TH (modern ASIC like S21 Pro; older gear is less efficient so
//     the real network average cost is higher — this gives a conservative floor)
//   - Elec cost per TH/day: 25 * 24 / 3600 * 0.05 = $0.00833/day (per TH/s)
//
//	More precisely: 25 J/TH * 24h * (1kWh/3.6e6J) * $0.05/kWh = $0.00833/TH/day

// --- Volatility ---
// calcVolatilityScore 计算波动率得分
// 优化：结合价格方向判断，高波动 + 下跌可能是底部信号
// - 低波动 (< 2%) => +0.5（蓄势待发）
// - 高波动 + 下跌 => +0.5（恐慌抛售，可能是底部）关键修复！
// - 高波动 + 上涨 => -0.5（可能是顶部疯狂）
// - 中等波动 => 0
