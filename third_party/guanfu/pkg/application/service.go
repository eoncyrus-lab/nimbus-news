// Package application contains transport-independent guanfu use cases.
package application

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/client"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/engine"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast/features"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/history"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/ports"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

var ErrInvalidDomain = errors.New("invalid domain")

// Dependencies are the replaceable application boundaries. New fills nil
// fields with production adapters, so callers may override only what they test.
type Dependencies struct {
	Assets    ports.AssetResolver
	Snapshot  ports.SnapshotProvider
	History   ports.HistoryProvider
	Claims    ports.ClaimRecorder
	Baselines ports.BaselineAnnotator
	Clock     ports.Clock
}

type Service struct {
	assets    ports.AssetResolver
	snapshot  ports.SnapshotProvider
	history   ports.HistoryProvider
	claims    ports.ClaimRecorder
	baselines ports.BaselineAnnotator
	clock     ports.Clock
}

func New(deps Dependencies) *Service {
	defaults := defaultDependencies()
	if deps.Assets == nil {
		deps.Assets = defaults.Assets
	}
	if deps.History == nil {
		deps.History = defaults.History
	}
	if deps.Snapshot == nil {
		deps.Snapshot = defaults.Snapshot
	}
	if deps.Claims == nil {
		deps.Claims = defaults.Claims
	}
	if deps.Baselines == nil {
		deps.Baselines = defaults.Baselines
	}
	if deps.Clock == nil {
		deps.Clock = defaults.Clock
	}
	return &Service{
		assets: deps.Assets, snapshot: deps.Snapshot, history: deps.History, claims: deps.Claims,
		baselines: deps.Baselines, clock: deps.Clock,
	}
}

type BTCPanelOptions struct {
	Config         *model.Config
	HistoryDB      string
	DisableHistory bool
	UsePriceStore  bool
}

type BTCAnalysis struct {
	Snapshot     *model.MarketSnapshot
	Panel        *model.IndicatorPanel
	GeneratedAt  time.Time
	HistoryError error
}

// BTCPanel keeps the legacy Calculator configuration injectable while moving
// snapshot fetch and panel construction out of CLI/MCP transports.
func (s *Service) BTCPanel(ctx context.Context, opts BTCPanelOptions) (*BTCAnalysis, error) {
	snapshot, err := s.snapshot.GetSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	calc := engine.NewCalculator(opts.Config)
	if opts.UsePriceStore {
		calc = calc.WithPriceStore(&store.PriceStore{})
	}
	var historyErr error
	if !opts.DisableHistory {
		historyStore, err := history.Open(opts.HistoryDB)
		if err != nil {
			historyErr = err
		} else {
			defer historyStore.Close()
			calc = calc.WithHistory(historyStore)
		}
	}
	return &BTCAnalysis{
		Snapshot: snapshot, Panel: calc.BuildPanel(snapshot),
		GeneratedAt: s.clock.Now(), HistoryError: historyErr,
	}, nil
}

func NewDefault() *Service { return New(Dependencies{}) }

// Analysis is a panel together with the snapshot and asset implementation that
// produced it. GeneratedAt is intentionally transport metadata and is not part
// of any existing CLI or MCP payload.
type Analysis struct {
	Asset       ports.Asset
	Snapshot    *ports.AssetSnapshot
	Panel       *model.IndicatorPanel
	GeneratedAt time.Time
}

func (s *Service) Panel(ctx context.Context, asset string) (*Analysis, error) {
	a, err := s.assets.ResolveAsset(asset)
	if err != nil {
		return nil, err
	}
	snapshot, err := a.FetchSnapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch %s snapshot: %w", asset, err)
	}
	panel, err := a.BuildPanel(snapshot)
	if err != nil {
		return nil, fmt.Errorf("build %s panel: %w", asset, err)
	}
	return &Analysis{Asset: a, Snapshot: snapshot, Panel: panel, GeneratedAt: s.clock.Now()}, nil
}

func (s *Service) Verdict(asset string, panel *model.IndicatorPanel) (*engine.Verdict, error) {
	if panel == nil {
		return nil, errors.New("nil panel")
	}
	a, err := s.assets.ResolveAsset(asset)
	if err != nil {
		return nil, err
	}
	return a.BuildVerdict(panel), nil
}

func (s *Service) Forecast(ctx context.Context, asset string, opts forecast.Options) (*forecast.Forecast, error) {
	a, err := s.assets.ResolveAsset(asset)
	if err != nil {
		return nil, err
	}
	snapshot, err := a.FetchSnapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch %s snapshot: %w", asset, err)
	}
	return a.BuildForecast(snapshot, opts)
}

func (s *Service) ForecastFromSnapshot(asset string, snapshot *ports.AssetSnapshot, opts forecast.Options) (*forecast.Forecast, error) {
	a, err := s.assets.ResolveAsset(asset)
	if err != nil {
		return nil, err
	}
	return a.BuildForecast(snapshot, opts)
}

// BTCForecastFromSnapshot preserves the legacy BTC transport's explicit
// forecast options instead of applying BTCAsset's opinionated defaults.
func (s *Service) BTCForecastFromSnapshot(snapshot *model.MarketSnapshot, opts forecast.Options) (*forecast.Forecast, error) {
	points, err := forecast.PointsFromSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	if len(opts.Horizons) == 0 {
		opts.Horizons = forecast.HorizonsForAsset("btc")
	}
	opts.Asset = "btc"
	if opts.Extractors == nil {
		opts.Extractors = features.ExtractorsForAsset("btc", &store.PriceStore{})
	}
	return forecast.Build(points, opts)
}

// BTCForecast loads the legacy full daily history used by the MCP transport.
func (s *Service) BTCForecast(ctx context.Context, opts forecast.Options) (*forecast.Forecast, error) {
	points, err := s.history.LoadBTC(ctx)
	if err != nil {
		return nil, err
	}
	if len(opts.Horizons) == 0 {
		opts.Horizons = forecast.HorizonsForAsset("btc")
	}
	opts.Asset = "btc"
	if opts.Extractors == nil {
		opts.Extractors = features.ExtractorsForAsset("btc", &store.PriceStore{})
	}
	return forecast.Build(forecast.PointsFromBTCDaily(points), opts)
}

func (s *Service) StockForecast(ctx context.Context, ticker string, days int, opts forecast.Options) (*forecast.Forecast, error) {
	if days <= 0 {
		days = 3650
	}
	raw, err := s.history.LoadStock(ctx, ticker, days)
	if err != nil {
		return nil, err
	}
	if len(raw) < 200 {
		return nil, fmt.Errorf("%s: only %d data points (need >= 200)", ticker, len(raw))
	}
	points := make([]forecast.Point, len(raw))
	for i, point := range raw {
		points[i] = forecast.Point{Date: point.Date, Close: point.Close, Source: point.Source}
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Date < points[j].Date })
	opts.Asset = client.StockKey(ticker)
	if opts.Extractors == nil {
		opts.Extractors = features.ExtractorsForAsset(opts.Asset, &store.PriceStore{})
	}
	return forecast.Build(points, opts)
}

func (s *Service) AnnotateForecast(fc *forecast.Forecast) {
	s.baselines.Annotate(fc)
}

func (s *Service) RecordForecast(fc *forecast.Forecast, asset string, panel *model.IndicatorPanel) {
	s.claims.RecordForecast(fc, asset, panel)
}

func (s *Service) CompleteForecast(fc *forecast.Forecast, asset string, panel *model.IndicatorPanel) {
	s.AnnotateForecast(fc)
	s.RecordForecast(fc, asset, panel)
}

func (s *Service) Domain(panel *model.IndicatorPanel, domain string) (map[string]model.Indicator, error) {
	if panel == nil {
		return nil, errors.New("nil panel")
	}
	switch strings.ToLower(domain) {
	case "cycle":
		return panel.Cycle, nil
	case "valuation":
		return panel.Valuation, nil
	case "network":
		return panel.Network, nil
	case "positioning":
		return panel.Positioning, nil
	case "macro":
		return panel.Macro, nil
	case "flow":
		return panel.Flow, nil
	case "technical":
		return panel.Technical, nil
	case "cross_asset":
		return panel.CrossAsset, nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrInvalidDomain, domain)
	}
}

func (s *Service) Indicator(panel *model.IndicatorPanel, name string) (*model.Indicator, bool) {
	if panel == nil {
		return nil, false
	}
	for _, domain := range []map[string]model.Indicator{
		panel.Cycle, panel.Valuation, panel.Network, panel.Positioning,
		panel.Macro, panel.Flow, panel.Technical, panel.CrossAsset,
	} {
		if indicator, ok := domain[name]; ok {
			return &indicator, true
		}
	}
	return nil, false
}
