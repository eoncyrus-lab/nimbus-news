// Package deribit is the independent Deribit options data source — the implied
// volatility index (DVOL) and per-option mark IV + greeks. This is new coverage
// (options analytics) that no existing first-party code had.
package deribit

import (
	"context"
	"fmt"

	"github.com/Ricaardo/nimbus-os/datasources/internal/httpx"
)

const base = "https://www.deribit.com/api/v2/public"

// DVOL is the latest implied-volatility index value for a currency.
type DVOL struct {
	Currency string  `json:"currency"`
	Value    float64 `json:"value"`
	Time     int64   `json:"time"`
}

// OptionTicker is mark price, IV and greeks for one option instrument.
type OptionTicker struct {
	Instrument string  `json:"instrument"`
	MarkPrice  float64 `json:"mark_price"`
	MarkIV     float64 `json:"mark_iv"`
	Underlying float64 `json:"underlying_price"`
	Delta      float64 `json:"delta"`
	Gamma      float64 `json:"gamma"`
	Vega       float64 `json:"vega"`
	Theta      float64 `json:"theta"`
	Rho        float64 `json:"rho"`
}

// GetDVOL returns the latest DVOL value for a currency, e.g. "BTC" or "ETH".
func GetDVOL(ctx context.Context, currency string, nowUnix int64) (*DVOL, error) {
	endMs := nowUnix * 1000
	startMs := (nowUnix - 86400) * 1000 // last 24h, hourly
	url := fmt.Sprintf("%s/get_volatility_index_data?currency=%s&start_timestamp=%d&end_timestamp=%d&resolution=3600", base, currency, startMs, endMs)
	var r struct {
		Result struct {
			Data [][]float64 `json:"data"` // [ts, open, high, low, close]
		} `json:"result"`
	}
	if err := httpx.Default.GetJSON(ctx, url, nil, &r); err != nil {
		return nil, err
	}
	if len(r.Result.Data) == 0 {
		return nil, fmt.Errorf("deribit: no DVOL data for %s", currency)
	}
	last := r.Result.Data[len(r.Result.Data)-1]
	if len(last) < 5 {
		return nil, fmt.Errorf("deribit: malformed DVOL row")
	}
	return &DVOL{Currency: currency, Value: last[4], Time: int64(last[0])}, nil
}

// GetOptionTicker returns mark IV + greeks for an option instrument,
// e.g. "BTC-25SEP26-66000-P".
func GetOptionTicker(ctx context.Context, instrument string) (*OptionTicker, error) {
	var r struct {
		Result struct {
			MarkPrice       float64 `json:"mark_price"`
			MarkIV          float64 `json:"mark_iv"`
			UnderlyingPrice float64 `json:"underlying_price"`
			Greeks          struct {
				Delta float64 `json:"delta"`
				Gamma float64 `json:"gamma"`
				Vega  float64 `json:"vega"`
				Theta float64 `json:"theta"`
				Rho   float64 `json:"rho"`
			} `json:"greeks"`
		} `json:"result"`
	}
	if err := httpx.Default.GetJSON(ctx, base+"/ticker?instrument_name="+instrument, nil, &r); err != nil {
		return nil, err
	}
	res := r.Result
	return &OptionTicker{
		Instrument: instrument,
		MarkPrice:  res.MarkPrice,
		MarkIV:     res.MarkIV,
		Underlying: res.UnderlyingPrice,
		Delta:      res.Greeks.Delta,
		Gamma:      res.Greeks.Gamma,
		Vega:       res.Greeks.Vega,
		Theta:      res.Greeks.Theta,
		Rho:        res.Greeks.Rho,
	}, nil
}
