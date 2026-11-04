// Asset interface + registry — the core abstraction for multi-asset support.
//
// Every asset (BTC, QQQ, SPY, Gold) implements this interface.
// The registry maps asset keys ("btc", "qqq", ...) to implementations,
// enabling CLI subcommand routing and MCP tool dispatch.
//
// Design:
//   - FetchSnapshot: pull all data needed for the asset (prices, fundamentals, etc.)
//   - BuildPanel: compute all indicators from the snapshot into an IndicatorPanel
//   - BuildVerdict: synthesize the panel into a structured verdict
//   - BuildForecast: run kNN analogue matching for forward-looking inference
//
// NOT in the interface: scoring, trade signals, allocation advice.

package engine

import (
	"context"
	"fmt"
	"sync"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
)

// Asset is the core interface for any asset analyzable by guanfu.
type Asset interface {
	Key() string
	Name() string

	// FetchSnapshot fetches all data needed for this asset (prices, indicators, etc.)
	FetchSnapshot(ctx context.Context) (*AssetSnapshot, error)

	// BuildPanel computes an indicator panel from the snapshot.
	BuildPanel(snap *AssetSnapshot) (*model.IndicatorPanel, error)

	// BuildVerdict synthesizes the panel into a structured verdict.
	BuildVerdict(panel *model.IndicatorPanel) *Verdict

	// BuildForecast runs kNN analogue matching for forward-looking inference.
	// Returns nil if forecasting is not supported for this asset.
	BuildForecast(snap *AssetSnapshot, opts forecast.Options) (*forecast.Forecast, error)
}

// AssetSnapshot holds the data needed to build a panel for a specific asset.
// This is a generic container — each asset populates the fields it needs.
type AssetSnapshot struct {
	Asset     string  `json:"asset"`
	Date      string  `json:"date"`
	Price     float64 `json:"price"`
	PriceAsOf string  `json:"price_as_of,omitempty"`

	// Price history (newest-first, for MA/indicator computation)
	PriceHistory []float64 `json:"price_history,omitempty"`

	// Cross-asset prices (for multi-asset correlation / comparison)
	CrossAssetPrices map[string]float64 `json:"cross_asset_prices,omitempty"`

	// Valuation data
	PE float64 `json:"pe,omitempty"` // QQQ/SPY
	PB float64 `json:"pb,omitempty"`

	// Macro context
	RealYield10Y float64 `json:"real_yield_10y,omitempty"`
	DXY          float64 `json:"dxy,omitempty"`
	VIX          float64 `json:"vix,omitempty"`

	// BTC-specific (populated by BTCAsset, ignored by others)
	BTCMarketSnapshot *model.MarketSnapshot `json:"btc_market_snapshot,omitempty"`

	// Source health / warnings
	Warnings []string `json:"warnings,omitempty"`
}

// AssetRegistry maps asset keys to implementations (thread-safe).
var (
	assetRegistry   = make(map[string]Asset)
	assetRegistryMu sync.RWMutex
)

// Register adds an asset implementation to the global registry.
func Register(a Asset) {
	assetRegistryMu.Lock()
	defer assetRegistryMu.Unlock()
	assetRegistry[a.Key()] = a
}

// GetAsset returns the registered asset implementation by key, or error if not found.
func GetAsset(key string) (Asset, error) {
	assetRegistryMu.RLock()
	defer assetRegistryMu.RUnlock()
	a, ok := assetRegistry[key]
	if !ok {
		return nil, fmt.Errorf("unknown asset: %s (available: %v)", key, RegisteredKeys())
	}
	return a, nil
}

// RegisteredKeys returns all registered asset keys.
func RegisteredKeys() []string {
	assetRegistryMu.RLock()
	defer assetRegistryMu.RUnlock()
	keys := make([]string, 0, len(assetRegistry))
	for k := range assetRegistry {
		keys = append(keys, k)
	}
	return keys
}
