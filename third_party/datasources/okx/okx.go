// Package okx is the independent OKX data source. Beyond the spot ticker the old
// news code used, it exposes candles and the previously unused derivatives
// surface: funding rate, open interest, and the long/short account ratio
// (OKX "rubik" stats).
package okx

import (
	"context"
	"fmt"
	"strconv"

	"github.com/Ricaardo/nimbus-os/datasources/internal/httpx"
)

// base is a var (not const) so tests can point it at an httptest server.
var base = "https://www.okx.com"

// Ticker is a spot/contract snapshot.
type Ticker struct {
	InstID    string  `json:"inst_id"`
	Last      float64 `json:"last"`
	Open24h   float64 `json:"open_24h"`
	High24h   float64 `json:"high_24h"`
	Low24h    float64 `json:"low_24h"`
	Vol24h    float64 `json:"vol_24h"`     // 24h volume in base ccy
	VolCcy24h float64 `json:"vol_ccy_24h"` // 24h turnover in quote ccy (USD)
	ChangePct float64 `json:"change_pct"`
}

// Candle is one OHLCV bar.
type Candle struct {
	Time   int64   `json:"time"`
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume float64 `json:"volume"`
}

// Funding is the current perpetual funding rate.
type Funding struct {
	InstID      string  `json:"inst_id"`
	Rate        float64 `json:"funding_rate"`
	NextRate    float64 `json:"next_funding_rate"`
	NextFunding int64   `json:"next_funding_time"`
}

// OpenInterest is current OI for a contract.
type OpenInterest struct {
	InstID string  `json:"inst_id"`
	OI     float64 `json:"open_interest"`     // in contracts
	OICcy  float64 `json:"open_interest_ccy"` // in base ccy
	Time   int64   `json:"time"`
}

// Level is one order-book price level.
type Level struct {
	Price float64 `json:"price"`
	Qty   float64 `json:"qty"` // base-ccy size resting at Price
}

// OrderBook is a depth snapshot. Bids are descending, asks ascending. Carries
// pure liquidity helpers for 流动性评估.
type OrderBook struct {
	InstID string  `json:"inst_id"`
	Bids   []Level `json:"bids"`
	Asks   []Level `json:"asks"`
	Time   int64   `json:"time"`
}

// LongShort is one long/short account-ratio observation (ascending).
type LongShort struct {
	Ccy   string  `json:"ccy"`
	Ratio float64 `json:"long_short_ratio"`
	Time  int64   `json:"time"`
}

// okxResp is the standard v5 envelope.
type okxResp[T any] struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
	Data T      `json:"data"`
}

func get[T any](ctx context.Context, url string) (T, error) {
	var r okxResp[T]
	var zero T
	if err := httpx.Default.GetJSON(ctx, url, nil, &r); err != nil {
		return zero, err
	}
	if r.Code != "0" {
		return zero, fmt.Errorf("okx %s: %s", r.Code, r.Msg)
	}
	return r.Data, nil
}

// GetTicker returns the ticker for an instId, e.g. "BTC-USDT".
func GetTicker(ctx context.Context, instID string) (*Ticker, error) {
	data, err := get[[]struct {
		InstID    string `json:"instId"`
		Last      string `json:"last"`
		Open24h   string `json:"open24h"`
		High24h   string `json:"high24h"`
		Low24h    string `json:"low24h"`
		Vol24h    string `json:"vol24h"`
		VolCcy24h string `json:"volCcy24h"`
	}](ctx, base+"/api/v5/market/ticker?instId="+instID)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("okx: no ticker for %s", instID)
	}
	d := data[0]
	last, open := atof(d.Last), atof(d.Open24h)
	chg := 0.0
	if open != 0 {
		chg = (last - open) / open * 100
	}
	return &Ticker{InstID: d.InstID, Last: last, Open24h: open, High24h: atof(d.High24h), Low24h: atof(d.Low24h), Vol24h: atof(d.Vol24h), VolCcy24h: atof(d.VolCcy24h), ChangePct: chg}, nil
}

// GetCandles returns OHLCV bars ASCENDING. bar e.g. "1D","1H". instId e.g. "BTC-USDT".
func GetCandles(ctx context.Context, instID, bar string, limit int) ([]Candle, error) {
	if limit <= 0 {
		limit = 200
	}
	url := fmt.Sprintf("%s/api/v5/market/candles?instId=%s&bar=%s&limit=%d", base, instID, bar, limit)
	data, err := get[[][]string](ctx, url)
	if err != nil {
		return nil, err
	}
	out := make([]Candle, 0, len(data))
	for i := len(data) - 1; i >= 0; i-- { // OKX returns newest-first → reverse to ascending
		r := data[i]
		if len(r) < 6 {
			continue
		}
		t, _ := strconv.ParseInt(r[0], 10, 64)
		out = append(out, Candle{Time: t, Open: atof(r[1]), High: atof(r[2]), Low: atof(r[3]), Close: atof(r[4]), Volume: atof(r[5])})
	}
	return out, nil
}

// GetFunding returns the current funding rate for a perp, e.g. "BTC-USDT-SWAP".
func GetFunding(ctx context.Context, instID string) (*Funding, error) {
	data, err := get[[]struct {
		InstID          string `json:"instId"`
		FundingRate     string `json:"fundingRate"`
		NextFundingRate string `json:"nextFundingRate"`
		NextFundingTime string `json:"nextFundingTime"`
	}](ctx, base+"/api/v5/public/funding-rate?instId="+instID)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("okx: no funding for %s", instID)
	}
	d := data[0]
	nt, _ := strconv.ParseInt(d.NextFundingTime, 10, 64)
	return &Funding{InstID: d.InstID, Rate: atof(d.FundingRate), NextRate: atof(d.NextFundingRate), NextFunding: nt}, nil
}

// GetOpenInterest returns current OI for a perp, e.g. instType "SWAP", instId "BTC-USDT-SWAP".
func GetOpenInterest(ctx context.Context, instType, instID string) (*OpenInterest, error) {
	data, err := get[[]struct {
		InstID string `json:"instId"`
		OI     string `json:"oi"`
		OICcy  string `json:"oiCcy"`
		TS     string `json:"ts"`
	}](ctx, fmt.Sprintf("%s/api/v5/public/open-interest?instType=%s&instId=%s", base, instType, instID))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("okx: no OI for %s", instID)
	}
	d := data[0]
	ts, _ := strconv.ParseInt(d.TS, 10, 64)
	return &OpenInterest{InstID: d.InstID, OI: atof(d.OI), OICcy: atof(d.OICcy), Time: ts}, nil
}

// LongShortRatio returns the long/short account ratio series (ascending) for a
// currency, e.g. ccy "BTC", period "1D".
func LongShortRatio(ctx context.Context, ccy, period string) ([]LongShort, error) {
	url := fmt.Sprintf("%s/api/v5/rubik/stat/contracts/long-short-account-ratio?ccy=%s&period=%s", base, ccy, period)
	data, err := get[[][]string](ctx, url)
	if err != nil {
		return nil, err
	}
	out := make([]LongShort, 0, len(data))
	for i := len(data) - 1; i >= 0; i-- { // newest-first → ascending
		r := data[i]
		if len(r) < 2 {
			continue
		}
		t, _ := strconv.ParseInt(r[0], 10, 64)
		out = append(out, LongShort{Ccy: ccy, Ratio: atof(r[1]), Time: t})
	}
	return out, nil
}

// GetOrderBook returns a depth snapshot for an instId, e.g. "BTC-USDT". sz is
// levels per side (OKX caps /books at 400); <=0 defaults 100.
func GetOrderBook(ctx context.Context, instID string, sz int) (*OrderBook, error) {
	if sz <= 0 {
		sz = 100
	}
	if sz > 400 {
		sz = 400 // OKX hard caps /books at 400 levels; clamp to avoid a 400 error
	}
	url := fmt.Sprintf("%s/api/v5/market/books?instId=%s&sz=%d", base, instID, sz)
	data, err := get[[]struct {
		Asks [][]string `json:"asks"`
		Bids [][]string `json:"bids"`
		TS   string     `json:"ts"`
	}](ctx, url)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("okx: no order book for %s", instID)
	}
	d := data[0]
	ts, _ := strconv.ParseInt(d.TS, 10, 64)
	return &OrderBook{InstID: instID, Bids: toLevels(d.Bids), Asks: toLevels(d.Asks), Time: ts}, nil
}

func toLevels(rows [][]string) []Level {
	out := make([]Level, 0, len(rows))
	for _, r := range rows {
		if len(r) < 2 {
			continue
		}
		out = append(out, Level{Price: atof(r[0]), Qty: atof(r[1])})
	}
	return out
}

// BestBid / BestAsk return the top of book (ok=false if that side is empty).
func (ob *OrderBook) BestBid() (Level, bool) {
	if len(ob.Bids) == 0 {
		return Level{}, false
	}
	return ob.Bids[0], true
}

func (ob *OrderBook) BestAsk() (Level, bool) {
	if len(ob.Asks) == 0 {
		return Level{}, false
	}
	return ob.Asks[0], true
}

// MidPrice is the mid of best bid and best ask (0 if either side empty).
func (ob *OrderBook) MidPrice() float64 {
	bb, okb := ob.BestBid()
	ba, oka := ob.BestAsk()
	if !okb || !oka {
		return 0
	}
	return (bb.Price + ba.Price) / 2
}

// SpreadBps is the bid/ask spread in basis points of the mid (0 if either side
// empty). Negative would indicate a crossed/locked book.
func (ob *OrderBook) SpreadBps() float64 {
	bb, okb := ob.BestBid()
	ba, oka := ob.BestAsk()
	mid := ob.MidPrice()
	if !okb || !oka || mid == 0 {
		return 0
	}
	return (ba.Price - bb.Price) / mid * 1e4
}

// BidNotional / AskNotional sum Price*Qty across all returned levels, i.e. the
// quote-currency liquidity resting within the snapshot depth.
func (ob *OrderBook) BidNotional() float64 { return notional(ob.Bids) }
func (ob *OrderBook) AskNotional() float64 { return notional(ob.Asks) }

func notional(ls []Level) float64 {
	var sum float64
	for _, l := range ls {
		sum += l.Price * l.Qty
	}
	return sum
}

func atof(s string) float64 { f, _ := strconv.ParseFloat(s, 64); return f }
