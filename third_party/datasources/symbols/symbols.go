// Package symbols is the single canonical-symbol conversion library
// (ported from the retired Python data-gateway symbols.py — same semantics).
//
// Canonical forms: US:AAPL, HK:00700, CN:600519, CRYPTO:BTC-USD, FUT:GC.
// A bare symbol without a market prefix is treated as US.
package symbols

import (
	"fmt"
	"strings"
)

// Parse splits a canonical symbol into (market, local).
func Parse(symbol string) (market, local string, err error) {
	value := strings.ToUpper(strings.TrimSpace(symbol))
	if value == "" {
		return "", "", fmt.Errorf("empty symbol")
	}
	if i := strings.Index(value, ":"); i >= 0 {
		return value[:i], value[i+1:], nil
	}
	return "US", value, nil
}

func zfill(s string, width int) string {
	for len(s) < width {
		s = "0" + s
	}
	return s
}

// ToOpenBB converts canonical → OpenBB/yfinance-style symbol.
func ToOpenBB(symbol string) (string, error) {
	market, local, err := Parse(symbol)
	if err != nil {
		return "", err
	}
	switch market {
	case "US":
		return local, nil
	case "HK":
		return zfill(local, 5) + ".HK", nil
	case "CN":
		return local + cnSuffix(local, ".SS", ".SZ"), nil
	}
	return local, nil
}

// ToYahoo converts canonical → Yahoo symbol (HK is 4-digit zero-padded, 0700.HK).
func ToYahoo(symbol string) (string, error) {
	market, local, err := Parse(symbol)
	if err != nil {
		return "", err
	}
	switch market {
	case "US", "CRYPTO":
		return local, nil
	case "HK":
		return zfill(strings.TrimLeft(local, "0"), 4) + ".HK", nil
	case "CN":
		return local + cnSuffix(local, ".SS", ".SZ"), nil
	}
	return local, nil
}

// ToFutu converts canonical → Futu symbol (US.AAPL / HK.00700 / SH.600519).
func ToFutu(symbol string) (string, error) {
	market, local, err := Parse(symbol)
	if err != nil {
		return "", err
	}
	switch market {
	case "US":
		return "US." + local, nil
	case "HK":
		return "HK." + zfill(local, 5), nil
	case "CN":
		if strings.HasPrefix(local, "6") {
			return "SH." + local, nil
		}
		return "SZ." + local, nil
	}
	return local, nil
}

// ToLongbridge converts canonical → Longbridge symbol (AAPL.US / 700.HK / 600519.SH).
func ToLongbridge(symbol string) (string, error) {
	market, local, err := Parse(symbol)
	if err != nil {
		return "", err
	}
	switch market {
	case "US":
		return local + ".US", nil
	case "HK":
		return strings.TrimLeft(local, "0") + ".HK", nil
	case "CN":
		return local + cnSuffix(local, ".SH", ".SZ"), nil
	}
	return local, nil
}

// EdgarTicker strips the market prefix; EDGAR/Finnhub/FINRA/FMP handle US tickers only.
func EdgarTicker(symbol string) (string, error) {
	market, local, err := Parse(symbol)
	if err != nil {
		return "", err
	}
	if market != "US" && market != "" {
		return "", fmt.Errorf("EDGAR only supports US tickers, got %q", symbol)
	}
	return local, nil
}

func cnSuffix(local, sh, sz string) string {
	if strings.HasPrefix(local, "6") {
		return sh
	}
	return sz
}
