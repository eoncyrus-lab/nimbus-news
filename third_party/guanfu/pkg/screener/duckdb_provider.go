// DuckDBPriceProvider reads daily_prices exported from DuckDB as JSONL.
//
// Export step (one-time, from equity-screener venv):
//   uv run python scripts/export_prices.py --out /tmp/prices.jsonl
//
// Then the backtest reads this file via NewDuckDBPriceProvider.

package screener

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"
)

// DuckDBPriceProvider implements PriceProvider from exported JSONL data.
// Keeps data in memory for fast backtest access.
type DuckDBPriceProvider struct {
	// prices[symbol] = sorted date→price pairs
	prices    map[string][]PricePoint
	benchmark map[string][]PricePoint
}

// priceRecord is the JSONL row format from export_prices.py.
type priceRecord struct {
	Symbol string  `json:"symbol"`
	Market string  `json:"market"`
	Date   string  `json:"date"`
	Close  float64 `json:"close"`
}

// NewDuckDBPriceProvider loads a JSONL file exported from DuckDB.
func NewDuckDBPriceProvider(jsonlPath string) (*DuckDBPriceProvider, error) {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", jsonlPath, err)
	}
	defer f.Close()

	p := &DuckDBPriceProvider{
		prices:    make(map[string][]PricePoint),
		benchmark: make(map[string][]PricePoint),
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024) // 10MB line buffer
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		var rec priceRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			continue // skip malformed lines
		}
		date, err := time.Parse("2006-01-02", rec.Date[:10])
		if err != nil {
			continue
		}
		pt := PricePoint{Date: date, Price: rec.Close}
		p.prices[rec.Symbol] = append(p.prices[rec.Symbol], pt)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s line %d: %w", jsonlPath, lineNo, err)
	}

	// Sort each symbol's prices by date
	for sym := range p.prices {
		pts := p.prices[sym]
		sort.Slice(pts, func(i, j int) bool { return pts[i].Date.Before(pts[j].Date) })
		p.prices[sym] = pts
	}

	return p, nil
}

// AddBenchmark loads a separate JSONL file as benchmark data.
func (p *DuckDBPriceProvider) AddBenchmark(jsonlPath, benchName string) error {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return err
	}
	defer f.Close()

	var points []PricePoint
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var rec priceRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			continue
		}
		date, err := time.Parse("2006-01-02", rec.Date[:10])
		if err != nil {
			continue
		}
		points = append(points, PricePoint{Date: date, Price: rec.Close})
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Date.Before(points[j].Date) })
	p.benchmark[benchName] = points
	return scanner.Err()
}

func (p *DuckDBPriceProvider) Price(symbol string, date time.Time) (float64, time.Time, bool) {
	points, ok := p.prices[symbol]
	if !ok {
		return 0, time.Time{}, false
	}
	// Binary search for the price on or before date
	var best PricePoint
	found := false
	for _, pt := range points {
		if !pt.Date.After(date) {
			best = pt
			found = true
		} else {
			break // prices are sorted, stop at first future date
		}
	}
	if !found {
		return 0, time.Time{}, false
	}
	return best.Price, best.Date, true
}

func (p *DuckDBPriceProvider) Prices(symbol string, start, end time.Time) ([]PricePoint, error) {
	points, ok := p.prices[symbol]
	if !ok {
		return nil, fmt.Errorf("no data for %s", symbol)
	}
	var result []PricePoint
	for _, pt := range points {
		if !pt.Date.Before(start) && !pt.Date.After(end) {
			result = append(result, pt)
		}
	}
	return result, nil
}

func (p *DuckDBPriceProvider) Benchmark(bench string, start, end time.Time) ([]PricePoint, error) {
	points, ok := p.benchmark[bench]
	if !ok {
		return nil, fmt.Errorf("no benchmark %s", bench)
	}
	var result []PricePoint
	for _, pt := range points {
		if !pt.Date.Before(start) && !pt.Date.After(end) {
			result = append(result, pt)
		}
	}
	return result, nil
}

// SymbolCount returns the number of unique symbols loaded.
func (p *DuckDBPriceProvider) SymbolCount() int {
	return len(p.prices)
}

// DateRange returns the min/max dates across all symbols.
func (p *DuckDBPriceProvider) DateRange() (time.Time, time.Time) {
	var earliest, latest time.Time
	for _, pts := range p.prices {
		if len(pts) == 0 {
			continue
		}
		if earliest.IsZero() || pts[0].Date.Before(earliest) {
			earliest = pts[0].Date
		}
		last := pts[len(pts)-1].Date
		if latest.IsZero() || last.After(latest) {
			latest = last
		}
	}
	return earliest, latest
}

// Symbols returns all loaded symbols.
func (p *DuckDBPriceProvider) Symbols() []string {
	symbols := make([]string, 0, len(p.prices))
	for s := range p.prices {
		symbols = append(symbols, s)
	}
	sort.Strings(symbols)
	return symbols
}
