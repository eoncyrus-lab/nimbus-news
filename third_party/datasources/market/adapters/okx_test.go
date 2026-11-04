package adapters

import (
	"testing"
)

func TestOKXAdapter_CanHandle(t *testing.T) {
	adapter := NewOKXAdapter()

	tests := []struct {
		symbol   string
		expected bool
	}{
		// 应该处理
		{"BTC", true},
		{"btc", true},
		{"BTCUSDT", true},
		{"ETH", true},
		{"OKB", true},
		{"SOL", true},
		{"DOGEUSDT", true},
		{"BTC-USDT", true},

		// 不应该处理
		{"AAPL", false},
		{"^DJI", false},
		{"000001.SS", false},
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

func TestOKXAdapter_NormalizeSymbol(t *testing.T) {
	adapter := NewOKXAdapter()

	tests := []struct {
		input    string
		expected string
	}{
		{"btc", "BTC-USDT"},
		{"BTC", "BTC-USDT"},
		{"BTCUSDT", "BTC-USDT"},
		{"BTC-USDT", "BTC-USDT"},
		{"BTC/USDT", "BTC-USDT"},
		{"eth", "ETH-USDT"},
		{"ETHUSDC", "ETH-USDT"},
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

func TestOKXAdapter_Properties(t *testing.T) {
	adapter := NewOKXAdapter()

	if adapter.Name() != "okx" {
		t.Errorf("expected name 'okx', got '%s'", adapter.Name())
	}

	if adapter.Priority() != 6 {
		t.Errorf("expected priority 6, got %d", adapter.Priority())
	}

	types := adapter.SupportedTypes()
	if len(types) != 1 || types[0] != AssetTypeCrypto {
		t.Errorf("expected supported types [crypto], got %v", types)
	}
}
