// store_integration.go — syncs fetched data to PriceStore for incremental reuse.
//
// After each GetSnapshot, market data is persisted to ~/.guanfu/prices/.
// On subsequent runs, PriceStore is checked to reduce API calls:
//   - If an asset has data ending within 1 day → skip fetch
//   - If an asset has partial data → fetch only missing days
//
// This implements the Phase 0 "incremental" requirement: first run does
// full import, subsequent runs only fetch recent data.

package client

import (
	"fmt"
	"log"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
	"github.com/shopspring/decimal"
)

// SyncSnapshotToPriceStore persists the cross-asset price data from a MarketSnapshot
// into the PriceStore. Called after each successful GetSnapshot run.
func SyncSnapshotToPriceStore(snap *model.MarketSnapshot) {
	if snap == nil {
		return
	}
	s := &store.PriceStore{}

	// Sync BTC history
	if len(snap.BTCPriceHistory) > 0 {
		points := decimalHistoryToPricePoints(snap.BTCPriceHistory, snap.BTCPriceAsOf, "binance:btcusdt")
		if err := s.Append("btc", points); err != nil {
			log.Printf("PriceStore sync btc: %v", err)
		}
	}

	// Sync cross-asset histories
	syncHistoryToStore(s, "qqq", snap.QQQHistory, snap.QQQPriceAsOf, "futu:qqq")
	syncHistoryToStore(s, "spy", snap.SPYHistory, snap.SPYPriceAsOf, "futu:spy")
	syncHistoryToStore(s, "gld", snap.GoldETFHistory, snap.GoldETFAsOf, "futu:gld")
	syncHistoryToStore(s, "uup", snap.UUPHistory, snap.UUPPriceAsOf, "futu:uup")
	syncHistoryToStore(s, "tlt", snap.TLTHistory, snap.TLTPriceAsOf, "futu:tlt")
	syncHistoryToStore(s, "vixy", snap.VIXYHistory, snap.VIXYPriceAsOf, "futu:vixy")
	syncHistoryToStore(s, "wti", snap.WTIHistory, snap.WTIPriceAsOf, snap.OilPriceSource)

	// Sync gold spot (PAXG -> store as "gold")
	syncHistoryToStore(s, "gold", snap.GoldHistory, snap.GoldPriceAsOf, "binance:paxgusdt")
}

func syncHistoryToStore(s *store.PriceStore, asset string, history []decimal.Decimal, asOf string, source string) {
	if len(history) == 0 {
		return
	}
	points := decimalHistoryToPricePoints(history, asOf, source)
	if err := s.Append(asset, points); err != nil {
		log.Printf("PriceStore sync %s: %v", asset, err)
	}
}

// decimalHistoryToPricePoints converts a newest-first decimal.Decimal slice to oldest-first PricePoints.
func decimalHistoryToPricePoints(history []decimal.Decimal, asOf string, source string) []store.PricePoint {
	points := make([]store.PricePoint, len(history))
	for i, d := range history {
		date := ""
		if i == 0 && asOf != "" {
			// Try to parse asOf as the date for the newest point
			if t, err := parseAsOfDate(asOf); err == nil {
				date = t.Format("2006-01-02")
			}
		}
		close, _ := d.Float64()
		// Reverse: oldest-first
		points[len(history)-1-i] = store.PricePoint{
			Date:   date,
			Close:  close,
			Source: source,
		}
	}
	return points
}

func parseAsOfDate(asOf string) (time.Time, error) {
	formats := []string{
		"2006-01-02",
		"2006-01-02T15:04:05Z",
		time.RFC3339,
		"2006-01-02 15:04:05",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, asOf); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse date: %s", asOf)
}
