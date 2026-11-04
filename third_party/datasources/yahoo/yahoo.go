// Package yahoo is the single Yahoo Finance data source: quotes + daily history
// via the public chart API (query1.finance.yahoo.com/v8). All consumers must
// route through this package or the market adapter chain — never call
// query1.finance.yahoo.com directly.
//
// Callers pass a Yahoo-native ticker (e.g. "AAPL", "0700.HK", "GC=F",
// "BTC-USD"); canonical→yahoo mapping lives in the caller/gateway.
//
// Priority: Yahoo is the lowest-priority fallback (pri 10) in the adapter
// chain — behind Futu, Alpaca, CBOE, Binance, etc. See docs/design/yahoo-migration.md.
package yahoo

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/internal/httpx"
)

const (
	chartBase  = "https://query1.finance.yahoo.com/v8/finance/chart/"
	searchBase = "https://query1.finance.yahoo.com/v1/finance/search"
)

// Quote is a realtime-ish snapshot from the chart meta.
type Quote struct {
	Symbol    string  `json:"symbol"`
	Name      string  `json:"name"`
	Last      float64 `json:"last_price"`
	PrevClose float64 `json:"previous_close"`
	Open      float64 `json:"open"`
	High      float64 `json:"day_high"`
	Low       float64 `json:"day_low"`
	Volume    int64   `json:"volume"`
	Currency  string  `json:"currency"`
	Exchange  string  `json:"exchange"`
	Time      int64   `json:"time"` // unix seconds of regularMarketTime
}

// Bar is one daily OHLCV row.
type Bar struct {
	Date   string  `json:"date"`
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume int64   `json:"volume"`
}

// chartResp is the subset of the v8 chart payload we parse.
type chartResp struct {
	Chart struct {
		Result []struct {
			Meta struct {
				Symbol             string  `json:"symbol"`
				Currency           string  `json:"currency"`
				ExchangeName       string  `json:"exchangeName"`
				ShortName          string  `json:"shortName"`
				LongName           string  `json:"longName"`
				RegularMarketPrice float64 `json:"regularMarketPrice"`
				ChartPreviousClose float64 `json:"chartPreviousClose"`
				PreviousClose      float64 `json:"previousClose"`
				RegularMarketTime  int64   `json:"regularMarketTime"`
				RegularMarketDayHi float64 `json:"regularMarketDayHigh"`
				RegularMarketDayLo float64 `json:"regularMarketDayLow"`
				RegularMarketVol   int64   `json:"regularMarketVolume"`
			} `json:"meta"`
			Timestamp  []int64 `json:"timestamp"`
			Indicators struct {
				Quote []struct {
					Open   []*float64 `json:"open"`
					High   []*float64 `json:"high"`
					Low    []*float64 `json:"low"`
					Close  []*float64 `json:"close"`
					Volume []*int64   `json:"volume"`
				} `json:"quote"`
			} `json:"indicators"`
		} `json:"result"`
		Error any `json:"error"`
	} `json:"chart"`
}

func fetchChart(ctx context.Context, symbol, rng string) (*chartResp, error) {
	return fetchChartURL(ctx, symbol, fmt.Sprintf("%s%s?interval=1d&range=%s", chartBase, symbol, rng))
}

func fetchChartURL(ctx context.Context, symbol, url string) (*chartResp, error) {
	var cr chartResp
	if err := httpx.Default.GetJSON(ctx, url, nil, &cr); err != nil {
		return nil, err
	}
	if len(cr.Chart.Result) == 0 {
		return nil, fmt.Errorf("yahoo: empty result for %s", symbol)
	}
	return &cr, nil
}

// GetQuote returns a snapshot. Open is taken from the latest indicator bar when
// the meta lacks it; market_cap is not available from the chart API.
func GetQuote(ctx context.Context, symbol string) (*Quote, error) {
	// range=1d so chartPreviousClose is yesterday's close (matches Futu/yfinance
	// previous_close); a wider range would report the pre-window close.
	cr, err := fetchChart(ctx, symbol, "1d")
	if err != nil {
		return nil, err
	}
	r := cr.Chart.Result[0]
	q := &Quote{
		Symbol:    symbol,
		Name:      firstNonEmpty(r.Meta.ShortName, r.Meta.LongName, r.Meta.Symbol, symbol),
		Last:      r.Meta.RegularMarketPrice,
		PrevClose: pick(r.Meta.PreviousClose, r.Meta.ChartPreviousClose),
		High:      r.Meta.RegularMarketDayHi,
		Low:       r.Meta.RegularMarketDayLo,
		Volume:    r.Meta.RegularMarketVol,
		Currency:  r.Meta.Currency,
		Exchange:  r.Meta.ExchangeName,
		Time:      r.Meta.RegularMarketTime,
	}
	// Open from the most recent non-nil indicator bar.
	if len(r.Indicators.Quote) > 0 {
		o := r.Indicators.Quote[0].Open
		for i := len(o) - 1; i >= 0; i-- {
			if o[i] != nil && *o[i] > 0 {
				q.Open = *o[i]
				break
			}
		}
	}
	if q.Last == 0 {
		return nil, fmt.Errorf("yahoo: no price for %s", symbol)
	}
	return q, nil
}

// GetHistory returns daily bars ASCENDING (oldest-first), the most recent
// `limit` rows (0 = all available in the range). Mirrors the Python provider's
// tail(limit) + ascending order.
func GetHistory(ctx context.Context, symbol string, limit int) ([]Bar, error) {
	cr, err := fetchChart(ctx, symbol, "2y")
	if err != nil {
		return nil, err
	}
	r := cr.Chart.Result[0]
	if len(r.Indicators.Quote) == 0 {
		return nil, fmt.Errorf("yahoo: no indicators for %s", symbol)
	}
	q := r.Indicators.Quote[0]
	bars := make([]Bar, 0, len(r.Timestamp))
	for i, ts := range r.Timestamp {
		if i >= len(q.Close) || q.Close[i] == nil {
			continue
		}
		bars = append(bars, Bar{
			Date:   time.Unix(ts, 0).UTC().Format("2006-01-02"),
			Open:   deref(q.Open, i),
			High:   deref(q.High, i),
			Low:    deref(q.Low, i),
			Close:  deref(q.Close, i),
			Volume: derefI(q.Volume, i),
		})
	}
	if limit > 0 && len(bars) > limit {
		bars = bars[len(bars)-limit:]
	}
	return bars, nil
}

// GetHistoryDays returns daily bars ASCENDING over the last `days` calendar days
// using an explicit period1/period2 window (so deep history beyond the range=2y
// cap is available — callers like guanfu pull 4000–9000 days).
func GetHistoryDays(ctx context.Context, symbol string, days int) ([]Bar, error) {
	if days <= 0 {
		days = 4000
	}
	now := time.Now().Unix()
	from := now - int64(days)*86400
	url := fmt.Sprintf("%s%s?interval=1d&period1=%d&period2=%d&includePrePost=false", chartBase, symbol, from, now)
	cr, err := fetchChartURL(ctx, symbol, url)
	if err != nil {
		return nil, err
	}
	r := cr.Chart.Result[0]
	if len(r.Indicators.Quote) == 0 {
		return nil, fmt.Errorf("yahoo: no indicators for %s", symbol)
	}
	q := r.Indicators.Quote[0]
	bars := make([]Bar, 0, len(r.Timestamp))
	for i, ts := range r.Timestamp {
		if i >= len(q.Close) || q.Close[i] == nil || *q.Close[i] <= 0 {
			continue
		}
		bars = append(bars, Bar{
			Date:   time.Unix(ts, 0).UTC().Format("2006-01-02"),
			Open:   deref(q.Open, i),
			High:   deref(q.High, i),
			Low:    deref(q.Low, i),
			Close:  deref(q.Close, i),
			Volume: derefI(q.Volume, i),
		})
	}
	return bars, nil
}

// SearchResult is one hit from Yahoo's v1 symbol-search endpoint, ordered by
// Yahoo's relevance score. Used to resolve a free-text name/ticker (incl. CJK)
// to candidate symbols; the caller applies its own best-match policy.
type SearchResult struct {
	Symbol    string  `json:"symbol"`
	ShortName string  `json:"short_name"`
	LongName  string  `json:"long_name"`
	QuoteType string  `json:"quote_type"` // EQUITY / ETF / INDEX / FUTURE / CURRENCY ...
	Exchange  string  `json:"exchange"`
	Score     float64 `json:"score"`
}

// Search resolves a free-text query to candidate symbols via Yahoo's v1 search
// endpoint (quotesCount=10, no news).
func Search(ctx context.Context, query string) ([]SearchResult, error) {
	u := fmt.Sprintf("%s?q=%s&quotesCount=10&newsCount=0", searchBase, url.QueryEscape(query))
	var raw struct {
		Quotes []struct {
			Symbol    string  `json:"symbol"`
			ShortName string  `json:"shortname"`
			LongName  string  `json:"longname"`
			QuoteType string  `json:"quoteType"`
			Exchange  string  `json:"exchange"`
			Score     float64 `json:"score"`
		} `json:"quotes"`
	}
	if err := httpx.Default.GetJSON(ctx, u, nil, &raw); err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(raw.Quotes))
	for _, q := range raw.Quotes {
		out = append(out, SearchResult{
			Symbol:    q.Symbol,
			ShortName: q.ShortName,
			LongName:  q.LongName,
			QuoteType: q.QuoteType,
			Exchange:  q.Exchange,
			Score:     q.Score,
		})
	}
	return out, nil
}

func pick(a, b float64) float64 {
	if a != 0 {
		return a
	}
	return b
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func deref(s []*float64, i int) float64 {
	if i < len(s) && s[i] != nil {
		return *s[i]
	}
	return 0
}

func derefI(s []*int64, i int) int64 {
	if i < len(s) && s[i] != nil {
		return *s[i]
	}
	return 0
}
