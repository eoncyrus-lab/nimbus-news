package market

import (
	"context"
	"sync"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/cache"
	types "github.com/Ricaardo/nimbus-os/datasources/market/types"
)

// MarketType 市场类型
type MarketType int

const (
	MarketTypeUnknown   MarketType = iota
	MarketTypeCrypto               // 加密货币 (24/7)
	MarketTypeUS                   // 美股
	MarketTypeHK                   // 港股
	MarketTypeCN                   // A股
	MarketTypeJP                   // 日股
	MarketTypeEU                   // 欧洲
	MarketTypeCommodity            // 期货商品
	MarketTypeForex                // 外汇
)

// MarketStatus 市场状态
type MarketStatus int

const (
	MarketStatusUnknown    MarketStatus = iota
	MarketStatusOpen                    // 交易中
	MarketStatusClosed                  // 休市
	MarketStatusPreMarket               // 盘前
	MarketStatusAfterHours              // 盘后
)

// SmartCacheConfig 智能缓存配置
type SmartCacheConfig struct {
	TradingTTL      time.Duration // 交易时段 TTL
	ClosedTTL       time.Duration // 休市 TTL
	PreMarketTTL    time.Duration // 盘前 TTL
	AfterHoursTTL   time.Duration // 盘后 TTL
	CryptoTTL       time.Duration // 加密货币 TTL (24/7)
	CacheSize       int           // 缓存容量
	EnablePrefetch  bool          // 是否启用预取
	PrefetchSymbols []string      // 预取标的列表
}

// DefaultSmartCacheConfig 默认智能缓存配置
func DefaultSmartCacheConfig() *SmartCacheConfig {
	return &SmartCacheConfig{
		TradingTTL:     30 * time.Second, // 交易时段 30 秒
		ClosedTTL:      10 * time.Minute, // 休市 10 分钟
		PreMarketTTL:   2 * time.Minute,  // 盘前 2 分钟
		AfterHoursTTL:  2 * time.Minute,  // 盘后 2 分钟
		CryptoTTL:      30 * time.Second, // 加密货币 30 秒
		CacheSize:      2000,             // 缓存 2000 条
		EnablePrefetch: false,
	}
}

// SmartCache 智能行情缓存
type SmartCache struct {
	cache  *cache.LRUCache
	config *SmartCacheConfig
	mu     sync.RWMutex

	// 缓存统计
	smartHits   int64
	smartMisses int64
}

// NewSmartCache 创建智能缓存
func NewSmartCache(config *SmartCacheConfig) *SmartCache {
	if config == nil {
		config = DefaultSmartCacheConfig()
	}

	// 使用最长 TTL 作为底层缓存的默认 TTL
	maxTTL := config.ClosedTTL
	if config.CryptoTTL > maxTTL {
		maxTTL = config.CryptoTTL
	}

	return &SmartCache{
		cache:  cache.NewLRUCache(config.CacheSize, maxTTL),
		config: config,
	}
}

// GetQuote 获取行情（智能缓存）
func (sc *SmartCache) GetQuote(ctx context.Context, symbol string, fetcher func(ctx context.Context, symbol string) (*types.MarketQuote, error)) (*types.MarketQuote, error) {
	cacheKey := "smart:" + symbol

	// 检查缓存
	if cached, ok := sc.cache.Get(cacheKey); ok {
		sc.mu.Lock()
		sc.smartHits++
		sc.mu.Unlock()
		return cached.(*types.MarketQuote), nil
	}

	sc.mu.Lock()
	sc.smartMisses++
	sc.mu.Unlock()

	// 调用底层获取
	quote, err := fetcher(ctx, symbol)
	if err != nil {
		return nil, err
	}

	// 根据市场状态决定 TTL
	ttl := sc.determineTTL(symbol, quote)
	sc.cache.SetWithTTL(cacheKey, quote, ttl)

	return quote, nil
}

// determineTTL 根据市场状态确定 TTL
func (sc *SmartCache) determineTTL(symbol string, quote *types.MarketQuote) time.Duration {
	marketType := sc.detectMarketType(symbol, quote)

	// 加密货币 24/7 交易，使用固定 TTL
	if marketType == MarketTypeCrypto {
		return sc.config.CryptoTTL
	}

	// 获取市场状态
	status := sc.getMarketStatus(marketType)

	switch status {
	case MarketStatusOpen:
		return sc.config.TradingTTL
	case MarketStatusPreMarket:
		return sc.config.PreMarketTTL
	case MarketStatusAfterHours:
		return sc.config.AfterHoursTTL
	default:
		return sc.config.ClosedTTL
	}
}

// detectMarketType 检测市场类型
func (sc *SmartCache) detectMarketType(symbol string, quote *types.MarketQuote) MarketType {
	// 根据交易所判断
	if quote != nil {
		switch quote.Exchange {
		case "Binance", "OKX", "Binance Alpha":
			return MarketTypeCrypto
		case "NYSE", "NASDAQ", "NYQ", "NMS", "PCX":
			return MarketTypeUS
		case "HKG", "HKSE":
			return MarketTypeHK
		case "SHH", "SHZ", "Shanghai", "Shenzhen":
			return MarketTypeCN
		case "TYO", "JPX":
			return MarketTypeJP
		case "FRA", "XETRA", "LSE":
			return MarketTypeEU
		case "COMEX", "NYMEX", "CME":
			return MarketTypeCommodity
		case "CCY", "FOREX":
			return MarketTypeForex
		}
	}

	// 根据符号模式判断
	switch {
	case isCryptoSymbol(symbol):
		return MarketTypeCrypto
	case isUSSymbol(symbol):
		return MarketTypeUS
	case isHKSymbol(symbol):
		return MarketTypeHK
	case isCNSymbol(symbol):
		return MarketTypeCN
	case isCommoditySymbol(symbol):
		return MarketTypeCommodity
	case isForexSymbol(symbol):
		return MarketTypeForex
	default:
		return MarketTypeUnknown
	}
}

// getMarketStatus 获取市场状态
func (sc *SmartCache) getMarketStatus(marketType MarketType) MarketStatus {
	now := time.Now()

	switch marketType {
	case MarketTypeCrypto:
		return MarketStatusOpen // 24/7

	case MarketTypeUS:
		return sc.getUSMarketStatus(now)

	case MarketTypeHK:
		return sc.getHKMarketStatus(now)

	case MarketTypeCN:
		return sc.getCNMarketStatus(now)

	case MarketTypeCommodity:
		// 商品期货交易时间复杂，简化为工作日交易
		if isWeekend(now) {
			return MarketStatusClosed
		}
		return MarketStatusOpen

	case MarketTypeForex:
		// 外汇周一到周五 24 小时交易
		if isWeekend(now) {
			return MarketStatusClosed
		}
		return MarketStatusOpen

	default:
		return MarketStatusUnknown
	}
}

// getUSMarketStatus 获取美股市场状态
func (sc *SmartCache) getUSMarketStatus(now time.Time) MarketStatus {
	// 转换到美东时间
	loc, _ := time.LoadLocation("America/New_York")
	nyTime := now.In(loc)

	if isWeekend(nyTime) {
		return MarketStatusClosed
	}

	hour := nyTime.Hour()
	minute := nyTime.Minute()
	timeVal := hour*100 + minute

	switch {
	case timeVal >= 400 && timeVal < 930:
		return MarketStatusPreMarket
	case timeVal >= 930 && timeVal < 1600:
		return MarketStatusOpen
	case timeVal >= 1600 && timeVal < 2000:
		return MarketStatusAfterHours
	default:
		return MarketStatusClosed
	}
}

// getHKMarketStatus 获取港股市场状态
func (sc *SmartCache) getHKMarketStatus(now time.Time) MarketStatus {
	// 转换到香港时间
	loc, _ := time.LoadLocation("Asia/Hong_Kong")
	hkTime := now.In(loc)

	if isWeekend(hkTime) {
		return MarketStatusClosed
	}

	hour := hkTime.Hour()
	minute := hkTime.Minute()
	timeVal := hour*100 + minute

	switch {
	case timeVal >= 900 && timeVal < 930:
		return MarketStatusPreMarket // 开市竞价
	case (timeVal >= 930 && timeVal < 1200) || (timeVal >= 1300 && timeVal < 1600):
		return MarketStatusOpen
	case timeVal >= 1600 && timeVal < 1610:
		return MarketStatusAfterHours // 收市竞价
	default:
		return MarketStatusClosed
	}
}

// getCNMarketStatus 获取 A 股市场状态
func (sc *SmartCache) getCNMarketStatus(now time.Time) MarketStatus {
	// 转换到北京时间
	loc, _ := time.LoadLocation("Asia/Shanghai")
	cnTime := now.In(loc)

	if isWeekend(cnTime) {
		return MarketStatusClosed
	}

	hour := cnTime.Hour()
	minute := cnTime.Minute()
	timeVal := hour*100 + minute

	switch {
	case timeVal >= 915 && timeVal < 930:
		return MarketStatusPreMarket // 集合竞价
	case (timeVal >= 930 && timeVal < 1130) || (timeVal >= 1300 && timeVal < 1500):
		return MarketStatusOpen
	case timeVal >= 1457 && timeVal < 1500:
		return MarketStatusAfterHours // 收盘集合竞价
	default:
		return MarketStatusClosed
	}
}

// 辅助函数

func isWeekend(t time.Time) bool {
	weekday := t.Weekday()
	return weekday == time.Saturday || weekday == time.Sunday
}

func isCryptoSymbol(symbol string) bool {
	// USDT 结尾或常见加密货币格式
	if len(symbol) > 4 {
		suffix := symbol[len(symbol)-4:]
		if suffix == "USDT" || suffix == "-USD" {
			return true
		}
	}
	return false
}

func isUSSymbol(symbol string) bool {
	// 美股通常是 1-5 个大写字母
	if len(symbol) >= 1 && len(symbol) <= 5 {
		for _, c := range symbol {
			if c < 'A' || c > 'Z' {
				return false
			}
		}
		return true
	}
	return false
}

func isHKSymbol(symbol string) bool {
	// 港股格式: XXXX.HK 或 0XXXX.HK
	if len(symbol) > 3 && symbol[len(symbol)-3:] == ".HK" {
		return true
	}
	return false
}

func isCNSymbol(symbol string) bool {
	// A 股格式: XXXXXX.SS 或 XXXXXX.SZ
	if len(symbol) > 3 {
		suffix := symbol[len(symbol)-3:]
		if suffix == ".SS" || suffix == ".SZ" {
			return true
		}
	}
	return false
}

func isCommoditySymbol(symbol string) bool {
	// 期货格式: XX=F
	if len(symbol) > 2 && symbol[len(symbol)-2:] == "=F" {
		return true
	}
	return false
}

func isForexSymbol(symbol string) bool {
	// 外汇格式: XXXXXX=X
	if len(symbol) > 2 && symbol[len(symbol)-2:] == "=X" {
		return true
	}
	return false
}

// Stats 返回智能缓存统计
func (sc *SmartCache) Stats() SmartCacheStats {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	cacheStats := sc.cache.Stats()
	var hitRate float64
	total := sc.smartHits + sc.smartMisses
	if total > 0 {
		hitRate = float64(sc.smartHits) / float64(total)
	}

	return SmartCacheStats{
		SmartHits:     sc.smartHits,
		SmartMisses:   sc.smartMisses,
		SmartHitRate:  hitRate,
		CacheSize:     cacheStats.Size,
		CacheCapacity: cacheStats.Capacity,
	}
}

// SmartCacheStats 智能缓存统计
type SmartCacheStats struct {
	SmartHits     int64
	SmartMisses   int64
	SmartHitRate  float64
	CacheSize     int
	CacheCapacity int
}

// Invalidate 使缓存失效
func (sc *SmartCache) Invalidate(symbol string) {
	sc.cache.Delete("smart:" + symbol)
}

// Clear 清空缓存
func (sc *SmartCache) Clear() {
	sc.cache.Clear()
}

// GetMarketStatusString 获取市场状态描述
func GetMarketStatusString(status MarketStatus) string {
	switch status {
	case MarketStatusOpen:
		return "交易中"
	case MarketStatusClosed:
		return "休市"
	case MarketStatusPreMarket:
		return "盘前"
	case MarketStatusAfterHours:
		return "盘后"
	default:
		return "未知"
	}
}

// GetMarketTypeString 获取市场类型描述
func GetMarketTypeString(marketType MarketType) string {
	switch marketType {
	case MarketTypeCrypto:
		return "加密货币"
	case MarketTypeUS:
		return "美股"
	case MarketTypeHK:
		return "港股"
	case MarketTypeCN:
		return "A股"
	case MarketTypeJP:
		return "日股"
	case MarketTypeEU:
		return "欧股"
	case MarketTypeCommodity:
		return "商品期货"
	case MarketTypeForex:
		return "外汇"
	default:
		return "未知"
	}
}
