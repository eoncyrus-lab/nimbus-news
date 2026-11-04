package market

import (
	"context"
	"fmt"
	"time"

	types "github.com/Ricaardo/nimbus-os/datasources/market/types"
)

// CachedService 带缓存和落库的行情服务包装器
type CachedService struct {
	underlying Service
	quoteStore QuoteStore
	smartCache *SmartCache
}

// CachedServiceConfig 缓存服务配置
type CachedServiceConfig struct {
	SmartCacheEnabled bool
	SmartCacheConfig  *SmartCacheConfig
}

// NewCachedService 创建带缓存的行情服务
func NewCachedService(underlying Service, quoteStore QuoteStore) *CachedService {
	return &CachedService{
		underlying: underlying,
		quoteStore: quoteStore,
		smartCache: NewSmartCache(nil), // 使用默认配置
	}
}

// NewCachedServiceWithConfig 创建带自定义配置的缓存服务
func NewCachedServiceWithConfig(underlying Service, quoteStore QuoteStore, config *CachedServiceConfig) *CachedService {
	cs := &CachedService{
		underlying: underlying,
		quoteStore: quoteStore,
	}

	if config != nil && config.SmartCacheEnabled {
		cs.smartCache = NewSmartCache(config.SmartCacheConfig)
	}

	return cs
}

// GetMarketQuote 获取行情并自动落库（使用智能缓存）
func (s *CachedService) GetMarketQuote(ctx context.Context, symbol string) (*types.MarketQuote, error) {
	// 使用智能缓存
	if s.smartCache != nil {
		return s.smartCache.GetQuote(ctx, symbol, s.fetchAndSaveQuote)
	}

	// 没有智能缓存时直接获取
	return s.fetchAndSaveQuote(ctx, symbol)
}

// fetchAndSaveQuote 获取行情并异步落库
func (s *CachedService) fetchAndSaveQuote(ctx context.Context, symbol string) (*types.MarketQuote, error) {
	quote, err := s.underlying.GetMarketQuote(ctx, symbol)
	if err != nil {
		return nil, err
	}

	// 异步落库，不阻塞返回
	if s.quoteStore != nil {
		go func() {
			if saveErr := s.quoteStore.Save(context.Background(), quote); saveErr != nil {
				fmt.Printf("Failed to save quote to store: %v\n", saveErr)
			}
		}()
	}

	return quote, nil
}

// GetMarketIndex 获取指数并自动落库
func (s *CachedService) GetMarketIndex(ctx context.Context) ([]types.IndexQuote, error) {
	indices, err := s.underlying.GetMarketIndex(ctx)
	if err != nil {
		return nil, err
	}

	// 异步落库
	if s.quoteStore != nil {
		go func() {
			for _, idx := range indices {
				quote := &types.MarketQuote{
					Symbol:        idx.Symbol,
					Name:          idx.Name,
					Price:         idx.Price,
					Change:        idx.Change,
					ChangePercent: idx.ChangePercent,
					UpdateTime:    idx.UpdateTime,
					AssetType:     "index",
				}
				s.quoteStore.Save(context.Background(), quote)
			}
		}()
	}

	return indices, nil
}

// GetSecurityAnalysis 获取技术分析
func (s *CachedService) GetSecurityAnalysis(ctx context.Context, symbol string, assetType string) (*types.SecurityAnalysis, error) {
	return s.underlying.GetSecurityAnalysis(ctx, symbol, assetType)
}

// GetAIAnalysis 获取AI分析
func (s *CachedService) GetAIAnalysis(ctx context.Context, symbol string, assetType string) (string, error) {
	return s.underlying.GetAIAnalysis(ctx, symbol, assetType)
}

// GetMarketSentiment 获取市场情绪
func (s *CachedService) GetMarketSentiment(ctx context.Context, market string) (*types.SentimentData, error) {
	return s.underlying.GetMarketSentiment(ctx, market)
}

// GetHistoricalQuotes 获取历史行情
func (s *CachedService) GetHistoricalQuotes(ctx context.Context, symbol string, interval string, rangeStr string) ([]types.KLineItem, error) {
	return s.underlying.GetHistoricalQuotes(ctx, symbol, interval, rangeStr)
}

// SearchNews 搜索新闻
func (s *CachedService) SearchNews(ctx context.Context, query string) ([]types.NewsItem, error) {
	return s.underlying.SearchNews(ctx, query)
}

// GetIPOList 获取 IPO 列表
func (s *CachedService) GetIPOList(ctx context.Context) ([]types.IPOInfo, error) {
	return s.underlying.GetIPOList(ctx)
}

// GetQuoteHistory 从本地存储获取历史行情
func (s *CachedService) GetQuoteHistory(ctx context.Context, symbol string, days int) ([]*types.QuoteRecord, error) {
	if s.quoteStore == nil {
		return nil, fmt.Errorf("quote store not available")
	}
	return s.quoteStore.GetHistory(ctx, symbol, days)
}

// GetLatestQuote 从本地存储获取最新行情（离线备用）
func (s *CachedService) GetLatestQuote(ctx context.Context, symbol string) (*types.QuoteRecord, error) {
	if s.quoteStore == nil {
		return nil, fmt.Errorf("quote store not available")
	}
	return s.quoteStore.GetLatest(ctx, symbol)
}

// GetAllTrackedSymbols 获取所有有历史记录的标的
func (s *CachedService) GetAllTrackedSymbols(ctx context.Context) ([]string, error) {
	if s.quoteStore == nil {
		return nil, fmt.Errorf("quote store not available")
	}
	return s.quoteStore.GetAllSymbols(ctx)
}

// GetPrice 获取价格（实现 alert.MarketDataProvider 接口）
func (s *CachedService) GetPrice(ctx context.Context, symbol string) (float64, error) {
	quote, err := s.GetMarketQuote(ctx, symbol)
	if err != nil {
		return 0, err
	}
	return quote.Price, nil
}

// GetMarketQuoteWithFallback 获取行情，API 失败时从本地存储获取
func (s *CachedService) GetMarketQuoteWithFallback(ctx context.Context, symbol string) (*types.MarketQuote, error) {
	// 先尝试实时获取
	quote, err := s.GetMarketQuote(ctx, symbol)
	if err == nil {
		return quote, nil
	}

	// API 失败，尝试从本地存储获取最新记录
	if s.quoteStore != nil {
		record, storeErr := s.quoteStore.GetLatest(ctx, normalizeSymbol(symbol))
		if storeErr == nil && record != nil {
			// 检查数据是否过期（超过 1 小时标记为历史数据）
			isStale := time.Since(record.Timestamp) > time.Hour
			name := record.Name
			if isStale {
				name = record.Name + " (历史)"
			}
			return &types.MarketQuote{
				Symbol:        record.Symbol,
				Name:          name,
				Price:         record.Price,
				Change:        record.Change,
				ChangePercent: record.ChangePercent,
				Volume:        record.Volume,
				High:          record.High,
				Low:           record.Low,
				Open:          record.Open,
				PrevClose:     record.PrevClose,
				MarketCap:     record.MarketCap,
				Currency:      record.Currency,
				Exchange:      record.Exchange,
				AssetType:     record.AssetType,
				UpdateTime:    record.Timestamp,
			}, nil
		}
	}

	return nil, err
}

// GetSmartCacheStats 获取智能缓存统计信息
func (s *CachedService) GetSmartCacheStats() *SmartCacheStats {
	if s.smartCache == nil {
		return nil
	}
	stats := s.smartCache.Stats()
	return &stats
}

// InvalidateCache 使指定标的的缓存失效
func (s *CachedService) InvalidateCache(symbol string) {
	if s.smartCache != nil {
		s.smartCache.Invalidate(symbol)
	}
}

// ClearCache 清空所有缓存
func (s *CachedService) ClearCache() {
	if s.smartCache != nil {
		s.smartCache.Clear()
	}
}

// GetUSMarketLiquidity 获取美股市场流动性
func (s *CachedService) GetUSMarketLiquidity(ctx context.Context) (*USMarketLiquidity, error) {
	if ms, ok := s.underlying.(*MarketService); ok {
		return ms.GetUSMarketLiquidity(ctx)
	}
	return nil, fmt.Errorf("underlying service does not support GetUSMarketLiquidity")
}

// GetUSIndexSnapshot 获取美股主要指数快照
func (s *CachedService) GetUSIndexSnapshot(ctx context.Context) (map[string]*types.IndexQuote, error) {
	if ms, ok := s.underlying.(*MarketService); ok {
		return ms.GetUSIndexSnapshot(ctx)
	}
	return nil, fmt.Errorf("underlying service does not support GetUSIndexSnapshot")
}

// GetSectorRotation 获取板块轮动数据
func (s *CachedService) GetSectorRotation(ctx context.Context) ([]SectorETF, error) {
	if ms, ok := s.underlying.(*MarketService); ok {
		return ms.GetSectorRotation(ctx)
	}
	return nil, fmt.Errorf("underlying service does not support GetSectorRotation")
}

// AnalyzeSectorRotation 分析板块轮动
func (s *CachedService) AnalyzeSectorRotation(ctx context.Context) (*SectorRotationReport, error) {
	if ms, ok := s.underlying.(*MarketService); ok {
		return ms.AnalyzeSectorRotation(ctx)
	}
	return nil, fmt.Errorf("underlying service does not support AnalyzeSectorRotation")
}
