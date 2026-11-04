// Gold asset — London gold (XAU/USD) panel.
//
// Domains:
//   - technical: RSI, MACD, SMA(50/200), BB, volatility (same as equities)
//   - valuation: real yield proxy (TLT trend), DXY direction, VIX level
//   - macro: VIX level, DXY/UUP price
//
// Data sources:
//   - PriceStore gold.json (London gold pipeline, DBnomics 1968+ / Yahoo XAUUSD=X)
//   - PriceStore vixy.json, uup.json, tlt.json (cross-asset context)
//   - FRED DFII10 (real yield, when available)

package engine

import (
	"context"
	"fmt"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/assetprofile"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast/features"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

// GoldAsset implements Asset for London Gold (XAU/USD).
type GoldAsset struct {
	store *store.PriceStore
}

// NewGoldAsset creates the gold asset implementation.
func NewGoldAsset() *GoldAsset {
	return &GoldAsset{store: &store.PriceStore{}}
}

func (a *GoldAsset) Key() string  { return "gold" }
func (a *GoldAsset) Name() string { return "London Gold (XAU/USD)" }

func (a *GoldAsset) FetchSnapshot(ctx context.Context) (*AssetSnapshot, error) {
	history, err := a.store.LoadHistory("gold")
	if err != nil || len(history) == 0 {
		return nil, fmt.Errorf("gold: no price data in PriceStore — run Phase 0 gold pipeline first")
	}

	latest, _ := a.store.Latest("gold")
	vixy, _ := a.store.Latest("vixy")
	uup, uupOK := a.store.Latest("uup")
	tlt, _ := a.store.Latest("tlt")

	// A3: fall back to FRED trade-weighted USD when UUP ETF data is missing.
	// Keep the existing "uup" key so the shared market panel's dxy_proxy path fires.
	var dxy float64
	if uupOK {
		dxy = uup.Close
	}
	if !uupOK || dxy == 0 {
		if fredDxy, ok := a.store.Latest("fred_dxy"); ok {
			dxy = fredDxy.Close
		}
	}

	as := &AssetSnapshot{
		Asset:        "gold",
		Date:         latest.Date,
		Price:        latest.Close,
		PriceAsOf:    latest.Date,
		PriceHistory: history,
		CrossAssetPrices: map[string]float64{
			"vixy": vixy.Close,
			"uup":  dxy,
			"tlt":  tlt.Close,
		},
		RealYield10Y: 0, // populated if FRED data is available
		DXY:          dxy,
		VIX:          vixy.Close,
	}

	return as, nil
}

func (a *GoldAsset) BuildPanel(as *AssetSnapshot) (*model.IndicatorPanel, error) {
	if len(as.PriceHistory) < 14 {
		return nil, fmt.Errorf("gold: insufficient price history (%d days)", len(as.PriceHistory))
	}

	// Use the neutral market panel builder for shared technical/macro indicators.
	in := &MarketPanelInput{
		Asset:        "gold",
		Date:         as.Date,
		Price:        as.Price,
		PriceAsOf:    as.PriceAsOf,
		PriceHistory: as.PriceHistory,
	}
	if as.CrossAssetPrices != nil {
		in.VIX = as.CrossAssetPrices["vixy"]
		in.DXY = as.CrossAssetPrices["uup"]
		in.TLT = as.CrossAssetPrices["tlt"]
	}

	panel := BuildMarketPanel(in)

	// ── Gold valuation domain ──
	panel.Valuation = buildGoldValuation(as, panel.Macro["tlt_proxy"])
	EnrichGlobalInvestorMacro(panel, a.store)

	return panel, nil
}

func (a *GoldAsset) BuildVerdict(panel *model.IndicatorPanel) *Verdict {
	return BuildGoldVerdict(panel)
}

func BuildGoldVerdict(panel *model.IndicatorPanel) *Verdict {
	policy, ok := assetprofile.VerdictPolicyFor("gold")
	if !ok {
		policy = assetprofile.VerdictPolicy{
			DomainOrder:          []string{"technical", "macro", "valuation"},
			BullThreshold:        3,
			BearThreshold:        -3,
			BullRegime:           "偏强积累区",
			NeutralRegime:        "中性",
			BearRegime:           "偏弱谨慎区",
			BullStance:           "实际利率/美元/技术面合力偏多",
			NeutralStance:        "黄金驱动不一致，等待实际利率/美元/风险偏好确认",
			BearStance:           "美元或实际利率压力偏强，黄金信号偏弱",
			LowCoverageThreshold: 0.5,
		}
	}
	v := &Verdict{
		Date:            panel.Date,
		Confidence:      "中",
		Domains:         make([]DomainVote, 0, len(policy.DomainOrder)),
		Reasons:         make([]string, 0),
		CounterEvidence: make([]string, 0),
		KillCriteria:    make([]string, 0),
	}

	totalCoverage := 0.0
	for _, domain := range policy.DomainOrder {
		vote, bull, bear, coverage := scoreGoldDomain(panel, domain)
		v.NetDirection += vote
		totalCoverage += coverage
		v.Domains = append(v.Domains, DomainVote{
			Domain:   domain,
			Vote:     vote,
			Bullish:  bull,
			Bearish:  bear,
			Coverage: coverage,
		})
	}
	if len(v.Domains) > 0 {
		v.Coverage = totalCoverage / float64(len(v.Domains))
	}

	applyVerdictPolicy(v, policy)
	v.Reasons, v.CounterEvidence = pickEvidenceFromVotes(v.Domains, v.NetDirection)
	v.TopProximity = topProximityFromPanel(panel)
	v.BottomProximity = bottomProximityFromPanel(panel)

	return v
}

func scoreGoldDomain(panel *model.IndicatorPanel, domain string) (vote int, bull, bear []string, coverage float64) {
	switch domain {
	case "technical":
		vote, bull, bear = scoreEquityTechnicalForAsset(panel.Technical, "gold")
		coverage = coverageScore(panel.Technical)
	case "macro":
		vote, bull, bear = scoreGoldMacro(panel.Macro)
		coverage = coverageScore(panel.Macro)
	case "valuation":
		vote, bull, bear = scoreGoldValuation(panel.Valuation)
		coverage = coverageScore(panel.Valuation)
	default:
		coverage = 0
	}
	return vote, bull, bear, coverage
}

func (a *GoldAsset) BuildForecast(as *AssetSnapshot, opts forecast.Options) (*forecast.Forecast, error) {
	raw, err := a.store.Load("gold")
	if err != nil {
		return nil, fmt.Errorf("gold forecast: load price store: %w", err)
	}
	if len(raw) < 200 {
		return nil, fmt.Errorf("gold forecast: need at least 200 days, got %d", len(raw))
	}
	points := make([]forecast.Point, len(raw))
	for i, p := range raw {
		points[i] = forecast.Point{Date: p.Date, Close: p.Close, Source: "price_store:gold"}
	}
	if len(opts.Horizons) == 0 {
		opts.Horizons = forecast.HorizonsForAsset("gold")
	}
	opts.Asset = "gold"
	opts.Extractors = features.ExtractorsForAsset("gold", a.store)
	// RegimeGate hurts gold: gold frequently transitions between regimes
	// (risk-off/risk-on/inflation), so penalizing cross-regime analogs
	// removes useful historical parallels.
	return forecast.Build(points, opts)
}

// ─── Gold valuation domain ───────────────────────────────

func buildGoldValuation(as *AssetSnapshot, tltProxy model.Indicator) map[string]model.Indicator {
	m := make(map[string]model.Indicator)

	// TLT proxy for real yield: TLT rising ≈ real yields falling ≈ gold bullish
	if tltProxy.IsAvailable() && len(as.PriceHistory) >= 60 {
		// Compute 60d TLT trend from history if available
		tltTrend := computeTLTTrendFromStore(as)
		m["real_yield_proxy"] = model.Indicator{
			Value:  tltTrend,
			Label:  goldRealYieldLabel(tltTrend),
			Source: "price_store:tlt",
		}
	}

	// DXY/UUP direction
	if as.DXY > 0 && len(as.PriceHistory) >= 60 {
		dxyLabel := goldDXYLabel(as.DXY)
		m["dxy_level"] = model.Indicator{
			Value:  as.DXY,
			Label:  dxyLabel,
			Source: "price_store:uup",
		}
	}

	// VIX level
	if as.VIX > 0 {
		m["vix_level"] = model.Indicator{
			Value:  as.VIX,
			Label:  goldVIXLabel(as.VIX),
			Source: "price_store:vixy",
		}
	}

	return m
}

func computeTLTTrendFromStore(as *AssetSnapshot) float64 {
	// Use TLT price from cross-asset context
	tlt := as.CrossAssetPrices["tlt"]
	if tlt <= 0 {
		return 0
	}
	return tlt
}

func goldRealYieldLabel(tltPrice float64) string {
	// TLT > 92 ≈ real yields declining
	if tltPrice > 100 {
		return "实际利率下行 (利好黄金)"
	} else if tltPrice > 92 {
		return "实际利率中性偏低"
	} else if tltPrice > 85 {
		return "实际利率中性"
	} else {
		return "实际利率偏高 (压制黄金)"
	}
}

func goldDXYLabel(dxy float64) string {
	vote, _ := scoreGoldDXY(dxy)
	if vote < 0 {
		return "美元偏强 (压制黄金)"
	} else if vote > 0 {
		return "美元偏弱 (利好黄金)"
	}
	return "美元中性"
}

func goldVIXLabel(vix float64) string {
	switch {
	case vix > 30:
		return "恐慌→避险需求 (利好黄金)"
	case vix > 20:
		return "偏高→温和避险"
	default:
		return "低波动→风险偏好"
	}
}

func scoreGoldValuation(v map[string]model.Indicator) (vote int, bull, bear []string) {
	rules := assetprofile.ScoringRulesFor("gold", "valuation")
	if ryp, ok := v["real_yield_proxy"]; ok && ryp.IsAvailable() {
		if applyRule(rules, "real_yield_proxy", ryp.Value) > 0 {
			bull = append(bull, "实际利率下行")
			vote++
		} else if applyRule(rules, "real_yield_proxy", ryp.Value) < 0 {
			bear = append(bear, "实际利率偏高")
			vote--
		}
	}
	if dxy, ok := v["dxy_level"]; ok && dxy.IsAvailable() {
		dxyVote, note := scoreGoldDXY(dxy.Value)
		if dxyVote > 0 {
			bull = append(bull, note)
			vote++
		} else if dxyVote < 0 {
			bear = append(bear, note)
			vote--
		}
	}
	return
}

func scoreGoldMacro(m map[string]model.Indicator) (vote int, bull, bear []string) {
	rules := assetprofile.ScoringRulesFor("gold", "macro")
	if vix, ok := m["vix_level"]; ok && vix.IsAvailable() {
		if applyRule(rules, "vix_level", vix.Value) > 0 {
			bull = append(bull, fmt.Sprintf("VIX恐慌(%.0f)→避险需求", vix.Value))
			vote++
		} else if applyRule(rules, "vix_level", vix.Value) < 0 {
			bear = append(bear, "VIX极低→避险需求不足")
			vote--
		}
	}
	return
}

func scoreGoldDXY(dxy float64) (int, string) {
	if dxy >= 80 {
		switch {
		case dxy > 105:
			return -1, fmt.Sprintf("DXY偏强(%.1f)压制黄金", dxy)
		case dxy < 95:
			return +1, fmt.Sprintf("DXY偏弱(%.1f)支撑黄金", dxy)
		default:
			return 0, ""
		}
	}
	switch {
	case dxy > 30:
		return -1, fmt.Sprintf("UUP偏强(%.1f)压制黄金", dxy)
	case dxy < 25:
		return +1, fmt.Sprintf("UUP偏弱(%.1f)支撑黄金", dxy)
	default:
		return 0, ""
	}
}

func init() {
	Register(NewGoldAsset())
}
