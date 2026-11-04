// Cross-asset feature extractors for BTC forecast.
//
// These features require cross-asset price history that starts ~2015+
// (when QQQ/SPY/GLD/UUP/TLT daily data becomes available).
//
// Usage:
//
//	ca := features.NewCrossAssetData()
//	ca.LoadFromPriceStore()  // populates from ~/.guanfu/prices/
//	extractors := ca.Extractors()  // returns []FeatureExtractor
//
// Two-stage matching:
//
//	Stage1: Core (11 features, 2010+) → finds broad analogues
//	Stage2: Core + CrossAsset (21 features, 2015+) → re-ranks for precision

package features

import (
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

// CrossAssetData holds cross-asset price histories aligned to BTC dates.
type CrossAssetData struct {
	Gold    []float64 // oldest-first
	QQQ     []float64
	SPY     []float64
	UUP     []float64 // DXY proxy
	TLT     []float64
	VIXY    []float64
	HasData bool
}

// NewCrossAssetData creates an empty cross-asset data container.
func NewCrossAssetData() *CrossAssetData {
	return &CrossAssetData{}
}

// LoadFromPriceStore populates cross-asset histories from PriceStore.
func (ca *CrossAssetData) LoadFromPriceStore() {
	s := &store.PriceStore{}
	ca.Gold, _ = s.LoadHistory("gold")
	ca.QQQ, _ = s.LoadHistory("qqq")
	ca.SPY, _ = s.LoadHistory("spy")
	ca.UUP, _ = s.LoadHistory("uup")
	ca.TLT, _ = s.LoadHistory("tlt")
	ca.VIXY, _ = s.LoadHistory("vixy")
	ca.HasData = len(ca.Gold) > 0 || len(ca.QQQ) > 0
}

// Extractors returns all cross-asset feature extractors.
func (ca *CrossAssetData) Extractors() []forecast.FeatureExtractor {
	if !ca.HasData {
		return nil
	}
	return []forecast.FeatureExtractor{
		ca.btcGoldRatio30d,
		ca.btcQQQRatio30d,
		ca.btcSPYRatio30d,
		ca.dxy30dTrend,
		ca.tlt30dTrend,
		ca.vixLevel,
	}
}

func (ca *CrossAssetData) crossReturn(points []forecast.Point, i int, crossHistory []float64, days int) (float64, bool) {
	if len(crossHistory) == 0 {
		return 0, false
	}
	// Align: find cross-asset value at BTC date i
	crossIdx := alignIndex(len(crossHistory), len(points), i)
	if crossIdx < 0 || crossIdx-days < 0 {
		return 0, false
	}
	if !usablePositive(crossHistory[crossIdx-days]) || !usablePositive(crossHistory[crossIdx]) {
		return 0, false
	}
	return crossHistory[crossIdx]/crossHistory[crossIdx-days] - 1, true
}

func (ca *CrossAssetData) crossLevel(points []forecast.Point, i int, crossHistory []float64) (float64, bool) {
	if len(crossHistory) == 0 {
		return 0, false
	}
	crossIdx := alignIndex(len(crossHistory), len(points), i)
	if crossIdx < 0 || crossIdx >= len(crossHistory) {
		return 0, false
	}
	if !usablePositive(crossHistory[crossIdx]) {
		return 0, false
	}
	return crossHistory[crossIdx], true
}

// alignIndex maps a BTC point index to the closest cross-asset index.
// Both are oldest-first. Simple proportional mapping.
func alignIndex(crossLen, btcLen, btcIdx int) int {
	if crossLen <= 0 || btcLen <= 0 {
		return -1
	}
	frac := float64(btcIdx) / float64(btcLen-1)
	return int(frac * float64(crossLen-1))
}

func (ca *CrossAssetData) btcGoldRatio30d(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
	goldRet, ok := ca.crossReturn(points, i, ca.Gold, 30)
	if !ok {
		return nil, false
	}
	btcRet, ok := returnOver(points, i, 30)
	if !ok {
		return nil, false
	}
	ratio := btcRet - goldRet
	return []forecast.FeatureValue{{
		Name: "btc_gold_ratio_30d", Value: round4(ratio * 100),
		Normalized: round4(clip(ratio/0.30, 3)), Weight: 0.60,
	}}, true
}

func (ca *CrossAssetData) btcQQQRatio30d(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
	qqqRet, ok := ca.crossReturn(points, i, ca.QQQ, 30)
	if !ok {
		return nil, false
	}
	btcRet, ok := returnOver(points, i, 30)
	if !ok {
		return nil, false
	}
	ratio := btcRet - qqqRet
	return []forecast.FeatureValue{{
		Name: "btc_qqq_ratio_30d", Value: round4(ratio * 100),
		Normalized: round4(clip(ratio/0.30, 3)), Weight: 0.55,
	}}, true
}

func (ca *CrossAssetData) btcSPYRatio30d(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
	spyRet, ok := ca.crossReturn(points, i, ca.SPY, 30)
	if !ok {
		return nil, false
	}
	btcRet, ok := returnOver(points, i, 30)
	if !ok {
		return nil, false
	}
	ratio := btcRet - spyRet
	return []forecast.FeatureValue{{
		Name: "btc_spy_ratio_30d", Value: round4(ratio * 100),
		Normalized: round4(clip(ratio/0.30, 3)), Weight: 0.55,
	}}, true
}

func (ca *CrossAssetData) dxy30dTrend(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
	uupRet, ok := ca.crossReturn(points, i, ca.UUP, 30)
	if !ok {
		return nil, false
	}
	v := -uupRet // invert: strong USD = weak BTC
	return []forecast.FeatureValue{{
		Name: "dxy_trend_30d", Value: round4(v * 100),
		Normalized: round4(clip(v/0.05, 3)), Weight: 0.50,
	}}, true
}

func (ca *CrossAssetData) tlt30dTrend(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
	tltRet, ok := ca.crossReturn(points, i, ca.TLT, 30)
	if !ok {
		return nil, false
	}
	return []forecast.FeatureValue{{
		Name: "tlt_trend_30d", Value: round4(tltRet * 100),
		Normalized: round4(clip(tltRet/0.08, 3)), Weight: 0.45,
	}}, true
}

func (ca *CrossAssetData) vixLevel(points []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
	vix, ok := ca.crossLevel(points, i, ca.VIXY)
	if !ok {
		return nil, false
	}
	normVix := (vix - 20) / 30 // center around 20
	return []forecast.FeatureValue{{
		Name: "vix_level", Value: round4(vix),
		Normalized: round4(clip(normVix, 3)), Weight: 0.50,
	}}, true
}
