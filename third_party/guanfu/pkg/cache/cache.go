package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/shopspring/decimal"
)

// Cache 缓存管理器
type Cache struct {
	data map[string]*model.MarketSnapshot
	file string
	mu   sync.RWMutex
}

// NewCache 创建新的缓存实例
func NewCache(cacheDir string) (*Cache, error) {
	if cacheDir == "" {
		cacheDir = DefaultDir()
	}
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	cacheFile := filepath.Join(cacheDir, "market_cache.json")
	cache := &Cache{
		data: make(map[string]*model.MarketSnapshot),
		file: cacheFile,
	}

	// 尝试从文件加载现有缓存
	if err := cache.loadFromFile(); err != nil {
		// 如果文件不存在，创建空缓存
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to load cache from file: %w", err)
		}
	}

	return cache, nil
}

// DefaultDir returns guanfu's runtime cache directory. CACHE_DIR remains an
// explicit override for reproducible backtests and cron jobs.
// Default places cache under ~/.guanfu/cache/ to keep all guanfu data
// (panels, history.db, market_cache.json, btc_daily_history.json) in one tree.
func DefaultDir() string {
	if dir := strings.TrimSpace(os.Getenv("CACHE_DIR")); dir != "" {
		return expandHome(dir)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".guanfu", "cache")
	}
	if dir, err := os.UserCacheDir(); err == nil && dir != "" {
		return filepath.Join(dir, "guanfu")
	}
	return "./cache"
}

// Save 保存快照到缓存
func (c *Cache) Save(snapshot *model.MarketSnapshot) error {
	if snapshot == nil {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if snapshot.SnapshotSchemaVersion == 0 {
		snapshot.SnapshotSchemaVersion = model.CurrentMarketSnapshotSchemaVersion
	}

	dateKey := snapshot.Date.Format("2006-01-02")
	c.data[dateKey] = snapshot

	return c.saveToFile()
}

// Get 获取指定日期的快照
func (c *Cache) Get(date time.Time) (*model.MarketSnapshot, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	dateKey := date.Format("2006-01-02")
	snapshot, exists := c.data[dateKey]
	if !exists {
		return nil, false
	}

	// 版本检查：schema 过期则视为未命中，触发重新拉取
	if snapshot.SnapshotSchemaVersion < model.CurrentMarketSnapshotSchemaVersion {
		return nil, false
	}
	// 返回副本以避免外部修改
	return c.copySnapshot(snapshot), true
}

// GetLatest 获取最新的快照
func (c *Cache) GetLatest() (*model.MarketSnapshot, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var latestDate time.Time
	var latestSnapshot *model.MarketSnapshot

	for dateStr, snapshot := range c.data {
		date, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}
		if date.After(latestDate) {
			latestDate = date
			latestSnapshot = snapshot
		}
	}

	if latestSnapshot == nil {
		return nil, false
	}

	return c.copySnapshot(latestSnapshot), true
}

// GetRange 获取指定日期范围内的快照
func (c *Cache) GetRange(startDate, endDate time.Time) []*model.MarketSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var snapshots []*model.MarketSnapshot
	for dateStr, snapshot := range c.data {
		date, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}
		if date.After(startDate) && date.Before(endDate) {
			snapshots = append(snapshots, c.copySnapshot(snapshot))
		}
	}

	// 按日期排序
	for i := 0; i < len(snapshots)-1; i++ {
		for j := i + 1; j < len(snapshots); j++ {
			if snapshots[i].Date.Before(snapshots[j].Date) {
				snapshots[i], snapshots[j] = snapshots[j], snapshots[i]
			}
		}
	}

	return snapshots
}

// HasDataForDate 检查指定日期是否有数据
func (c *Cache) HasDataForDate(date time.Time) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	dateKey := date.Format("2006-01-02")
	_, exists := c.data[dateKey]
	return exists
}

// loadFromFile 从文件加载缓存
func (c *Cache) loadFromFile() error {
	data, err := os.ReadFile(c.file)
	if err != nil {
		return err
	}

	var cacheData map[string]*model.MarketSnapshot
	if err := json.Unmarshal(data, &cacheData); err != nil {
		return err
	}

	c.data = cacheData
	return nil
}

// saveToFile 将缓存保存到文件
func (c *Cache) saveToFile() error {
	data, err := json.MarshalIndent(c.data, "", "  ")
	if err != nil {
		return err
	}

	return WriteFileAtomic(c.file, data, 0o644)
}

// copySnapshot 创建快照副本。先做结构体值拷贝（覆盖所有标量字段），再深拷
// 引用类型字段，避免向 caller 暴露内部切片。
//
// 改回值拷贝是为了避免每次新增 MarketSnapshot 字段都要回头补丁这个函数 —
// 原实现枚举字段，加 ETF / FRED / mempool 后曾静默丢字段。
func (c *Cache) copySnapshot(original *model.MarketSnapshot) *model.MarketSnapshot {
	if original == nil {
		return nil
	}
	snap := *original

	cloneDec := func(src []decimal.Decimal) []decimal.Decimal {
		if src == nil {
			return nil
		}
		dst := make([]decimal.Decimal, len(src))
		copy(dst, src)
		return dst
	}

	snap.BTCPriceHistory = cloneDec(original.BTCPriceHistory)
	snap.BTCVolumeHistory = cloneDec(original.BTCVolumeHistory)
	snap.ETHPriceHistory = cloneDec(original.ETHPriceHistory)
	snap.ETHVolumeHistory = cloneDec(original.ETHVolumeHistory)
	snap.TotalMarketCapHistory = cloneDec(original.TotalMarketCapHistory)
	snap.StablecoinMarketCapHistory = cloneDec(original.StablecoinMarketCapHistory)
	if original.SourceWarnings != nil {
		snap.SourceWarnings = append([]string(nil), original.SourceWarnings...)
	}

	if original.Top50Coins != nil {
		snap.Top50Coins = make([]model.CoinSnapshot, len(original.Top50Coins))
		for i, coin := range original.Top50Coins {
			snap.Top50Coins[i] = model.CoinSnapshot{
				Symbol:       coin.Symbol,
				Price:        coin.Price,
				PriceHistory: cloneDec(coin.PriceHistory),
			}
		}
	}

	return &snap
}

// WriteFileAtomic writes data to path via a same-directory temp file and rename.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func expandHome(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
