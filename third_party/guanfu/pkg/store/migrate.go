// migrate.go — data migration from old cache formats to PriceStore.
//
// On first run, guanfu automatically migrates existing data:
//   - BTC: btc_daily_history.json → prices/btc.json
//   - Cross-asset: imports from Futu/Yahoo → prices/{qqq,spy,gold,uup,tlt,vixy,wti}.json
//
// Migrations are idempotent — if the target file already exists with sufficient
// data, the step is skipped.

package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// MigrateBTCHistory migrates the old btc_daily_history.json cache to prices/btc.json.
// Returns (migrated, error). If the old file does not exist or the target already
// has data, returns (false, nil).
func (s *PriceStore) MigrateBTCHistory() (bool, error) {
	// Check if target already has sufficient data
	if existing, _ := s.Load("btc"); len(existing) >= 2000 {
		return false, nil
	}

	// Find old cache file
	oldPath := findOldBTCCache()
	if oldPath == "" {
		return false, nil
	}

	points, err := loadOldBTCCache(oldPath)
	if err != nil {
		return false, fmt.Errorf("migrate BTC: read old cache: %w", err)
	}
	if len(points) == 0 {
		return false, nil
	}

	if err := s.Save("btc", points); err != nil {
		return false, fmt.Errorf("migrate BTC: save to PriceStore: %w", err)
	}
	return true, nil
}

// findOldBTCCache looks for the old btc_daily_history.json in known locations.
func findOldBTCCache() string {
	candidates := []string{}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".guanfu", "cache", "btc_daily_history.json"),
			filepath.Join(home, ".guanfu", "btc_daily_history.json"),
			filepath.Join(home, ".coinman", "cache", "btc_daily_history.json"),
		)
	}
	if path := os.Getenv("GUANFU_BTC_KLINE_CACHE"); path != "" {
		candidates = append([]string{path}, candidates...)
	}
	candidates = append(candidates, "btc_daily_history.json", "cache/btc_daily_history.json")
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// loadOldBTCCache reads the old btc_daily_history.json format.
func loadOldBTCCache(path string) ([]PricePoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Try wrapped format: {"schema_version": 1, "points": [...]}
	type oldCache struct {
		SchemaVersion int          `json:"schema_version"`
		Points        []PricePoint `json:"points"`
	}
	var wrapped oldCache
	if err := json.Unmarshal(data, &wrapped); err == nil && len(wrapped.Points) > 0 {
		return wrapped.Points, nil
	}

	// Try legacy map format: {"YYYY-MM-DD": close_price, ...}
	var legacy map[string]float64
	if err := json.Unmarshal(data, &legacy); err != nil {
		return nil, err
	}
	points := make([]PricePoint, 0, len(legacy))
	for date, close := range legacy {
		if close <= 0 {
			continue
		}
		points = append(points, PricePoint{Date: date, Close: close, Source: "legacy:kline_cache"})
	}
	return points, nil
}

// ImportFutuHistory imports QQQ/SPY/UUP/TLT/VIXY/WTI/GLD from Futu into PriceStore.
// fetcher is called with (symbol, targetDays) and should return ([]PricePoint, error).
// Skips assets that already have sufficient data.
func (s *PriceStore) ImportFutuHistory(fetcher func(symbol string, days int) ([]PricePoint, error), days int) ([]string, error) {
	symbols := []string{"QQQ", "SPY", "GLD", "UUP", "TLT", "VIXY", "USO"}
	var imported []string

	for _, sym := range symbols {
		asset := mapFutuSymToAsset(sym)
		existing, _ := s.Count(asset)
		if existing >= days-10 {
			continue
		}

		points, err := fetcher("US."+sym, days)
		if err != nil {
			return imported, fmt.Errorf("import %s: %w", sym, err)
		}
		if len(points) == 0 {
			continue
		}

		if err := s.Save(asset, points); err != nil {
			return imported, fmt.Errorf("save %s: %w", sym, err)
		}
		imported = append(imported, asset)
	}
	return imported, nil
}

// ImportETFHistory imports BIL/SHY/BND/VTI from Futu into PriceStore.
func (s *PriceStore) ImportETFHistory(fetcher func(symbol string, days int) ([]PricePoint, error), days int) ([]string, error) {
	symbols := []string{"BIL", "SHY", "BND", "VTI"}
	var imported []string

	for _, sym := range symbols {
		asset := mapFutuSymToAsset(sym)
		existing, _ := s.Count(asset)
		if existing >= days-30 {
			continue
		}

		points, err := fetcher("US."+sym, days)
		if err != nil {
			return imported, fmt.Errorf("import %s: %w", sym, err)
		}
		if len(points) == 0 {
			continue
		}

		if err := s.Save(asset, points); err != nil {
			return imported, fmt.Errorf("save %s: %w", sym, err)
		}
		imported = append(imported, asset)
	}
	return imported, nil
}

// IncrementalFetchDays returns how many days of new data are needed for an asset.
// Returns 0 if the data is fresh enough (within 1 day).
func (s *PriceStore) IncrementalFetchDays(asset string, maxDays int) int {
	lastDate, err := s.LastDate(asset)
	if err != nil || lastDate == "" {
		return maxDays // full import needed
	}
	last, err := time.Parse("2006-01-02", lastDate)
	if err != nil {
		return maxDays
	}
	daysSince := int(time.Since(last).Hours() / 24)
	if daysSince <= 1 {
		return 0 // fresh enough
	}
	if daysSince < maxDays {
		return daysSince + 3 // add buffer
	}
	return maxDays
}

// NeedsFullImport returns true if the asset has no data or very little data.
func (s *PriceStore) NeedsFullImport(asset string, minDays int) bool {
	count, _ := s.Count(asset)
	return count < minDays
}

func mapFutuSymToAsset(sym string) string {
	switch sym {
	case "USO":
		return "wti"
	case "GLD":
		return "gld"
	default:
		return sym
	}
}
