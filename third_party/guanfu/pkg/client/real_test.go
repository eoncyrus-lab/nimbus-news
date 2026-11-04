package client

import (
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"

	"github.com/shopspring/decimal"
)

func TestUsableCachedSnapshotValidatesSchemaAndFreshness(t *testing.T) {
	valid := &model.MarketSnapshot{
		Date:                  time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
		SnapshotSchemaVersion: model.CurrentMarketSnapshotSchemaVersion,
		FetchedAt:             time.Now().UTC().Format(time.RFC3339),
		BTCPrice:              decimal.NewFromInt(100000),
		BTCPriceAsOf:          "2026-05-02",
		BTCPriceHistory:       make([]decimal.Decimal, btcMinFullHistoryDays(time.Now())),
	}

	if ok, reason := usableCachedSnapshot(valid); !ok {
		t.Fatalf("expected valid cached snapshot, got reason %q", reason)
	}

	oldSchema := *valid
	oldSchema.SnapshotSchemaVersion = model.CurrentMarketSnapshotSchemaVersion - 1
	if ok, _ := usableCachedSnapshot(&oldSchema); ok {
		t.Fatal("expected old schema cache to be rejected")
	}

	missingAsOf := *valid
	missingAsOf.BTCPriceAsOf = ""
	if ok, _ := usableCachedSnapshot(&missingAsOf); ok {
		t.Fatal("expected cache without BTC as-of timestamp to be rejected")
	}

	shortHistory := *valid
	shortHistory.BTCPriceHistory = make([]decimal.Decimal, 3000)
	if ok, _ := usableCachedSnapshot(&shortHistory); ok {
		t.Fatal("expected cache with Binance-era-only BTC history to be rejected")
	}

	stale := *valid
	stale.FetchedAt = time.Now().Add(-marketCacheMaxAge - time.Minute).UTC().Format(time.RFC3339)
	if ok, _ := usableCachedSnapshot(&stale); ok {
		t.Fatal("expected stale intraday cache to be rejected")
	}
}

func TestCalculateAltcoinSeasonAllowsRealZero(t *testing.T) {
	btc := make([]decimal.Decimal, 91)
	for i := range btc {
		btc[i] = decimal.NewFromInt(100)
	}
	btc[0] = decimal.NewFromInt(200)

	coins := make([]model.CoinSnapshot, 10)
	for i := range coins {
		history := make([]decimal.Decimal, 91)
		for j := range history {
			history[j] = decimal.NewFromInt(100)
		}
		history[0] = decimal.NewFromInt(150)
		coins[i] = model.CoinSnapshot{Symbol: "ALT", PriceHistory: history}
	}

	got, ok := calculateAltcoinSeason(coins, btc)
	if !ok {
		t.Fatal("expected altcoin season to be available")
	}
	if !got.IsZero() {
		t.Fatalf("altcoin season = %s, want 0", got)
	}
}
