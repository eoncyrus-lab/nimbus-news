// Per-asset extractor bundles. Each asset's BuildForecast calls its bundle
// to assemble the right mix of generic technicals + asset-specific macro.
//
// Design:
//   - Generic technicals (return/dd/RSI/vol/Mayer) work for any asset.
//   - Asset-specific macro extractors are constructed against PriceStore.
//   - If a macro source is missing, the extractor is silently dropped.
//   - BTC keeps its dedicated CoreExtractors() (halving/AHR are BTC-only).
//   - Non-BTC assets inject per-profile normalization scales so that equity
//     and gold returns are not compressed by BTC's extreme-volatility defaults.

package features

import (
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/assetprofile"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

func ExtractorsForAsset(asset string, s *store.PriceStore) []forecast.FeatureExtractor {
	p, ok := assetprofile.For(asset)
	if !ok {
		return GenericTechnicalExtractors()
	}
	return ExtractorsForProfile(p, s)
}

func ExtractorsForProfile(p assetprofile.Profile, s *store.PriceStore) []forecast.FeatureExtractor {
	switch p.FeatureBundle {
	case "btc_core":
		return CoreExtractors()
	case "equity_index":
		return EquityExtractorsWithScales(s, PutCallAll, p.FeatureScales)
	case "gold":
		return GoldExtractorsWithScales(s, p.FeatureScales)
	case "us_stock":
		return USStockExtractorsWithScales(s, p.FeatureScales)
	default:
		return GenericTechnicalExtractors()
	}
}

func EquityExtractorsWithPutCallMode(s *store.PriceStore, putCallMode PutCallFeatureMode) []forecast.FeatureExtractor {
	return EquityExtractorsWithScales(s, putCallMode, nil)
}

func EquityExtractorsWithScales(s *store.PriceStore, putCallMode PutCallFeatureMode, scales map[string]float64) []forecast.FeatureExtractor {
	exts := GenericTechnicalExtractorsWithScales(scales)
	for _, ex := range []forecast.FeatureExtractor{
		CAPEExtractor(s),
		DGS10Extractor(s),
		DXYExtractor(s),
		HYSpreadExtractor(s),
		YieldCurveExtractor(s),
		VIXExtractor(s),
		PutCallRatioExtractorWithMode(s, putCallMode),
	} {
		if ex != nil {
			exts = append(exts, ex)
		}
	}
	return exts
}

func GoldExtractorsWithScales(s *store.PriceStore, scales map[string]float64) []forecast.FeatureExtractor {
	exts := GenericTechnicalExtractorsWithScales(scales)
	for _, ex := range []forecast.FeatureExtractor{
		RealYield10YExtractor(s),
		BreakevenExtractor(s),
		DXYExtractor(s),   // USD 30d trend: DXY falling = gold bullish
		DGS10Extractor(s), // 10Y rate 30d change: rising rates = gold headwind
		GoldCOTExtractor(s),
		VIXExtractor(s),
	} {
		if ex != nil {
			exts = append(exts, ex)
		}
	}
	return exts
}

func USStockExtractorsWithScales(s *store.PriceStore, scales map[string]float64) []forecast.FeatureExtractor {
	exts := GenericTechnicalExtractorsWithScales(scales)
	for _, ex := range []forecast.FeatureExtractor{
		DGS10Extractor(s),
		DXYExtractor(s),
		HYSpreadExtractor(s),
		YieldCurveExtractor(s),
		VIXExtractor(s),
	} {
		if ex != nil {
			exts = append(exts, ex)
		}
	}
	return exts
}
