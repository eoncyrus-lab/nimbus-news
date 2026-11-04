// BTCAsset — wraps existing Calculator/BuildPanel/BuildVerdict logic into the Asset interface.

package engine

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/client"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast/features"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/history"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

// BTCAsset implements Asset for Bitcoin.
type BTCAsset struct {
	cfg    *model.Config
	client *client.RealClient
}

// NewBTCAsset creates the BTC asset implementation.
// If cfg is nil, defaults are used.
func NewBTCAsset(cfg *model.Config) *BTCAsset {
	if cfg == nil {
		cfg = &model.Config{
			Weights:    model.Weights{Trend: 0.30, Reversal: 0.25, Valuation: 0.25, Structure: 0.20},
			Thresholds: model.Thresholds{BTCMAFast: 120, BTCMASlow: 200, TopCoinCount: 50},
			API:        model.APIConfig{Timeout: "10s", Retries: 3, Mock: false},
		}
	}
	return &BTCAsset{cfg: cfg, client: client.NewRealClient()}
}

func (a *BTCAsset) Key() string  { return "btc" }
func (a *BTCAsset) Name() string { return "Bitcoin" }

func (a *BTCAsset) FetchSnapshot(ctx context.Context) (*AssetSnapshot, error) {
	snap, err := a.client.GetSnapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("btc fetch: %w", err)
	}

	price, _ := snap.BTCPrice.Float64()
	as := &AssetSnapshot{
		Asset:             "btc",
		Date:              snap.Date.Format("2006-01-02"),
		Price:             price,
		PriceAsOf:         snap.BTCPriceAsOf,
		BTCMarketSnapshot: snap,
		Warnings:          snap.SourceWarnings,
	}

	// Populate cross-asset prices
	qqq, _ := snap.QQQPrice.Float64()
	spy, _ := snap.SPYPrice.Float64()
	gold, _ := snap.GoldPriceUSD.Float64()
	as.CrossAssetPrices = map[string]float64{
		"qqq":  qqq,
		"spy":  spy,
		"gold": gold,
	}

	return as, nil
}

func (a *BTCAsset) BuildPanel(as *AssetSnapshot) (*model.IndicatorPanel, error) {
	if as.BTCMarketSnapshot == nil {
		return nil, fmt.Errorf("btc: AssetSnapshot missing BTCMarketSnapshot")
	}

	calc := NewCalculator(a.cfg).WithPriceStore(&store.PriceStore{})

	// Attach history store if available
	if os.Getenv("GUANFU_NO_HISTORY") != "1" {
		store, err := history.Open("")
		if err != nil {
			log.Printf("history.Open failed (continuing without quantiles): %v", err)
		} else {
			defer store.Close()
			calc = calc.WithHistory(store)
		}
	}

	return calc.BuildPanel(as.BTCMarketSnapshot), nil
}

func (a *BTCAsset) BuildVerdict(panel *model.IndicatorPanel) *Verdict {
	return BuildVerdict(panel)
}

func (a *BTCAsset) BuildForecast(as *AssetSnapshot, opts forecast.Options) (*forecast.Forecast, error) {
	if as.BTCMarketSnapshot == nil {
		return nil, fmt.Errorf("btc forecast: missing market snapshot")
	}
	points, err := forecast.PointsFromSnapshot(as.BTCMarketSnapshot)
	if err != nil {
		return nil, fmt.Errorf("btc forecast: %w", err)
	}
	if len(opts.Horizons) == 0 {
		opts.Horizons = forecast.HorizonsForAsset("btc")
	}
	opts.Asset = "btc"
	opts.Extractors = features.ExtractorsForAsset("btc", &store.PriceStore{})
	// G5: recent analogs (last 5y) get 1.25× effective weight to reduce
	// the pull of 2018-2022 bear-market analogs on current-cycle forecasts.
	// G2: regime gate penalizes cross-regime analogs by 1.2× distance.
	opts.RecencyWeighted = true
	opts.RegimeGate = true
	return forecast.Build(points, opts)
}

func init() {
	// Register BTC as the default asset
	Register(NewBTCAsset(nil))
}
