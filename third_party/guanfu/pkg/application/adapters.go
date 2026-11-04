package application

import (
	"context"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/claim"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/client"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/engine"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/ports"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

type registryResolver struct{}

func (registryResolver) ResolveAsset(key string) (ports.Asset, error) {
	return engine.GetAsset(key)
}

type realSnapshot struct{}

func (realSnapshot) GetSnapshot(ctx context.Context) (*model.MarketSnapshot, error) {
	return client.NewRealClient().GetSnapshot(ctx)
}

type realHistory struct{ prices *store.PriceStore }

func (realHistory) LoadBTC(ctx context.Context) ([]client.BTCDailyPoint, error) {
	return client.LoadOrUpdateBTCDailyHistory(ctx, "")
}

func (h realHistory) LoadStock(ctx context.Context, ticker string, days int) ([]store.PricePoint, error) {
	return client.FetchAndCacheStock(ctx, h.prices, ticker, days)
}

type ledgerClaims struct{}

func (ledgerClaims) RecordForecast(fc *forecast.Forecast, asset string, panel *model.IndicatorPanel) {
	if fc == nil || claim.Disabled() {
		return
	}
	ledger, err := claim.Open("")
	if err != nil {
		return
	}
	_, panelJSON, _ := claim.PanelJSONHash(panel)
	ledger.RecordForecast(fc, asset, panelJSON)
}

type priceStoreBaselines struct{ prices *store.PriceStore }

func (b priceStoreBaselines) Annotate(fc *forecast.Forecast) {
	if fc == nil {
		return
	}
	annualRiskFree := 4.5
	rfSource := "fallback flat 4.5%"
	if point, ok := b.prices.Latest("fred_dgs3mo"); ok && point.Close > 0 {
		annualRiskFree = point.Close
		rfSource = "FRED DGS3MO " + point.Date
	}
	const annualPassive = 6.0
	forecast.AnnotateBaselines(fc, func(days int) (float64, float64, bool, string) {
		tbill, passive, ok, _ := forecast.FlatRateBaseline(annualRiskFree, annualPassive)(days)
		if !ok {
			return 0, 0, false, ""
		}
		return tbill, passive, true, rfSource
	})
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func defaultDependencies() Dependencies {
	prices := &store.PriceStore{}
	return Dependencies{
		Assets:    registryResolver{},
		Snapshot:  realSnapshot{},
		History:   realHistory{prices: prices},
		Claims:    ledgerClaims{},
		Baselines: priceStoreBaselines{prices: prices},
		Clock:     systemClock{},
	}
}
