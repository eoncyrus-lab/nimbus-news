package engine

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"

	"github.com/shopspring/decimal"
)

// TestBuildPanelAllDomainsNonEmpty verifies that a minimal valid snapshot
// produces a panel where all 8 domain maps are non-nil.
func TestBuildPanelAllDomainsNonEmpty(t *testing.T) {
	snap := &model.MarketSnapshot{
		Date:                    time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
		SnapshotSchemaVersion:   model.CurrentMarketSnapshotSchemaVersion,
		BTCPrice:                decimal.NewFromInt(80000),
		ETHPrice:                decimal.NewFromInt(2500),
		BTCDominance:            decimal.NewFromFloat(0.55),
		TotalMarketCap:          decimal.NewFromFloat(1.5e12),
		FearGreedIndex:          decimal.NewFromInt(50),
		AltcoinSeasonIndex:      decimal.NewFromInt(35),
		BTCFundingRate:          decimal.NewFromFloat(0.0001),
		BTCOpenInterest:         decimal.NewFromFloat(500000),
		OnchainValuationFetched: true,
		CapMVRVCur:              decimal.NewFromFloat(2.0),
		MVRVZScore:              decimal.NewFromFloat(1.5),
		NUPL:                    decimal.NewFromFloat(0.5),
		HashRateEHs:             decimal.NewFromFloat(600),
		HashRibbonsLabel:        "上行（矿工扩张）",
		DifficultyChangePct:     decimal.NewFromFloat(2.0),
		MempoolMB:               decimal.NewFromFloat(20),
		ETFNetFlow7dUSD:         decimal.NewFromFloat(5e8),
		ETFNetFlow30dUSD:        decimal.NewFromFloat(2e9),
		ETFTotalAssetUSD:        decimal.NewFromFloat(1e11),
		CrossAssetFetched:       true,
		GoldPriceUSD:            decimal.NewFromInt(4500),
		QQQPrice:                decimal.NewFromInt(500),
		SPYPrice:                decimal.NewFromInt(550),
	}

	// Generate synthetic history (1000 days, enough for all calculations)
	n := 1000
	snap.BTCPriceHistory = make([]decimal.Decimal, n)
	snap.ETHPriceHistory = make([]decimal.Decimal, n)
	snap.GoldHistory = make([]decimal.Decimal, n)
	snap.QQQHistory = make([]decimal.Decimal, n)
	snap.SPYHistory = make([]decimal.Decimal, n)
	for i := 0; i < n; i++ {
		p := 80000.0 * (1 + float64(n-i)/float64(n)*0.5)
		snap.BTCPriceHistory[i] = decimal.NewFromFloat(p)
		snap.ETHPriceHistory[i] = decimal.NewFromFloat(p * 0.03)
		snap.GoldHistory[i] = decimal.NewFromFloat(4500.0)
		snap.QQQHistory[i] = decimal.NewFromFloat(500.0)
		snap.SPYHistory[i] = decimal.NewFromFloat(550.0)
	}

	calc := NewCalculator(&model.Config{})
	panel := calc.BuildPanel(snap)

	// Verify all 8 domains exist
	domains := map[string]map[string]model.Indicator{
		"cycle":       panel.Cycle,
		"valuation":   panel.Valuation,
		"network":     panel.Network,
		"positioning": panel.Positioning,
		"macro":       panel.Macro,
		"flow":        panel.Flow,
		"technical":   panel.Technical,
		"cross_asset": panel.CrossAsset,
	}

	for name, dom := range domains {
		if dom == nil {
			t.Errorf("domain %q is nil", name)
		}
		if len(dom) == 0 {
			t.Errorf("domain %q is empty", name)
		}
	}

	// Verify JSON marshaling
	b, err := json.Marshal(panel)
	if err != nil {
		t.Fatalf("json.Marshal panel: %v", err)
	}
	if len(b) < 100 {
		t.Fatalf("panel JSON too short: %d bytes", len(b))
	}

	// Check key indicators are present
	if _, ok := panel.Technical["rsi_14"]; !ok {
		t.Error("technical.rsi_14 missing")
	}
	if _, ok := panel.CrossAsset["btc_gold_ratio"]; !ok {
		t.Error("cross_asset.btc_gold_ratio missing")
	}
	if _, ok := panel.Cycle["mayer_multiple"]; !ok {
		t.Error("cycle.mayer_multiple missing")
	}

	t.Logf("Panel OK: %d bytes JSON, 8 domains populated", len(b))
}

// TestSkillIndicatorContract — guarantees that every indicator key the
// SKILL.md (~/.claude/skills/btc-guanfu/SKILL.md) references in its 读盘协议
// is actually emitted by BuildPanel. Prevents "documented but never produced"
// (the v3-era drift that this refactor fixes).
//
// 测试方式：维护一份"必须存在"的指标清单。SKILL.md 的指标手册 + 读盘框架
// 涉及的 key 都在这里。新增 SKILL.md 指标必须同步加到这里 + Go 实现，
// 否则 CI 失败。
func TestSkillIndicatorContract(t *testing.T) {
	expected := map[string][]string{
		"cycle":       {"days_since_halving", "days_to_halving", "sma_200w", "sma_200w_dev", "mayer_multiple", "pi_cycle_top_ratio", "phase"},
		"valuation":   {"ahr999", "ahr999_compressed", "mvrv_z_score", "nupl", "realized_price", "price_to_realized_dev_pct"},
		"network":     {"hash_rate_ehs", "hash_ribbons", "difficulty_change_pct", "mempool_mb"},
		"positioning": {"funding_rate_pct", "oi_to_mc", "fear_greed", "altcoin_season", "dvol", "dvol_60d_trend_pct", "skew_25d_pct"},
		"macro":       {"dxy_60d_trend_pct", "real_yield_10y_pct", "m2_yoy", "spx_correlation_30d"},
		"flow":        {"etf_net_flow_7d_usd", "etf_net_flow_30d_usd", "etf_total_assets_usd", "stablecoin_market_cap_usd", "eth_btc_ratio"},
		"technical":   {"rsi_14", "macd_histogram", "ema_cross", "ma_alignment", "bb_position", "volatility_20d"},
		"cross_asset": {"btc_gold_ratio", "btc_qqq_ratio", "btc_spy_ratio", "btc_gold_corr_30d", "btc_spy_corr_30d", "rel_strength_90d_gold"},
	}

	// 显式去重：以下 key 不应再出现在输出中（v4 已合并）
	deprecated := map[string][]string{
		"cross_asset": {"btc_qqq_corr_30d", "rel_strength_90d_qqq"},
	}

	// 用一个完整的 snapshot（DVOL/skew Available=true）触发全部指标
	snap := &model.MarketSnapshot{
		Date:                    time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
		SnapshotSchemaVersion:   model.CurrentMarketSnapshotSchemaVersion,
		BTCPrice:                decimal.NewFromInt(80000),
		ETHPrice:                decimal.NewFromInt(2500),
		BTCDominance:            decimal.NewFromFloat(0.55),
		TotalMarketCap:          decimal.NewFromFloat(1.5e12),
		StablecoinMarketCap:     decimal.NewFromFloat(2e11),
		FearGreedIndex:          decimal.NewFromInt(50),
		AltcoinSeasonIndex:      decimal.NewFromInt(35),
		BTCFundingRate:          decimal.NewFromFloat(0.0001),
		BTCOpenInterest:         decimal.NewFromFloat(500000),
		OnchainValuationFetched: true,
		CapMVRVCur:              decimal.NewFromFloat(2.0),
		MVRVZScore:              decimal.NewFromFloat(1.5),
		NUPL:                    decimal.NewFromFloat(0.5),
		HashRateEHs:             decimal.NewFromFloat(600),
		HashRibbonsLabel:        "上行（矿工扩张）",
		DifficultyChangePct:     decimal.NewFromFloat(2.0),
		MempoolMB:               decimal.NewFromFloat(20),
		ETFNetFlow7dUSD:         decimal.NewFromFloat(5e8),
		ETFNetFlow30dUSD:        decimal.NewFromFloat(2e9),
		ETFTotalAssetUSD:        decimal.NewFromFloat(1e11),
		MacroFetched:            true,
		DXY60dTrendPct:          decimal.NewFromFloat(-0.5),
		RealYield10YPct:         decimal.NewFromFloat(1.8),
		M2YoYPct:                decimal.NewFromFloat(4.5),
		SPXCorrelation30d:       decimal.NewFromFloat(0.4),
		CrossAssetFetched:       true,
		GoldPriceUSD:            decimal.NewFromInt(4500),
		QQQPrice:                decimal.NewFromInt(500),
		SPYPrice:                decimal.NewFromInt(550),
		DVOLAvailable:           true,
		DVOL:                    decimal.NewFromFloat(40),
		DVOL60dTrendPct:         decimal.NewFromFloat(5),
		SkewAvailable:           true,
		Skew25dNearTermPct:      decimal.NewFromFloat(2.0),
		SkewExpiry:              "2026-06-26",
	}
	n := 1500
	snap.BTCPriceHistory = make([]decimal.Decimal, n)
	snap.ETHPriceHistory = make([]decimal.Decimal, n)
	snap.GoldHistory = make([]decimal.Decimal, n)
	snap.QQQHistory = make([]decimal.Decimal, n)
	snap.SPYHistory = make([]decimal.Decimal, n)
	for i := 0; i < n; i++ {
		p := 80000.0 * (1 + float64(n-i)/float64(n)*0.5)
		snap.BTCPriceHistory[i] = decimal.NewFromFloat(p)
		snap.ETHPriceHistory[i] = decimal.NewFromFloat(p * 0.03)
		snap.GoldHistory[i] = decimal.NewFromFloat(4500.0)
		snap.QQQHistory[i] = decimal.NewFromFloat(500.0)
		snap.SPYHistory[i] = decimal.NewFromFloat(550.0)
	}

	calc := NewCalculator(&model.Config{})
	panel := calc.BuildPanel(snap)

	domainGetter := map[string]map[string]model.Indicator{
		"cycle":       panel.Cycle,
		"valuation":   panel.Valuation,
		"network":     panel.Network,
		"positioning": panel.Positioning,
		"macro":       panel.Macro,
		"flow":        panel.Flow,
		"technical":   panel.Technical,
		"cross_asset": panel.CrossAsset,
	}

	for domain, keys := range expected {
		dom := domainGetter[domain]
		for _, k := range keys {
			if _, ok := dom[k]; !ok {
				t.Errorf("SKILL contract violation: %s.%s 在 SKILL.md 引用，但 BuildPanel 未输出。要么实现指标，要么从 SKILL.md 删除引用。", domain, k)
			}
		}
	}

	for domain, keys := range deprecated {
		dom := domainGetter[domain]
		for _, k := range keys {
			if _, ok := dom[k]; ok {
				t.Errorf("SKILL contract violation: %s.%s 已在 v4 SKILL.md 标注合并/废弃，但 BuildPanel 仍输出。请从 panel.go 删除。", domain, k)
			}
		}
	}
}

func TestBuildPanelAvoidsExecutionInstructions(t *testing.T) {
	snap := &model.MarketSnapshot{
		Date:                    time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
		SnapshotSchemaVersion:   model.CurrentMarketSnapshotSchemaVersion,
		BTCPrice:                decimal.NewFromInt(80000),
		ETHPrice:                decimal.NewFromInt(2500),
		BTCDominance:            decimal.NewFromFloat(0.55),
		TotalMarketCap:          decimal.NewFromFloat(1.5e12),
		StablecoinMarketCap:     decimal.NewFromFloat(2e11),
		FearGreedIndex:          decimal.NewFromInt(15),
		AltcoinSeasonIndex:      decimal.NewFromInt(35),
		BTCFundingRate:          decimal.NewFromFloat(0.0001),
		BTCOpenInterest:         decimal.NewFromFloat(500000),
		OnchainValuationFetched: true,
		CapMVRVCur:              decimal.NewFromFloat(0.9),
		MVRVZScore:              decimal.NewFromFloat(-0.2),
		NUPL:                    decimal.NewFromFloat(-0.1),
		HashRateEHs:             decimal.NewFromFloat(600),
		HashRibbonsLabel:        "下行（矿工投降信号）",
		DifficultyChangePct:     decimal.NewFromFloat(-8.0),
		MempoolMB:               decimal.NewFromFloat(20),
		ETFNetFlow7dUSD:         decimal.NewFromFloat(5e8),
		ETFNetFlow30dUSD:        decimal.NewFromFloat(2e9),
		ETFTotalAssetUSD:        decimal.NewFromFloat(1e11),
		CrossAssetFetched:       true,
		GoldPriceUSD:            decimal.NewFromInt(4500),
		QQQPrice:                decimal.NewFromInt(500),
		SPYPrice:                decimal.NewFromInt(550),
	}

	n := 1500
	snap.BTCPriceHistory = make([]decimal.Decimal, n)
	snap.ETHPriceHistory = make([]decimal.Decimal, n)
	snap.GoldHistory = make([]decimal.Decimal, n)
	snap.QQQHistory = make([]decimal.Decimal, n)
	snap.SPYHistory = make([]decimal.Decimal, n)
	for i := 0; i < n; i++ {
		p := 80000.0 * (1 + float64(n-i)/float64(n)*0.5)
		snap.BTCPriceHistory[i] = decimal.NewFromFloat(p)
		snap.ETHPriceHistory[i] = decimal.NewFromFloat(p * 0.03)
		snap.GoldHistory[i] = decimal.NewFromFloat(4500.0)
		snap.QQQHistory[i] = decimal.NewFromFloat(500.0)
		snap.SPYHistory[i] = decimal.NewFromFloat(550.0)
	}

	panel := NewCalculator(&model.Config{}).BuildPanel(snap)
	forbidden := []string{"买入", "卖出", "加仓", "减仓", "清仓", "仓位", "建仓", "全仓", "抄底", "定投", "止损"}
	for domain, indicators := range map[string]map[string]model.Indicator{
		"cycle":       panel.Cycle,
		"valuation":   panel.Valuation,
		"network":     panel.Network,
		"positioning": panel.Positioning,
		"macro":       panel.Macro,
		"flow":        panel.Flow,
		"technical":   panel.Technical,
		"cross_asset": panel.CrossAsset,
	} {
		for key, ind := range indicators {
			text := ind.Label + "\n" + ind.Note
			for _, term := range forbidden {
				if strings.Contains(text, term) {
					t.Fatalf("%s.%s contains execution term %q in label/note: label=%q note=%q", domain, key, term, ind.Label, ind.Note)
				}
			}
		}
	}
}
