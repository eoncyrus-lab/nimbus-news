// SPY Asset — S&P 500 ETF panel via shared equity builder.

package engine

import (
	"context"
	"fmt"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast/features"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

// SPYAsset implements Asset for SPY (S&P 500 ETF).
type SPYAsset struct {
	store *store.PriceStore
}

// NewSPYAsset creates the SPY asset implementation.
func NewSPYAsset() *SPYAsset {
	return &SPYAsset{store: &store.PriceStore{}}
}

func (a *SPYAsset) Key() string  { return "spy" }
func (a *SPYAsset) Name() string { return "S&P 500 ETF" }

func (a *SPYAsset) FetchSnapshot(ctx context.Context) (*AssetSnapshot, error) {
	history, err := a.store.LoadHistory("spy")
	if err != nil || len(history) == 0 {
		return nil, fmt.Errorf("spy: no price data in PriceStore — run Phase 0 Futu import first")
	}

	latest, _ := a.store.Latest("spy")
	vixy, _ := a.store.Latest("vixy")
	uup, _ := a.store.Latest("uup")
	tlt, _ := a.store.Latest("tlt")
	gold, _ := a.store.Latest("gold")

	as := &AssetSnapshot{
		Asset:        "spy",
		Date:         latest.Date,
		Price:        latest.Close,
		PriceAsOf:    latest.Date,
		PriceHistory: history,
		CrossAssetPrices: map[string]float64{
			"vixy": vixy.Close,
			"uup":  uup.Close,
			"tlt":  tlt.Close,
			"gold": gold.Close,
		},
	}

	return as, nil
}

func (a *SPYAsset) BuildPanel(as *AssetSnapshot) (*model.IndicatorPanel, error) {
	if len(as.PriceHistory) < 14 {
		return nil, fmt.Errorf("spy: insufficient price history (%d days)", len(as.PriceHistory))
	}

	in := &EquityPanelInput{
		Asset:        "spy",
		Date:         as.Date,
		Price:        as.Price,
		PriceAsOf:    as.PriceAsOf,
		PriceHistory: as.PriceHistory,
		PE:           as.PE,
		PB:           as.PB,
	}
	if as.CrossAssetPrices != nil {
		in.VIX = as.CrossAssetPrices["vixy"]
		in.DXY = as.CrossAssetPrices["uup"]
		in.TLT = as.CrossAssetPrices["tlt"]
		in.Gold = as.CrossAssetPrices["gold"]
	}

	panel := BuildEquityPanel(in)
	enrichEquityPanelWithValuation(panel, "spy", as.PE, as.PB)
	EnrichGlobalInvestorMacro(panel, a.store)
	return panel, nil
}

// TryFetchValuation attempts to populate PE/PB via Futu snapshot.
func (a *SPYAsset) TryFetchValuation(as *AssetSnapshot) {
	v := tryFetchEquityValuation()
	if v != nil && v.SPYPE > 0 {
		as.PE = v.SPYPE
		as.PB = v.SPYPB
	}
}

func (a *SPYAsset) BuildVerdict(panel *model.IndicatorPanel) *Verdict {
	return BuildEquityVerdict(panel)
}

func (a *SPYAsset) BuildForecast(as *AssetSnapshot, opts forecast.Options) (*forecast.Forecast, error) {
	raw, err := a.store.Load("spy")
	if err != nil {
		return nil, fmt.Errorf("spy forecast: load price store: %w", err)
	}
	if len(raw) < 200 {
		return nil, fmt.Errorf("spy forecast: need at least 200 days, got %d", len(raw))
	}
	points := make([]forecast.Point, len(raw))
	for i, p := range raw {
		points[i] = forecast.Point{Date: p.Date, Close: p.Close, Source: "price_store:spy"}
	}
	if len(opts.Horizons) == 0 {
		opts.Horizons = forecast.HorizonsForAsset("spy")
	}
	opts.Asset = "spy"
	opts.Extractors = features.ExtractorsForAsset("spy", a.store)
	opts.RegimeGate = true
	return forecast.Build(points, opts)
}
