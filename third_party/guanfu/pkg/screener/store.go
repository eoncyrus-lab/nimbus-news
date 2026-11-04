// Candidate persistence — JSON file cache following the PriceStore pattern.
//
// Candidates are cached per (strategy, market, date) in ~/.guanfu/screener/
// so that coarse screening results are reusable within the same day.

package screener

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
)

const defaultCacheDir = ".guanfu/screener"

// CandidateStore caches screened candidates as JSON files.
// Follows the same pattern as store.PriceStore (~/.guanfu/prices/).
type CandidateStore struct {
	dir string
}

// NewCandidateStore creates a store with the default cache directory.
func NewCandidateStore() *CandidateStore {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return &CandidateStore{dir: filepath.Join(home, defaultCacheDir)}
}

// NewCandidateStoreWithDir creates a store with a custom directory.
func NewCandidateStoreWithDir(dir string) *CandidateStore {
	return &CandidateStore{dir: dir}
}

// cacheEntry is the on-disk format for a cached screening result.
type cacheEntry struct {
	Strategy   string                    `json:"strategy"`
	Market     string                    `json:"market"`
	Date       string                    `json:"date"` // YYYY-MM-DD
	FetchedAt  string                    `json:"fetched_at"` // RFC3339
	Candidates []model.ScreenedCandidate `json:"candidates"`
	Total      int                       `json:"total"`
}

// Load returns cached candidates if they exist and are from today.
// Returns nil, nil if cache miss or stale.
func (s *CandidateStore) Load(strategy, market string) ([]model.ScreenedCandidate, error) {
	path := s.cachePath(strategy, market)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // cache miss
		}
		return nil, fmt.Errorf("read cache %s: %w", path, err)
	}

	var entry cacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, fmt.Errorf("parse cache %s: %w", path, err)
	}

	// Only serve today's cache.
	today := time.Now().Format("2006-01-02")
	if entry.Date != today {
		return nil, nil // stale
	}

	return entry.Candidates, nil
}

// Save persists candidates to the cache.
func (s *CandidateStore) Save(strategy, market string, candidates []model.ScreenedCandidate) error {
	if err := os.MkdirAll(s.dir, 0755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	entry := cacheEntry{
		Strategy:   strategy,
		Market:     market,
		Date:       time.Now().Format("2006-01-02"),
		FetchedAt:  time.Now().Format(time.RFC3339),
		Candidates: candidates,
		Total:      len(candidates),
	}

	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cache: %w", err)
	}

	path := s.cachePath(strategy, market)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write cache %s: %w", path, err)
	}
	return nil
}

func (s *CandidateStore) cachePath(strategy, market string) string {
	return filepath.Join(s.dir, fmt.Sprintf("%s_%s.json", strategy, market))
}

// Dir returns the cache directory path.
func (s *CandidateStore) Dir() string {
	return s.dir
}

// Prune removes cache files older than maxAge.
func (s *CandidateStore) Prune(maxAge time.Duration) (int, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	cutoff := time.Now().Add(-maxAge)
	removed := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			path := filepath.Join(s.dir, e.Name())
			if err := os.Remove(path); err == nil {
				removed++
			}
		}
	}
	return removed, nil
}
