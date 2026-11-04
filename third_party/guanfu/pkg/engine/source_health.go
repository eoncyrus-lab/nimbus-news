package engine

import (
	"fmt"
	"strings"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
)

func buildSourceHealth(snap *model.MarketSnapshot) []model.SourceHealth {
	warnings := dedupeStrings(append([]string(nil), snap.SourceWarnings...))
	out := make([]model.SourceHealth, 0, 11)

	btcOK := !snap.BTCPrice.IsZero() && len(snap.BTCPriceHistory) > 0
	ethOK := !snap.ETHPrice.IsZero() && len(snap.ETHPriceHistory) > 0
	out = append(out, healthEntry(
		"binance_spot",
		combinedStatus(btcOK, ethOK),
		latestAsOf(snap.BTCPriceAsOf, snap.ETHPriceAsOf),
		false,
		"BTC full daily history from CoinMetrics PriceUSD plus Binance latest; ETH spot history from Binance",
		matchingWarnings(warnings, "binance btc", "coinmetrics priceusd", "binance eth"),
	))

	futuresOK := snap.FuturesFetched || !snap.BTCFundingRate.IsZero() || !snap.BTCOpenInterest.IsZero()
	out = append(out, healthEntry(
		"binance_futures",
		statusFromBool(futuresOK),
		sourceTimestamp(snap.FetchedAt, ""),
		false,
		"BTC funding rate and open interest",
		matchingWarnings(warnings, "binance futures"),
	))

	coingeckoTotalOK := snap.GlobalMarketFetched || !snap.TotalMarketCap.IsZero()
	coingeckoStableOK := snap.StablecoinMarketCapFetched || !snap.StablecoinMarketCap.IsZero()
	out = append(out, healthEntry(
		"coingecko_market",
		combinedStatus(coingeckoTotalOK, coingeckoStableOK),
		"",
		false,
		"global market cap and stablecoin cap inputs",
		matchingWarnings(warnings, "coingecko global", "stablecoin"),
	))
	top50OK := snap.Top50Fetched && len(snap.Top50Coins) > 0 && snap.AltcoinSeasonAvailable
	out = append(out, healthEntry(
		"coingecko_top50",
		statusFromBool(top50OK),
		sourceTimestamp(snap.FetchedAt, ""),
		false,
		"CoinGecko top-50 list plus Binance top-50 history for altcoin season",
		matchingWarnings(warnings, "top50", "altcoin season"),
	))

	out = append(out, healthEntry(
		"alternative_fear_greed",
		statusFromBool(snap.FearGreedFetched || !snap.FearGreedIndex.IsZero() || snap.FearGreedAsOf != ""),
		snap.FearGreedAsOf,
		false,
		"Fear & Greed index",
		matchingWarnings(warnings, "fear", "greed", "alternative.me"),
	))

	mempoolOK := snap.MempoolFetched || !snap.HashRateEHs.IsZero() || snap.HashRibbonsLabel != "" || !snap.MempoolMB.IsZero()
	out = append(out, healthEntry(
		"mempool_space",
		statusFromBool(mempoolOK),
		snap.MempoolAsOf,
		false,
		"hash rate, hash ribbons, difficulty and mempool depth",
		matchingWarnings(warnings, "mempool"),
	))

	etfOK := !snap.ETFNetFlow7dUSD.IsZero() || !snap.ETFNetFlow30dUSD.IsZero() || !snap.ETFTotalAssetUSD.IsZero() || snap.ETFAsOf != ""
	etfStatus := statusFromBool(etfOK)
	etfNote := "US BTC spot ETF flows and total assets"
	if etfOK && snap.ETFStaleDays >= 2 {
		etfStatus = "stale"
		etfNote = fmt.Sprintf("%s; latest sample is %d days old", etfNote, snap.ETFStaleDays)
	}
	out = append(out, healthEntry(
		"sosovalue_etf",
		etfStatus,
		snap.ETFAsOf,
		false,
		etfNote,
		matchingWarnings(warnings, "sosovalue", "etf"),
	))

	deribitStatus := combinedStatus(snap.DVOLAvailable, snap.SkewAvailable)
	out = append(out, healthEntry(
		"deribit_options",
		deribitStatus,
		latestAsOf(snap.DVOLAsOf, snap.SkewAsOf),
		false,
		"DVOL and 25-delta skew",
		matchingWarnings(warnings, "deribit"),
	))

	out = append(out, healthEntry(
		"coinmetrics_onchain",
		statusFromBool(snap.OnchainValuationFetched),
		snap.OnchainValuationAsOf,
		false,
		"MVRV, MVRV Z, NUPL and realized cap inputs",
		matchingWarnings(warnings, "coinmetrics"),
	))

	out = append(out, healthEntry(
		"fred_macro",
		statusFromBool(snap.MacroFetched),
		latestAsOf(snap.DXYAsOf, snap.RealYield10YAsOf, snap.M2AsOf, snap.SPXAsOf, snap.HYSpreadAsOf, snap.YieldCurveAsOf),
		false,
		"DXY, real yield, M2, SPX correlation, HY spread and yield curve",
		matchingWarnings(warnings, "fred"),
	))

	crossOK := snap.CrossAssetFetched
	crossCoreOK := !snap.GoldPriceUSD.IsZero() && !snap.QQQPrice.IsZero() && !snap.SPYPrice.IsZero()
	crossStatus := combinedStatus(crossOK, crossCoreOK)
	oilNote := oilSourceHealthNote(snap.OilPriceSource)
	out = append(out, healthEntry(
		"cross_asset",
		crossStatus,
		latestAsOf(snap.GoldPriceAsOf, snap.QQQPriceAsOf, snap.SPYPriceAsOf, snap.WTIPriceAsOf, snap.UUPPriceAsOf, snap.VIXYPriceAsOf),
		crossFallbackUsed(snap, warnings),
		oilNote,
		matchingWarnings(warnings, "cross-asset", "futu", "yahoo", "paxg", "wti"),
	))

	return out
}

func healthEntry(source, status, asOf string, fallback bool, note string, warnings []string) model.SourceHealth {
	if len(warnings) > 0 && status == "ok" {
		status = "warning"
	}
	return model.SourceHealth{
		Source:       source,
		Status:       status,
		AsOf:         asOf,
		FallbackUsed: fallback,
		Impact:       sourceHealthImpact(source),
		Note:         note,
		Warnings:     warnings,
	}
}

func sourceHealthImpact(source string) string {
	switch source {
	case "binance_spot":
		return "both"
	case "fred_macro", "cross_asset":
		return "both"
	case "investor_fx", "global_central_bank_rates":
		return "market_reading"
	case "binance_futures", "coingecko_market", "coingecko_top50",
		"alternative_fear_greed", "mempool_space", "sosovalue_etf",
		"deribit_options", "coinmetrics_onchain":
		return "market_reading"
	default:
		return "optional"
	}
}

func statusFromBool(ok bool) string {
	if ok {
		return "ok"
	}
	return "missing"
}

func combinedStatus(primaryOK, secondaryOK bool) string {
	switch {
	case primaryOK && secondaryOK:
		return "ok"
	case primaryOK || secondaryOK:
		return "partial"
	default:
		return "missing"
	}
}

func matchingWarnings(warnings []string, needles ...string) []string {
	var out []string
	for _, w := range warnings {
		lower := strings.ToLower(w)
		for _, needle := range needles {
			if strings.Contains(lower, strings.ToLower(needle)) {
				out = append(out, w)
				break
			}
		}
	}
	return out
}

func latestAsOf(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func crossFallbackUsed(snap *model.MarketSnapshot, warnings []string) bool {
	if snap.OilPriceSource == "yahoo:CL=F" {
		return true
	}
	for _, w := range warnings {
		lower := strings.ToLower(w)
		if strings.Contains(lower, "will try yahoo") || strings.Contains(lower, "yahoo") {
			return true
		}
	}
	return false
}

func oilSourceHealthNote(source string) string {
	switch source {
	case "futu:US.USO":
		return "cross-asset data includes USO ETF as oil proxy; do not interpret it as WTI $/barrel"
	case "yahoo:CL=F":
		return "cross-asset data includes Yahoo CL=F WTI futures fallback"
	case "":
		return "gold, QQQ, SPY and optional oil proxy inputs"
	default:
		return fmt.Sprintf("cross-asset data includes oil source %s", source)
	}
}
