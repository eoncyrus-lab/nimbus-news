package adapters

import (
	"testing"
)

func TestBinanceAdapter_CanHandle(t *testing.T) {
	adapter := NewBinanceAdapter()

	tests := []struct {
		symbol   string
		expected bool
	}{
		// 应该处理
		{"BTC", true},
		{"BTCUSDT", true},
		{"btcusdt", true},
		{"ETH", true},
		{"ETHUSDT", true},
		{"BNB", true},
		{"SOL", true},
		{"DOGEUSDT", true},
		{"ADAUSDT", true},

		// 不应该处理
		{"AAPL", false},
		{"^DJI", false},
		{"000001.SS", false},
		{"GC=F", false},
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

func TestBinanceAdapter_NormalizeSymbol(t *testing.T) {
	adapter := NewBinanceAdapter()

	tests := []struct {
		input    string
		expected string
	}{
		// 注意: "BTC" 以 "BTC" 结尾，被当作交易对后缀处理
		{"btc", "BTC"},
		{"BTC", "BTC"},
		{"BTCUSDT", "BTCUSDT"},
		{"btcusdt", "BTCUSDT"},
		{"BTC-USDT", "BTCUSDT"},
		{"BTC/USDT", "BTCUSDT"},
		{"eth", "ETHUSDT"},
		{"ETH", "ETHUSDT"},
		{"ETHBUSD", "ETHBUSD"},
		{"ETHBTC", "ETHBTC"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := adapter.normalizeSymbol(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeSymbol(%s) = %s, expected %s", tt.input, result, tt.expected)
			}
		})
	}
}

func TestBinanceAdapter_Properties(t *testing.T) {
	adapter := NewBinanceAdapter()

	if adapter.Name() != "binance" {
		t.Errorf("expected name 'binance', got '%s'", adapter.Name())
	}

	if adapter.Priority() != 5 {
		t.Errorf("expected priority 5, got %d", adapter.Priority())
	}

	types := adapter.SupportedTypes()
	if len(types) != 1 || types[0] != AssetTypeCrypto {
		t.Errorf("expected supported types [crypto], got %v", types)
	}
}
