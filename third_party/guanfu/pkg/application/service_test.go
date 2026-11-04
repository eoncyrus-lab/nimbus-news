package application

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/client"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/engine"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/ports"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

type fakeResolver struct{ asset ports.Asset }

func (r fakeResolver) ResolveAsset(key string) (ports.Asset, error) { return r.asset, nil }

type fakeAsset struct {
	snapshot      *engine.AssetSnapshot
	panel         *model.IndicatorPanel
	verdict       *engine.Verdict
	forecast      *forecast.Forecast
	fetchCalls    int
	panelCalls    int
	verdictCalls  int
	forecastCalls int
}

func (a *fakeAsset) Key() string  { return "fake" }
func (a *fakeAsset) Name() string { return "Fake" }
func (a *fakeAsset) FetchSnapshot(context.Context) (*engine.AssetSnapshot, error) {
	a.fetchCalls++
	return a.snapshot, nil
}
func (a *fakeAsset) BuildPanel(*engine.AssetSnapshot) (*model.IndicatorPanel, error) {
	a.panelCalls++
	return a.panel, nil
}
func (a *fakeAsset) BuildVerdict(*model.IndicatorPanel) *engine.Verdict {
	a.verdictCalls++
	return a.verdict
}
func (a *fakeAsset) BuildForecast(*engine.AssetSnapshot, forecast.Options) (*forecast.Forecast, error) {
	a.forecastCalls++
	return a.forecast, nil
}

type fakeHistory struct{ stock []store.PricePoint }

func (h fakeHistory) LoadBTC(context.Context) ([]client.BTCDailyPoint, error) { return nil, nil }
func (h fakeHistory) LoadStock(context.Context, string, int) ([]store.PricePoint, error) {
	return append([]store.PricePoint(nil), h.stock...), nil
}

type fakeSnapshotProvider struct {
	snapshot *model.MarketSnapshot
	calls    int
}

func (p *fakeSnapshotProvider) GetSnapshot(context.Context) (*model.MarketSnapshot, error) {
	p.calls++
	return p.snapshot, nil
}

type fakeClaims struct{ calls int }

func (c *fakeClaims) RecordForecast(*forecast.Forecast, string, *model.IndicatorPanel) {
	c.calls++
}

type fakeBaselines struct{ calls int }

func (b *fakeBaselines) Annotate(*forecast.Forecast) { b.calls++ }

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func TestServiceRoutesPanelVerdictAndForecastThroughInjectedAsset(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	asset := &fakeAsset{
		snapshot: &engine.AssetSnapshot{Asset: "fake", Date: "2026-07-23", Price: 123},
		panel: &model.IndicatorPanel{
			Asset: "fake", Date: "2026-07-23",
			Technical: map[string]model.Indicator{"rsi_14": {Value: 55}},
		},
		verdict:  &engine.Verdict{Date: "2026-07-23", Stance: "neutral"},
		forecast: &forecast.Forecast{Date: "2026-07-23", CurrentPrice: 123},
	}
	service := New(Dependencies{Assets: fakeResolver{asset: asset}, Clock: fixedClock{now: now}})

	analysis, err := service.Panel(context.Background(), "fake")
	if err != nil {
		t.Fatalf("Panel: %v", err)
	}
	verdict, err := service.Verdict("fake", analysis.Panel)
	if err != nil {
		t.Fatalf("Verdict: %v", err)
	}
	gotForecast, err := service.ForecastFromSnapshot("fake", analysis.Snapshot, forecast.Options{})
	if err != nil {
		t.Fatalf("ForecastFromSnapshot: %v", err)
	}
	if !analysis.GeneratedAt.Equal(now) || verdict != asset.verdict || gotForecast != asset.forecast {
		t.Fatalf("unexpected routed result: analysis=%+v verdict=%+v forecast=%+v", analysis, verdict, gotForecast)
	}
	if asset.fetchCalls != 1 || asset.panelCalls != 1 || asset.verdictCalls != 1 || asset.forecastCalls != 1 {
		t.Fatalf("unexpected call counts: %+v", asset)
	}
}

func TestBTCPanelUsesInjectedSnapshotAndClock(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	provider := &fakeSnapshotProvider{snapshot: &model.MarketSnapshot{}}
	service := New(Dependencies{Snapshot: provider, Clock: fixedClock{now: now}})

	analysis, err := service.BTCPanel(context.Background(), BTCPanelOptions{
		Config:         &model.Config{},
		DisableHistory: true,
	})
	if err != nil {
		t.Fatalf("BTCPanel: %v", err)
	}
	if provider.calls != 1 || analysis.Snapshot != provider.snapshot || analysis.Panel == nil || !analysis.GeneratedAt.Equal(now) {
		t.Fatalf("unexpected BTC analysis: calls=%d analysis=%+v", provider.calls, analysis)
	}
}

func TestServiceDomainAndIndicatorFixedFixture(t *testing.T) {
	service := New(Dependencies{})
	panel := &model.IndicatorPanel{
		Cycle:       map[string]model.Indicator{"mayer_multiple": {Value: 0.75, Label: "低估"}},
		Valuation:   map[string]model.Indicator{},
		Network:     map[string]model.Indicator{},
		Positioning: map[string]model.Indicator{},
		Macro:       map[string]model.Indicator{},
		Flow:        map[string]model.Indicator{},
		Technical:   map[string]model.Indicator{},
		CrossAsset:  map[string]model.Indicator{},
	}

	domain, err := service.Domain(panel, "cycle")
	if err != nil {
		t.Fatalf("Domain: %v", err)
	}
	indicator, ok := service.Indicator(panel, "mayer_multiple")
	if !ok || !reflect.DeepEqual(*indicator, domain["mayer_multiple"]) {
		t.Fatalf("indicator/domain parity failed: indicator=%+v domain=%+v", indicator, domain)
	}
	if _, err := service.Domain(panel, "orders"); err == nil {
		t.Fatal("expected invalid domain error")
	}
}

func TestServiceStockForecastMatchesDirectFixedFixture(t *testing.T) {
	raw := make([]store.PricePoint, 520)
	points := make([]forecast.Point, len(raw))
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range raw {
		price := 100 + float64(i)*0.08 + float64((i%17)-8)*0.3
		date := start.AddDate(0, 0, i).Format("2006-01-02")
		raw[i] = store.PricePoint{Date: date, Close: price, Source: "fixture"}
		points[i] = forecast.Point{Date: date, Close: price, Source: "fixture"}
	}
	extractor := func(values []forecast.Point, i int) ([]forecast.FeatureValue, bool) {
		if i < 30 {
			return nil, false
		}
		momentum := values[i].Close / values[i-30].Close
		return []forecast.FeatureValue{
			{Name: "momentum_1", Value: momentum, Normalized: momentum, Weight: 1},
			{Name: "momentum_2", Value: momentum * 2, Normalized: momentum * 2, Weight: 1},
			{Name: "momentum_3", Value: momentum * 3, Normalized: momentum * 3, Weight: 1},
			{Name: "momentum_4", Value: momentum * 4, Normalized: momentum * 4, Weight: 1},
			{Name: "momentum_5", Value: momentum * 5, Normalized: momentum * 5, Weight: 1},
			{Name: "momentum_6", Value: momentum * 6, Normalized: momentum * 6, Weight: 1},
		}, true
	}
	opts := forecast.DefaultOptions()
	opts.Horizons = []int{30}
	opts.TopK = 5
	opts.StepDays = 5
	opts.MinFeatures = 6
	opts.Extractors = []forecast.FeatureExtractor{extractor}

	service := New(Dependencies{History: fakeHistory{stock: raw}})
	got, err := service.StockForecast(context.Background(), "TEST", 3650, opts)
	if err != nil {
		t.Fatalf("StockForecast: %v", err)
	}
	wantOpts := opts
	wantOpts.Asset = client.StockKey("TEST")
	want, err := forecast.Build(points, wantOpts)
	if err != nil {
		t.Fatalf("direct forecast.Build: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("application/direct forecast mismatch\ngot:  %+v\nwant: %+v", got, want)
	}
}

func TestCompleteForecastUsesInjectedEffects(t *testing.T) {
	claims := &fakeClaims{}
	baselines := &fakeBaselines{}
	service := New(Dependencies{Claims: claims, Baselines: baselines})
	service.CompleteForecast(&forecast.Forecast{}, "btc", &model.IndicatorPanel{})
	if claims.calls != 1 || baselines.calls != 1 {
		t.Fatalf("effect calls: claims=%d baselines=%d", claims.calls, baselines.calls)
	}
}
