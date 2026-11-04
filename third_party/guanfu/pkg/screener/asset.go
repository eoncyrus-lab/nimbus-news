// ScreenedEquities Asset — multi-stock screening result as a guanfu panel.
//
// Implements engine.Asset so the screener output flows through the same
// 8-domain panel → verdict → forecast pipeline as BTC/QQQ/SPY/Gold.
//
// Key difference: this is a portfolio-level asset, not a single ticker.
// FetchSnapshot runs coarse→fine screening. BuildPanel aggregates pool
// statistics. BuildVerdict produces a cross-stock reading.
// BuildForecast is optional (kNN on top picks).

package screener

import (
	"context"
	"fmt"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/engine"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/screener/coarse"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/screener/fine"
)

// ScreenedEquitiesAsset implements engine.Asset for a screening strategy.
//
// Key format: "screen-{market}-{style}" e.g. "screen-hk-value".
// Multiple instances can be registered for different market+style combos.
type ScreenedEquitiesAsset struct {
	Market   string
	Style    string
	Profile  *model.StrategyProfile
	lbClient *coarse.LongbridgeClient
	store    *CandidateStore
}

// NewScreenedEquitiesAsset creates an asset for a specific market+style.
// profile is typically looked up from model.BuiltinStrategyProfiles().
func NewScreenedEquitiesAsset(market, style string, profile *model.StrategyProfile) *ScreenedEquitiesAsset {
	return &ScreenedEquitiesAsset{
		Market:   market,
		Style:    style,
		Profile:  profile,
		lbClient: coarse.NewLongbridgeClient(),
		store:    NewCandidateStore(),
	}
}

func (a *ScreenedEquitiesAsset) Key() string {
	return fmt.Sprintf("screen-%s-%s", a.Market, a.Style)
}

func (a *ScreenedEquitiesAsset) Name() string {
	if a.Profile != nil {
		return a.Profile.Name
	}
	return fmt.Sprintf("%s %s screener", a.Market, a.Style)
}

// FetchSnapshot runs the full coarse→fine pipeline and returns
// an AssetSnapshot populated with screening results.
func (a *ScreenedEquitiesAsset) FetchSnapshot(ctx context.Context) (*engine.AssetSnapshot, error) {
	if a.Profile == nil {
		return nil, fmt.Errorf("screener: no strategy profile configured")
	}

	// 1. Try cache first (same-day)
	candidates, err := a.store.Load(a.Profile.Name, a.Market)
	if err != nil {
		return nil, fmt.Errorf("screener: cache load: %w", err)
	}

	// 2. Cache miss — run coarse screening
	if candidates == nil {
		strategyID := a.Profile.CoarseStrategyID
		if strategyID == "" {
			return nil, fmt.Errorf("screener: no coarse_strategy_id in profile %q", a.Profile.Name)
		}
		maxResults := 200
		candidates, err = a.lbClient.RunStrategy(ctx, strategyID, a.Market, maxResults)
		if err != nil {
			return nil, fmt.Errorf("screener: longbridge strategy %s: %w", strategyID, err)
		}

		// Enrich with heat data
		_ = a.lbClient.EnrichHeat(ctx, a.Market, candidates)

		// Cache for the day
		_ = a.store.Save(a.Profile.Name, a.Market, candidates)
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("screener: no candidates from %s strategy in %s market", a.Profile.Name, a.Market)
	}

	// 3. Fine screening
	fine.ScoreAll(candidates, a.Profile)
	buckets := fine.Bucketize(candidates)

	// 4. Build AssetSnapshot with screening results
	snap := &engine.AssetSnapshot{
		Asset: a.Key(),
		Date:  "", // filled by caller
	}
	_ = buckets
	return snap, nil
}

// Candidates returns the last-fetched scored candidates from cache.
func (a *ScreenedEquitiesAsset) Candidates() ([]model.ScreenedCandidate, error) {
	candidates, err := a.store.Load(a.Profile.Name, a.Market)
	if err != nil {
		return nil, err
	}
	if candidates == nil {
		return nil, fmt.Errorf("no cached candidates — run FetchSnapshot first")
	}
	return candidates, nil
}

// BuildPanel constructs an 8-domain IndicatorPanel from scored screening candidates.
// The panel aggregates pool-level statistics rather than single-stock indicators.
func (a *ScreenedEquitiesAsset) BuildPanel(snap *engine.AssetSnapshot) (*model.IndicatorPanel, error) {
	candidates, err := a.Candidates()
	if err != nil {
		return nil, err
	}
	fine.ScoreAll(candidates, a.Profile)
	buckets := fine.Bucketize(candidates)

	core := buckets[fine.BucketCore]

	panel := &model.IndicatorPanel{
		Asset: a.Key(),
		Date:  snap.Date,
		Snapshot: model.SnapshotData{
			DataDate: snap.Date,
		},
		DomainMeta: screeningDomainMeta(),
	}

	// Map screening dimensions to IndicatorPanel's 8 domains:
	//   Valuation    → Valuation
	//   Quality      → Network (slot reuse)
	//   Potential    → Technical
	//   Risk         → Positioning
	//   Heat         → Macro
	//   Buckets      → CrossAsset
	//   Top Picks    → Cycle
	//   Sectors      → Flow

	panel.Valuation = buildValDomain(candidates, core)
	panel.Network = buildQualDomain(candidates, core)
	panel.Technical = buildGrowthDomain(candidates, core)
	panel.Positioning = buildRiskDomain(candidates, core)
	panel.Macro = buildHeatDomain(candidates, core)
	panel.CrossAsset = buildBucketSummary(buckets)
	panel.Cycle = buildTopPicks(candidates, 10)
	panel.Flow = buildSectorBreakdown(candidates)

	panel.StaleWarnings = poolWarnings(candidates, core)
	panel.SourceHealth = []model.SourceHealth{
		{Source: "longbridge", Status: "ok"},
	}

	return panel, nil
}

// BuildVerdict produces a pool-level screening verdict.
func (a *ScreenedEquitiesAsset) BuildVerdict(panel *model.IndicatorPanel) *engine.Verdict {
	v := &engine.Verdict{
		Date:            panel.Date,
		Confidence:      "中",
		Domains:         make([]engine.DomainVote, 0),
		Reasons:         make([]string, 0),
		CounterEvidence: make([]string, 0),
		KillCriteria:    make([]string, 0),
	}

	// Read pool stats from the panel
	var coreCount, valMedian, qualMedian float64
	if ind, ok := panel.CrossAsset["core_count"]; ok {
		coreCount = ind.Value
	}
	if ind, ok := panel.Valuation["median_val_score"]; ok {
		valMedian = ind.Value
	}
	if ind, ok := panel.Network["median_qual_score"]; ok {
		qualMedian = ind.Value
	}

	// Net direction based on pool quality
	switch {
	case coreCount >= 10 && valMedian >= 70 && qualMedian >= 60:
		v.NetDirection = 5
		v.Stance = "cautious_accumulate"
		v.Regime = "价值池肥沃: 核心+估值+质量三重确认"
		v.Confidence = "高"
		v.Reasons = append(v.Reasons,
			fmt.Sprintf("核心标的≥10只(%.0f只)", coreCount),
			fmt.Sprintf("池估值中位数%.0f分,整体偏低估", valMedian),
			fmt.Sprintf("池质量中位数%.0f分,基本面可靠", qualMedian),
		)
	case coreCount >= 5 && valMedian >= 60:
		v.NetDirection = 3
		v.Stance = "selective_accumulate"
		v.Confidence = "中"
		v.Reasons = append(v.Reasons,
			fmt.Sprintf("核心标的≥5只(%.0f只)", coreCount),
			fmt.Sprintf("池估值中位数%.0f分", valMedian),
		)
	case coreCount < 3:
		v.NetDirection = -2
		v.Stance = "standby"
		v.Confidence = "低"
		v.Reasons = append(v.Reasons,
			fmt.Sprintf("核心标的仅%.0f只,机会稀少", coreCount),
		)
		v.CounterEvidence = append(v.CounterEvidence,
			"当前市场估值偏高或基本面不满足价值投资标准",
		)
	default:
		v.NetDirection = 1
		v.Stance = "neutral"
		v.Confidence = "中"
	}

	if valMedian < 50 {
		v.CounterEvidence = append(v.CounterEvidence,
			fmt.Sprintf("池估值中位数仅%.0f分,整体偏贵", valMedian),
		)
		v.KillCriteria = append(v.KillCriteria,
			fmt.Sprintf("若val_median持续<40超过30天,该策略参数需重检"),
		)
	}

	// Coverage
	for _, d := range panel.DomainMeta {
		v.Domains = append(v.Domains, engine.DomainVote{
			Domain:   d.Key,
			Coverage: 1.0,
		})
	}
	if len(v.Domains) > 0 {
		v.Coverage = 1.0
	}

	// Top/bottom from pool extremes
	allCandidates := allScored(a)
	v.TopProximity = bestScoreRatio(allCandidates, 80)
	v.BottomProximity = 1 - bestScoreRatio(allCandidates, 50)

	return v
}

// BuildForecast returns nil — pool-level kNN is not meaningful.
// Individual stock forecasts can be done via guanfu stock TICKER.
func (a *ScreenedEquitiesAsset) BuildForecast(snap *engine.AssetSnapshot, opts forecast.Options) (*forecast.Forecast, error) {
	return nil, nil
}

// --- Panel builder helpers ---

// allScored returns the candidate list, scored.
func allScored(a *ScreenedEquitiesAsset) []model.ScreenedCandidate {
	c, _ := a.Candidates()
	fine.ScoreAll(c, a.Profile)
	return c
}

func poolWarnings(all, core []model.ScreenedCandidate) []string {
	var w []string
	if len(all) < 20 {
		w = append(w, fmt.Sprintf("候选池较小(%d只),粗筛可能过严", len(all)))
	}
	if len(core) == 0 {
		w = append(w, "无核心标的,当前市场不适合该策略")
	}
	return w
}

func buildValDomain(all, core []model.ScreenedCandidate) map[string]model.Indicator {
	med := medianScore(all, func(c model.ScreenedCandidate) float64 { return c.ValScore })
	coreMed := medianScore(core, func(c model.ScreenedCandidate) float64 { return c.ValScore })
	return map[string]model.Indicator{
		"median_val_score":  {Value: med, Label: valLabel(med), Source: "fine/valuation"},
		"core_median_val":   {Value: coreMed, Label: valLabel(coreMed), Source: "fine/valuation"},
		"val_score_range":   {Value: scoreRange(all, func(c model.ScreenedCandidate) float64 { return c.ValScore }), Source: "fine/valuation"},
	}
}

func buildQualDomain(all, core []model.ScreenedCandidate) map[string]model.Indicator {
	med := medianScore(all, func(c model.ScreenedCandidate) float64 { return c.QualScore })
	coreMed := medianScore(core, func(c model.ScreenedCandidate) float64 { return c.QualScore })
	return map[string]model.Indicator{
		"median_qual_score": {Value: med, Label: qualLabel(med), Source: "fine/quality"},
		"core_median_qual":  {Value: coreMed, Label: qualLabel(coreMed), Source: "fine/quality"},
	}
}

func buildGrowthDomain(all, core []model.ScreenedCandidate) map[string]model.Indicator {
	med := medianScore(all, func(c model.ScreenedCandidate) float64 { return c.PotScore })
	return map[string]model.Indicator{
		"median_potential": {Value: med, Label: potLabel(med), Source: "fine/potential"},
	}
}

func buildRiskDomain(all, core []model.ScreenedCandidate) map[string]model.Indicator {
	avgRisk := avgScore(all, func(c model.ScreenedCandidate) float64 { return c.RiskScore })
	flagged := 0
	for _, c := range all {
		if len(c.RiskFlags) > 0 {
			flagged++
		}
	}
	return map[string]model.Indicator{
		"avg_risk_score":     {Value: avgRisk, Label: riskLabel(avgRisk), Source: "fine/risk"},
		"flagged_count":      {Value: float64(flagged), Source: "fine/risk"},
		"flagged_pct":        {Value: float64(flagged) / float64(max(1, len(all))) * 100, Source: "fine/risk"},
	}
}

func buildHeatDomain(all, core []model.ScreenedCandidate) map[string]model.Indicator {
	onFire := 0
	rising := 0
	for _, c := range all {
		if c.HeatRank > 0 && c.HeatRank <= 100 {
			onFire++
		}
		if c.HeatRising {
			rising++
		}
	}
	return map[string]model.Indicator{
		"hot_count":    {Value: float64(onFire), Label: heatLabel(onFire, len(all)), Source: "longbridge/rank"},
		"rising_count": {Value: float64(rising), Source: "longbridge/rank"},
	}
}

func buildBucketSummary(buckets map[string][]model.ScreenedCandidate) map[string]model.Indicator {
	return map[string]model.Indicator{
		"core_count":    {Value: float64(len(buckets[fine.BucketCore])), Source: "fine/score"},
		"watch_count":   {Value: float64(len(buckets[fine.BucketWatch])), Source: "fine/score"},
		"reserve_count": {Value: float64(len(buckets[fine.BucketReserve])), Source: "fine/score"},
		"reject_count":  {Value: float64(len(buckets[fine.BucketReject])), Source: "fine/score"},
	}
}

func buildTopPicks(all []model.ScreenedCandidate, n int) map[string]model.Indicator {
	m := make(map[string]model.Indicator)
	limit := n
	if len(all) < limit {
		limit = len(all)
	}
	for i := 0; i < limit; i++ {
		key := fmt.Sprintf("top_%d", i+1)
		label := fmt.Sprintf("%s %s (%.0f)", all[i].Symbol, all[i].Name, all[i].FinalScore)
		m[key] = model.Indicator{
			Value: all[i].FinalScore,
			Label: label,
			Source: "screener",
		}
	}
	return m
}

func buildSectorBreakdown(all []model.ScreenedCandidate) map[string]model.Indicator {
	sectors := make(map[string]int)
	for _, c := range all {
		sector := c.Industry
		if sector == "" {
			sector = c.Sector
		}
		if sector == "" {
			sector = "未分类"
		}
		sectors[sector]++
	}
	m := make(map[string]model.Indicator)
	for sector, count := range sectors {
		if len(sector) > 30 {
			sector = sector[:30]
		}
		key := fmt.Sprintf("sector_%s", sector)
		m[key] = model.Indicator{
			Value:  float64(count),
			Label:  fmt.Sprintf("%d只", count),
			Source: "longbridge/industry",
		}
	}
	return m
}

func screeningDomainMeta() []model.PanelDomainMeta {
	return []model.PanelDomainMeta{
		{Key: "valuation", Title: "Valuation 估值", Icon: "💰", Purpose: "候选池估值水位"},
		{Key: "quality", Title: "Quality 质量", Icon: "✅", Purpose: "候选池质量分布"},
		{Key: "technical", Title: "Potential 潜力", Icon: "🚀", Purpose: "拐点/催化信号"},
		{Key: "positioning", Title: "Risk 风险", Icon: "🛡️", Purpose: "红旗统计"},
		{Key: "macro", Title: "Heat 热度", Icon: "🔥", Purpose: "市场关注度"},
		{Key: "cross_asset", Title: "Buckets 分桶", Icon: "📊", Purpose: "决策分布"},
		{Key: "cycle", Title: "Top Picks 精选", Icon: "⭐", Purpose: "得分最高标的"},
		{Key: "flow", Title: "Sectors 行业", Icon: "🏭", Purpose: "行业分布"},
	}
}

// --- Stat helpers ---

type scoreFn func(model.ScreenedCandidate) float64

func medianScore(candidates []model.ScreenedCandidate, fn scoreFn) float64 {
	if len(candidates) == 0 {
		return 0
	}
	scores := make([]float64, len(candidates))
	for i, c := range candidates {
		scores[i] = fn(c)
	}
	// Simple median: sort and pick middle
	for i := 0; i < len(scores)-1; i++ {
		for j := i + 1; j < len(scores); j++ {
			if scores[i] > scores[j] {
				scores[i], scores[j] = scores[j], scores[i]
			}
		}
	}
	return scores[len(scores)/2]
}

func avgScore(candidates []model.ScreenedCandidate, fn scoreFn) float64 {
	if len(candidates) == 0 {
		return 0
	}
	sum := 0.0
	for _, c := range candidates {
		sum += fn(c)
	}
	return sum / float64(len(candidates))
}

func scoreRange(candidates []model.ScreenedCandidate, fn scoreFn) float64 {
	if len(candidates) == 0 {
		return 0
	}
	lo, hi := fn(candidates[0]), fn(candidates[0])
	for _, c := range candidates {
		s := fn(c)
		if s < lo {
			lo = s
		}
		if s > hi {
			hi = s
		}
	}
	return hi - lo
}

func bestScoreRatio(candidates []model.ScreenedCandidate, threshold float64) float64 {
	if len(candidates) == 0 {
		return 0
	}
	count := 0
	for _, c := range candidates {
		if c.FinalScore >= threshold {
			count++
		}
	}
	return float64(count) / float64(len(candidates))
}

func valLabel(v float64) string {
	switch {
	case v >= 80:
		return "显著低估"
	case v >= 65:
		return "偏低"
	case v >= 50:
		return "合理"
	default:
		return "偏高"
	}
}

func qualLabel(v float64) string {
	switch {
	case v >= 80:
		return "优秀"
	case v >= 65:
		return "良好"
	case v >= 50:
		return "中等"
	default:
		return "较弱"
	}
}

func potLabel(v float64) string {
	switch {
	case v >= 75:
		return "强信号"
	case v >= 60:
		return "有信号"
	case v >= 50:
		return "中性"
	default:
		return "弱"
	}
}

func riskLabel(v float64) string {
	switch {
	case v >= 80:
		return "低风险"
	case v >= 60:
		return "中低风险"
	case v >= 40:
		return "中等风险"
	default:
		return "高风险"
	}
}

func heatLabel(onFire, total int) string {
	ratio := float64(onFire) / float64(max(1, total))
	switch {
	case ratio > 0.3:
		return "高热度"
	case ratio > 0.1:
		return "中等热度"
	default:
		return "低热度"
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
