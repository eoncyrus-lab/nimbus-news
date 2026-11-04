// Package store provides a JSON-based incremental price archive for all assets.
//
// Each asset is stored as an oldest-first array of PricePoint under
// ~/.guanfu/prices/{asset}.json, with a companion meta.json tracking
// last_date, count, source, and updated_at.
//
// Usage:
//
//	s := store.PriceStore{}
//	points, _ := s.Load("btc")
//	s.Append("btc", newPoints)
//	lastDate, _ := s.LastDate("btc")
package store

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/cache"
)

// PricePoint is a single daily closing price for any asset.
type PricePoint struct {
	Date   string  `json:"date"`
	Close  float64 `json:"close"`
	Source string  `json:"source,omitempty"`
}

// PriceStore manages per-asset price archives under ~/.guanfu/prices/.
type PriceStore struct {
	Dir string // defaults to ~/.guanfu/prices/
}

// DefaultPricesDir returns the default price store directory.
func DefaultPricesDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".guanfu", "prices")
	}
	return "./prices"
}

func (s *PriceStore) dir() string {
	if s.Dir != "" {
		return s.Dir
	}
	return DefaultPricesDir()
}

func (s *PriceStore) path(asset string) string {
	return filepath.Join(s.dir(), asset+".json")
}

func (s *PriceStore) metaPath() string {
	return filepath.Join(s.dir(), "meta.json")
}

// Load reads all price points for an asset, oldest-first.
// Returns nil slice and no error if the file does not exist.
func (s *PriceStore) Load(asset string) ([]PricePoint, error) {
	data, err := os.ReadFile(s.path(asset))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var points []PricePoint
	if err := json.Unmarshal(data, &points); err != nil {
		return nil, err
	}
	return NormalizePricePoints(points), nil
}

// LoadSeries reads a generic indicator series, allowing zero and negative
// values. Use this for rates, spreads, skew, and other non-price data.
func (s *PriceStore) LoadSeries(asset string) ([]PricePoint, error) {
	data, err := os.ReadFile(s.path(asset))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var points []PricePoint
	if err := json.Unmarshal(data, &points); err != nil {
		return nil, err
	}
	return NormalizeSeriesPoints(points), nil
}

// Save writes all price points (oldest-first) to the asset's JSON file.
func (s *PriceStore) Save(asset string, points []PricePoint) error {
	points = NormalizePricePoints(points)
	if err := os.MkdirAll(s.dir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(points, "", "  ")
	if err != nil {
		return err
	}
	if err := cache.WriteFileAtomic(s.path(asset), data, 0o644); err != nil {
		return err
	}
	return s.updateMeta(asset, points)
}

// SaveSeries writes a generic indicator series, allowing zero and negative
// values. It shares the same on-disk format as Save.
func (s *PriceStore) SaveSeries(asset string, points []PricePoint) error {
	points = NormalizeSeriesPoints(points)
	if err := os.MkdirAll(s.dir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(points, "", "  ")
	if err != nil {
		return err
	}
	if err := cache.WriteFileAtomic(s.path(asset), data, 0o644); err != nil {
		return err
	}
	return s.updateMeta(asset, points)
}

// Append merges new points into the existing archive, deduplicating by date.
// Newer values overwrite older ones for the same date.
func (s *PriceStore) Append(asset string, newPoints []PricePoint) error {
	existing, _ := s.Load(asset)
	merged := NormalizePricePoints(append(existing, newPoints...))
	return s.Save(asset, merged)
}

// AppendSeries merges generic indicator points into the existing archive.
func (s *PriceStore) AppendSeries(asset string, newPoints []PricePoint) error {
	existing, _ := s.LoadSeries(asset)
	merged := NormalizeSeriesPoints(append(existing, newPoints...))
	return s.SaveSeries(asset, merged)
}

// LastDate returns the latest date in the asset archive, or empty string if empty.
func (s *PriceStore) LastDate(asset string) (string, error) {
	points, err := s.Load(asset)
	if err != nil || len(points) == 0 {
		return "", err
	}
	return points[len(points)-1].Date, nil
}

// Count returns the number of price points for an asset.
func (s *PriceStore) Count(asset string) (int, error) {
	points, err := s.Load(asset)
	if err != nil {
		return 0, err
	}
	return len(points), nil
}

// CountSeries returns the number of points in a generic indicator series.
func (s *PriceStore) CountSeries(asset string) (int, error) {
	points, err := s.LoadSeries(asset)
	if err != nil {
		return 0, err
	}
	return len(points), nil
}

// LatestSeries returns the most recent point from a generic indicator series.
func (s *PriceStore) LatestSeries(asset string) (PricePoint, bool) {
	points, err := s.LoadSeries(asset)
	if err != nil || len(points) == 0 {
		return PricePoint{}, false
	}
	return points[len(points)-1], true
}

// LoadHistory loads an asset's closes as []float64, newest-first (matching MarketSnapshot convention).
func (s *PriceStore) LoadHistory(asset string) ([]float64, error) {
	points, err := s.Load(asset)
	if err != nil || len(points) == 0 {
		return nil, err
	}
	n := len(points)
	history := make([]float64, n)
	for i, p := range points {
		history[n-1-i] = p.Close
	}
	return history, nil
}

// Latest returns the most recent PricePoint, or zero value if empty.
func (s *PriceStore) Latest(asset string) (PricePoint, bool) {
	points, err := s.Load(asset)
	if err != nil || len(points) == 0 {
		return PricePoint{}, false
	}
	return points[len(points)-1], true
}

// updateMeta writes the meta.json tracking file.
func (s *PriceStore) updateMeta(asset string, points []PricePoint) error {
	meta, err := s.loadMeta()
	if err != nil || meta == nil {
		meta = make(map[string]AssetMeta)
	}
	entry := AssetMeta{
		Count:     len(points),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if len(points) > 0 {
		entry.LastDate = points[len(points)-1].Date
		if len(points) > 0 && points[0].Source != "" {
			entry.Source = points[0].Source
		}
	}
	meta[asset] = entry
	return s.saveMeta(meta)
}

// loadMeta reads meta.json.
func (s *PriceStore) loadMeta() (map[string]AssetMeta, error) {
	data, err := os.ReadFile(s.metaPath())
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]AssetMeta), nil
		}
		return nil, err
	}
	var meta map[string]AssetMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	if meta == nil {
		meta = make(map[string]AssetMeta)
	}
	return meta, nil
}

// saveMeta writes meta.json.
func (s *PriceStore) saveMeta(meta map[string]AssetMeta) error {
	if err := os.MkdirAll(s.dir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return cache.WriteFileAtomic(s.metaPath(), data, 0o644)
}

// GetMeta returns the meta entry for an asset, or false if not found.
func (s *PriceStore) GetMeta(asset string) (AssetMeta, bool) {
	meta, err := s.loadMeta()
	if err != nil {
		return AssetMeta{}, false
	}
	m, ok := meta[asset]
	return m, ok
}

// ListAssets returns all asset keys in meta.json.
func (s *PriceStore) ListAssets() ([]string, error) {
	meta, err := s.loadMeta()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	keys := make([]string, 0, len(meta))
	for k := range meta {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

// DaysSinceLastUpdate returns the number of days since the asset was last updated.
// Returns -1 if no meta entry exists.
func (s *PriceStore) DaysSinceLastUpdate(asset string) int {
	m, ok := s.GetMeta(asset)
	if !ok || m.LastDate == "" {
		return -1
	}
	last, err := time.Parse("2006-01-02", m.LastDate)
	if err != nil {
		return -1
	}
	return int(time.Since(last).Hours() / 24)
}

// PriceHead returns the most recent n points for incremental comparison.
// The returned data is oldest-first.
func (s *PriceStore) PriceHead(asset string, n int) ([]PricePoint, error) {
	points, err := s.Load(asset)
	if err != nil || len(points) == 0 {
		return nil, err
	}
	if len(points) < n {
		return points, nil
	}
	return points[len(points)-n:], nil
}

// NormalizePricePoints deduplicates by date, filters invalid closes, and sorts oldest-first.
func NormalizePricePoints(points []PricePoint) []PricePoint {
	if len(points) == 0 {
		return points
	}
	byDate := make(map[string]PricePoint, len(points))
	for _, p := range points {
		if p.Close <= 0 {
			continue
		}
		byDate[p.Date] = p
	}
	out := make([]PricePoint, 0, len(byDate))
	for _, p := range byDate {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

// NormalizeSeriesPoints deduplicates by date, filters NaN/Inf values, and
// sorts oldest-first. Unlike NormalizePricePoints, signed and zero values are
// valid because many macro/positioning series are not prices.
func NormalizeSeriesPoints(points []PricePoint) []PricePoint {
	if len(points) == 0 {
		return points
	}
	byDate := make(map[string]PricePoint, len(points))
	for _, p := range points {
		if p.Date == "" || math.IsNaN(p.Close) || math.IsInf(p.Close, 0) {
			continue
		}
		byDate[p.Date] = p
	}
	out := make([]PricePoint, 0, len(byDate))
	for _, p := range byDate {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}
