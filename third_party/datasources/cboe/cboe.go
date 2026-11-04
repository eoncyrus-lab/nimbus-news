// Package cboe is the independent CBOE data source: the total equity+index
// Put/Call ratio, with no API key. Two paths, matching what CBOE publishes:
//   - HistoricalCSV: the archive + recent total put/call CSVs (full history).
//   - DailyRange:    the Daily Market Statistics pages (recent/current days),
//     scraped concurrently.
//
// Fetch-only and stateless; callers persist and dedupe their own history.
package cboe

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/internal/httpx"
)

// Endpoint hosts. Vars (not const) so tests can point them at httptest servers.
var (
	ArchiveCSVURL = "https://cdn.cboe.com/resources/options/volume_and_call_put_ratios/totalpcarchive.csv"
	RecentCSVURL  = "https://cdn.cboe.com/resources/options/volume_and_call_put_ratios/totalpc.csv"
	DailyBaseURL  = "https://www.cboe.com/markets/us/options/market-statistics/daily"
	QuotesBaseURL = "https://cdn.cboe.com/api/global/delayed_quotes/quotes"
)

// DailyWorkers caps concurrent Daily Market Statistics page fetches.
const DailyWorkers = 12

// PutCall is one day's total Put/Call ratio.
type PutCall struct {
	Date  string // YYYY-MM-DD
	Ratio float64
}

// Quote is a CBOE delayed quote for an index/option symbol (e.g. _VIX, _VIX3M).
// CurrentPrice is the field the VIX term-structure consumer needs; the rest are
// exposed for richer downstream use (IV30, bid/ask, OHLC, volume).
type Quote struct {
	Symbol         string  `json:"symbol"`
	CurrentPrice   float64 `json:"current_price"`
	PriceChange    float64 `json:"price_change"`
	PriceChangePct float64 `json:"price_change_percent"`
	Bid            float64 `json:"bid"`
	Ask            float64 `json:"ask"`
	Open           float64 `json:"open"`
	High           float64 `json:"high"`
	Low            float64 `json:"low"`
	Close          float64 `json:"close"`
	PrevDayClose   float64 `json:"prev_day_close"`
	Volume         float64 `json:"volume"`
	IV30           float64 `json:"iv30"`
	LastTradeTime  string  `json:"last_trade_time"`
}

// GetQuote fetches the CBOE delayed quote for a symbol, e.g. "_VIX","_VIX3M"
// (CBOE index symbols are underscore-prefixed). The symbol is used verbatim in
// the path.
func GetQuote(ctx context.Context, symbol string) (*Quote, error) {
	body, err := httpx.Default.GetBytes(ctx, QuotesBaseURL+"/"+symbol+".json",
		map[string]string{"Accept": "application/json"})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Symbol string `json:"symbol"`
		Data   Quote  `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("cboe quote %s: %w", symbol, err)
	}
	q := resp.Data
	if q.Symbol == "" {
		q.Symbol = resp.Symbol
	}
	return &q, nil
}

var (
	dailyEscapedRatioRE = regexp.MustCompile(`TOTAL PUT/CALL RATIO\\",\\"value\\":\\"([0-9]+(?:\.[0-9]+)?)\\"`)
	dailyTextRatioRE    = regexp.MustCompile(`TOTAL PUT/CALL RATIO[^0-9]{0,120}([0-9]+(?:\.[0-9]+)?)`)
	htmlTagRE           = regexp.MustCompile(`<[^>]+>`)
)

// HistoricalCSV fetches the archive + recent total put/call CSVs, parsed,
// deduped (last value per date wins) and sorted oldest-first. Empty → (nil,nil).
func HistoricalCSV(ctx context.Context) ([]PutCall, error) {
	var all []PutCall
	for _, u := range []string{ArchiveCSVURL, RecentCSVURL} {
		pts, err := fetchCSV(ctx, u)
		if err != nil {
			return nil, err
		}
		all = append(all, pts...)
	}
	return normalize(all), nil
}

func fetchCSV(ctx context.Context, url string) ([]PutCall, error) {
	body, err := httpx.Default.GetBytes(ctx, url, map[string]string{"Accept": "text/csv,*/*"})
	if err != nil {
		return nil, fmt.Errorf("cboe put/call csv %s: %w", url, err)
	}
	r := csv.NewReader(bytes.NewReader(body))
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	r.LazyQuotes = true

	var out []PutCall
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("cboe put/call csv parse %s: %w", url, err)
		}
		if p, ok := parseCSVRow(row); ok {
			out = append(out, p)
		}
	}
	return out, nil
}

func parseCSVRow(row []string) (PutCall, bool) {
	if len(row) < 5 {
		return PutCall{}, false
	}
	t, err := time.Parse("1/2/2006", cleanField(row[0]))
	if err != nil {
		return PutCall{}, false
	}
	ratioRaw := ""
	for i := len(row) - 1; i >= 1; i-- {
		ratioRaw = cleanField(row[i])
		if ratioRaw != "" {
			break
		}
	}
	ratio, err := strconv.ParseFloat(strings.ReplaceAll(ratioRaw, ",", ""), 64)
	if err != nil || ratio <= 0 {
		return PutCall{}, false
	}
	return PutCall{Date: t.Format("2006-01-02"), Ratio: ratio}, true
}

func cleanField(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "\ufeff") // strip UTF-8 BOM
	s = strings.Trim(s, "\"")
	return strings.TrimSpace(s)
}

// DailyRange fetches the Daily Market Statistics total put/call ratio for each
// business day in [start, end] (concurrent), deduped and sorted oldest-first.
// Empty → (nil, nil).
func DailyRange(ctx context.Context, start, end time.Time) ([]PutCall, error) {
	start, end = dateOnly(start), dateOnly(end)
	if start.After(end) {
		return nil, nil
	}
	var dates []string
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		dates = append(dates, d.Format("2006-01-02"))
	}
	if len(dates) == 0 {
		return nil, nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan string)
	results := make(chan PutCall, len(dates))
	errs := make(chan error, len(dates))

	workers := DailyWorkers
	if len(dates) < workers {
		workers = len(dates)
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for date := range jobs {
				p, ok, err := fetchDaily(ctx, date)
				if err != nil {
					errs <- err
					cancel()
					continue
				}
				if ok {
					results <- p
				}
			}
		}()
	}

sendLoop:
	for _, date := range dates {
		select {
		case <-ctx.Done():
			break sendLoop
		case jobs <- date:
		}
	}
	close(jobs)
	wg.Wait()
	close(results)
	close(errs)

	if len(errs) > 0 {
		return nil, <-errs
	}
	out := make([]PutCall, 0, len(results))
	for p := range results {
		out = append(out, p)
	}
	return normalize(out), nil
}

func fetchDaily(ctx context.Context, date string) (PutCall, bool, error) {
	body, err := httpx.Default.GetBytes(ctx, DailyBaseURL+"?dt="+date,
		map[string]string{"Accept": "text/html,application/xhtml+xml"})
	if err != nil {
		if ctx.Err() != nil {
			return PutCall{}, false, ctx.Err()
		}
		return PutCall{}, false, fmt.Errorf("cboe daily put/call %s: %w", date, err)
	}
	ratio, ok := parseDailyRatio(string(body))
	if !ok {
		return PutCall{}, false, nil
	}
	return PutCall{Date: date, Ratio: ratio}, true, nil
}

func parseDailyRatio(body string) (float64, bool) {
	if m := dailyEscapedRatioRE.FindStringSubmatch(body); len(m) == 2 {
		v, err := strconv.ParseFloat(m[1], 64)
		return v, err == nil && v > 0
	}
	text := html.UnescapeString(body)
	text = strings.ReplaceAll(text, `\"`, `"`)
	text = htmlTagRE.ReplaceAllString(text, " ")
	text = strings.Join(strings.Fields(text), " ")
	if m := dailyTextRatioRE.FindStringSubmatch(text); len(m) == 2 {
		v, err := strconv.ParseFloat(m[1], 64)
		return v, err == nil && v > 0
	}
	return 0, false
}

// normalize dedupes by date (last value wins), drops ratio<=0, sorts ascending.
func normalize(pts []PutCall) []PutCall {
	if len(pts) == 0 {
		return nil
	}
	byDate := make(map[string]PutCall, len(pts))
	for _, p := range pts {
		if p.Ratio <= 0 {
			continue
		}
		byDate[p.Date] = p
	}
	out := make([]PutCall, 0, len(byDate))
	for _, p := range byDate {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}
