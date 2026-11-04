package market

import (
	"context"

	types "github.com/Ricaardo/nimbus-os/datasources/market/types"
)

// Service 行情数据服务接口
type Service interface {
	// GetMarketQuote 获取实时行情
	GetMarketQuote(ctx context.Context, symbol string) (*types.MarketQuote, error)
	// GetMarketIndex 获取主要市场指数
	GetMarketIndex(ctx context.Context) ([]types.IndexQuote, error)
	// GetSecurityAnalysis 获取证券技术分析
	GetSecurityAnalysis(ctx context.Context, symbol string, assetType string) (*types.SecurityAnalysis, error)
	// GetAIAnalysis 获取AI分析
	GetAIAnalysis(ctx context.Context, symbol string, assetType string) (string, error)
	// GetMarketSentiment 获取市场情绪
	GetMarketSentiment(ctx context.Context, market string) (*types.SentimentData, error)
	// GetHistoricalQuotes 获取历史行情
	GetHistoricalQuotes(ctx context.Context, symbol string, interval string, rangeStr string) ([]types.KLineItem, error)
	// SearchNews 搜索新闻
	SearchNews(ctx context.Context, query string) ([]types.NewsItem, error)
	// GetIPOList 获取IPO列表
	GetIPOList(ctx context.Context) ([]types.IPOInfo, error)
	// GetUSMarketLiquidity 获取美股市场流动性
	GetUSMarketLiquidity(ctx context.Context) (*USMarketLiquidity, error)
	// GetUSIndexSnapshot 获取美股主要指数快照
	GetUSIndexSnapshot(ctx context.Context) (map[string]*types.IndexQuote, error)
	// GetSectorRotation 获取板块轮动数据
	GetSectorRotation(ctx context.Context) ([]SectorETF, error)
	// AnalyzeSectorRotation 分析板块轮动
	AnalyzeSectorRotation(ctx context.Context) (*SectorRotationReport, error)
}

// LLMMessage / LLMProvider — minimal seam so this package does not depend on
// the news platform's llm package. news wraps its provider in a tiny adapter.
type LLMMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type LLMProvider interface {
	Chat(ctx context.Context, messages []LLMMessage) (string, error)
}

// QuoteStore — persistence seam for CachedService; satisfied by the news
// platform's bolt-backed store once its record type aliases types.QuoteRecord.
type QuoteStore interface {
	Save(ctx context.Context, quote *types.MarketQuote) error
	GetHistory(ctx context.Context, symbol string, days int) ([]*types.QuoteRecord, error)
	GetLatest(ctx context.Context, symbol string) (*types.QuoteRecord, error)
	GetAllSymbols(ctx context.Context) ([]string, error)
}
