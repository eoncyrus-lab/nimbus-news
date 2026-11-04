package market

import "testing"

func TestToCanonical(t *testing.T) {
	cases := map[string]string{
		"AAPL":      "US:AAPL",
		"TME":       "US:TME",
		"600519.SS": "CN:600519",
		"000001.SZ": "CN:000001",
		"0700.HK":   "HK:00700",
		"00700.HK":  "HK:00700",
		"BTCUSDT":   "CRYPTO:BTC-USD",
		"ETHUSDT":   "CRYPTO:ETH-USD",
		"AP0":       "", // futures — no canonical form
		"":          "",
	}
	for in, want := range cases {
		if got := ToCanonical(in); got != want {
			t.Errorf("ToCanonical(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveCanonical(t *testing.T) {
	r := NewSymbolResolver()
	if got := r.ResolveCanonical("茅台"); got != "CN:600519" {
		t.Errorf("ResolveCanonical(茅台) = %q, want CN:600519", got)
	}
	if got := r.ResolveCanonical("腾讯"); got != "HK:00700" {
		t.Errorf("ResolveCanonical(腾讯) = %q, want HK:00700", got)
	}
	if got := r.ResolveCanonical("比特币"); got != "CRYPTO:BTC-USD" {
		t.Errorf("ResolveCanonical(比特币) = %q, want CRYPTO:BTC-USD", got)
	}
}
