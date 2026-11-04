package engine

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/shopspring/decimal"
)

// build a panel with a small set of indicators for testing.
func newTestPanel() *model.IndicatorPanel {
	return &model.IndicatorPanel{
		Date:        "2026-05-03",
		Cycle:       map[string]model.Indicator{},
		Valuation:   map[string]model.Indicator{},
		Network:     map[string]model.Indicator{},
		Positioning: map[string]model.Indicator{},
		Macro:       map[string]model.Indicator{},
		Flow:        map[string]model.Indicator{},
		Technical:   map[string]model.Indicator{},
		CrossAsset:  map[string]model.Indicator{},
	}
}

func TestVerdictMissingIsolation(t *testing.T) {
	// Missing 指标必须不影响其他指标的计票。
	p := newTestPanel()
	// 1 个真实看涨指标 + 1 个 missing 看跌指标
	p.Cycle["mayer_multiple"] = model.Indicator{Value: 0.7}                    // bull
	p.Cycle["pi_cycle_top_ratio"] = model.Indicator{Missing: true, Value: 1.5} // would be bear if not missing

	v := BuildVerdict(p)

	cycle := v.Domains[0]
	if cycle.Vote != +1 {
		t.Fatalf("expected cycle vote +1 (only mayer counted), got %d", cycle.Vote)
	}
	if len(cycle.Skipped) != 1 || cycle.Skipped[0] != "pi_cycle_top_ratio" {
		t.Fatalf("expected pi_cycle skipped, got %v", cycle.Skipped)
	}
	if len(cycle.Bearish) != 0 {
		t.Fatalf("missing pi_cycle leaked into bearish votes: %v", cycle.Bearish)
	}
}

func TestVerdictNaNIsAvailable(t *testing.T) {
	p := newTestPanel()
	p.Cycle["mayer_multiple"] = model.Indicator{Value: math.NaN()}
	v := BuildVerdict(p)
	cycle := v.Domains[0]
	if len(cycle.Skipped) != 1 {
		t.Fatalf("NaN should be skipped, got skipped=%v", cycle.Skipped)
	}
}

func TestVerdictBullStanceOnStrongConsensus(t *testing.T) {
	p := newTestPanel()
	// 6 个域看涨，且组合型域都提供确认信号。
	p.Cycle["mayer_multiple"] = model.Indicator{Value: 0.7}            // bull
	p.Valuation["ahr999_compressed"] = model.Indicator{Value: 0.5}     // bull
	p.Network["hash_ribbons"] = model.Indicator{Label: "上行（扩张）"}       // bull
	p.Positioning["funding_rate_pct"] = model.Indicator{Value: -0.005} // bull
	p.Positioning["oi_to_mc"] = model.Indicator{Value: 0.012}          // bull
	p.Macro["m2_yoy"] = model.Indicator{Value: 6.0}                    // bull
	p.Macro["real_yield_10y_pct"] = model.Indicator{Value: 0.5}        // bull
	p.Macro["dxy_60d_trend_pct"] = model.Indicator{Value: -2.0}        // bull
	p.Flow["etf_net_flow_30d_usd"] = model.Indicator{Value: 2e9}       // bull
	p.Technical["rsi_14"] = model.Indicator{Value: 55}
	p.Technical["macd_histogram"] = model.Indicator{Value: 100} // bull
	p.Technical["ema_cross"] = model.Indicator{Value: 1.2}      // bull
	p.Technical["ma_alignment"] = model.Indicator{Value: 2.0}   // bull
	p.CrossAsset["btc_spy_corr_30d"] = model.Indicator{Value: 0.2}
	p.CrossAsset["rel_strength_90d_gold"] = model.Indicator{Value: 8.0} // bull

	v := BuildVerdict(p)
	if v.NetDirection < 5 {
		t.Fatalf("expected net direction >= 5, got %d", v.NetDirection)
	}
	if v.Stance != "强积累倾向" {
		t.Fatalf("expected 强积累倾向, got %q", v.Stance)
	}
	if v.Regime != "风险偏多" {
		t.Fatalf("expected 风险偏多, got %q", v.Regime)
	}
}

func TestVerdictTopProximity(t *testing.T) {
	p := newTestPanel()
	p.Cycle["pi_cycle_top_ratio"] = model.Indicator{Value: 1.0} // hard top signal
	p.Cycle["mayer_multiple"] = model.Indicator{Value: 2.6}     // top
	p.Cycle["sma_200w_dev"] = model.Indicator{Value: 1.6}       // top (+160%)
	p.Valuation["mvrv_z_score"] = model.Indicator{Value: 7.5}   // top
	p.Positioning["funding_rate_pct"] = model.Indicator{Value: 0.06}
	p.Positioning["fear_greed"] = model.Indicator{Value: 85}

	v := BuildVerdict(p)
	if v.TopProximity < 0.6 {
		t.Fatalf("expected top proximity >= 0.6 with multiple top signals, got %.2f", v.TopProximity)
	}
}

func TestVerdictCoverageAffectsConfidence(t *testing.T) {
	p := newTestPanel()
	// 只有 1 个 available 指标，其余 missing
	p.Cycle["mayer_multiple"] = model.Indicator{Value: 0.7}
	p.Cycle["pi_cycle_top_ratio"] = model.Indicator{Missing: true}
	p.Cycle["sma_200w_dev"] = model.Indicator{Missing: true}
	p.Valuation["ahr999_compressed"] = model.Indicator{Missing: true}
	p.Positioning["funding_rate_pct"] = model.Indicator{Missing: true}
	p.Macro["m2_yoy"] = model.Indicator{Missing: true}

	v := BuildVerdict(p)
	if v.Coverage > 0.5 {
		t.Fatalf("expected low coverage, got %.2f", v.Coverage)
	}
	if v.Confidence != "低（覆盖率不足）" {
		t.Fatalf("expected low-confidence label due to coverage, got %q", v.Confidence)
	}
}

func TestVerdictDedupValuationCluster(t *testing.T) {
	p := newTestPanel()
	// Cycle 域两个估值类指标看涨 + Valuation 域两个看涨 → 应该去重
	p.Cycle["mayer_multiple"] = model.Indicator{Value: 0.6}
	p.Cycle["sma_200w_dev"] = model.Indicator{Value: -0.1}
	p.Valuation["ahr999_compressed"] = model.Indicator{Value: 0.5}
	p.Valuation["mvrv_z_score"] = model.Indicator{Value: -0.5}

	v := BuildVerdict(p)
	cycle := findDomain(v, "cycle")
	if cycle.Vote != 0 {
		t.Fatalf("cycle should be deduped to 0 since valuation 也同向，got %d", cycle.Vote)
	}
	if len(v.ClusterNotes) == 0 {
		t.Fatalf("expected a cluster note about dedup")
	}
}

func TestVerdictValuationUsesCompressedAHR(t *testing.T) {
	p := newTestPanel()
	p.Valuation["ahr999"] = model.Indicator{Value: 0.3}
	p.Valuation["ahr999_compressed"] = model.Indicator{Value: 2.0}

	v := BuildVerdict(p)
	valuation := findDomain(v, "valuation")
	if valuation.Vote != 0 {
		t.Fatalf("adaptive ahr999 should not drive valuation vote when compressed AHR is neutral, got %+v", valuation)
	}

	p.Valuation["ahr999_compressed"] = model.Indicator{Value: 0.5}
	v = BuildVerdict(p)
	valuation = findDomain(v, "valuation")
	if valuation.Vote != +1 {
		t.Fatalf("compressed AHR below bottom threshold should vote bullish, got %+v", valuation)
	}

	p.Valuation["ahr999_compressed"] = model.Indicator{Value: 3.5}
	v = BuildVerdict(p)
	valuation = findDomain(v, "valuation")
	if valuation.Vote != -1 {
		t.Fatalf("compressed AHR above top threshold should vote bearish, got %+v", valuation)
	}
}

func TestVerdictNeutralAvailableIndicatorsCountTowardCoverage(t *testing.T) {
	p := newTestPanel()
	p.Macro["m2_yoy"] = model.Indicator{Value: 3.0}
	p.Macro["real_yield_10y_pct"] = model.Indicator{Value: 1.5}
	p.Macro["dxy_60d_trend_pct"] = model.Indicator{Value: 0.2}

	v := BuildVerdict(p)
	macro := findDomain(v, "macro")
	if macro.Vote != 0 {
		t.Fatalf("neutral macro indicators should not vote, got %d", macro.Vote)
	}
	if macro.Coverage != 1 {
		t.Fatalf("neutral but available macro indicators should count as covered, got %.2f", macro.Coverage)
	}
	if v.Coverage != 1 {
		t.Fatalf("global coverage should count neutral available indicators, got %.2f", v.Coverage)
	}
}

func TestBuildPanelMarksMacroPlaceholdersMissing(t *testing.T) {
	calc := NewCalculator(&model.Config{})
	panel := calc.BuildPanel(&model.MarketSnapshot{
		Date:     time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC),
		BTCPrice: decimal.NewFromInt(100000),
	})

	for _, key := range []string{"dxy_60d_trend_pct", "real_yield_10y_pct", "m2_yoy", "spx_correlation_30d"} {
		ind, ok := panel.Macro[key]
		if !ok {
			t.Fatalf("expected macro placeholder %s", key)
		}
		if !ind.Missing {
			t.Fatalf("expected macro placeholder %s to be missing", key)
		}
	}
	v := BuildVerdict(panel)
	macro := findDomain(v, "macro")
	if macro.Vote != 0 || len(macro.Bullish) != 0 || len(macro.Bearish) != 0 {
		t.Fatalf("macro placeholders leaked into vote: %+v", macro)
	}
}

func TestBuildPanelKeepsAvailableMacroSignalsWhenFREDFails(t *testing.T) {
	calc := NewCalculator(&model.Config{})
	wtiHistory := make([]decimal.Decimal, 61)
	wtiHistory[60] = decimal.NewFromInt(50)

	panel := calc.BuildPanel(&model.MarketSnapshot{
		Date:           time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC),
		BTCPrice:       decimal.NewFromInt(100000),
		WTIPrice:       decimal.NewFromInt(75),
		WTIHistory:     wtiHistory,
		WTIPriceAsOf:   "2026-05-02",
		OilPriceSource: "yahoo:CL=F",
	})

	if ind := panel.Macro["dxy_60d_trend_pct"]; !ind.Missing {
		t.Fatalf("expected DXY placeholder to remain missing when FRED is unavailable: %+v", ind)
	}
	if ind, ok := panel.Macro["wti_crude_usd"]; !ok || ind.Missing || ind.Value != 75 {
		t.Fatalf("expected WTI to be retained despite FRED failure, got ok=%v ind=%+v", ok, ind)
	}
	if ind, ok := panel.Macro["wti_crude_60d_trend_pct"]; !ok || ind.Missing || ind.Value != 50 {
		t.Fatalf("expected WTI 60d trend to be computed, got ok=%v ind=%+v", ok, ind)
	}
}

func TestBuildPanelLabelsUSOAsOilProxy(t *testing.T) {
	calc := NewCalculator(&model.Config{})
	panel := calc.BuildPanel(&model.MarketSnapshot{
		Date:           time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC),
		BTCPrice:       decimal.NewFromInt(100000),
		WTIPrice:       decimal.NewFromInt(75),
		WTIPriceAsOf:   "2026-05-02",
		OilPriceSource: "futu:US.USO",
	})

	ind, ok := panel.Macro["oil_proxy_usd"]
	if !ok || ind.Missing || ind.Source != "futu:US.USO" {
		t.Fatalf("expected USO to be exposed as oil proxy, got ok=%v ind=%+v", ok, ind)
	}
	if _, ok := panel.Macro["wti_crude_usd"]; ok {
		t.Fatal("USO ETF proxy should not be emitted as WTI crude")
	}
	if !strings.Contains(ind.Note, "不是 $/桶 WTI") {
		t.Fatalf("expected USO note to avoid barrel-price interpretation, got %q", ind.Note)
	}
}

func TestBuildPanelIncludesSourceHealth(t *testing.T) {
	calc := NewCalculator(&model.Config{})
	panel := calc.BuildPanel(&model.MarketSnapshot{
		Date:              time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC),
		BTCPrice:          decimal.NewFromInt(100000),
		BTCPriceHistory:   []decimal.Decimal{decimal.NewFromInt(100000)},
		BTCPriceAsOf:      "2026-05-03",
		ETHPrice:          decimal.NewFromInt(3000),
		ETHPriceHistory:   []decimal.Decimal{decimal.NewFromInt(3000)},
		ETHPriceAsOf:      "2026-05-03",
		ETFNetFlow30dUSD:  decimal.NewFromInt(1),
		ETFAsOf:           "2026-04-30",
		ETFStaleDays:      3,
		WTIPrice:          decimal.NewFromInt(75),
		WTIPriceAsOf:      "2026-05-02",
		OilPriceSource:    "yahoo:CL=F",
		CrossAssetFetched: true,
		GoldPriceUSD:      decimal.NewFromInt(4500),
		QQQPrice:          decimal.NewFromInt(500),
		SPYPrice:          decimal.NewFromInt(550),
		GoldPriceAsOf:     "2026-05-02",
		QQQPriceAsOf:      "2026-05-02",
		SPYPriceAsOf:      "2026-05-02",
		SourceWarnings:    []string{"fred macro data unavailable: FRED_API_KEY is not set"},
	})

	spot := findSourceHealth(panel, "binance_spot")
	if spot.Status != "ok" || spot.AsOf == "" {
		t.Fatalf("expected binance spot source health ok with as_of, got %+v", spot)
	}
	etf := findSourceHealth(panel, "sosovalue_etf")
	if etf.Status != "stale" || !strings.Contains(etf.Note, "3 days old") {
		t.Fatalf("expected ETF stale health, got %+v", etf)
	}
	fred := findSourceHealth(panel, "fred_macro")
	if fred.Status != "missing" || len(fred.Warnings) == 0 {
		t.Fatalf("expected FRED missing health with warning, got %+v", fred)
	}
	top50 := findSourceHealth(panel, "coingecko_top50")
	if top50.Status != "missing" {
		t.Fatalf("expected top50 missing health when breadth inputs are absent, got %+v", top50)
	}
	cross := findSourceHealth(panel, "cross_asset")
	if cross.Status != "ok" || !cross.FallbackUsed {
		t.Fatalf("expected cross asset ok with Yahoo fallback noted, got %+v", cross)
	}
}

func findSourceHealth(panel *model.IndicatorPanel, source string) model.SourceHealth {
	for _, h := range panel.SourceHealth {
		if h.Source == source {
			return h
		}
	}
	return model.SourceHealth{}
}

func TestVerdictSMA200WDevUsesRatioUnits(t *testing.T) {
	p := newTestPanel()
	p.Cycle["sma_200w_dev"] = model.Indicator{Value: 1.6}

	v := BuildVerdict(p)
	cycle := findDomain(v, "cycle")
	if cycle.Vote != -1 {
		t.Fatalf("sma_200w_dev=1.6 (+160%%) should be bearish, got %d", cycle.Vote)
	}
	if v.TopProximity == 0 {
		t.Fatalf("sma_200w_dev=1.6 should contribute to top proximity")
	}
}

func TestVerdictCompositeDomainsRequireConfirmation(t *testing.T) {
	p := newTestPanel()
	p.Network["hash_ribbons"] = model.Indicator{Label: "下行（矿工投降信号）"}
	p.Positioning["funding_rate_pct"] = model.Indicator{Value: -0.004}
	p.Macro["m2_yoy"] = model.Indicator{Value: 6.0}
	p.Technical["macd_histogram"] = model.Indicator{Value: 1.0}
	p.CrossAsset["btc_spy_corr_30d"] = model.Indicator{Value: 0.1}

	v := BuildVerdict(p)
	for _, domain := range []string{"network", "positioning", "macro", "technical", "cross_asset"} {
		if got := findDomain(v, domain).Vote; got != 0 {
			t.Fatalf("%s should require confirmation, got vote %d", domain, got)
		}
	}

	p.Network["difficulty_change_pct"] = model.Indicator{Value: -6.0}
	p.Positioning["oi_to_mc"] = model.Indicator{Value: 0.01}
	p.Macro["real_yield_10y_pct"] = model.Indicator{Value: 0.5}
	p.Technical["ema_cross"] = model.Indicator{Value: 0.5}
	p.CrossAsset["rel_strength_90d_gold"] = model.Indicator{Value: 2.0}

	v = BuildVerdict(p)
	if got := findDomain(v, "network").Vote; got != -1 {
		t.Fatalf("network confirmed capitulation should be bearish, got %d", got)
	}
	for _, domain := range []string{"positioning", "macro", "technical", "cross_asset"} {
		if got := findDomain(v, domain).Vote; got != +1 {
			t.Fatalf("%s confirmed bullish setup should vote +1, got %d", domain, got)
		}
	}
}

func TestBottomProximitySkipsMissingHashRibbons(t *testing.T) {
	p := newTestPanel()
	p.Cycle["mayer_multiple"] = model.Indicator{Value: 0.6}

	base := BuildVerdict(p).BottomProximity

	p.Network["hash_ribbons"] = model.Indicator{Missing: true, Label: "下行（矿工投降信号）"}
	withMissing := BuildVerdict(p).BottomProximity
	if withMissing != base {
		t.Fatalf("missing hash_ribbons should not change bottom denominator: base %.4f missing %.4f", base, withMissing)
	}

	p.Network["hash_ribbons"] = model.Indicator{Label: "交叉中"}
	withAvailableNeutral := BuildVerdict(p).BottomProximity
	if withAvailableNeutral >= base {
		t.Fatalf("available neutral hash_ribbons should count in denominator and reduce proximity: base %.4f neutral %.4f", base, withAvailableNeutral)
	}
}

func TestVerdictEvidenceComesFromDomainVotes(t *testing.T) {
	p := newTestPanel()
	p.Cycle["mayer_multiple"] = model.Indicator{Value: 0.7}
	p.Positioning["funding_rate_pct"] = model.Indicator{Value: -0.005}
	p.Macro["m2_yoy"] = model.Indicator{Value: 6.0}
	p.Technical["macd_histogram"] = model.Indicator{Value: 1.0}

	v := BuildVerdict(p)
	if len(v.Reasons) == 0 {
		t.Fatal("expected domain-level reason from cycle vote")
	}
	for _, reason := range v.Reasons {
		if reason == "positioning: funding_rate_pct=-0.005 (杠杆反向信号)" ||
			reason == "macro: m2_yoy=6" ||
			reason == "technical: macd_hist>0" {
			t.Fatalf("unconfirmed single-indicator signal leaked into reasons: %v", v.Reasons)
		}
	}
}

func TestKillCriteriaAvoidsPositionInstructions(t *testing.T) {
	p := newTestPanel()
	p.Cycle["pi_cycle_top_ratio"] = model.Indicator{Value: 1.0}
	p.Cycle["mayer_multiple"] = model.Indicator{Value: 2.6}
	p.Cycle["sma_200w_dev"] = model.Indicator{Value: 1.6}
	p.Valuation["mvrv_z_score"] = model.Indicator{Value: 7.5}
	p.Positioning["funding_rate_pct"] = model.Indicator{Value: 0.06}
	p.Positioning["fear_greed"] = model.Indicator{Value: 85}

	v := BuildVerdict(p)
	for _, criterion := range v.KillCriteria {
		if containsAny([]string{criterion}, "减仓", "调高仓位") {
			t.Fatalf("kill criteria should avoid position instructions, got %q", criterion)
		}
	}
}

func findDomain(v *Verdict, name string) DomainVote {
	for _, d := range v.Domains {
		if d.Domain == name {
			return d
		}
	}
	return DomainVote{}
}
