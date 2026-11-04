package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/assetprofile"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
)

// generatePriceHistory creates a synthetic price history with a simple trend.
// Returns newest-first slice.
func generatePriceHistory(days int, startPrice, trend float64) []float64 {
	history := make([]float64, days)
	for i := 0; i < days; i++ {
		history[days-1-i] = startPrice + float64(i)*trend
	}
	return history
}

func TestBuildEquityPanel_Basic(t *testing.T) {
	history := generatePriceHistory(250, 400, 0.5) // trending up from 400

	in := &EquityPanelInput{
		Asset:        "qqq",
		Date:         "2026-05-05",
		Price:        history[0],
		PriceHistory: history,
		VIX:          18,
		DXY:          28,
		TLT:          88,
		FearGreed:    55,
	}

	panel := BuildEquityPanel(in)

	// Technical domain should have indicators
	if len(panel.Technical) == 0 {
		t.Fatal("expected technical indicators")
	}

	// RSI should exist
	if rsi, ok := panel.Technical["rsi_14"]; !ok {
		t.Fatal("expected rsi_14 indicator")
	} else if rsi.Value < 0 || rsi.Value > 100 {
		t.Fatalf("rsi out of range: %f", rsi.Value)
	}

	// SMA indicators
	if _, ok := panel.Technical["sma_50"]; !ok {
		t.Fatal("expected sma_50 indicator")
	}
	if _, ok := panel.Technical["sma_200"]; !ok {
		t.Fatal("expected sma_200 indicator")
	}

	// Macro domain
	if vix, ok := panel.Macro["vix_level"]; !ok || vix.Value != 18 {
		t.Fatal("expected vix_level indicator")
	}
	if panel.ProfileKey != "qqq" || panel.AssetClass != "equity_index" || len(panel.DomainMeta) == 0 {
		t.Fatalf("expected profile metadata, got profile=%q class=%q domains=%+v", panel.ProfileKey, panel.AssetClass, panel.DomainMeta)
	}

	// Sentiment
	if fg, ok := panel.Positioning["fear_greed"]; !ok || fg.Value != 55 {
		t.Fatal("expected fear_greed indicator")
	}

	// JSON roundtrip
	b, err := json.Marshal(panel)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
}

func TestBuildEquityPanel_InsufficientHistory(t *testing.T) {
	history := generatePriceHistory(10, 400, 1.0)

	in := &EquityPanelInput{
		Asset:        "qqq",
		Date:         "2026-05-05",
		Price:        history[0],
		PriceHistory: history,
	}

	panel := BuildEquityPanel(in)

	// Should have no RSI (need 14+ days), no SMA50 (need 50+), etc.
	if _, ok := panel.Technical["rsi_14"]; ok {
		t.Fatal("rsi_14 should be missing with only 10 days")
	}
	if _, ok := panel.Technical["sma_50"]; ok {
		t.Fatal("sma_50 should be missing with only 10 days")
	}
}

func TestBuildMarketPanelForStockUsesStockProfile(t *testing.T) {
	history := generatePriceHistory(250, 200, 0.3)
	panel := BuildMarketPanel(&MarketPanelInput{
		Asset:        "stock_aapl",
		Date:         "2026-05-12",
		Price:        history[0],
		PriceHistory: history,
		VIX:          18,
		DXY:          102,
		TLT:          90,
	})

	if panel.Asset != "stock_aapl" {
		t.Fatalf("asset = %q, want stock_aapl", panel.Asset)
	}
	if panel.ProfileKey != "us_stock" || panel.AssetClass != "us_stock" {
		t.Fatalf("expected us_stock profile metadata, got profile=%q class=%q", panel.ProfileKey, panel.AssetClass)
	}
	if panel.Snapshot.QQQPrice != 0 || panel.Snapshot.SPYPrice != 0 || panel.Snapshot.GoldPrice != 0 {
		t.Fatalf("stock panel should not populate ETF/gold snapshot price fields: %+v", panel.Snapshot)
	}
	if !domainMetaHas(panel.DomainMeta, "technical") || !domainMetaHas(panel.DomainMeta, "macro") {
		t.Fatalf("stock domain_meta missing expected domains: %+v", panel.DomainMeta)
	}
}

func TestBuildEquityDashboardAnnotatesProfile(t *testing.T) {
	history := generatePriceHistory(250, 400, 0.5)
	panel := BuildEquityDashboard(&EquityDashboardInput{
		Asset:        "qqq",
		Date:         "2026-05-12",
		Price:        history[0],
		PriceHistory: history,
		VIX:          18,
	})
	if panel == nil {
		t.Fatal("expected dashboard panel")
	}
	if panel.ProfileKey != "qqq" || panel.SkillProfileURI == "" || !domainMetaHas(panel.DomainMeta, "macro") {
		t.Fatalf("dashboard profile metadata missing: profile=%q uri=%q domains=%+v", panel.ProfileKey, panel.SkillProfileURI, panel.DomainMeta)
	}
}

func TestBuildEquityVerdict_Bullish(t *testing.T) {
	// Create a strongly bullish panel
	history := generatePriceHistory(250, 400, 1.0) // strong uptrend
	in := &EquityPanelInput{
		Asset:        "qqq",
		Date:         "2026-05-05",
		Price:        history[0],
		PriceHistory: history,
		VIX:          12, // low VIX
	}
	panel := BuildEquityPanel(in)

	verdict := BuildEquityVerdict(panel)
	if verdict.NetDirection <= 0 {
		t.Fatalf("expected positive net direction, got %d", verdict.NetDirection)
	}
	if verdict.Coverage <= 0 {
		t.Fatalf("expected positive coverage, got %f", verdict.Coverage)
	}
}

func TestBuildEquityVerdictUsesProfilePolicy(t *testing.T) {
	panel := &model.IndicatorPanel{
		Asset: "qqq",
		Date:  "2026-05-12",
		Technical: map[string]model.Indicator{
			"sma_200_dev":  {Value: 15},
			"momentum_90d": {Value: 12},
		},
		Macro: map[string]model.Indicator{
			"vix_level": {Value: 12},
		},
		Positioning: map[string]model.Indicator{
			"put_call_ratio": {Value: 1.3},
		},
	}
	verdict := BuildEquityVerdict(panel)
	policy, _ := assetprofile.VerdictPolicyFor("qqq")
	if verdict.Regime != policy.BullRegime || verdict.Stance != policy.BullStance {
		t.Fatalf("verdict did not use profile policy: got regime=%q stance=%q policy=%+v", verdict.Regime, verdict.Stance, policy)
	}
	if len(verdict.Domains) != len(policy.DomainOrder) {
		t.Fatalf("domain count = %d, want %d", len(verdict.Domains), len(policy.DomainOrder))
	}
	for i, domain := range policy.DomainOrder {
		if verdict.Domains[i].Domain != domain {
			t.Fatalf("domain[%d] = %q, want %q", i, verdict.Domains[i].Domain, domain)
		}
	}
}

func TestBuildEquityVerdictWithoutAssetDefaultsToEquityPolicy(t *testing.T) {
	panel := &model.IndicatorPanel{
		Date: "2026-05-12",
		Technical: map[string]model.Indicator{
			"sma_200_dev":  {Value: 15},
			"momentum_90d": {Value: 12},
		},
		Macro: map[string]model.Indicator{
			"vix_level": {Value: 12},
		},
		Positioning: map[string]model.Indicator{
			"put_call_ratio": {Value: 1.3},
		},
	}
	verdict := BuildEquityVerdict(panel)
	policy, _ := assetprofile.VerdictPolicyFor("qqq")
	if verdict.Regime != policy.BullRegime || len(verdict.Domains) != len(policy.DomainOrder) {
		t.Fatalf("empty-asset equity verdict should use qqq policy, got %+v", verdict)
	}
}

func TestBuildProfiledMarketVerdictForStockUsesStockPolicy(t *testing.T) {
	panel := &model.IndicatorPanel{
		Asset: "stock_aapl",
		Date:  "2026-05-12",
		Technical: map[string]model.Indicator{
			"sma_200_dev":  {Value: 15},
			"momentum_90d": {Value: 12},
		},
		Macro: map[string]model.Indicator{
			"vix_level": {Value: 12},
		},
		Positioning: map[string]model.Indicator{},
	}
	verdict := BuildProfiledMarketVerdict(panel)
	policy, _ := assetprofile.VerdictPolicyFor("stock_aapl")
	if verdict.Regime != policy.BullRegime || verdict.Stance != policy.BullStance {
		t.Fatalf("stock verdict did not use stock policy: got regime=%q stance=%q policy=%+v", verdict.Regime, verdict.Stance, policy)
	}
}

func TestBuildEquityVerdict_Bearish(t *testing.T) {
	// Create a bearish panel: high VIX, declining prices
	history := generatePriceHistory(250, 500, -1.0) // downtrend
	in := &EquityPanelInput{
		Asset:        "spy",
		Date:         "2026-05-05",
		Price:        history[0],
		PriceHistory: history,
		VIX:          35, // high VIX fear
	}
	panel := BuildEquityPanel(in)

	// Manually set RSI to oversold to test bearish signals
	panel.Technical["rsi_14"] = model.Indicator{Value: 25, Label: "超卖"}
	panel.Technical["sma_200_dev"] = model.Indicator{Value: -15, Label: "偏低"}

	verdict := BuildEquityVerdict(panel)
	t.Logf("verdict: net=%d, stance=%s, reasons=%v", verdict.NetDirection, verdict.Stance, verdict.Reasons)
	// Should have some bearish signal
	if len(verdict.Domains) == 0 {
		t.Fatal("expected domain votes")
	}
}

func TestBuildGoldVerdictUsesGoldRiskOffSemantics(t *testing.T) {
	panel := &model.IndicatorPanel{
		Asset: "gold",
		Date:  "2026-05-05",
		Technical: map[string]model.Indicator{
			"rsi_14": {Value: 50},
		},
		Macro: map[string]model.Indicator{
			"vix_level": {Value: 35},
		},
		Valuation: map[string]model.Indicator{
			"real_yield_proxy": {Value: 101},
			"dxy_level":        {Value: 92},
		},
	}
	AnnotatePanelProfile(panel, "gold")

	verdict := BuildGoldVerdict(panel)
	policy, _ := assetprofile.VerdictPolicyFor("gold")
	macro := findDomain(verdict, "macro")
	if macro.Vote <= 0 || len(macro.Bullish) == 0 {
		t.Fatalf("expected VIX risk-off to be gold-bullish, got %+v", macro)
	}
	if containsString(macro.Bearish, "VIX恐慌") {
		t.Fatalf("gold macro should not use equity VIX-bearish semantics: %+v", macro)
	}
	if verdict.NetDirection <= 0 {
		t.Fatalf("expected positive gold verdict, got %+v", verdict)
	}
	if verdict.Regime != policy.BullRegime || verdict.Stance != policy.BullStance {
		t.Fatalf("gold verdict did not use profile policy: got regime=%q stance=%q policy=%+v", verdict.Regime, verdict.Stance, policy)
	}
}

func TestCalcRSI(t *testing.T) {
	// Newest-first increasing sequence: all gains → RSI should be 100
	up := []float64{15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}
	rsi := calcRSI(up, 14)
	if rsi != 100 {
		t.Fatalf("expected RSI=100 for all gains, got %f", rsi)
	}

	// Sideways: all same → RSI should be 100 (no losses)
	flat := make([]float64, 15)
	for i := range flat {
		flat[i] = 10
	}
	rsi = calcRSI(flat, 14)
	if rsi != 100 {
		t.Fatalf("expected RSI=100 for flat, got %f", rsi)
	}
}

func TestComputeVol30d(t *testing.T) {
	history := generatePriceHistory(30, 400, 0)
	vol := computeVol30d(history)
	if vol.Value < 0 {
		t.Fatalf("expected non-negative vol, got %f", vol.Value)
	}
}

func containsString(values []string, needle string) bool {
	for _, v := range values {
		if strings.Contains(v, needle) {
			return true
		}
	}
	return false
}

func domainMetaHas(values []model.PanelDomainMeta, key string) bool {
	for _, v := range values {
		if v.Key == key {
			return true
		}
	}
	return false
}

func TestComputeDrawdown(t *testing.T) {
	// Peak at 500, current at 450 → 10% drawdown
	history := make([]float64, 200)
	for i := 0; i < 200; i++ {
		history[199-i] = 450 + float64(i%50)*1.0
	}
	history[0] = 500 // newest is peak
	history[199] = 450

	dd := computeDrawdown(history, 450)
	if dd.Value > 0 {
		t.Fatalf("expected non-positive drawdown, got %f", dd.Value)
	}
}

func TestEquityAssetRegistry(t *testing.T) {
	// QQQ and SPY should be registered
	qqq, err := GetAsset("qqq")
	if err != nil {
		t.Fatalf("qqq not registered: %v", err)
	}
	if qqq.Key() != "qqq" || qqq.Name() == "" {
		t.Fatalf("qqq asset invalid: key=%s name=%s", qqq.Key(), qqq.Name())
	}

	spy, err := GetAsset("spy")
	if err != nil {
		t.Fatalf("spy not registered: %v", err)
	}
	if spy.Key() != "spy" || spy.Name() == "" {
		t.Fatalf("spy asset invalid: key=%s name=%s", spy.Key(), spy.Name())
	}

	// BTC should be registered (from asset_btc.go init)
	btc, err := GetAsset("btc")
	if err != nil {
		t.Fatalf("btc not registered: %v", err)
	}
	if btc.Key() != "btc" {
		t.Fatalf("btc asset invalid: key=%s", btc.Key())
	}
}

func TestCoverageScore(t *testing.T) {
	empty := map[string]model.Indicator{}
	if c := coverageScore(empty); c != 0 {
		t.Fatalf("expected 0 coverage for empty, got %f", c)
	}

	full := map[string]model.Indicator{
		"a": {Value: 1, Missing: false},
		"b": {Value: 2, Missing: false},
		"c": {Value: 3, Missing: true},
	}
	if c := coverageScore(full); c != 2.0/3.0 {
		t.Fatalf("expected 2/3 coverage, got %f", c)
	}
}

func TestComputeMomentum(t *testing.T) {
	history := make([]float64, 100)
	for i := 0; i < 100; i++ {
		history[99-i] = 400 + float64(i)*0.5
	}
	history[0] = 450 // newest
	mom := computeMomentum(history, 90)
	if mom.Value <= 0 {
		t.Fatalf("expected positive momentum, got %f", mom.Value)
	}
}
