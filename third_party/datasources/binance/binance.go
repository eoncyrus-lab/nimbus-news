// Package binance is the independent Binance data source. Beyond the spot
// ticker/klines that the old news/guanfu code used, it exposes the previously
// unused derivatives-positioning surface (funding rate, open interest,
// long/short account ratio, taker buy/sell volume) from the USDⓈ-M futures API.
package binance

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/internal/httpx"
)

// spotBase/fapiBase are vars (not const) so tests can point them at httptest.
var (
	spotBase = "https://api.binance.com"
	fapiBase = "https://fapi.binance.com"
)

// perReqCap is Binance's max klines rows per request.
const perReqCap = 1000

// Ticker is a 24h spot snapshot.
type Ticker struct {
	Symbol      string  `json:"symbol"`
	Last        float64 `json:"last"`
	Open        float64 `json:"open"`
	High        float64 `json:"high"`
	Low         float64 `json:"low"`
	ChangePct   float64 `json:"change_pct"`
	Volume      float64 `json:"volume"`       // 24h volume in base asset
	QuoteVolume float64 `json:"quote_volume"` // 24h turnover in quote (USDT)
}

// Kline is one OHLCV bar.
type Kline struct {
	OpenTime int64   `json:"open_time"`
	Open     float64 `json:"open"`
	High     float64 `json:"high"`
	Low      float64 `json:"low"`
	Close    float64 `json:"close"`
	Volume   float64 `json:"volume"`
}

// Funding is the current perpetual funding rate (USDM perp).
type Funding struct {
	Symbol      string  `json:"symbol"`
	Rate        float64 `json:"funding_rate"`
	MarkPrice   float64 `json:"mark_price"`
	NextFunding int64   `json:"next_funding_time"`
}

// OpenInterest is current open interest (contracts) for a perp.
type OpenInterest struct {
	Symbol string  `json:"symbol"`
	OI     float64 `json:"open_interest"`
	Time   int64   `json:"time"`
}

// LongShort is one global long/short account-ratio observation.
type LongShort struct {
	Symbol    string  `json:"symbol"`
	Ratio     float64 `json:"long_short_ratio"`
	LongAcct  float64 `json:"long_account"`
	ShortAcct float64 `json:"short_account"`
	Time      int64   `json:"time"`
}

// Level is one order-book price level.
type Level struct {
	Price float64 `json:"price"`
	Qty   float64 `json:"qty"` // base-asset quantity resting at Price
}

// OrderBook is a depth snapshot. Bids are descending, asks ascending, as the
// exchange returns them. Carries pure liquidity helpers for 流动性评估.
type OrderBook struct {
	Symbol       string  `json:"symbol"`
	Bids         []Level `json:"bids"`
	Asks         []Level `json:"asks"`
	LastUpdateID int64   `json:"last_update_id"`
}

// Ticker24h returns the spot 24h snapshot for e.g. "BTCUSDT".
func Ticker24h(ctx context.Context, symbol string) (*Ticker, error) {
	var raw struct {
		LastPrice, OpenPrice, HighPrice, LowPrice, Volume, QuoteVolume, PriceChangePercent string
	}
	if err := httpx.Default.GetJSON(ctx, spotBase+"/api/v3/ticker/24hr?symbol="+symbol, nil, &raw); err != nil {
		return nil, err
	}
	return &Ticker{
		Symbol:      symbol,
		Last:        atof(raw.LastPrice),
		Open:        atof(raw.OpenPrice),
		High:        atof(raw.HighPrice),
		Low:         atof(raw.LowPrice),
		ChangePct:   atof(raw.PriceChangePercent),
		Volume:      atof(raw.Volume),
		QuoteVolume: atof(raw.QuoteVolume),
	}, nil
}

// Klines returns spot OHLCV bars (ascending). interval e.g. "1d", "1h".
func Klines(ctx context.Context, symbol, interval string, limit int) ([]Kline, error) {
	if limit <= 0 {
		limit = 200
	}
	url := fmt.Sprintf("%s/api/v3/klines?symbol=%s&interval=%s&limit=%d", spotBase, symbol, interval, limit)
	var rows [][]any
	if err := httpx.Default.GetJSON(ctx, url, nil, &rows); err != nil {
		return nil, err
	}
	out := make([]Kline, 0, len(rows))
	for _, r := range rows {
		if len(r) < 6 {
			continue
		}
		out = append(out, Kline{
			OpenTime: int64(toF(r[0])),
			Open:     atof(str(r[1])),
			High:     atof(str(r[2])),
			Low:      atof(str(r[3])),
			Close:    atof(str(r[4])),
			Volume:   atof(str(r[5])),
		})
	}
	return out, nil
}

// KlinesDeep returns up to `days` daily spot bars (ascending), paginating
// backwards via endTime past the single-request 1000-row cap. interval is fixed
// to "1d" since deep history only makes sense at daily granularity.
func KlinesDeep(ctx context.Context, symbol string, days int) ([]Kline, error) {
	if days <= 0 {
		days = perReqCap
	}
	endTime := time.Now().UnixMilli()
	var all []Kline
	for len(all) < days {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		limit := perReqCap
		if remaining := days - len(all); remaining < limit {
			limit = remaining
		}
		url := fmt.Sprintf("%s/api/v3/klines?symbol=%s&interval=1d&limit=%d&endTime=%d", spotBase, symbol, limit, endTime)
		var rows [][]any
		if err := httpx.Default.GetJSON(ctx, url, nil, &rows); err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			break
		}
		batch := make([]Kline, 0, len(rows))
		for _, r := range rows {
			if len(r) < 6 {
				continue
			}
			batch = append(batch, Kline{
				OpenTime: int64(toF(r[0])),
				Open:     atof(str(r[1])),
				High:     atof(str(r[2])),
				Low:      atof(str(r[3])),
				Close:    atof(str(r[4])),
				Volume:   atof(str(r[5])),
			})
		}
		if len(batch) == 0 {
			break
		}
		all = append(batch, all...)
		if len(rows) < limit {
			break
		}
		endTime = batch[0].OpenTime - 1
	}
	if len(all) > days {
		all = all[len(all)-days:]
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("empty klines for %s", symbol)
	}
	return all, nil
}

// FundingRate returns the current funding rate + mark price for a USDM perp.
// symbol is the perp, e.g. "BTCUSDT".
func FundingRate(ctx context.Context, symbol string) (*Funding, error) {
	var raw struct {
		Symbol          string `json:"symbol"`
		MarkPrice       string `json:"markPrice"`
		LastFundingRate string `json:"lastFundingRate"`
		NextFundingTime int64  `json:"nextFundingTime"`
	}
	if err := httpx.Default.GetJSON(ctx, fapiBase+"/fapi/v1/premiumIndex?symbol="+symbol, nil, &raw); err != nil {
		return nil, err
	}
	return &Funding{
		Symbol:      raw.Symbol,
		Rate:        atof(raw.LastFundingRate),
		MarkPrice:   atof(raw.MarkPrice),
		NextFunding: raw.NextFundingTime,
	}, nil
}

// OpenInterestNow returns current open interest for a USDM perp.
func OpenInterestNow(ctx context.Context, symbol string) (*OpenInterest, error) {
	var raw struct {
		Symbol       string `json:"symbol"`
		OpenInterest string `json:"openInterest"`
		Time         int64  `json:"time"`
	}
	if err := httpx.Default.GetJSON(ctx, fapiBase+"/fapi/v1/openInterest?symbol="+symbol, nil, &raw); err != nil {
		return nil, err
	}
	return &OpenInterest{Symbol: raw.Symbol, OI: atof(raw.OpenInterest), Time: raw.Time}, nil
}

// LongShortRatio returns the global long/short ACCOUNT ratio series (ascending).
// period e.g. "5m","1h","4h","1d". symbol e.g. "BTCUSDT".
func LongShortRatio(ctx context.Context, symbol, period string, limit int) ([]LongShort, error) {
	if limit <= 0 {
		limit = 30
	}
	url := fmt.Sprintf("%s/futures/data/globalLongShortAccountRatio?symbol=%s&period=%s&limit=%d", fapiBase, symbol, period, limit)
	var rows []struct {
		Symbol         string `json:"symbol"`
		LongShortRatio string `json:"longShortRatio"`
		LongAccount    string `json:"longAccount"`
		ShortAccount   string `json:"shortAccount"`
		Timestamp      int64  `json:"timestamp"`
	}
	if err := httpx.Default.GetJSON(ctx, url, nil, &rows); err != nil {
		return nil, err
	}
	out := make([]LongShort, 0, len(rows))
	for _, r := range rows {
		out = append(out, LongShort{
			Symbol:    r.Symbol,
			Ratio:     atof(r.LongShortRatio),
			LongAcct:  atof(r.LongAccount),
			ShortAcct: atof(r.ShortAccount),
			Time:      r.Timestamp,
		})
	}
	return out, nil
}

// Depth returns a spot order-book snapshot for e.g. "BTCUSDT". limit is the
// number of levels per side ∈ {5,10,20,50,100,500,1000,5000}; <=0 defaults 100.
func Depth(ctx context.Context, symbol string, limit int) (*OrderBook, error) {
	if limit <= 0 {
		limit = 100
	}
	url := fmt.Sprintf("%s/api/v3/depth?symbol=%s&limit=%d", spotBase, symbol, limit)
	var raw struct {
		LastUpdateID int64      `json:"lastUpdateId"`
		Bids         [][]string `json:"bids"`
		Asks         [][]string `json:"asks"`
	}
	if err := httpx.Default.GetJSON(ctx, url, nil, &raw); err != nil {
		return nil, err
	}
	return &OrderBook{
		Symbol:       symbol,
		Bids:         toLevels(raw.Bids),
		Asks:         toLevels(raw.Asks),
		LastUpdateID: raw.LastUpdateID,
	}, nil
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
func toF(v any) float64     { f, _ := v.(float64); return f }
func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
