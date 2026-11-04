package symbols

import "testing"

// Cases mirror the retired Python data_gateway/symbols.py semantics exactly.
func TestConversions(t *testing.T) {
	cases := []struct {
		in                                     string
		openbb, yahoo, futu, longbridge string
	}{
		{"US:AAPL", "AAPL", "AAPL", "US.AAPL", "AAPL.US"},
		{"AAPL", "AAPL", "AAPL", "US.AAPL", "AAPL.US"},          // bare = US
		{"us:aapl", "AAPL", "AAPL", "US.AAPL", "AAPL.US"},       // case-insensitive
		{"HK:00700", "00700.HK", "0700.HK", "HK.00700", "700.HK"},
		{"HK:700", "00700.HK", "0700.HK", "HK.00700", "700.HK"}, // zero-padding
		{"CN:600519", "600519.SS", "600519.SS", "SH.600519", "600519.SH"},
		{"CN:000001", "000001.SZ", "000001.SZ", "SZ.000001", "000001.SZ"},
		{"CRYPTO:BTC-USD", "BTC-USD", "BTC-USD", "BTC-USD", "BTC-USD"},
	}
	for _, c := range cases {
		if got, _ := ToOpenBB(c.in); got != c.openbb {
			t.Errorf("ToOpenBB(%q) = %q, want %q", c.in, got, c.openbb)
		}
		if got, _ := ToYahoo(c.in); got != c.yahoo {
			t.Errorf("ToYahoo(%q) = %q, want %q", c.in, got, c.yahoo)
		}
		if got, _ := ToFutu(c.in); got != c.futu {
			t.Errorf("ToFutu(%q) = %q, want %q", c.in, got, c.futu)
		}
		if got, _ := ToLongbridge(c.in); got != c.longbridge {
			t.Errorf("ToLongbridge(%q) = %q, want %q", c.in, got, c.longbridge)
		}
	}
}

func TestEdgarTicker(t *testing.T) {
	if got, err := EdgarTicker("US:NVDA"); err != nil || got != "NVDA" {
		t.Errorf("EdgarTicker(US:NVDA) = %q, %v", got, err)
	}
	if got, err := EdgarTicker("nvda"); err != nil || got != "NVDA" {
		t.Errorf("EdgarTicker(nvda) = %q, %v", got, err)
	}
	if _, err := EdgarTicker("HK:00700"); err == nil {
		t.Error("EdgarTicker(HK:00700) should error")
	}
}

func TestParseEmpty(t *testing.T) {
	if _, _, err := Parse("  "); err == nil {
		t.Error("Parse(blank) should error")
	}
}
