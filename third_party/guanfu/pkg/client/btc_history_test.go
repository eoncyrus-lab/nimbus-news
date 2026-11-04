package client

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/shopspring/decimal"
)

func TestMergeBTCDailyHistoryUpsertsAndSorts(t *testing.T) {
	base := []BTCDailyPoint{
		{Date: "2026-05-01", Close: decimal.NewFromInt(100), Source: "old"},
		{Date: BTCFullHistoryStart, Close: decimal.NewFromFloat(0.08584), Source: "coinmetrics"},
	}
	updates := []BTCDailyPoint{
		{Date: "2026-05-01", Close: decimal.NewFromInt(110), Source: "binance"},
		{Date: "2026-05-02", Close: decimal.NewFromInt(120), Source: "binance"},
	}

	got := mergeBTCDailyHistory(base, updates)
	if len(got) != 3 {
		t.Fatalf("merged length = %d, want 3", len(got))
	}
	if got[0].Date != BTCFullHistoryStart {
		t.Fatalf("first date = %s, want %s", got[0].Date, BTCFullHistoryStart)
	}
	if got[1].Close.String() != "110" || got[1].Source != "binance" {
		t.Fatalf("update did not override duplicate date: %+v", got[1])
	}
	if got[2].Date != "2026-05-02" {
		t.Fatalf("last date = %s, want 2026-05-02", got[2].Date)
	}
}

func TestBTCDailyHistoryCoversFullRange(t *testing.T) {
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	points := syntheticBTCDailyPoints(btcFullHistoryStartDate(), btcLatestRequiredDate(now))

	if ok, reason := btcDailyHistoryCoversFullRange(points, now); !ok {
		t.Fatalf("expected full range to be accepted: %s", reason)
	}
	if ok, _ := btcDailyHistoryCoversFullRange(points[100:], now); ok {
		t.Fatal("expected history missing 2010 start to be rejected")
	}
	if ok, _ := btcDailyHistoryCoversFullRange(points[:3000], now); ok {
		t.Fatal("expected short Binance-era-only history to be rejected")
	}
}

func TestApplyBTCDailyHistoryToSnapshotUsesNewestFirst(t *testing.T) {
	points := []BTCDailyPoint{
		{Date: BTCFullHistoryStart, Close: decimal.NewFromFloat(0.08584)},
		{Date: "2026-05-01", Close: decimal.NewFromInt(100), Volume: decimal.NewFromInt(10)},
		{Date: "2026-05-02", Close: decimal.NewFromInt(120), Volume: decimal.NewFromInt(12)},
	}
	snap := &model.MarketSnapshot{}

	if err := applyBTCDailyHistoryToSnapshot(points, snap); err != nil {
		t.Fatal(err)
	}
	if snap.BTCPrice.String() != "120" {
		t.Fatalf("BTCPrice = %s, want latest close 120", snap.BTCPrice)
	}
	if snap.BTCPriceHistory[0].String() != "120" || snap.BTCPriceHistory[2].String() != "0.08584" {
		t.Fatalf("BTCPriceHistory order should be newest-first, got %+v", snap.BTCPriceHistory)
	}
	if snap.BTCVolume24h.String() != "12" {
		t.Fatalf("BTCVolume24h = %s, want 12", snap.BTCVolume24h)
	}
	if snap.BTCPriceAsOf != "2026-05-02T00:00:00Z" {
		t.Fatalf("BTCPriceAsOf = %s", snap.BTCPriceAsOf)
	}
}

func TestBTCDailyHistoryCacheRoundTripAndLegacyMap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "btc_daily_history.json")
	points := []BTCDailyPoint{
		{Date: BTCFullHistoryStart, Close: decimal.NewFromFloat(0.08584)},
		{Date: "2026-05-02", Close: decimal.NewFromInt(120)},
	}
	if err := SaveBTCDailyHistoryCache(path, points); err != nil {
		t.Fatal(err)
	}
	got, err := LoadBTCDailyHistoryCache(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Date != BTCFullHistoryStart || got[1].Date != "2026-05-02" {
		t.Fatalf("unexpected round-trip cache: %+v", got)
	}

	legacy := filepath.Join(dir, "legacy.json")
	if err := os.WriteFile(legacy, []byte(`{"2026-05-02":120,"2010-07-18":0.08584}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = LoadBTCDailyHistoryCache(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Date != BTCFullHistoryStart {
		t.Fatalf("legacy cache not loaded as sorted daily points: %+v", got)
	}
}

func syntheticBTCDailyPoints(from, to time.Time) []BTCDailyPoint {
	var out []BTCDailyPoint
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		out = append(out, BTCDailyPoint{
			Date:   d.Format("2006-01-02"),
			Close:  decimal.NewFromInt(100),
			Source: "test",
		})
	}
	return out
}
