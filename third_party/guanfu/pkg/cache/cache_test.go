package cache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/shopspring/decimal"
)

func TestCache(t *testing.T) {
	// 创建临时目录用于测试
	tempDir := t.TempDir()
	cache, err := NewCache(tempDir)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}

	// 创建测试快照
	snapshot := &model.MarketSnapshot{
		Date:                time.Now(),
		BTCPrice:            decimal.NewFromFloat(60000.0),
		ETHPrice:            decimal.NewFromFloat(3000.0),
		TotalMarketCap:      decimal.NewFromFloat(1200000000000.0),
		StablecoinMarketCap: decimal.NewFromFloat(120000000000.0),
		BTCDominance:        decimal.NewFromFloat(0.5),
		FearGreedIndex:      decimal.NewFromFloat(70),
		AltcoinSeasonIndex:  decimal.NewFromFloat(55),
	}

	// 测试保存
	err = cache.Save(snapshot)
	if err != nil {
		t.Errorf("Failed to save snapshot: %v", err)
	}

	// 测试获取
	retrieved, exists := cache.Get(snapshot.Date)
	if !exists {
		t.Error("Snapshot not found in cache")
	} else {
		if !retrieved.BTCPrice.Equal(snapshot.BTCPrice) {
			t.Errorf("BTC price mismatch: expected %v, got %v", snapshot.BTCPrice, retrieved.BTCPrice)
		}
		if !retrieved.ETHPrice.Equal(snapshot.ETHPrice) {
			t.Errorf("ETH price mismatch: expected %v, got %v", snapshot.ETHPrice, retrieved.ETHPrice)
		}
		if retrieved.SnapshotSchemaVersion != model.CurrentMarketSnapshotSchemaVersion {
			t.Errorf("schema version mismatch: expected %d, got %d", model.CurrentMarketSnapshotSchemaVersion, retrieved.SnapshotSchemaVersion)
		}
	}

	// 测试获取最新快照
	latest, exists := cache.GetLatest()
	if !exists {
		t.Error("Latest snapshot not found in cache")
	} else {
		if !latest.BTCPrice.Equal(snapshot.BTCPrice) {
			t.Errorf("Latest snapshot BTC price mismatch: expected %v, got %v", snapshot.BTCPrice, latest.BTCPrice)
		}
	}

	// 测试HasDataForDate
	if !cache.HasDataForDate(snapshot.Date) {
		t.Error("HasDataForDate should return true for saved date")
	}

	// 测试不存在的日期
	fakeDate := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if cache.HasDataForDate(fakeDate) {
		t.Error("HasDataForDate should return false for non-existent date")
	}
}

func TestCachePersistence(t *testing.T) {
	// 创建临时目录用于测试
	tempDir := t.TempDir()

	// 创建并保存数据到缓存
	cache1, err := NewCache(tempDir)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}

	snapshot := &model.MarketSnapshot{
		Date:     time.Now(),
		BTCPrice: decimal.NewFromFloat(60000.0),
	}

	err = cache1.Save(snapshot)
	if err != nil {
		t.Fatalf("Failed to save snapshot: %v", err)
	}

	// 创建新实例并验证它可以加载先前保存的数据
	cache2, err := NewCache(tempDir)
	if err != nil {
		t.Fatalf("Failed to create second cache instance: %v", err)
	}

	retrieved, exists := cache2.Get(snapshot.Date)
	if !exists {
		t.Error("Snapshot not found in reloaded cache")
	}
	if retrieved.SnapshotSchemaVersion != model.CurrentMarketSnapshotSchemaVersion {
		t.Errorf("schema version mismatch after reload: expected %d, got %d", model.CurrentMarketSnapshotSchemaVersion, retrieved.SnapshotSchemaVersion)
	}
}

func TestDefaultDirUsesCacheDirOverride(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "custom-cache")
	t.Setenv("CACHE_DIR", dir)

	if got := DefaultDir(); got != dir {
		t.Fatalf("DefaultDir() = %q, want %q", got, dir)
	}
}

func TestSaveUsesAtomicRename(t *testing.T) {
	tempDir := t.TempDir()
	cache, err := NewCache(tempDir)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}
	if err := cache.Save(&model.MarketSnapshot{
		Date:     time.Now(),
		BTCPrice: decimal.NewFromFloat(60000.0),
	}); err != nil {
		t.Fatalf("Failed to save snapshot: %v", err)
	}

	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".tmp-market_cache.json-") {
			t.Fatalf("atomic temp file was left behind: %s", entry.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(tempDir, "market_cache.json")); err != nil {
		t.Fatalf("market_cache.json not written: %v", err)
	}
}
