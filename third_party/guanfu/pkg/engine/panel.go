// CoinMan v2: IndicatorPanel —— 投资盘面（无评分，无 action）
//
// BuildPanel() 直接从 MarketSnapshot 计算所有指标，按 8 个 domain 分组返回。
// 每个指标包含：原始值、历史分位、解读标签、数据源、更新时间、备注。
//
// 设计原则：
//   - 指标都是数学严格的原始量（不做 sigmoid/scaling 隐式压缩）
//   - 历史分位（q）告诉 Claude 当前值在历史分布中的位置
//   - label 仅用于人类速览，不应作为决策依据
//   - 决策由 skill SKILL.md（知识库）+ Claude 综合完成

package engine

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/mathutil"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"

	"github.com/shopspring/decimal"
)

// halvingDates BTC 减半历史日期（UTC）
var halvingDates = []time.Time{
	time.Date(2012, 11, 28, 0, 0, 0, 0, time.UTC), // 50 → 25 BTC
	time.Date(2016, 7, 9, 0, 0, 0, 0, time.UTC),   // 25 → 12.5
	time.Date(2020, 5, 11, 0, 0, 0, 0, time.UTC),  // 12.5 → 6.25
	time.Date(2024, 4, 20, 0, 0, 0, 0, time.UTC),  // 6.25 → 3.125
	time.Date(2028, 4, 20, 0, 0, 0, 0, time.UTC),  // 估计：3.125 → 1.5625
}

const btcDailyHistorySource = "coinmetrics:PriceUSD + binance:BTCUSDT"

// BuildPanel 构造 v2 指标盘面
func (c *Calculator) BuildPanel(snap *model.MarketSnapshot) *model.IndicatorPanel {
	dataDate := snap.Date.Format("2006-01-02")
	now := time.Now().UTC().Format(time.RFC3339)

	panel := &model.IndicatorPanel{
		Asset: "btc",
		Date:  dataDate,
		Snapshot: model.SnapshotData{
			BTCPrice:            f(snap.BTCPrice),
			ETHPrice:            f(snap.ETHPrice),
			GoldPrice:           f(snap.GoldPriceUSD),
			QQQPrice:            f(snap.QQQPrice),
			SPYPrice:            f(snap.SPYPrice),
			BTCDominance:        f(snap.BTCDominance),
			TotalMarketCap:      f(snap.TotalMarketCap),
			StablecoinMarketCap: f(snap.StablecoinMarketCap),
			FearGreed:           f(snap.FearGreedIndex),
			DataDate:            dataDate,
		},
		Cycle:         map[string]model.Indicator{},
		Valuation:     map[string]model.Indicator{},
		Network:       map[string]model.Indicator{},
		Positioning:   map[string]model.Indicator{},
		Macro:         map[string]model.Indicator{},
		Flow:          map[string]model.Indicator{},
		Technical:     map[string]model.Indicator{},
		CrossAsset:    map[string]model.Indicator{},
		StaleWarnings: append([]string(nil), snap.SourceWarnings...),
		SourceHealth:  buildSourceHealth(snap),
	}

	c.fillCycle(panel, snap, now)
	c.fillValuation(panel, snap, now)
	c.fillNetwork(panel, snap, now)
	c.fillPositioning(panel, snap, now)
	c.fillMacro(panel, snap, now)
	c.fillFlow(panel, snap, now)
	c.fillTechnical(panel, snap, now)
	c.fillCrossAsset(panel, snap, now)
	EnrichGlobalInvestorMacro(panel, c.PriceStore)
	AnnotatePanelProfile(panel, "btc")

	c.persistAndAnnotateHistory(panel, dataDate)
	panel.StaleWarnings = dedupeStrings(panel.StaleWarnings)

	return panel
}

// ----------------- Cycle 周期 -----------------

func (c *Calculator) fillCycle(p *model.IndicatorPanel, snap *model.MarketSnapshot, ts string) {
	btcTS := sourceTimestamp(snap.BTCPriceAsOf, ts)

	// halving cycle days: 距离上次减半天数 + 距离下次减半天数
	prevHalving, nextHalving := nearestHalvings(snap.Date)
	if !prevHalving.IsZero() {
		days := int(snap.Date.Sub(prevHalving).Hours() / 24)
		p.Cycle["days_since_halving"] = model.Indicator{
			Value:     float64(days),
			Label:     halvingPhaseLabel(days),
			Source:    "static",
			UpdatedAt: ts,
			Note:      fmt.Sprintf("上次减半: %s", prevHalving.Format("2006-01-02")),
		}
	}
	if !nextHalving.IsZero() {
		days := int(nextHalving.Sub(snap.Date).Hours() / 24)
		p.Cycle["days_to_halving"] = model.Indicator{
			Value:     float64(days),
			Source:    "static",
			UpdatedAt: ts,
			Note:      fmt.Sprintf("下次减半（估计）: %s", nextHalving.Format("2006-01-02")),
		}
	}

	// 200-week SMA + 偏离度
	if maW, ok := wmaSMA(snap.BTCPriceHistory, 200); ok {
		price := f(snap.BTCPrice)
		dev := (price - maW) / maW
		p.Cycle["sma_200w"] = model.Indicator{
			Value:     maW,
			Source:    btcDailyHistorySource,
			UpdatedAt: btcTS,
			Note:      "200 周（1400 日）SMA — 历史价格地板",
		}
		p.Cycle["sma_200w_dev"] = model.Indicator{
			Value:     dev,
			Quantile:  displayQuantile(quantileOfDeviation(snap.BTCPriceHistory, 200*7, dev)),
			Label:     devLabel(dev),
			Source:    btcDailyHistorySource,
			UpdatedAt: btcTS,
			Note:      "(price - 200wSMA) / 200wSMA。<0 历史低位区，>1.5 牛市末期",
		}
	}

	// Mayer Multiple = price / 200d SMA
	if ma200d, ok := smaFromHistory(snap.BTCPriceHistory, 200); ok {
		mayer := f(snap.BTCPrice) / ma200d
		p.Cycle["mayer_multiple"] = model.Indicator{
			Value:     mayer,
			Quantile:  displayQuantile(quantileOfMayer(snap.BTCPriceHistory, 200)),
			Label:     mayerLabel(mayer),
			Source:    btcDailyHistorySource,
			UpdatedAt: btcTS,
			Note:      "price / 200d SMA。<1 偏低估，>2.4 历史顶部区",
		}
	}

	// Pi Cycle Top: 111d MA vs 2 × 350d MA（顶部信号 = 111dMA 上穿 2×350dMA）
	ma111, ok1 := smaFromHistory(snap.BTCPriceHistory, 111)
	ma350, ok2 := smaFromHistory(snap.BTCPriceHistory, 350)
	if ok1 && ok2 {
		ratio := ma111 / (2 * ma350)
		p.Cycle["pi_cycle_top_ratio"] = model.Indicator{
			Value:     ratio,
			Label:     piCycleLabel(ratio),
			Source:    btcDailyHistorySource,
			UpdatedAt: btcTS,
			Note:      "111dMA / (2×350dMA)。>=1 触发 Pi Cycle Top（历史顶部信号）",
		}
	}

	// Cycle phase 分类（基于 sma_200w_dev + days_since_halving 简单启发式）
	p.Cycle["phase"] = model.Indicator{
		Label:     classifyCyclePhase(snap, prevHalving),
		Source:    "derived",
		UpdatedAt: ts,
		Note:      "启发式分类: accumulation/markup/distribution/markdown",
	}

	// ── Diminishing Returns Cycle Model (Priority 5) ──
	if !prevHalving.IsZero() {
		daysSince := int(snap.Date.Sub(prevHalving).Hours() / 24)
		// Compute 200w SMA at halving date as the structural price anchor.
		// The 200w SMA at halving ≈ floor price that anchors the cycle ROI estimate.
		// We approximate using the current 200w SMA (which drifts up ~2%/year)
		// adjusted back to halving date.
		halving200w, hwOK := wmaSMA(snap.BTCPriceHistory, 200)
		if hwOK && halving200w > 0 {
			// Adjust backward: 200w SMA grows roughly with power-law (~2-3%/year).
			// Simple time-based adjustment: 200wSMA at halving ≈ current 200wSMA * (halving_age/current_age)^beta
			// where beta ≈ 2.4 (the power-law exponent from the log-log fit).
			// Approximate: halving 200w ≈ current 200w * 0.92 (743 days ago, ~2.4*log(1-743/6000) ≈ -8%)
			halvingAnchor := halving200w * 0.92
			if estPeak, estDelay, roiMult, droiOK := calcDiminishingROI(halvingAnchor); droiOK {
				p.Cycle["diminishing_roi_peak_est"] = model.Indicator{
					Value:     estPeak,
					Source:    "derived: cross-cycle ROI decay",
					UpdatedAt: ts,
					Note:      fmt.Sprintf("跨周期 ROI 衰减模型: rho≈0.29, 峰值延迟→552d。本周期估算 %.1fx ROI (halving锚≈$%.0fK), 峰值 ~%d 天 post-halving (当前 %d 天)。历史: 2013 98x/370d, 2017 30x/520d, 2021 8x/550d", roiMult, halvingAnchor/1000, estDelay, daysSince),
				}
			}
		}
	}
}

// ----------------- Valuation 估值 -----------------

func (c *Calculator) fillValuation(p *model.IndicatorPanel, snap *model.MarketSnapshot, ts string) {
	btcTS := sourceTimestamp(snap.BTCPriceAsOf, ts)
	onchainTS := sourceTimestamp(snap.OnchainValuationAsOf, ts)
	networkTS := sourceTimestamp(snap.MempoolAsOf, ts)

	// ── AHR999 压缩版（sqrt-AHR: pow(raw, 0.75), 主估值信号）──
	// 回测验证：压缩后 5.0-20.0 桶 fwd180 -15.4%，显著优于原始 AHR999 的分档效果。
	// 推荐作为唯一主 AHR 信号源，自适应版降级为分歧检测的诊断量。
	_, ahrCompressed, ahrOrig, compOK := c.calcCompressedAhr999(snap)
	if compOK {
		p.Valuation["ahr999_compressed"] = model.Indicator{
			Value:     ahrCompressed,
			Label:     ahrCompressedLabel(ahrCompressed),
			Source:    btcDailyHistorySource + " + power-law (compressed)",
			UpdatedAt: btcTS,
			Note:      fmt.Sprintf("sqrt-AHR = (原始 AHR999)^0.75。回测：<0.45→+86.5%%/88%%胜率, 2.0-5.0→-15.4%%/20%%胜率, >=20.0→0%%胜率。原始值: %.3f", ahrOrig),
		}
	}

	// ── AHR999 Jacobian-corrected (Priority 1: scale-invariant power-law) ──
	// Uses w=1/ageDays so every multiplicative decade of BTC history gets equal fit weight.
	// This is the "structural" fair value — doesn't drift with recent cycles.
	if jacRaw, jacOK := c.calcAhr999Jacobian(snap); jacOK {
		jacCompressed := math.Pow(jacRaw, ahrCompressionExponent)
		p.Valuation["ahr999_jacobian"] = model.Indicator{
			Value:     jacCompressed,
			Label:     ahrCompressedLabel(jacCompressed),
			Source:    btcDailyHistorySource + " + jacobian power-law",
			UpdatedAt: btcTS,
			Note:      fmt.Sprintf("Jacobian-corrected sqrt-AHR。w=1/t 使各时期等权，不随近期牛熊漂移。原始值: %.3f", jacRaw),
		}
	}

	// ── AHR999 自适应版（展示用 + 分歧检测诊断量）──
	_, ahrAdaptiveRaw, ahrQ, adaptiveOK := c.calcAhr999Detailed(snap)
	if adaptiveOK && !ahrAdaptiveRaw.IsZero() {
		raw := f(ahrAdaptiveRaw)
		p.Valuation["ahr999"] = model.Indicator{
			Value:     raw,
			Quantile:  displayQuantile(ahrQ),
			Label:     ahrLabel(raw),
			Source:    btcDailyHistorySource + " + adaptive log-log fit",
			UpdatedAt: btcTS,
			Note:      "自适应 AHR（展示用 + 分歧检测）。回测显示极端牛市后 fair value 上移导致分桶失效，决策请用 ahr999_compressed",
		}
	}

	// ── AHR999 Divergence Detector ──
	// 当自适应版百分位与固定公式方向打架时，是市场转向预警。
	// 回测：原始贵 + 自适应百分位低 → fwd180 -34%~-53%, 0% 胜率。
	if adaptiveOK && compOK && ahrQ >= 0 {
		divergence := detectAhrDivergence(ahrOrig, ahrQ)
		if divergence != "" {
			p.Valuation["ahr999_divergence"] = model.Indicator{
				Label:     divergence,
				Source:    "derived: original vs adaptive",
				UpdatedAt: btcTS,
				Note:      fmt.Sprintf("原始 %.3f vs 自适应 q=%.0f%%。分歧 = 转向预警", ahrOrig, ahrQ*100),
			}
		}
	}

	// ── Miner Cost Floor (Priority 2+4: electricity cost anchor) ──
	if !snap.HashRateEHs.IsZero() {
		hashRate := f(snap.HashRateEHs)
		if costFloor, cfOK := calcMinerCostFloor(hashRate); cfOK {
			btc := f(snap.BTCPrice)
			ratio := btc / costFloor
			p.Valuation["miner_cost_floor"] = model.Indicator{
				Value:     costFloor,
				Source:    "mempool.space + derived",
				UpdatedAt: networkTS,
				Note:      fmt.Sprintf("电费成本锚: %.0f EH/s × $%.3f/TH/天 ÷ %.0f BTC/天。hashrate=%.0f EH/s, efficiency=%d J/TH, elec=$%.2f/kWh", costFloor, minerElecCostPerKWh, minerBTCMinedPerDay, hashRate, int(minerEfficiencyJPerTH), minerElecCostPerKWh),
			}
			p.Valuation["price_to_miner_cost"] = model.Indicator{
				Value:     ratio,
				Label:     minerCostRatioLabel(ratio),
				Source:    "derived",
				UpdatedAt: networkTS,
				Note:      fmt.Sprintf("price $%.0f / miner cost floor $%.0f = %.2fx。<1.0 = 矿工亏损（历史低位区），<0.8 = 矿工深度亏损（仅 2018/2020/2022 出现）", btc, costFloor, ratio),
			}
		}
	}

	// ── Difficulty Cost Proximity ──
	if !snap.DifficultyChangePct.IsZero() {
		dc := f(snap.DifficultyChangePct)
		_, dcNote := calcDifficultyCostRatio(dc)
		p.Valuation["difficulty_cost_signal"] = model.Indicator{
			Value:     dc,
			Label:     difficultyCostLabel(dc),
			Source:    "mempool.space + derived",
			UpdatedAt: networkTS,
			Note:      dcNote,
		}
	}

	// ── 三维打分 V/M/P（估值 × 动量 × 恐慌）──
	if d3score, d3val, d3mayer, d3dd, d3ok := c.calc3DScore(snap); d3ok {
		p.Valuation["d3_score"] = model.Indicator{
			Value:     float64(d3score),
			Label:     d3Label(d3score, d3val, d3mayer, d3dd),
			Source:    "derived",
			UpdatedAt: btcTS,
			Note:      "三维打分 0-3: V(price/fair<0.8) + M(mayer<1.0) + P(90d dd<-20%)",
		}
		p.Valuation["d3_val_ratio"] = model.Indicator{
			Value:     d3val,
			Label:     d3ValLabel(d3val),
			Source:    "derived",
			UpdatedAt: btcTS,
			Note:      "V 维度: price / power-law fair value。<0.8=估值便宜（+1分）",
		}
		p.Valuation["d3_momentum"] = model.Indicator{
			Value:     d3mayer,
			Label:     d3MayerLabel(d3mayer),
			Source:    "derived",
			UpdatedAt: btcTS,
			Note:      "M 维度: Mayer Multiple = price / 200d SMA。<1.0=均线下方（+1分）",
		}
		p.Valuation["d3_panic"] = model.Indicator{
			Value:     d3dd * 100,
			Label:     d3PanicLabel(d3dd),
			Source:    "derived",
			UpdatedAt: btcTS,
			Note:      "P 维度: 90d drawdown %。< -20%=恐慌状态（+1分）",
		}
	}

	if snap.OnchainValuationFetched {
		mvrv := f(snap.CapMVRVCur)
		nupl := f(snap.NUPL)
		z := f(snap.MVRVZScore)

		p.Valuation["mvrv"] = model.Indicator{
			Value:     mvrv,
			Quantile:  displayQuantile(f(snap.MVRVQuantile)),
			Label:     mvrvLabel(mvrv),
			Source:    "coinmetrics:CapMVRVCur",
			UpdatedAt: onchainTS,
			Note:      "MVRV = market cap / realized cap。CoinMetrics community metric",
		}
		p.Valuation["mvrv_z_score"] = model.Indicator{
			Value:     z,
			Label:     mvrvZLabel(z),
			Source:    "coinmetrics:CapMrktCurUSD+CapMVRVCur",
			UpdatedAt: onchainTS,
			Note:      "MVRV Z = (market cap - realized cap) / std(market cap)。realized cap direct or implied by CapMVRVCur",
		}
		p.Valuation["nupl"] = model.Indicator{
			Value:     nupl,
			Quantile:  displayQuantile(f(snap.NUPLQuantile)),
			Label:     nuplLabel(nupl),
			Source:    "coinmetrics:CapMVRVCur",
			UpdatedAt: onchainTS,
			Note:      "NUPL = (market cap - realized cap) / market cap = 1 - 1/MVRV",
		}

		// realized_price + price/realized 偏离 — 链上持币者平均成本基础
		// realized_price = realized_cap / supply，等价于 btc_price / mvrv（数学恒等）
		btc := f(snap.BTCPrice)
		if mvrv > 0 && btc > 0 {
			rp := btc / mvrv
			p.Valuation["realized_price"] = model.Indicator{
				Value:     rp,
				Source:    "coinmetrics:derived",
				UpdatedAt: onchainTS,
				Note:      "realized_price = realized_cap / supply = btc_price / mvrv。链上持币者平均成本",
			}
			devPct := (btc/rp - 1.0) * 100
			p.Valuation["price_to_realized_dev_pct"] = model.Indicator{
				Value:     devPct,
				Label:     priceToRealizedLabel(devPct),
				Source:    "coinmetrics:derived",
				UpdatedAt: onchainTS,
				Note:      "(price - realized_price) / realized_price × 100。<0 = 持币者整体亏损 = 历史级低位区（仅 2015/2018-12/2020-03/2022-11 出现）",
			}
		}
	} else {
		addStaleWarning(p, "coinmetrics valuation unavailable: MVRV/NUPL/MVRV Z/realized_price shown as placeholders")
		p.Valuation["mvrv_z_score"] = model.Indicator{
			Missing:   true,
			Source:    "coinmetrics (待接入)",
			UpdatedAt: ts,
			Note:      "需要 CoinMetrics CapMVRVCur + CapMrktCurUSD；若有 COINMETRICS_API_KEY 可尝试直接 CapRealUSD",
		}
		p.Valuation["nupl"] = model.Indicator{
			Missing:   true,
			Source:    "coinmetrics (待接入)",
			UpdatedAt: ts,
			Note:      "NUPL = (market cap - realized cap) / market cap；无数据时不估算",
		}
		p.Valuation["realized_price"] = model.Indicator{
			Missing:   true,
			Source:    "coinmetrics (待接入)",
			UpdatedAt: ts,
			Note:      "realized_price = realized_cap / supply；需 CoinMetrics 数据",
		}
		p.Valuation["price_to_realized_dev_pct"] = model.Indicator{
			Missing:   true,
			Source:    "coinmetrics (待接入)",
			UpdatedAt: ts,
			Note:      "需 realized_price 数据",
		}
	}
}

// ----------------- Network 网络 -----------------

func (c *Calculator) fillNetwork(p *model.IndicatorPanel, snap *model.MarketSnapshot, ts string) {
	networkTS := sourceTimestamp(snap.MempoolAsOf, ts)

	// 哈希率（EH/s）
	if !snap.HashRateEHs.IsZero() {
		hr := f(snap.HashRateEHs)
		p.Network["hash_rate_ehs"] = model.Indicator{
			Value:     hr,
			Label:     hashRateLabel(hr),
			Source:    "mempool.space",
			UpdatedAt: networkTS,
			Note:      "BTC 网络哈希率 (EH/s)。长期趋势上行 = 矿工对网络投票",
		}
	}

	// Hash ribbons（30d vs 60d）
	if snap.HashRibbonsLabel != "" {
		p.Network["hash_ribbons"] = model.Indicator{
			Label:     snap.HashRibbonsLabel,
			Source:    "mempool.space",
			UpdatedAt: networkTS,
			Note:      "30d MA vs 60d MA 交叉。'下行' = 矿工投降信号（历史底部前常见）",
		}
	}

	// 难度调整
	if snap.MempoolFetched || !snap.DifficultyChangePct.IsZero() {
		dc := f(snap.DifficultyChangePct)
		p.Network["difficulty_change_pct"] = model.Indicator{
			Value:     dc,
			Label:     difficultyLabel(dc),
			Source:    "mempool.space",
			UpdatedAt: networkTS,
			Note:      "上次难度调整 %。+ = 算力流入，- = 算力流出/投降",
		}
	}

	// Mempool 拥堵
	if snap.MempoolFetched || !snap.MempoolMB.IsZero() {
		mb := f(snap.MempoolMB)
		p.Network["mempool_mb"] = model.Indicator{
			Value:     mb,
			Label:     mempoolLabel(mb),
			Source:    "mempool.space",
			UpdatedAt: networkTS,
			Note:      "Mempool 待打包字节数 (MB)。>100 拥堵 = 链上活跃 / 牛市顶部常见",
		}
	}
}

// ----------------- Positioning 杠杆 & 情绪 -----------------

func (c *Calculator) fillPositioning(p *model.IndicatorPanel, snap *model.MarketSnapshot, ts string) {
	fearGreedTS := sourceTimestamp(snap.FearGreedAsOf, ts)

	// Funding rate
	if snap.FuturesFetched || !snap.BTCFundingRate.IsZero() {
		fr := f(snap.BTCFundingRate)
		p.Positioning["funding_rate_pct"] = model.Indicator{
			Value:     fr * 100, // 转百分比方便看
			Label:     fundingRateLabel(fr),
			Source:    "binance",
			UpdatedAt: ts,
			Note:      "BTC 永续合约资金费率 (%/8h)。>0.05% 高，<0 多头不愿付费 = 偏熊或反转",
		}
	}

	// OI / Market Cap
	// snap.BTCOpenInterest 存的是 BTC 数量（real.go fetchFuturesData 不再
	// 在那里乘价格，避免与 fetchBTCData 竞争）。这里折算 USD 后除以市值。
	if !snap.BTCOpenInterest.IsZero() && !snap.BTCPrice.IsZero() {
		oiUSD := f(snap.BTCOpenInterest) * f(snap.BTCPrice)
		mcBTC := f(snap.BTCPrice) * 19_700_000 // 大约流通量
		ratio := oiUSD / mcBTC
		p.Positioning["oi_to_mc"] = model.Indicator{
			Value:     ratio,
			Label:     oiLabel(ratio),
			Source:    "binance",
			UpdatedAt: ts,
			Note:      "OI(USD) / BTC 市值。>0.04 杠杆拥挤（清算风险），<0.015 杠杆松弛",
		}
	}

	// Fear & Greed
	if snap.FearGreedFetched || !snap.FearGreedIndex.IsZero() {
		fg := f(snap.FearGreedIndex)
		p.Positioning["fear_greed"] = model.Indicator{
			Value:     fg,
			Label:     fearGreedLabel(fg),
			Source:    "alternative.me",
			UpdatedAt: fearGreedTS,
			Note:      "0=极度恐慌, 100=极度贪婪。<20 历史低位常见, >80 谨慎",
		}
	}

	// Altcoin Season Index（自算 — 与 blockchaincenter.net 定义一致）
	if snap.AltcoinSeasonAvailable || !snap.AltcoinSeasonIndex.IsZero() {
		asi := f(snap.AltcoinSeasonIndex)
		p.Positioning["altcoin_season"] = model.Indicator{
			Value:     asi,
			Label:     altcoinSeasonLabel(asi),
			Source:    "computed:Binance Top50 vs BTC 90d",
			UpdatedAt: ts,
			Note:      "Top 50 中 90 日跑赢 BTC 的占比 (0-100)。>75=山寨季, <25=BTC 季。基于实时 Binance kline 计算",
		}
	} else {
		p.Positioning["altcoin_season"] = model.Indicator{
			Missing:   true,
			Source:    "computed:Binance Top50 vs BTC 90d",
			UpdatedAt: ts,
			Note:      "Top50 历史不足或抓取失败，山寨季指数本次跳过",
		}
	}

	// Deribit DVOL — 前瞻性 IV 指数（BTC 版 VIX）
	if snap.DVOLAvailable {
		dvol := f(snap.DVOL)
		dvolTS := sourceTimestamp(snap.DVOLAsOf, ts)
		// 历史分位（直接从 history 切片算）
		var q float64
		if n := len(snap.DVOLHistory); n >= 30 {
			vals := make([]float64, 0, n)
			for _, v := range snap.DVOLHistory {
				vals = append(vals, f(v))
			}
			q = quantileRank(vals, dvol)
		}
		p.Positioning["dvol"] = model.Indicator{
			Value:     dvol,
			Quantile:  displayQuantile(q),
			Label:     dvolLabel(dvol),
			Source:    "deribit:DVOL",
			UpdatedAt: dvolTS,
			Note:      "Deribit BTC IV 指数（30 日年化预期波动率）。BTC 版 VIX。<40 平静、>80 高度恐慌",
		}
		p.Positioning["dvol_60d_trend_pct"] = model.Indicator{
			Value:     f(snap.DVOL60dTrendPct),
			Source:    "deribit:DVOL",
			UpdatedAt: dvolTS,
			Note:      "DVOL 60 日变化 %。短期内大幅升 = 行情前波动率重定价",
		}
	} else {
		p.Positioning["dvol"] = model.Indicator{
			Missing:   true,
			Source:    "deribit (未拉到)",
			UpdatedAt: ts,
			Note:      "Deribit DVOL 拉取失败，本次 verdict 引擎跳过此指标",
		}
		p.Positioning["dvol_60d_trend_pct"] = model.Indicator{
			Missing:   true,
			Source:    "deribit (未拉到)",
			UpdatedAt: ts,
		}
	}

	// Deribit 25-delta skew — IV(25Δ put) - IV(25Δ call)
	if snap.SkewAvailable {
		skew := f(snap.Skew25dNearTermPct)
		p.Positioning["skew_25d_pct"] = model.Indicator{
			Value:     skew,
			Label:     skew25Label(skew),
			Source:    "deribit:options",
			UpdatedAt: sourceTimestamp(snap.SkewAsOf, ts),
			Note:      fmt.Sprintf("到期 %s。IV(25Δ put) - IV(25Δ call) (pp)。>0 = 下行对冲需求 / 恐慌；<0 = 上行追价 / 贪婪", snap.SkewExpiry),
		}
	} else {
		p.Positioning["skew_25d_pct"] = model.Indicator{
			Missing:   true,
			Source:    "deribit (未拉到)",
			UpdatedAt: ts,
			Note:      "Deribit 期权 skew 拉取失败，本次 verdict 引擎跳过此指标",
		}
	}
}

// ----------------- Macro 宏观 -----------------

func (c *Calculator) fillMacro(p *model.IndicatorPanel, snap *model.MarketSnapshot, ts string) {
	if !snap.MacroFetched {
		// FRED_API_KEY 缺失或拉取失败 — 保留 placeholder
		p.Macro["dxy_60d_trend_pct"] = model.Indicator{
			Missing:   true,
			Source:    "fred (待接入)",
			UpdatedAt: ts,
			Note:      "需要 FRED_API_KEY 环境变量。DTWEXBGS 60 日趋势",
		}
		p.Macro["real_yield_10y_pct"] = model.Indicator{
			Missing:   true,
			Source:    "fred (待接入)",
			UpdatedAt: ts,
			Note:      "需要 FRED_API_KEY。DFII10 10Y TIPS",
		}
		p.Macro["m2_yoy"] = model.Indicator{
			Missing:   true,
			Source:    "fred (待接入)",
			UpdatedAt: ts,
			Note:      "需要 FRED_API_KEY。M2SL 同比",
		}
		p.Macro["spx_correlation_30d"] = model.Indicator{
			Missing:   true,
			Source:    "computed (待接入)",
			UpdatedAt: ts,
			Note:      "需要 FRED_API_KEY 拉 SP500 后计算",
		}
	} else {
		// DXY 60d 趋势 (DTWEXBGS)
		if !snap.DXY60dTrendPct.IsZero() || !snap.DXYLatest.IsZero() {
			dxyTrend := f(snap.DXY60dTrendPct)
			p.Macro["dxy_60d_trend_pct"] = model.Indicator{
				Value:     dxyTrend,
				Label:     dxyTrendLabel(dxyTrend),
				Source:    "fred:DTWEXBGS",
				UpdatedAt: sourceTimestamp(snap.DXYAsOf, ts),
				Note:      fmt.Sprintf("Trade-Weighted USD (Broad) 60 日变化 %%。最新 %.2f (as of %s)。下行利好 BTC", f(snap.DXYLatest), snap.DXYAsOf),
			}
		}

		// 10Y 实际利率 (DFII10)
		if snap.RealYield10YAsOf != "" || !snap.RealYield10YPct.IsZero() {
			ry := f(snap.RealYield10YPct)
			p.Macro["real_yield_10y_pct"] = model.Indicator{
				Value:     ry,
				Label:     realYieldLabel(ry),
				Source:    "fred:DFII10",
				UpdatedAt: sourceTimestamp(snap.RealYield10YAsOf, ts),
				Note:      fmt.Sprintf("10Y TIPS 实际利率 %%（as of %s）。>2%% 历史性逆风，<0%% 极度宽松", snap.RealYield10YAsOf),
			}
		}

		// M2 同比 (M2SL)
		if !snap.M2YoYPct.IsZero() || !snap.M2LatestB.IsZero() {
			m2yoy := f(snap.M2YoYPct)
			p.Macro["m2_yoy"] = model.Indicator{
				Value:     m2yoy,
				Label:     m2YoYLabel(m2yoy),
				Source:    "fred:M2SL",
				UpdatedAt: sourceTimestamp(snap.M2AsOf, ts),
				Note:      fmt.Sprintf("M2 货币供应同比 %%。最新 $%.2fT（as of %s）。扩张 = BTC 顺风", f(snap.M2LatestB)/1000, snap.M2AsOf),
			}
		}
	}

	// ── US Liquidity (Fed Assets, RRP, TGA, Net Liquidity) ──
	if !snap.FedAssetsB.IsZero() || snap.FedAssetsAsOf != "" {
		fedA := f(snap.FedAssetsB)
		rrp := f(snap.RRPB)
		tga := f(snap.TGA_B)
		netLiq := f(snap.NetLiquidityB)
		liqTS := sourceTimestamp(snap.FedAssetsAsOf, ts)

		p.Macro["fed_assets_b"] = model.Indicator{
			Value:     fedA,
			Source:    "fred:WALCL",
			UpdatedAt: liqTS,
			Note:      fmt.Sprintf("Fed 总资产 ($%.2fT, as of %s)。QT 进行中则下降，QE 则上升", fedA/1000, snap.FedAssetsAsOf),
		}
		p.Macro["fed_assets_60d_trend_pct"] = model.Indicator{
			Value:     f(snap.FedAssets60dTrendPct),
			Label:     liqTrendLabel(f(snap.FedAssets60dTrendPct)),
			Source:    "fred:WALCL",
			UpdatedAt: liqTS,
			Note:      "Fed 总资产 60 日变化 %。负值 = QT 缩表，正值 = QE 扩表",
		}
		p.Macro["rrp_b"] = model.Indicator{
			Value:     rrp,
			Label:     rrpLabel(rrp),
			Source:    "fred:RRPONTSYD",
			UpdatedAt: sourceTimestamp(snap.RRPAsOf, ts),
			Note:      fmt.Sprintf("隔夜逆回购 ($%.0fB, as of %s)。下降 = 钱流入市场（顺风），上升 = 钱锁在 Fed（逆风）", rrp, snap.RRPAsOf),
		}
		p.Macro["rrp_60d_trend_pct"] = model.Indicator{
			Value:     f(snap.RRP60dTrendPct),
			Label:     liqTrendLabel(f(snap.RRP60dTrendPct)),
			Source:    "fred:RRPONTSYD",
			UpdatedAt: sourceTimestamp(snap.RRPAsOf, ts),
			Note:      "RRP 60 日变化 %。RRP 下降 = 流动性释出 = BTC 顺风（2023-2024 RRP 耗尽是隐性的主要推力）",
		}
		p.Macro["tga_b"] = model.Indicator{
			Value:     tga,
			Label:     tgaLabel(tga),
			Source:    "fred:WTREGEN",
			UpdatedAt: sourceTimestamp(snap.TGAAsOf, ts),
			Note:      fmt.Sprintf("财政部现金余额 ($%.0fB, as of %s)。下降 = 政府花钱注水，上升 = 抽水回笼（债务上限博弈期关键变量）", tga, snap.TGAAsOf),
		}
		p.Macro["tga_60d_trend_pct"] = model.Indicator{
			Value:     f(snap.TGA60dTrendPct),
			Label:     liqTrendLabel(f(snap.TGA60dTrendPct)),
			Source:    "fred:WTREGEN",
			UpdatedAt: sourceTimestamp(snap.TGAAsOf, ts),
			Note:      "TGA 60 日变化 %。TGA 上升 = 财政部回收流动性（逆风），下降 = 注水（顺风）",
		}
		if netLiq != 0 {
			p.Macro["net_liquidity_b"] = model.Indicator{
				Value:     netLiq,
				Label:     netLiqLabel(netLiq),
				Source:    "derived: WALCL - RRPONTSYD - WTREGEN",
				UpdatedAt: liqTS,
				Note:      fmt.Sprintf("净流动性 = Fed资产($%.0fB) - RRP($%.0fB) - TGA($%.0fB) = $%.0fB。公式: WALCL - RRPONTSYD - WTREGEN", fedA, rrp, tga, netLiq),
			}
			p.Macro["net_liquidity_60d_trend_pct"] = model.Indicator{
				Value:     f(snap.NetLiq60dTrendPct),
				Label:     liqTrendLabel(f(snap.NetLiq60dTrendPct)),
				Source:    "derived",
				UpdatedAt: liqTS,
				Note:      "净流动性 60 日变化 %。正值 = 市场注水（风险资产顺风），负值 = 抽水（逆风）",
			}
		}
	}

	// Oil price/proxy. Futu returns USO ETF proxy; Yahoo CL=F is WTI futures.
	if !snap.WTIPrice.IsZero() {
		oilPrice := f(snap.WTIPrice)
		oilSource := oilSource(snap)
		priceKey, trendKey := oilIndicatorKeys(oilSource)
		p.Macro[priceKey] = model.Indicator{
			Value:     oilPrice,
			Label:     oilPriceLabel(oilPrice),
			Source:    oilSource,
			UpdatedAt: sourceTimestamp(snap.WTIPriceAsOf, ts),
			Note:      oilPriceNote(oilSource),
		}
		// 60d 趋势
		if len(snap.WTIHistory) > 60 {
			wti60dAgo := f(snap.WTIHistory[60])
			if wti60dAgo > 0 {
				wti60dTrend := (oilPrice - wti60dAgo) / wti60dAgo * 100
				p.Macro[trendKey] = model.Indicator{
					Value:     wti60dTrend,
					Label:     oilTrendLabel(wti60dTrend),
					Source:    oilSource,
					UpdatedAt: sourceTimestamp(snap.WTIPriceAsOf, ts),
					Note:      oilTrendNote(oilSource),
				}
			}
		}
	}

	// 信用利差 — HY Spread (BAMLH0A0HYM2)
	if snap.HYSpreadAsOf != "" || !snap.HYSpreadBps.IsZero() {
		hyBps := f(snap.HYSpreadBps)
		p.Macro["hy_spread_bps"] = model.Indicator{
			Value:     hyBps,
			Label:     hySpreadLabel(hyBps),
			Source:    "fred:BAMLH0A0HYM2",
			UpdatedAt: sourceTimestamp(snap.HYSpreadAsOf, ts),
			Note:      fmt.Sprintf("ICE BofA US High Yield OAS (bp)。<350 风险偏好，>500 信用紧缩，>800 恐慌 (as of %s)", snap.HYSpreadAsOf),
		}
	}

	// 收益率曲线 — 10Y-2Y spread (T10Y2Y)
	if snap.YieldCurveAsOf != "" || !snap.YieldCurve10Y2YBps.IsZero() {
		ycBps := f(snap.YieldCurve10Y2YBps)
		p.Macro["yield_curve_10y2y_bps"] = model.Indicator{
			Value:     ycBps,
			Label:     yieldCurveLabel(ycBps),
			Source:    "fred:T10Y2Y",
			UpdatedAt: sourceTimestamp(snap.YieldCurveAsOf, ts),
			Note:      fmt.Sprintf("10Y-2Y Treasury spread (bp)。<0=倒挂(衰退预警)，>100=陡峭(复苏) (as of %s)", snap.YieldCurveAsOf),
		}
	}

	// BTC vs SPX 30d 相关
	if snap.SPXAsOf != "" || !snap.SPXCorrelation30d.IsZero() {
		corr := f(snap.SPXCorrelation30d)
		p.Macro["spx_correlation_30d"] = model.Indicator{
			Value:     corr,
			Label:     spxCorrLabel(corr),
			Source:    "fred:SP500 + binance:BTC",
			UpdatedAt: sourceTimestamp(snap.SPXAsOf, ts),
			Note:      fmt.Sprintf("BTC 与 SPX 30 日对数收益率 Pearson 相关（as of %s）。>0.5 BTC 强 risk-on，<0 BTC 走独立行情", snap.SPXAsOf),
		}
	}
}

func dxyTrendLabel(pct float64) string {
	switch {
	case pct < -3:
		return "美元大幅走弱（BTC 顺风）"
	case pct < -1:
		return "美元走弱"
	case pct < 1:
		return "美元横盘"
	case pct < 3:
		return "美元走强"
	default:
		return "美元大幅走强（BTC 逆风）"
	}
}

func realYieldLabel(pct float64) string {
	switch {
	case pct < 0:
		return "负实际利率（极度宽松，风险资产顺风）"
	case pct < 1:
		return "低位（BTC 顺风）"
	case pct < 2:
		return "正常"
	case pct < 2.5:
		return "高位（BTC 逆风）"
	default:
		return "极高（历史性逆风）"
	}
}

func m2YoYLabel(yoy float64) string {
	switch {
	case yoy < -1:
		return "收缩（罕见，2008/2022 后期出现）"
	case yoy < 2:
		return "停滞（流动性紧）"
	case yoy < 5:
		return "温和扩张"
	case yoy < 8:
		return "扩张（BTC 顺风）"
	default:
		return "强劲扩张（2020-21 印钞峰值）"
	}
}

func spxCorrLabel(c float64) string {
	switch {
	case c < -0.3:
		return "负相关（BTC 走独立避险行情）"
	case c < 0.2:
		return "弱相关（独立性较强）"
	case c < 0.5:
		return "中等相关"
	case c < 0.7:
		return "强相关（高 beta 风险资产）"
	default:
		return "极强相关（与股市同步度高）"
	}
}

// ----------------- Flow 资金流 -----------------

func (c *Calculator) fillFlow(p *model.IndicatorPanel, snap *model.MarketSnapshot, ts string) {
	etfTS := sourceTimestamp(snap.ETFAsOf, ts)

	// 现货 BTC ETF 净流入
	if snap.ETFAsOf != "" || !snap.ETFNetFlow7dUSD.IsZero() || !snap.ETFNetFlow30dUSD.IsZero() {
		f7 := f(snap.ETFNetFlow7dUSD)
		f30 := f(snap.ETFNetFlow30dUSD)
		assets := f(snap.ETFTotalAssetUSD)

		note7 := "现货 BTC ETF 7 日净流入 (USD)。2024+ 主要边际驱动"
		if snap.ETFStaleDays >= 2 {
			note7 += fmt.Sprintf("（数据距今 %d 天）", snap.ETFStaleDays)
		}

		p.Flow["etf_net_flow_7d_usd"] = model.Indicator{
			Value:     f7,
			Label:     etfFlowLabel(f7, 7),
			Source:    "sosovalue",
			UpdatedAt: etfTS,
			Note:      note7,
		}
		p.Flow["etf_net_flow_30d_usd"] = model.Indicator{
			Value:     f30,
			Label:     etfFlowLabel(f30, 30),
			Source:    "sosovalue",
			UpdatedAt: etfTS,
			Note:      "30 日累计净流入。持续正流入 = 机构稳定接盘",
		}
		if assets > 0 {
			p.Flow["etf_total_assets_usd"] = model.Indicator{
				Value:     assets,
				Source:    "sosovalue",
				UpdatedAt: etfTS,
				Note:      "ETF 总持仓 USD。整体规模代表机构覆盖深度",
			}
		}
	}

	// Stablecoin 总市值（入库供后续计算 30d 增速）
	if snap.StablecoinMarketCapFetched || !snap.StablecoinMarketCap.IsZero() {
		scap := f(snap.StablecoinMarketCap)
		p.Flow["stablecoin_market_cap_usd"] = model.Indicator{
			Value:     scap,
			Source:    "coingecko",
			UpdatedAt: ts,
			Note:      "主要稳定币（USDT+USDC+DAI+FDUSD+FRAX）总市值。每日入库，攒够 30 天后回显 stablecoin_supply_30d_pct",
		}

		// 从 history.db 中查 30 天前的市值算出增速
		if c.History != nil {
			asOf := snap.Date.Format("2006-01-02")
			pastCap, pastOK := c.stablecoinMarketCapNDaysAgo(asOf, 30)
			if pastOK && pastCap > 0 {
				growth := (scap - pastCap) / pastCap * 100
				p.Flow["stablecoin_supply_30d_pct"] = model.Indicator{
					Value:     growth,
					Label:     stablecoinGrowthLabel(growth),
					Source:    "coingecko + history.db",
					UpdatedAt: ts,
					Note:      "稳定币总市值 30 日增速（基于 history.db 采集）。扩张 = 加密链上流动性进入",
				}
			}
		}
	}

	// ETH/BTC ratio（保留 — 资金风险偏好代理）
	if !snap.ETHPrice.IsZero() && !snap.BTCPrice.IsZero() {
		ratio := f(snap.ETHPrice) / f(snap.BTCPrice)
		p.Flow["eth_btc_ratio"] = model.Indicator{
			Value:     ratio,
			Quantile:  displayQuantile(ethBtcRatioQuantile(snap)),
			Label:     ethBtcLabel(ratio),
			Source:    "binance:ETHUSDT + " + btcDailyHistorySource,
			UpdatedAt: sourceTimestamp(snap.ETHPriceAsOf, sourceTimestamp(snap.BTCPriceAsOf, ts)),
			Note:      "ETH/BTC 比率。低位 = 资金避险偏 BTC，高位 = 风险偏好 ETH/Alt",
		}
	}
}

// ============== Helpers ==============

func f(d decimal.Decimal) float64 { v, _ := d.Float64(); return v }

func sourceTimestamp(asOf, fallback string) string {
	if asOf == "" {
		return fallback
	}
	if t, err := time.Parse("2006-01-02", asOf); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	if t, err := time.Parse(time.RFC3339, asOf); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	if t, err := time.Parse(time.RFC3339Nano, asOf); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return asOf
}

func addStaleWarning(p *model.IndicatorPanel, warning string) {
	if warning == "" {
		return
	}
	p.StaleWarnings = append(p.StaleWarnings, warning)
}

func dedupeStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// displayQuantile keeps the JSON contract strict: q is shown only when the
// value is a real historical percentile in [0,1]. Unknown quantiles use 0 so
// the existing omitempty tag suppresses q in JSON output.
func displayQuantile(q float64) float64 {
	if q < 0 || q > 1 || math.IsNaN(q) || math.IsInf(q, 0) {
		return 0
	}
	return q
}

// nearestHalvings 返回当前日期的前后减半事件
func nearestHalvings(now time.Time) (prev, next time.Time) {
	for i, h := range halvingDates {
		if h.After(now) {
			next = h
			if i > 0 {
				prev = halvingDates[i-1]
			}
			return
		}
	}
	if len(halvingDates) > 0 {
		prev = halvingDates[len(halvingDates)-1]
	}
	return
}

// halvingPhaseLabel 基于减半后天数的标签
func halvingPhaseLabel(daysSince int) string {
	switch {
	case daysSince < 180:
		return "halving 后早期 (0-6m)"
	case daysSince < 540:
		return "halving 后牛市期 (6-18m)"
	case daysSince < 900:
		return "顶部 / 分配期 (18-30m)"
	case daysSince < 1260:
		return "熊市 / 积累期 (30-42m)"
	default:
		return "halving 前期"
	}
}

// classifyCyclePhase 周期阶段启发式分类
func classifyCyclePhase(snap *model.MarketSnapshot, prevHalving time.Time) string {
	if prevHalving.IsZero() {
		return "unknown"
	}
	daysSince := int(snap.Date.Sub(prevHalving).Hours() / 24)

	// 200wSMA 偏离度作为辅助
	devLabel := ""
	if maW, ok := wmaSMA(snap.BTCPriceHistory, 200); ok && maW > 0 {
		dev := (f(snap.BTCPrice) - maW) / maW
		if dev > 1.5 {
			devLabel = "极高"
		} else if dev > 0.5 {
			devLabel = "高"
		} else if dev < -0.2 {
			devLabel = "极低"
		} else if dev < 0.2 {
			devLabel = "低"
		}
	}

	switch {
	case daysSince < 180 && (devLabel == "低" || devLabel == "极低"):
		return "early_post_halving"
	case daysSince < 540:
		return "markup"
	case daysSince < 900 && (devLabel == "高" || devLabel == "极高"):
		return "distribution_risk"
	case daysSince >= 540 && daysSince < 900:
		return "late_markup_or_top"
	case daysSince >= 900 && (devLabel == "低" || devLabel == "极低"):
		return "accumulation"
	default:
		return "transition"
	}
}

// smaFromHistory 简单移动平均（取最近 N 个）
func smaFromHistory(history []decimal.Decimal, n int) (float64, bool) {
	if len(history) < n {
		return 0, false
	}
	sum := 0.0
	for i := 0; i < n; i++ {
		sum += f(history[i])
	}
	return sum / float64(n), true
}

// wmaSMA 周线 SMA = 取 7N 日的算术均（近似 N 周 SMA）
func wmaSMA(history []decimal.Decimal, weeks int) (float64, bool) {
	return smaFromHistory(history, weeks*7)
}

// quantileOfDeviation 当前偏离值在过去 windowDays 偏离分布中的分位
func quantileOfDeviation(history []decimal.Decimal, windowDays int, currentDev float64) float64 {
	if len(history) < windowDays+200 {
		return -1
	}
	devs := make([]float64, 0, windowDays)
	for i := 0; i < windowDays && i+200 < len(history); i++ {
		// 滚动计算每天的 200dMA 偏离
		ma, ok := smaFromHistory(history[i:], 200)
		if !ok {
			continue
		}
		price := f(history[i])
		if ma > 0 {
			devs = append(devs, (price-ma)/ma)
		}
	}
	return quantileRank(devs, currentDev)
}

// quantileOfMayer 当前 Mayer 在过去分布中的分位
func quantileOfMayer(history []decimal.Decimal, ma int) float64 {
	if len(history) < ma+1000 {
		return -1
	}
	mayers := make([]float64, 0, 1000)
	for i := 0; i < 1000 && i+ma < len(history); i++ {
		m, ok := smaFromHistory(history[i:], ma)
		if !ok || m <= 0 {
			continue
		}
		mayers = append(mayers, f(history[i])/m)
	}
	current, ok := smaFromHistory(history, ma)
	if !ok || current <= 0 {
		return -1
	}
	return quantileRank(mayers, f(history[0])/current)
}

// ethBtcRatioQuantile
func ethBtcRatioQuantile(snap *model.MarketSnapshot) float64 {
	if len(snap.ETHPriceHistory) < 365 || len(snap.BTCPriceHistory) < 365 {
		return -1
	}
	ratios := make([]float64, 0, 365)
	for i := 0; i < 365; i++ {
		btc := f(snap.BTCPriceHistory[i])
		eth := f(snap.ETHPriceHistory[i])
		if btc > 0 {
			ratios = append(ratios, eth/btc)
		}
	}
	current := f(snap.ETHPrice) / f(snap.BTCPrice)
	return quantileRank(ratios, current)
}

// quantileRank 在 sorted-after-input distribution 里 v 的分位 [0, 1]
func quantileRank(samples []float64, v float64) float64 {
	if len(samples) == 0 || !isUsableFinite(v) {
		return -1
	}
	sorted := make([]float64, 0, len(samples))
	for _, sample := range samples {
		if isUsableFinite(sample) {
			sorted = append(sorted, sample)
		}
	}
	if len(sorted) == 0 {
		return -1
	}
	sort.Float64s(sorted)
	// 二分定位：<= v 的样本占比，与 history.Store.Quantile 语义一致。
	belowOrEqual := sort.Search(len(sorted), func(i int) bool { return sorted[i] > v })
	return float64(belowOrEqual) / float64(len(sorted))
}

// ----------------- Technical 技术指标 -----------------

func (c *Calculator) fillTechnical(p *model.IndicatorPanel, snap *model.MarketSnapshot, ts string) {
	btcTS := sourceTimestamp(snap.BTCPriceAsOf, ts)
	if len(snap.BTCPriceHistory) < 50 {
		return
	}
	price := f(snap.BTCPrice)

	// RSI(14)
	rsi14 := mathutil.CalculateRSI(snap.BTCPriceHistory, 14)
	p.Technical["rsi_14"] = model.Indicator{
		Value:     f(rsi14),
		Label:     rsiLabel(f(rsi14)),
		Source:    btcDailyHistorySource,
		UpdatedAt: btcTS,
		Note:      "RSI(14)。<30 超卖，>70 超买",
	}

	// MACD 柱状图
	macdHist := mathutil.CalculateMACD(snap.BTCPriceHistory)
	macdPrev := mathutil.CalculateMACD(snap.BTCPriceHistory[1:])
	p.Technical["macd_histogram"] = model.Indicator{
		Value:     f(macdHist),
		Label:     macdLabelFunc(f(macdHist), f(macdPrev)),
		Source:    btcDailyHistorySource,
		UpdatedAt: btcTS,
		Note:      "MACD 柱 (12,26,9)。>0 多头动能，<0 空头动能；柱收窄 = 趋势减弱/反转",
	}

	// EMA 交叉 (12/26)
	ema12 := mathutil.CalculateEMA(snap.BTCPriceHistory, 12)
	ema26 := mathutil.CalculateEMA(snap.BTCPriceHistory, 26)
	emaCross := f(ema12)/f(ema26) - 1
	p.Technical["ema_cross"] = model.Indicator{
		Value:     emaCross * 100,
		Label:     emaCrossLabel(emaCross),
		Source:    btcDailyHistorySource,
		UpdatedAt: btcTS,
		Note:      "(EMA12 - EMA26) / EMA26 %。>0 短期均线在上 = 多头排列，<0 = 空头",
	}

	// MA50 / MA200
	ma50, ok50 := smaFromHistory(snap.BTCPriceHistory, 50)
	ma200, ok200 := smaFromHistory(snap.BTCPriceHistory, 200)
	if ok50 && ok200 {
		p.Technical["ma_50"] = model.Indicator{
			Value:     ma50,
			Source:    btcDailyHistorySource,
			UpdatedAt: btcTS,
			Note:      "50 日简单移动均线",
		}
		p.Technical["ma_200"] = model.Indicator{
			Value:     ma200,
			Source:    btcDailyHistorySource,
			UpdatedAt: btcTS,
			Note:      "200 日简单移动均线 — 牛熊分界",
		}
		// MA50 vs MA200 排列
		p.Technical["ma_alignment"] = model.Indicator{
			Value:     (ma50 - ma200) / ma200 * 100,
			Label:     maAlignLabel(ma50, ma200),
			Source:    btcDailyHistorySource,
			UpdatedAt: btcTS,
			Note:      "(MA50 - MA200) / MA200 %。>0 = 金叉多头，<0 = 死叉空头",
		}
	}

	// Bollinger Bands
	ma20, ok20 := smaFromHistory(snap.BTCPriceHistory, 20)
	std20 := mathutil.CalculateStdDev(snap.BTCPriceHistory, 20)
	if ok20 && !std20.IsZero() {
		upper := ma20 + 2*f(std20)
		lower := ma20 - 2*f(std20)
		bbPos := (price - lower) / (upper - lower)
		p.Technical["bb_position"] = model.Indicator{
			Value:     bbPos,
			Label:     bbLabel(bbPos),
			Source:    btcDailyHistorySource,
			UpdatedAt: btcTS,
			Note:      fmt.Sprintf("Bollinger (20,2) 位置。0=下轨 %.0f, 1=上轨 %.0f", lower, upper),
		}
	}

	// 波动率 (20d)
	cv20 := f(std20) / ma20 * 100
	p.Technical["volatility_20d"] = model.Indicator{
		Value:     cv20,
		Label:     volLabel(cv20),
		Source:    btcDailyHistorySource,
		UpdatedAt: btcTS,
		Note:      "20 日波动率 (CV = std/mean %)。<2% 蓄势，>6% 高波动",
	}
}

// ----------------- CrossAsset 跨资产对比 -----------------

func (c *Calculator) fillCrossAsset(p *model.IndicatorPanel, snap *model.MarketSnapshot, ts string) {
	if !snap.CrossAssetFetched {
		return
	}
	btcTS := sourceTimestamp(snap.BTCPriceAsOf, ts)
	btc := f(snap.BTCPrice)

	// BTC / Gold
	if !snap.GoldPriceUSD.IsZero() {
		gold := f(snap.GoldPriceUSD)
		ratio := btc / gold
		p.CrossAsset["btc_gold_ratio"] = model.Indicator{
			Value:     ratio,
			Label:     btcGoldLabel(ratio),
			Source:    "yahoo",
			UpdatedAt: sourceTimestamp(snap.GoldPriceAsOf, btcTS),
			Note:      fmt.Sprintf("BTC / 黄金。BTC $%.0f / Gold $%.0f/oz。1 BTC = %.2f oz 黄金", btc, gold, ratio),
		}
		// BTC / oil proxy or WTI futures
		if !snap.WTIPrice.IsZero() {
			oil := f(snap.WTIPrice)
			if oil > 0 {
				oilRatio := btc / oil
				oilSource := oilSource(snap)
				p.CrossAsset["btc_oil_ratio"] = model.Indicator{
					Value:     oilRatio,
					Source:    oilSource,
					UpdatedAt: sourceTimestamp(snap.WTIPriceAsOf, btcTS),
					Note:      oilRatioNote(oilSource, btc, oil, oilRatio),
				}
			}
		}
	}

	// BTC / QQQ
	if !snap.QQQPrice.IsZero() {
		qqq := f(snap.QQQPrice)
		ratio := btc / qqq
		p.CrossAsset["btc_qqq_ratio"] = model.Indicator{
			Value:     ratio,
			Source:    "yahoo",
			UpdatedAt: sourceTimestamp(snap.QQQPriceAsOf, btcTS),
			Note:      fmt.Sprintf("BTC / QQQ。BTC $%.0f / QQQ $%.0f。1 BTC = %.2f 股 QQQ", btc, qqq, ratio),
		}
	}

	// BTC / SPY
	if !snap.SPYPrice.IsZero() {
		spy := f(snap.SPYPrice)
		ratio := btc / spy
		p.CrossAsset["btc_spy_ratio"] = model.Indicator{
			Value:     ratio,
			Source:    "yahoo",
			UpdatedAt: sourceTimestamp(snap.SPYPriceAsOf, btcTS),
			Note:      fmt.Sprintf("BTC / SPY。BTC $%.0f / SPY $%.0f。1 BTC = %.2f 股 SPY", btc, spy, ratio),
		}
	}

	// 30d 滚动相关性
	lookback := 30
	if len(snap.GoldHistory) >= lookback && len(snap.BTCPriceHistory) >= lookback {
		corr := rollingCorrelation(snap.BTCPriceHistory, snap.GoldHistory, lookback)
		p.CrossAsset["btc_gold_corr_30d"] = model.Indicator{
			Value:     corr,
			Label:     corrLabel(corr),
			Source:    "computed",
			UpdatedAt: ts,
			Note:      "BTC vs Gold 30 日对数收益率 Pearson 相关系数",
		}
	}
	// btc_qqq_corr_30d 已去重：SPY 与 QQQ 日收益相关 ~0.85-0.90，留 SPY 作为唯一股市相关性代理
	if len(snap.SPYHistory) >= lookback && len(snap.BTCPriceHistory) >= lookback {
		corr := rollingCorrelation(snap.BTCPriceHistory, snap.SPYHistory, lookback)
		p.CrossAsset["btc_spy_corr_30d"] = model.Indicator{
			Value:     corr,
			Label:     corrLabel(corr),
			Source:    "computed",
			UpdatedAt: ts,
			Note:      "BTC vs SPY 30 日对数收益率 Pearson 相关系数",
		}
	}

	// 90 日相对强弱（BTC vs 各资产）
	period := 90
	if len(snap.BTCPriceHistory) > period {
		btcRet := (btc - f(snap.BTCPriceHistory[period])) / f(snap.BTCPriceHistory[period]) * 100
		if len(snap.GoldHistory) > period && !snap.GoldPriceUSD.IsZero() {
			goldRet := (f(snap.GoldPriceUSD) - f(snap.GoldHistory[period])) / f(snap.GoldHistory[period]) * 100
			p.CrossAsset["rel_strength_90d_gold"] = model.Indicator{
				Value:     btcRet - goldRet,
				Label:     relStrengthLabel(btcRet - goldRet),
				Source:    "computed",
				UpdatedAt: ts,
				Note:      fmt.Sprintf("BTC 90d %.1f%% vs Gold 90d %.1f%%", btcRet, goldRet),
			}
		}
		// rel_strength_90d_qqq 已去重：与 SPY 信号几乎等同，留 gold 作为唯一跨资产相对强弱
	}

	// TLT — 20+ Year Treasury Bond ETF (long-end Treasury price proxy, inverse of 30Y yield)
	if !snap.TLTPrice.IsZero() {
		tltPrice := f(snap.TLTPrice)
		p.CrossAsset["tlt_price"] = model.Indicator{
			Value:     tltPrice,
			Label:     tltPriceLabel(tltPrice),
			Source:    "futu:US.TLT",
			UpdatedAt: sourceTimestamp(snap.TLTPriceAsOf, ts),
			Note:      "iShares 20+Y Treasury Bond ETF。长端美债价格代理，与 30Y 收益率反向。下跌 = 长端利率上行 = BTC 估值分母上移",
		}
		if len(snap.TLTHistory) > 60 {
			tlt60dAgo := f(snap.TLTHistory[60])
			if tlt60dAgo > 0 {
				tlt60dTrend := (tltPrice - tlt60dAgo) / tlt60dAgo * 100
				p.CrossAsset["tlt_60d_trend_pct"] = model.Indicator{
					Value:     tlt60dTrend,
					Label:     tltTrendLabel(tlt60dTrend),
					Source:    "futu:US.TLT",
					UpdatedAt: sourceTimestamp(snap.TLTPriceAsOf, ts),
					Note:      "TLT 60 日变化 %。<-5% 长端利率快速上行（紧缩 / 通胀反扑），>+5% 长端利率回落（衰退预期 / 政策转鸽）",
				}
			}
		}
	}
}

// rollingCorrelation 计算两个价格序列的对数收益率 Pearson 相关系数
func rollingCorrelation(a, b []decimal.Decimal, n int) float64 {
	if len(a) < n+1 || len(b) < n+1 {
		return 0
	}
	ra := make([]float64, n)
	rb := make([]float64, n)
	for i := 0; i < n; i++ {
		if a[i+1].IsZero() || b[i+1].IsZero() {
			continue
		}
		ra[i] = math.Log(f(a[i]) / f(a[i+1]))
		rb[i] = math.Log(f(b[i]) / f(b[i+1]))
	}
	return pearson(ra, rb)
}

func pearson(x, y []float64) float64 {
	n := len(x)
	if n < 2 {
		return 0
	}
	var sx, sy, sxy, sx2, sy2 float64
	for i := 0; i < n; i++ {
		sx += x[i]
		sy += y[i]
		sxy += x[i] * y[i]
		sx2 += x[i] * x[i]
		sy2 += y[i] * y[i]
	}
	den := math.Sqrt((float64(n)*sx2 - sx*sx) * (float64(n)*sy2 - sy*sy))
	if den == 0 {
		return 0
	}
	return (float64(n)*sxy - sx*sy) / den
}

// ============== Labels ==============
//
// 以下 label 函数的阈值基于 2013-2024 周期经验，仅用于人类速览。
// 2024 ETF 通过后波动结构可能已变 — label 不可作为决策依据。
// Claude 决策时应以 q（历史分位）为准，label 只作辅助提示。

func devLabel(dev float64) string {
	switch {
	case dev < -0.3:
		return "深度低估（200wSMA 之下）"
	case dev < 0:
		return "低于 200wSMA"
	case dev < 0.5:
		return "正常区"
	case dev < 1.5:
		return "偏高"
	default:
		return "极端高估"
	}
}

func mayerLabel(m float64) string {
	switch {
	case m < 0.8:
		return "深度低估"
	case m < 1.0:
		return "偏低估"
	case m < 1.5:
		return "中性"
	case m < 2.4:
		return "偏高估"
	default:
		return "顶部区"
	}
}

func piCycleLabel(ratio float64) string {
	switch {
	case ratio >= 1.0:
		return "🔴 Pi Cycle Top 触发"
	case ratio > 0.85:
		return "接近触发"
	default:
		return "未触发"
	}
}

func ahrLabel(v float64) string {
	switch {
	case v < 0.45:
		return "极端低估区（历史底部）"
	case v < 0.8:
		return "低估区"
	case v < 1.2:
		return "合理"
	case v < 2.0:
		return "高估"
	default:
		return "泡沫"
	}
}

// detectAhrDivergence 检测原始 AHR999（固定公式）与自适应百分位的方向分歧。
// 回测验证：分歧 = 市场转向预警，历史 -34%~-53% fwd180, 0% 胜率。
//
// 三种分歧模式：
//  1. 原始便宜 (<0.8) + 自适应偏贵 (q>55%) → 下跌中继风险
//  2. 原始贵 (>1.2) + 自适应偏低 (q<35%) → 熊市初期反弹陷阱
//  3. 原始泡沫 (>2.0) + 自适应不极端 (q<50%) → 最危险，历史 -53%
func detectAhrDivergence(origRaw float64, adaptiveQ float64) string {
	switch {
	case origRaw < 0.45 && adaptiveQ > 0.35:
		return "⚠️ 原始极端低估 + 自适应中性偏高 — 可能下跌中继，低估信号未充分确认"
	case origRaw < 0.8 && adaptiveQ > 0.55:
		return "⚠️ 原始低估 + 自适应偏贵 — 价格虽低但百分位偏高，可能尚未到底"
	case origRaw > 1.2 && adaptiveQ < 0.35:
		return "⚠️ 原始偏贵 + 自适应偏低 — 熊市初期反弹陷阱，反弹质量偏弱"
	case origRaw > 2.0 && adaptiveQ < 0.50:
		return "🔴 原始泡沫 + 自适应不极端 — 最危险分歧，历史 fwd180 -53%，0% 胜率"
	default:
		return "" // 无分歧
	}
}

func mvrvLabel(v float64) string {
	switch {
	case v < 0.8:
		return "历史底部区"
	case v < 1.2:
		return "低估"
	case v < 2.0:
		return "中性"
	case v < 3.5:
		return "偏高"
	default:
		return "过热 / 顶部风险"
	}
}

func mvrvZLabel(v float64) string {
	switch {
	case v < 0:
		return "历史底部区"
	case v < 2:
		return "中性偏低"
	case v < 5:
		return "偏高"
	case v < 7:
		return "高位"
	default:
		return "顶部风险"
	}
}

func nuplLabel(v float64) string {
	switch {
	case v < 0:
		return "capitulation"
	case v < 0.25:
		return "hope / fear"
	case v < 0.5:
		return "optimism"
	case v < 0.75:
		return "belief"
	default:
		return "euphoria"
	}
}

// dvolLabel — DVOL 是 BTC IV 指数，年化 %
func dvolLabel(d float64) string {
	switch {
	case d < 30:
		return "极平静（市场自满 / 警惕黑天鹅）"
	case d < 45:
		return "平静"
	case d < 60:
		return "正常"
	case d < 80:
		return "偏高（紧张）"
	default:
		return "高度恐慌"
	}
}

// skew25Label — 25-delta skew (pp)。>0 = put 比 call 贵 = 下行对冲贵 = 恐慌
func skew25Label(s float64) string {
	switch {
	case s < -3:
		return "极度看涨偏 (call 贵 — 顶部区典型)"
	case s < 0:
		return "偏看涨"
	case s < 3:
		return "中性"
	case s < 8:
		return "偏看跌（防守需求）"
	default:
		return "极度看跌偏 (put 贵 — 底部前夕典型)"
	}
}

// priceToRealizedLabel — 价格相对持币者平均成本的偏离
// <0 历史低位；0-50 低估；50-150 中性；>150 顶部区
func priceToRealizedLabel(devPct float64) string {
	switch {
	case devPct < 0:
		return "持币者整体亏损（历史级低位）"
	case devPct < 50:
		return "低估"
	case devPct < 150:
		return "中性"
	case devPct < 300:
		return "高估"
	default:
		return "极端高估（顶部区）"
	}
}

func fundingRateLabel(fr float64) string {
	switch {
	case fr < 0:
		return "负值（多头不愿付，潜在反转）"
	case fr < 0.0001:
		return "极低"
	case fr < 0.0003:
		return "正常"
	case fr < 0.0005:
		return "偏高"
	default:
		return "过热（清算风险）"
	}
}

func oiLabel(r float64) string {
	switch {
	case r < 0.015:
		return "杠杆松弛"
	case r < 0.025:
		return "中性"
	case r < 0.035:
		return "偏拥挤"
	default:
		return "杠杆拥挤（清算风险）"
	}
}

func fearGreedLabel(fg float64) string {
	switch {
	case fg < 20:
		return "极度恐慌（历史低位常见）"
	case fg < 40:
		return "恐慌"
	case fg < 60:
		return "中性"
	case fg < 80:
		return "贪婪"
	default:
		return "极度贪婪（顶部信号）"
	}
}

func stablecoinGrowthLabel(g float64) string {
	switch {
	case g < -3:
		return "收缩（流动性退潮）"
	case g < 1:
		return "停滞"
	case g < 5:
		return "温和扩张"
	default:
		return "强劲扩张（流动性宽松）"
	}
}

func etfFlowLabel(usd float64, days int) string {
	// 阈值按 7d 校准：>1B 强劲，>0 温和正，<-1B 大幅流出
	switch d := float64(days); {
	case usd < -1e9*d/7:
		return "大幅流出（机构减持）"
	case usd < -3e8*d/7:
		return "持续流出"
	case usd < 0:
		return "小幅流出"
	case usd < 5e8*d/7:
		return "微弱流入"
	case usd < 2e9*d/7:
		return "持续流入"
	default:
		return "强劲流入（机构 FOMO）"
	}
}

func hashRateLabel(eh float64) string {
	switch {
	case eh < 200:
		return "历史低位"
	case eh < 400:
		return "正常"
	case eh < 700:
		return "高位"
	default:
		return "历史峰值区"
	}
}

func difficultyLabel(d float64) string {
	switch {
	case d < -5:
		return "大幅下调（矿工投降信号 ⚠）"
	case d < 0:
		return "小幅下调"
	case d < 3:
		return "持平"
	case d < 8:
		return "上调（算力扩张）"
	default:
		return "大幅上调（FOMO 入场）"
	}
}

func mempoolLabel(mb float64) string {
	switch {
	case mb < 5:
		return "畅通"
	case mb < 30:
		return "正常"
	case mb < 100:
		return "拥堵"
	default:
		return "极度拥堵（链上活跃高峰）"
	}
}

func altcoinSeasonLabel(v float64) string {
	switch {
	case v >= 75:
		return "山寨季（资金溢出 BTC → Alt）"
	case v >= 50:
		return "偏山寨季"
	case v >= 25:
		return "偏 BTC 季"
	default:
		return "BTC 季（资金集中 BTC）"
	}
}

func ethBtcLabel(r float64) string {
	switch {
	case r < 0.030:
		return "ETH 极弱（避险偏 BTC）"
	case r < 0.045:
		return "ETH 弱"
	case r < 0.06:
		return "中性"
	case r < 0.075:
		return "ETH 偏强"
	default:
		return "ETH 极强（风险偏好高）"
	}
}

// ============== Technical Labels ==============

func rsiLabel(v float64) string {
	switch {
	case v < 20:
		return "极度超卖"
	case v < 30:
		return "超卖"
	case v < 45:
		return "偏弱"
	case v < 55:
		return "中性"
	case v < 70:
		return "偏强"
	case v < 80:
		return "超买"
	default:
		return "极度超买"
	}
}

func macdLabelFunc(hist, prev float64) string {
	if hist > 0 {
		if hist > prev {
			return "多头增强"
		}
		return "多头减弱（可能见顶）"
	}
	if hist > prev {
		return "空头收窄（底部反转信号）"
	}
	return "空头增强"
}

func emaCrossLabel(v float64) string {
	switch {
	case v > 3:
		return "强多头排列"
	case v > 0:
		return "多头排列"
	case v > -3:
		return "空头排列"
	default:
		return "强空头排列"
	}
}

func maAlignLabel(ma50, ma200 float64) string {
	if ma50 > ma200 {
		return "金叉（多头）"
	}
	return "死叉（空头）"
}

func bbLabel(pos float64) string {
	switch {
	case pos < 0:
		return "跌破下轨（超卖/极端）"
	case pos < 0.2:
		return "接近下轨"
	case pos < 0.8:
		return "区间中部"
	case pos < 1:
		return "接近上轨"
	default:
		return "突破上轨（超买/极端）"
	}
}

func volLabel(cv float64) string {
	switch {
	case cv < 2:
		return "极低波动（蓄势）"
	case cv < 4:
		return "低波动"
	case cv < 6:
		return "正常"
	case cv < 10:
		return "高波动"
	default:
		return "极高波动（恐慌/狂热）"
	}
}

// ============== Cross-Asset Labels ==============

func btcGoldLabel(ratio float64) string {
	switch {
	case ratio < 5:
		return "BTC 相对黄金极弱"
	case ratio < 15:
		return "BTC 相对黄金偏弱"
	case ratio < 25:
		return "中性"
	case ratio < 40:
		return "BTC 相对黄金偏强"
	default:
		return "BTC 相对黄金极强（历史峰值区）"
	}
}

func corrLabel(c float64) string {
	switch {
	case c < -0.3:
		return "负相关（独立行情）"
	case c < 0.2:
		return "弱相关"
	case c < 0.5:
		return "中等相关"
	case c < 0.7:
		return "强相关"
	default:
		return "极强相关（同涨同跌）"
	}
}

func relStrengthLabel(diff float64) string {
	switch {
	case diff > 20:
		return "BTC 大幅跑赢"
	case diff > 5:
		return "BTC 跑赢"
	case diff > -5:
		return "持平"
	case diff > -20:
		return "BTC 跑输"
	default:
		return "BTC 大幅跑输"
	}
}

// --- TLT (long-end Treasury bond ETF) labels ---
//
// 历史区间参考（2010-2025）：COVID 高点 ~170（极低利率），2022-23 紧缩低点 ~80（30Y 收益率冲到 ~5%）。
// TLT 与 30Y 收益率反向：TLT 跌 = 长端利率上行 = BTC 估值分母上移 = 风险资产逆风。

func tltPriceLabel(price float64) string {
	switch {
	case price < 85:
		return "极低（长端利率高位）"
	case price < 95:
		return "偏低（长端利率偏高）"
	case price < 110:
		return "中性"
	case price < 130:
		return "偏高（长端利率偏低）"
	default:
		return "极高（长端利率深度低位）"
	}
}

func tltTrendLabel(pct float64) string {
	switch {
	case pct < -10:
		return "暴跌（长端利率冲击 / 紧缩）"
	case pct < -5:
		return "下跌（利率走高）"
	case pct < 5:
		return "横盘"
	case pct < 10:
		return "上涨（利率回落）"
	default:
		return "暴涨（衰退避险 / 政策转鸽）"
	}
}

// --- Oil proxy / WTI labels ---

func oilSource(snap *model.MarketSnapshot) string {
	if snap.OilPriceSource != "" {
		return snap.OilPriceSource
	}
	if !snap.WTIPrice.IsZero() {
		return "unknown:oil"
	}
	return ""
}

func oilIndicatorKeys(source string) (priceKey, trendKey string) {
	if source == "yahoo:CL=F" {
		return "wti_crude_usd", "wti_crude_60d_trend_pct"
	}
	return "oil_proxy_usd", "oil_proxy_60d_trend_pct"
}

func oilPriceLabel(price float64) string {
	switch {
	case price < 50:
		return "极低（通缩压力 / 需求崩溃）"
	case price < 65:
		return "偏低"
	case price < 80:
		return "正常"
	case price < 95:
		return "偏高（通胀压力上升）"
	case price < 110:
		return "高位（成本推动型通胀 → BTC 不确定）"
	default:
		return "极端高位（供给冲击 → Fed 困境 → BTC 严重逆风）"
	}
}

func oilTrendLabel(pct float64) string {
	switch {
	case pct < -20:
		return "暴跌（需求崩塌）"
	case pct < -5:
		return "下行"
	case pct < 5:
		return "横盘"
	case pct < 20:
		return "上行（通胀压力↑）"
	default:
		return "飙升（供给冲击风险）"
	}
}

func oilPriceNote(source string) string {
	switch source {
	case "yahoo:CL=F":
		return "WTI 近月原油期货价格 ($/桶)。>100 成本推动型通胀 → Fed 困境 → BTC 不确定性"
	case "futu:US.USO":
		return "USO 原油 ETF 价格，作为油价流动性 proxy；不是 $/桶 WTI，不能按桶数解释"
	default:
		return "油价或油价 proxy。来源未知时只用于趋势参考，不能按 WTI 桶价解释"
	}
}

func oilTrendNote(source string) string {
	switch source {
	case "yahoo:CL=F":
		return "WTI 近月原油期货 60 日变化 %。>20% = 供给冲击风险；<-20% = 需求崩溃"
	case "futu:US.USO":
		return "USO 原油 ETF 60 日变化 %，作为油价 proxy 趋势；避免和 WTI 桶价混用"
	default:
		return "油价或油价 proxy 60 日变化 %。来源未知时只用于方向参考"
	}
}

func oilRatioNote(source string, btc, oil, ratio float64) string {
	switch source {
	case "yahoo:CL=F":
		return fmt.Sprintf("BTC / WTI。BTC $%.0f / WTI $%.0f。1 BTC = %.0f 桶 WTI 原油", btc, oil, ratio)
	case "futu:US.USO":
		return fmt.Sprintf("BTC / USO。BTC $%.0f / USO $%.0f。1 BTC = %.0f 份 USO ETF（油价 proxy，不是桶数）", btc, oil, ratio)
	default:
		return fmt.Sprintf("BTC / oil proxy。BTC $%.0f / proxy $%.0f。1 BTC = %.0f proxy units", btc, oil, ratio)
	}
}

// --- Credit / Yield Curve labels ---

func hySpreadLabel(bps float64) string {
	switch {
	case bps < 300:
		return "极低（信用市场自满 / 狂热）"
	case bps < 400:
		return "偏低（风险偏好正常）"
	case bps < 500:
		return "正常"
	case bps < 650:
		return "偏高（信用紧缩）"
	case bps < 850:
		return "高位（严重信用紧缩 — 类似 2016/2020）"
	default:
		return "极端（信用危机 — 类似 2008/2020-03）"
	}
}

func yieldCurveLabel(bps float64) string {
	switch {
	case bps < -50:
		return "深度倒挂（强烈衰退预警）"
	case bps < 0:
		return "倒挂（衰退预警）"
	case bps < 50:
		return "平坦（经济过渡期）"
	case bps < 150:
		return "正常陡峭"
	default:
		return "极陡（复苏初期 / 通胀预期高）"
	}
}

// 防止 mathutil 没用到的编译警告

// ── 3D Score 辅助标签 ──

func d3ValLabel(v float64) string {
	switch {
	case v < 0.3:
		return "极端便宜"
	case v < 0.5:
		return "很便宜"
	case v < 0.8:
		return "偏便宜"
	case v < 1.5:
		return "合理偏高"
	case v < 3.0:
		return "偏贵"
	default:
		return "昂贵"
	}
}

func d3MayerLabel(m float64) string {
	switch {
	case m < 0.6:
		return "深度低估（极端超卖）"
	case m < 1.0:
		return "均线下方（偏弱）"
	case m < 1.5:
		return "均线上方（偏强）"
	case m < 2.4:
		return "偏高（超买风险）"
	default:
		return "极端高位（顶部风险）"
	}
}

// ── US Liquidity labels ──

func liqTrendLabel(pct float64) string {
	switch {
	case pct < -10:
		return "大幅收缩（流动性急剧恶化）"
	case pct < -3:
		return "收缩（流动性偏紧）"
	case pct < 3:
		return "横盘"
	case pct < 10:
		return "扩张（流动性改善）"
	default:
		return "大幅扩张（流动性宽裕）"
	}
}

func rrpLabel(v float64) string {
	switch {
	case v < 10:
		return "耗尽（资金已全流入市场 — 2023-24 牛市关键推力）"
	case v < 200:
		return "低位（蓄水池近干）"
	case v < 500:
		return "正常"
	case v < 1000:
		return "偏高（大量资金锁在 Fed）"
	case v < 2000:
		return "高位（类似 2022 紧缩高峰）"
	default:
		return "极高（历史性紧缩蓄水）"
	}
}

func tgaLabel(v float64) string {
	switch {
	case v < 100:
		return "极低（债务上限约束，政府快没钱 — 短期顺风但风险积累）"
	case v < 300:
		return "偏低（政府花钱释放流动性）"
	case v < 600:
		return "正常（债务上限后重建中）"
	case v < 900:
		return "偏高（财政部回收流动性 — 逆风）"
	default:
		return "极高（大量抽水回 TGA — 类似 2021 债务上限后）"
	}
}

func netLiqLabel(v float64) string {
	switch {
	case v < 4000:
		return "极低（类似 2019 回购危机前 — 高波动预警）"
	case v < 5000:
		return "偏低（流动性紧张）"
	case v < 6000:
		return "正常偏紧"
	case v < 7000:
		return "正常"
	case v < 8000:
		return "宽裕（风险资产顺风）"
	default:
		return "极度宽裕（类似 2020-21 QE 高峰）"
	}
}

func d3PanicLabel(dd float64) string {
	switch {
	case dd < -0.40:
		return "深度恐慌（>40%回撤）"
	case dd < -0.20:
		return "恐慌状态（>20%回撤）"
	case dd < -0.10:
		return "小幅回调（>10%回撤）"
	default:
		return "无恐慌（横盘或上涨）"
	}
}

// ── Miner Cost Floor labels ──

func minerCostRatioLabel(ratio float64) string {
	switch {
	case ratio < 0.8:
		return "矿工深度亏损（历史级低位，仅 2018/2020/2022 出现）"
	case ratio < 1.0:
		return "矿工亏损（成本支撑区，历史低位常见）"
	case ratio < 1.3:
		return "矿工微利（正常熊市/积累区）"
	case ratio < 2.0:
		return "矿工盈利（牛市区间）"
	case ratio < 3.0:
		return "矿工高盈利（牛市加速期）"
	default:
		return "矿工暴利（极端高估，顶部常见）"
	}
}

func difficultyCostLabel(dc float64) string {
	switch {
	case dc < -7:
		return "矿工投降（难度骤降，底部信号）"
	case dc < -5:
		return "矿工承压（成本线附近）"
	case dc < -2:
		return "边际矿工退出"
	case dc < 0:
		return "难度持平/微降"
	case dc < 5:
		return "难度温和上升（算力扩张）"
	default:
		return "难度快速上升（算力 FOMO）"
	}
}
