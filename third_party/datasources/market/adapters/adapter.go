// Package adapters 提供行情数据源适配器
package adapters

import (
	"context"
	"net/http"
	"time"

	types "github.com/Ricaardo/nimbus-os/datasources/market/types"
)

// QuoteAdapter 行情数据适配器接口
type QuoteAdapter interface {
	// Name 返回适配器名称
	Name() string

	// Priority 返回优先级（数值越小越优先）
	Priority() int

	// SupportedTypes 返回支持的资产类型
	SupportedTypes() []string

	// CanHandle 判断是否能处理该符号
	CanHandle(symbol string) bool

	// GetQuote 获取行情数据
	GetQuote(ctx context.Context, symbol string) (*types.MarketQuote, error)
}

// KLineAdapter K线数据适配器接口
type KLineAdapter interface {
	QuoteAdapter

	// GetKLines 获取K线数据
	GetKLines(ctx context.Context, symbol string, interval string, limit int) ([]types.KLineItem, error)
}

// 资产类型常量
const (
	AssetTypeCrypto  = "crypto"
	AssetTypeStock   = "stock"
	AssetTypeForex   = "forex"
	AssetTypeFutures = "futures"
	AssetTypeIndex   = "index"
	AssetTypeETF     = "etf"
)

// BaseAdapter 基础适配器（提供默认实现）
type BaseAdapter struct {
	name     string
	priority int
	client   *http.Client
}

// NewBaseAdapter 创建基础适配器
func NewBaseAdapter(name string, priority int) BaseAdapter {
	return BaseAdapter{
		name:     name,
		priority: priority,
		client:   newHTTPClient(),
	}
}

// Name 返回适配器名称
func (a *BaseAdapter) Name() string {
	return a.name
}

// Priority 返回优先级
func (a *BaseAdapter) Priority() int {
	return a.priority
}

// Client 获取 HTTP 客户端
func (a *BaseAdapter) Client() *http.Client {
	return a.client
}

// newHTTPClient 创建带连接池的 HTTP 客户端
func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}
}

// DefaultUserAgent 默认 User-Agent
const DefaultUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// AdapterRegistry 适配器注册表
type AdapterRegistry struct {
	adapters []QuoteAdapter
}

// NewAdapterRegistry 创建适配器注册表
func NewAdapterRegistry() *AdapterRegistry {
	return &AdapterRegistry{
		adapters: make([]QuoteAdapter, 0),
	}
}

// Register 注册适配器
func (r *AdapterRegistry) Register(adapter QuoteAdapter) {
	r.adapters = append(r.adapters, adapter)
	// 按优先级排序
	for i := len(r.adapters) - 1; i > 0; i-- {
		if r.adapters[i].Priority() < r.adapters[i-1].Priority() {
			r.adapters[i], r.adapters[i-1] = r.adapters[i-1], r.adapters[i]
		}
	}
}

// FindAdapter 查找能处理该符号的适配器
func (r *AdapterRegistry) FindAdapter(symbol string) QuoteAdapter {
	for _, adapter := range r.adapters {
		if adapter.CanHandle(symbol) {
			return adapter
		}
	}
	return nil
}

// FindAdapters 查找所有能处理该符号的适配器
func (r *AdapterRegistry) FindAdapters(symbol string) []QuoteAdapter {
	var result []QuoteAdapter
	for _, adapter := range r.adapters {
		if adapter.CanHandle(symbol) {
			result = append(result, adapter)
		}
	}
	return result
}

// All 返回所有适配器
func (r *AdapterRegistry) All() []QuoteAdapter {
	return r.adapters
}
