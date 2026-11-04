package adapters

import (
	"testing"
)

func TestYahooAdapter_CanHandle(t *testing.T) {
	adapter := NewYahooAdapter()

	tests := []struct {
		symbol   string
		expected bool
	}{
		// 应该处理 - 股票
		{"AAPL", true},
		{"GOOGL", true},
		{"MSFT", true},
		{"TSLA", true},

		// 应该处理 - 指数
		{"^DJI", true},
		{"^GSPC", true},
		{"^IXIC", true},
		{"^HSI", true},

		// 应该处理 - A股
		{"000001.SS", true},
		{"600519.SS", true},
		{"000858.SZ", true},

		// 应该处理 - 港股
		{"0700.HK", true},
		{"9988.HK", true},

		// 应该处理 - 期货商品
		{"GC=F", true},
		{"CL=F", true},

		// 应该处理 - 外汇
		{"EURUSD=X", true},

		// 不应该处理 - 带 USDT/BUSD 后缀的加密货币 (走 Binance)
		{"BTCUSDT", false},
		{"ETHUSDT", false},
		{"BNBBUSD", false},

		// 应该处理 - Yahoo 支持 BTC-USD 等加密货币行情
		{"BTC-USD", true},
		{"ETH-USD", true},
	}

	for _, tt := range tests {
		t.Run(tt.symbol, func(t *testing.T) {
			result := adapter.CanHandle(tt.symbol)
			if result != tt.expected {
				t.Errorf("CanHandle(%s) = %v, expected %v", tt.symbol, result, tt.expected)
			}
		})
	}
}

func TestYahooAdapter_Properties(t *testing.T) {
	adapter := NewYahooAdapter()

	if adapter.Name() != "yahoo" {
		t.Errorf("expected name 'yahoo', got '%s'", adapter.Name())
	}

	if adapter.Priority() != 10 {
		t.Errorf("expected priority 10, got %d", adapter.Priority())
	}

	types := adapter.SupportedTypes()
	expectedTypes := map[string]bool{
		AssetTypeStock:   true,
		AssetTypeIndex:   true,
		AssetTypeETF:     true,
		AssetTypeFutures: true,
		AssetTypeForex:   true,
	}

	for _, typ := range types {
		if !expectedTypes[typ] {
			t.Errorf("unexpected supported type: %s", typ)
		}
	}
}
