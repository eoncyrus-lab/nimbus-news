// Package ports defines the external capabilities used by the application
// layer. The existing engine and client interfaces remain the source of truth;
// aliases keep current callers and implementations compatible.
package ports

import (
	"context"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/client"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/engine"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

type Asset = engine.Asset
type AssetSnapshot = engine.AssetSnapshot
type SnapshotProvider = client.DataProvider

// AssetResolver isolates the application layer from the global engine registry.
type AssetResolver interface {
	ResolveAsset(key string) (Asset, error)
}

// HistoryProvider supplies the two historical series used by forecast use cases.
type HistoryProvider interface {
	LoadBTC(ctx context.Context) ([]client.BTCDailyPoint, error)
	LoadStock(ctx context.Context, ticker string, days int) ([]store.PricePoint, error)
}

// ClaimRecorder persists forecast evidence. Implementations must keep failures
// non-fatal because claims are auxiliary to the user-visible forecast.
type ClaimRecorder interface {
	RecordForecast(fc *forecast.Forecast, asset string, panel *model.IndicatorPanel)
}

// BaselineAnnotator adds passive comparison data to a forecast.
type BaselineAnnotator interface {
	Annotate(fc *forecast.Forecast)
}

// Clock makes application timestamps deterministic in tests.
type Clock interface {
	Now() time.Time
}
