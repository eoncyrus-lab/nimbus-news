package adapters

import (
	"testing"
)

func TestAdapterRegistry_Register(t *testing.T) {
	registry := NewAdapterRegistry()

	binance := NewBinanceAdapter()
	okx := NewOKXAdapter()
	yahoo := NewYahooAdapter()

	registry.Register(binance) // priority 5
	registry.Register(yahoo)   // priority 10
	registry.Register(okx)     // priority 6

	// 验证按优先级排序
	adapters := registry.All()
	if len(adapters) != 3 {
		t.Errorf("expected 3 adapters, got %d", len(adapters))
	}

	// Binance (5) < OKX (6) < Yahoo (10)
	if adapters[0].Name() != "binance" {
		t.Errorf("expected first adapter to be binance, got %s", adapters[0].Name())
	}
	if adapters[1].Name() != "okx" {
		t.Errorf("expected second adapter to be okx, got %s", adapters[1].Name())
	}
	if adapters[2].Name() != "yahoo" {
		t.Errorf("expected third adapter to be yahoo, got %s", adapters[2].Name())
	}
}

func TestAdapterRegistry_FindAdapter(t *testing.T) {
	registry := NewAdapterRegistry()
	registry.Register(NewBinanceAdapter())
	registry.Register(NewOKXAdapter())
	registry.Register(NewYahooAdapter())

	tests := []struct {
		symbol       string
		expectedName string
	}{
		{"BTCUSDT", "binance"},
		{"BTC", "binance"},
		{"ETHUSDT", "binance"},
		{"AAPL", "yahoo"},
		{"^DJI", "yahoo"},
		{"000001.SS", "yahoo"},
	}

	for _, tt := range tests {
		t.Run(tt.symbol, func(t *testing.T) {
			adapter := registry.FindAdapter(tt.symbol)
			if adapter == nil {
				t.Errorf("no adapter found for symbol: %s", tt.symbol)
				return
			}
			if adapter.Name() != tt.expectedName {
				t.Errorf("expected adapter %s for symbol %s, got %s", tt.expectedName, tt.symbol, adapter.Name())
			}
		})
	}
}

func TestAdapterRegistry_FindAdapters(t *testing.T) {
	registry := NewAdapterRegistry()
	registry.Register(NewBinanceAdapter())
	registry.Register(NewOKXAdapter())
	registry.Register(NewYahooAdapter())

	// BTC 应该同时被 Binance 和 OKX 处理
	adapters := registry.FindAdapters("BTC")
	if len(adapters) < 2 {
		t.Errorf("expected at least 2 adapters for BTC, got %d", len(adapters))
	}

	// AAPL 只应该被 Yahoo 处理
	adapters = registry.FindAdapters("AAPL")
	if len(adapters) != 1 {
		t.Errorf("expected 1 adapter for AAPL, got %d", len(adapters))
	}
}
