package client

import (
	"strings"
	"testing"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

func TestStockKey(t *testing.T) {
	cases := map[string]string{
		"AAPL":    "stock_aapl",
		"  msft ": "stock_msft",
		"BRK.B":   "stock_brk.b",
	}
	for in, want := range cases {
		if got := StockKey(in); got != want {
			t.Errorf("StockKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateStockTickerRejectsCollisions(t *testing.T) {
	dir := t.TempDir()
	s := &store.PriceStore{Dir: dir}
	if err := s.Save("btc", []store.PricePoint{{Date: "2020-01-01", Close: 1}}); err != nil {
		t.Fatalf("setup save: %v", err)
	}
	if err := s.Save("vixy", []store.PricePoint{{Date: "2020-01-01", Close: 20}}); err != nil {
		t.Fatalf("setup save: %v", err)
	}

	cases := []struct {
		ticker  string
		wantErr string
	}{
		{"BTC", "conflicts"},   // core asset
		{"vixy", "conflicts"},  // feature data
		{"", "empty"},          // empty
		{"AA PL", "invalid"},   // whitespace inside
		{"NEW/SYM", "invalid"}, // slash
	}
	for _, c := range cases {
		err := ValidateStockTicker(s, c.ticker)
		if err == nil {
			t.Errorf("ValidateStockTicker(%q) expected error, got nil", c.ticker)
			continue
		}
		if !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("ValidateStockTicker(%q) = %v, want substring %q", c.ticker, err, c.wantErr)
		}
	}

	if err := ValidateStockTicker(s, "AAPL"); err != nil {
		t.Errorf("ValidateStockTicker(AAPL) unexpected error: %v", err)
	}
}

func TestStockNamespaceDoesNotShadowItself(t *testing.T) {
	// Once stock_aapl is saved, importing "AAPL" again must not be rejected
	// (the existing key is "stock_aapl", not "aapl").
	dir := t.TempDir()
	s := &store.PriceStore{Dir: dir}
	if err := s.Save(StockKey("AAPL"), []store.PricePoint{{Date: "2020-01-01", Close: 100}}); err != nil {
		t.Fatalf("setup save: %v", err)
	}
	if err := ValidateStockTicker(s, "AAPL"); err != nil {
		t.Errorf("re-importing AAPL after caching should be fine; got %v", err)
	}
}
