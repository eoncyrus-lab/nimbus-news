package engine

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/assetprofile"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

const (
	investorMacroUSDCNYKey      = "usd_cny"
	investorMacroUSDCNYTrendKey = "usd_cny_60d_trend_pct"

	investorFXSourceName          = "investor_fx"
	globalCentralBankRateSource   = "global_central_bank_rates"
	globalCentralBankRateCoverage = 4
)

// EnrichGlobalInvestorMacro adds non-asset-specific macro context to an
// existing panel. These indicators are intended for user context, especially
// non-USD investors, and are not forecast features unless explicitly wired
// into a backtested extractor bundle.
func EnrichGlobalInvestorMacro(panel *model.IndicatorPanel, ps *store.PriceStore) {
	if panel == nil || ps == nil {
		return
	}
	if panel.Macro == nil {
		panel.Macro = make(map[string]model.Indicator)
	}

	fxAsOf, fxSpotOK, fxTrendOK := addUSDCNYMacro(panel, ps)
	addCNYInvestorLens(panel, ps)
	addEquityPutCallPositioning(panel, ps)
	addAssetPriceSourceHealth(panel, ps)
	addForecastBundleSourceHealth(panel, ps)
	ratesAsOf, ratesAvailable := addCentralBankMacro(panel, ps)
	fxStatus := combinedStatus(fxSpotOK, fxTrendOK)
	if fxSpotOK && staleDate(fxAsOf, 3) {
		fxStatus = "stale"
	}
	rateStatus := coverageStatus(ratesAvailable, globalCentralBankRateCoverage)
	rateNote := "Fed, ECB, BOJ and China front-end rate context from refreshed macro PriceStore data"
	if ratesAvailable > 0 && anyLatestStale(ps, 45, "fred_fed_funds", "fred_dgs3mo", "fred_ecb_deposit_rate", "fred_boj_call_rate", "fred_pboc_interbank_rate") {
		rateStatus = "stale"
		rateNote += "; one or more rates are stale, read as macro background rather than a forecast input"
	}

	panel.SourceHealth = append(panel.SourceHealth,
		healthEntry(
			investorFXSourceName,
			fxStatus,
			fxAsOf,
			false,
			"USD/CNY spot and 60d trend for CNY investor context",
			nil,
		),
		healthEntry(
			globalCentralBankRateSource,
			rateStatus,
			ratesAsOf,
			false,
			rateNote,
			nil,
		),
	)
}

func addAssetPriceSourceHealth(panel *model.IndicatorPanel, ps *store.PriceStore) {
	if panel != nil && strings.ToLower(strings.TrimSpace(panel.Asset)) == "btc" {
		return
	}
	key := priceStoreKeyForPanel(panel)
	if key == "" {
		return
	}
	latest, ok := ps.Latest(key)
	status := "missing"
	asOf := ""
	note := fmt.Sprintf("PriceStore daily closes for %s", strings.ToUpper(panel.Asset))
	if ok {
		status = "ok"
		asOf = latest.Date
		if staleDate(latest.Date, 5) {
			status = "stale"
		}
	}
	panel.SourceHealth = append(panel.SourceHealth, model.SourceHealth{
		Source: "price_store_" + key,
		Status: status,
		AsOf:   asOf,
		Impact: "both",
		Note:   note,
	})
}

func addForecastBundleSourceHealth(panel *model.IndicatorPanel, ps *store.PriceStore) {
	for _, src := range forecastBundleSources(panel.Asset) {
		latest, ok := ps.Latest(src.key)
		status := "missing"
		asOf := ""
		if ok {
			status = "ok"
			asOf = latest.Date
			if staleDate(latest.Date, src.maxAgeDays) {
				status = "stale"
			}
		}
		panel.SourceHealth = append(panel.SourceHealth, model.SourceHealth{
			Source: "forecast_bundle_" + src.key,
			Status: status,
			AsOf:   asOf,
			Impact: "forecast",
			Note:   src.note,
		})
	}
}

type forecastBundleSource struct {
	key        string
	maxAgeDays int
	note       string
}

func forecastBundleSources(asset string) []forecastBundleSource {
	p, ok := assetprofile.For(asset)
	if !ok {
		return nil
	}
	switch p.FeatureBundle {
	case "equity_index":
		return []forecastBundleSource{
			{"spx_cape", 45, "Equity forecast valuation feature"},
			{"fred_dgs10", 7, "Equity forecast rates feature"},
			{"fred_dxy", 7, "Equity forecast USD feature"},
			{"fred_hy_spread", 7, "Equity forecast credit-spread feature"},
			{"fred_yield_curve", 7, "Equity forecast yield-curve feature"},
			{"vixy", 5, "Equity forecast volatility feature"},
			{"stooq_putcall", 5, "Equity forecast/options sentiment feature (CBOE official; legacy storage key)"},
		}
	case "gold":
		return []forecastBundleSource{
			{"fred_dfii10", 7, "Gold forecast real-yield feature"},
			{"fred_breakeven", 7, "Gold forecast inflation-expectation feature"},
			{"fred_dxy", 7, "Gold forecast USD feature"},
			{"gold_cot", 10, "Gold forecast COT positioning feature"},
			{"vixy", 5, "Gold forecast risk-off feature"},
		}
	default:
		return nil
	}
}

func addEquityPutCallPositioning(panel *model.IndicatorPanel, ps *store.PriceStore) {
	if panel == nil || ps == nil || !isEquityLikePanel(panel.Asset) {
		return
	}
	pts, err := ps.Load("stooq_putcall")
	if err != nil || len(pts) == 0 {
		return
	}
	latest := pts[len(pts)-1]
	if latest.Close <= 0 {
		return
	}
	if panel.Positioning == nil {
		panel.Positioning = make(map[string]model.Indicator)
	}
	panel.Positioning["put_call_ratio"] = model.Indicator{
		Value:     roundN(latest.Close, 3),
		Label:     putCallRatioLabel(latest.Close),
		Source:    sourceOr(latest.Source, "cboe:daily_market_statistics"),
		UpdatedAt: latest.Date,
		Note:      "CBOE total put/call ratio. >1.2 = hedging/fear; <0.7 = complacency/call chase.",
	}
	if pct, ok := percentileRankLast(pts, 252); ok {
		panel.Positioning["put_call_252d_percentile"] = model.Indicator{
			Value:     roundN(pct*100, 1),
			Label:     putCallPercentileLabel(pct),
			Source:    "computed:stooq_putcall",
			UpdatedAt: latest.Date,
			Note:      "Latest CBOE put/call ratio percentile versus trailing 252 observations.",
		}
	}
	if past, ok := pointAtLeastDaysBack(pts, latest.Date, 30); ok && past.Close > 0 {
		chg := latest.Close - past.Close
		panel.Positioning["put_call_30d_change"] = model.Indicator{
			Value:     roundN(chg, 3),
			Label:     putCallChangeLabel(chg),
			Source:    "computed:stooq_putcall",
			UpdatedAt: latest.Date,
			Note:      fmt.Sprintf("CBOE total put/call ratio change since %s.", past.Date),
		}
	}
}

func isEquityLikePanel(asset string) bool {
	asset = strings.ToLower(strings.TrimSpace(asset))
	if asset == "" || asset == "btc" || asset == "gold" {
		return false
	}
	return true
}

func addCNYInvestorLens(panel *model.IndicatorPanel, ps *store.PriceStore) {
	assetKey := priceStoreKeyForPanel(panel)
	if assetKey == "" {
		return
	}
	fxPts, err := ps.Load("usd_cny")
	if err != nil || len(fxPts) == 0 {
		return
	}
	assetPts, err := ps.Load(assetKey)
	if err != nil || len(assetPts) == 0 {
		return
	}
	latestFX := fxPts[len(fxPts)-1]
	latestAsset := assetPts[len(assetPts)-1]
	if latestFX.Close <= 0 || latestAsset.Close <= 0 {
		return
	}

	panel.Macro["asset_price_cny"] = model.Indicator{
		Value:     roundN(latestAsset.Close*latestFX.Close, 2),
		Label:     "CNY local price",
		Source:    "computed:price_store_asset*usd_cny",
		UpdatedAt: latestAsOf(latestAsset.Date, latestFX.Date),
		Note:      fmt.Sprintf("%s USD price %.2f × USD/CNY %.4f.", strings.ToUpper(panel.Asset), latestAsset.Close, latestFX.Close),
	}
	for _, days := range []int{30, 90} {
		addCNYReturnLens(panel, assetPts, fxPts, latestAsset, latestFX, days)
	}
}

func addCNYReturnLens(panel *model.IndicatorPanel, assetPts, fxPts []store.PricePoint, latestAsset, latestFX store.PricePoint, days int) {
	pastAsset, okA := pointAtLeastDaysBack(assetPts, latestAsset.Date, days)
	pastFX, okFX := pointAtLeastDaysBack(fxPts, latestFX.Date, days)
	if !okA || !okFX || pastAsset.Close <= 0 || pastFX.Close <= 0 {
		return
	}
	usdRet := latestAsset.Close/pastAsset.Close - 1
	cnyRet := (latestAsset.Close*latestFX.Close)/(pastAsset.Close*pastFX.Close) - 1
	spread := cnyRet - usdRet
	suffix := fmt.Sprintf("%dd", days)
	panel.Macro["asset_return_usd_"+suffix] = model.Indicator{
		Value:     roundN(usdRet*100, 2),
		Label:     "USD asset return",
		Source:    "computed:price_store",
		UpdatedAt: latestAsset.Date,
		Note:      fmt.Sprintf("USD price return since %s.", pastAsset.Date),
	}
	panel.Macro["asset_return_cny_"+suffix] = model.Indicator{
		Value:     roundN(cnyRet*100, 2),
		Label:     cnyReturnLabel(cnyRet),
		Source:    "computed:price_store*usd_cny",
		UpdatedAt: latestAsOf(latestAsset.Date, latestFX.Date),
		Note:      fmt.Sprintf("CNY investor return since %s using USD/CNY reference %s.", pastAsset.Date, pastFX.Date),
	}
	panel.Macro["asset_return_spread_cny_"+suffix] = model.Indicator{
		Value:     roundN(spread*100, 2),
		Label:     fxReturnSpreadLabel(spread, usdRet),
		Source:    "computed:cny_return-usd_return",
		UpdatedAt: latestAsOf(latestAsset.Date, latestFX.Date),
		Note:      "Positive means CNY depreciation added to local-currency return; negative means CNY appreciation reduced it.",
	}
}

func priceStoreKeyForPanel(panel *model.IndicatorPanel) string {
	if panel == nil {
		return ""
	}
	asset := strings.ToLower(strings.TrimSpace(panel.Asset))
	switch asset {
	case "", "btc", "qqq", "spy", "gold":
		if asset == "" {
			return "btc"
		}
		return asset
	default:
		if strings.HasPrefix(asset, "stock_") {
			return asset
		}
		return "stock_" + asset
	}
}

func addUSDCNYMacro(panel *model.IndicatorPanel, ps *store.PriceStore) (string, bool, bool) {
	pts, err := ps.Load("usd_cny")
	if err != nil || len(pts) == 0 {
		return "", false, false
	}
	latest := pts[len(pts)-1]
	panel.Macro[investorMacroUSDCNYKey] = model.Indicator{
		Value:     roundN(latest.Close, 4),
		Label:     usdCNYLevelLabel(latest.Close),
		Source:    sourceOr(latest.Source, "yahoo:CNY=X"),
		UpdatedAt: latest.Date,
		Note:      "USD/CNY spot. Higher = CNY weaker; for CNY-based investors, USD assets can rise in CNY terms even when USD price is flat.",
	}

	past, ok := pointAtLeastDaysBack(pts, latest.Date, 60)
	if !ok || past.Close <= 0 {
		return latest.Date, true, false
	}
	trend := (latest.Close - past.Close) / past.Close * 100
	panel.Macro[investorMacroUSDCNYTrendKey] = model.Indicator{
		Value:     roundN(trend, 2),
		Label:     usdCNYTrendLabel(trend),
		Source:    sourceOr(latest.Source, "yahoo:CNY=X"),
		UpdatedAt: latest.Date,
		Note:      fmt.Sprintf("USD/CNY 60d change. Reference %.4f on %s; positive = CNY depreciation / USD tailwind for CNY investors.", past.Close, past.Date),
	}
	return latest.Date, true, true
}

func addCentralBankMacro(panel *model.IndicatorPanel, ps *store.PriceStore) (string, int) {
	available := 0
	us, usAsOf, usOK := addLatestRate(panel, ps, "global_rate_us_fed_pct", "fred_fed_funds", "Fed effective rate", "fred:DFF")
	if !usOK {
		us, usAsOf, usOK = addLatestRate(panel, ps, "global_rate_us_fed_pct", "fred_dgs3mo", "US 3M T-bill proxy", "fred:DGS3MO")
	}
	eu, euAsOf, euOK := addLatestRate(panel, ps, "global_rate_eu_ecb_pct", "fred_ecb_deposit_rate", "ECB deposit rate", "fred:ECBDFR")
	jp, jpAsOf, jpOK := addLatestRate(panel, ps, "global_rate_jp_boj_pct", "fred_boj_call_rate", "BOJ overnight call rate", "fred:IRSTCI01JPM156N")
	cn, cnAsOf, cnOK := addLatestRate(panel, ps, "global_rate_cn_pboc_pct", "fred_pboc_interbank_rate", "PBoC interbank rate", "fred:IRSTCI01CNM156N")
	for _, ok := range []bool{usOK, euOK, jpOK, cnOK} {
		if ok {
			available++
		}
	}

	if usOK && cnOK {
		spread := us - cn
		panel.Macro["global_rate_spread_us_cn_pct"] = model.Indicator{
			Value:     roundN(spread, 2),
			Label:     usCNRateSpreadLabel(spread),
			Source:    "computed:fred_fed_funds-minus-fred_pboc_interbank_rate",
			UpdatedAt: latestAsOf(usAsOf, cnAsOf),
			Note:      "US policy/interbank rate minus China interbank rate. Positive spread supports USD yield advantage; read with USD/CNY trend.",
		}
	}
	if usOK && euOK && jpOK {
		avg := (us + eu + jp) / 3
		panel.Macro["global_dm_policy_rate_avg_pct"] = model.Indicator{
			Value:     roundN(avg, 2),
			Label:     globalPolicyRateLabel(avg),
			Source:    "computed:fed_ecb_boj",
			UpdatedAt: latestAsOf(usAsOf, euAsOf, jpAsOf),
			Note:      "Average of US, Euro Area and Japan front-end policy/interbank rates; context only, not a forecast trigger.",
		}
	}
	return latestAsOf(usAsOf, euAsOf, jpAsOf, cnAsOf), available
}

func addLatestRate(panel *model.IndicatorPanel, ps *store.PriceStore, indicatorKey, storeKey, labelPrefix, fallbackSource string) (float64, string, bool) {
	latest, ok := ps.Latest(storeKey)
	if !ok || latest.Close <= 0 {
		return 0, "", false
	}
	panel.Macro[indicatorKey] = model.Indicator{
		Value:     roundN(latest.Close, 3),
		Label:     fmt.Sprintf("%s %.2f%% (%s)", labelPrefix, latest.Close, policyRateLabel(latest.Close)),
		Source:    sourceOr(latest.Source, fallbackSource),
		UpdatedAt: latest.Date,
		Note:      "Global central-bank / front-end rate context. Use as macro background; do not treat as a standalone timing signal.",
	}
	return latest.Close, latest.Date, true
}

func coverageStatus(available, total int) string {
	switch {
	case total <= 0 || available <= 0:
		return "missing"
	case available >= total:
		return "ok"
	default:
		return "partial"
	}
}

func pointAtLeastDaysBack(points []store.PricePoint, latestDate string, days int) (store.PricePoint, bool) {
	if len(points) == 0 || latestDate == "" || days <= 0 {
		return store.PricePoint{}, false
	}
	target, ok := dateDaysBefore(latestDate, days)
	if !ok {
		idx := len(points) - 1 - days
		if idx >= 0 {
			return points[idx], true
		}
		return store.PricePoint{}, false
	}
	for i := len(points) - 1; i >= 0; i-- {
		if points[i].Date <= target {
			return points[i], true
		}
	}
	return store.PricePoint{}, false
}

func percentileRankLast(points []store.PricePoint, window int) (float64, bool) {
	if len(points) == 0 {
		return 0, false
	}
	start := 0
	if window > 0 && len(points) > window {
		start = len(points) - window
	}
	latest := points[len(points)-1].Close
	if latest <= 0 {
		return 0, false
	}
	total := 0
	le := 0
	for _, p := range points[start:] {
		if p.Close <= 0 {
			continue
		}
		total++
		if p.Close <= latest {
			le++
		}
	}
	if total == 0 {
		return 0, false
	}
	return float64(le) / float64(total), true
}

func dateDaysBefore(date string, days int) (string, bool) {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "", false
	}
	return t.AddDate(0, 0, -days).Format("2006-01-02"), true
}

func staleDate(date string, maxAgeDays int) bool {
	if date == "" || maxAgeDays <= 0 {
		return false
	}
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return false
	}
	return time.Since(t) > time.Duration(maxAgeDays)*24*time.Hour
}

func anyLatestStale(ps *store.PriceStore, maxAgeDays int, keys ...string) bool {
	if ps == nil {
		return false
	}
	for _, key := range keys {
		latest, ok := ps.Latest(key)
		if !ok {
			continue
		}
		if staleDate(latest.Date, maxAgeDays) {
			return true
		}
	}
	return false
}

func sourceOr(source, fallback string) string {
	if source != "" {
		return source
	}
	return fallback
}

func roundN(v float64, places int) float64 {
	if places < 0 {
		return v
	}
	scale := math.Pow10(places)
	return math.Round(v*scale) / scale
}

func usdCNYLevelLabel(v float64) string {
	switch {
	case v >= 7.30:
		return "CNY weak / USD strong"
	case v >= 7.00:
		return "CNY mildly weak"
	case v >= 6.70:
		return "CNY stable"
	default:
		return "CNY strong"
	}
}

func usdCNYTrendLabel(v float64) string {
	switch {
	case v >= 3:
		return "CNY depreciation tailwind for USD assets"
	case v >= 1:
		return "mild CNY depreciation"
	case v <= -3:
		return "CNY appreciation headwind for USD assets"
	case v <= -1:
		return "mild CNY appreciation"
	default:
		return "FX roughly stable"
	}
}

func putCallRatioLabel(v float64) string {
	switch {
	case v >= 1.2:
		return "hedging/fear elevated"
	case v <= 0.7:
		return "complacency/call chase"
	default:
		return "neutral options sentiment"
	}
}

func putCallPercentileLabel(p float64) string {
	switch {
	case p >= 0.85:
		return "high fear percentile"
	case p <= 0.15:
		return "low fear percentile"
	default:
		return "middle percentile"
	}
}

func putCallChangeLabel(v float64) string {
	switch {
	case v >= 0.2:
		return "hedging demand rising"
	case v <= -0.2:
		return "hedging demand falling"
	default:
		return "little change"
	}
}

func cnyReturnLabel(v float64) string {
	switch {
	case v >= 0.10:
		return "strong CNY return"
	case v >= 0.03:
		return "positive CNY return"
	case v <= -0.10:
		return "large CNY drawdown"
	case v <= -0.03:
		return "negative CNY return"
	default:
		return "flat CNY return"
	}
}

func fxReturnSpreadLabel(spread, usdRet float64) string {
	absSpread := math.Abs(spread)
	absAsset := math.Abs(usdRet)
	switch {
	case absSpread >= 0.02 && absSpread > absAsset:
		return "FX move dominates local return"
	case spread >= 0.02:
		return "CNY depreciation tailwind"
	case spread <= -0.02:
		return "CNY appreciation headwind"
	default:
		return "FX contribution small"
	}
}

func policyRateLabel(v float64) string {
	switch {
	case v >= 5:
		return "restrictive"
	case v >= 2:
		return "moderate"
	case v >= 0.5:
		return "easy"
	default:
		return "very easy"
	}
}

func usCNRateSpreadLabel(v float64) string {
	switch {
	case v >= 2:
		return "large USD yield advantage"
	case v >= 0.5:
		return "USD yield advantage"
	case v <= -1:
		return "CNY yield advantage"
	default:
		return "rate spread narrow"
	}
}

func globalPolicyRateLabel(v float64) string {
	switch {
	case v >= 4:
		return "DM policy restrictive"
	case v >= 2:
		return "DM policy moderately tight"
	case v >= 0.5:
		return "DM policy easy-normal"
	default:
		return "DM policy very easy"
	}
}
