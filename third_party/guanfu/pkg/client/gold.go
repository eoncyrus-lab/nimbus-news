// gold.go — London Gold (XAU/USD) price pipeline.
//
// Data sources:
//   - DBnomics LBMA PM Fix for deep history (1968+). Free, no API key.
//     Provider: LBMA or World Bank GOLD/PM series.
//   - Yahoo Finance XAUUSD=X for incremental daily updates (recent data).
//     Yahoo fill-gaps when DBnomics is unavailable.
//
// Staleness: LBMA publishes daily PM fix on business days only.
// Yahoo XAUUSD=X covers all trading days.

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
	dsyahoo "github.com/Ricaardo/nimbus-os/datasources/yahoo"
)

const (
	goldMaxHistoryDays = 22000 // ~60 years
	goldRecentDays     = 30
)

// dbnomicsSeriesResp DBnomics v22 series/observations response
type dbnomicsSeriesResp struct {
	Docs []struct {
		ProviderName string `json:"provider_name"`
		DatasetName  string `json:"dataset_name"`
		SeriesName   string `json:"series_name"`
		Period       string `json:"period"`
		Value        string `json:"value"`
	} `json:"docs"`
	Total struct {
		Value int `json:"value"`
	} `json:"total"`
}

// FetchLondonGoldPricePoints fetches XAU/USD daily price history.
// Returns oldest-first PricePoint slice.
// Uses DBnomics LBMA for deep history; falls back to Yahoo XAUUSD=X.
func FetchLondonGoldPricePoints(ctx context.Context) ([]store.PricePoint, error) {
	hc := &http.Client{Timeout: 30 * time.Second}

	// Try DBnomics first for deep history
	points, err := fetchDBnomicsGold(ctx, hc)
	if err != nil || len(points) < 1000 {
		// Fall back to Yahoo XAUUSD=X
		yahooPoints, yahooErr := fetchYahooGold(ctx)
		if yahooErr != nil {
			if err != nil {
				return nil, fmt.Errorf("gold fetch failed: dbnomics=%w, yahoo=%w", err, yahooErr)
			}
			return nil, yahooErr
		}
		points = yahooPoints
	}

	// Always overlay recent Yahoo data for freshness
	recent, err := fetchYahooGold(ctx)
	if err == nil && len(recent) > 0 {
		points = mergeGoldPoints(points, recent)
	}

	return store.NormalizePricePoints(points), nil
}

// fetchDBnomicsGold fetches LBMA PM gold fix from DBnomics.
// Known series:
//   - primary: LBMA provider (preferred, covers 1968+)
//   - fallback: WB/GOLD/PM (World Bank commodity prices)
func fetchDBnomicsGold(ctx context.Context, hc *http.Client) ([]store.PricePoint, error) {
	// DBnomics LBMA gold series. Try known provider codes; first one that
	// returns ≥1000 paginated records wins. The 2026 API enforces limit≤1000
	// per call so we page in 1000-row chunks until the upstream stops growing.
	providers := []string{"LBMA", "WB"}
	datasets := []string{"GOLD", "GOLD"}
	series := []string{"PM", "PM"}
	const pageSize = 1000

	for i := range providers {
		var all []store.PricePoint
		for offset := 0; ; offset += pageSize {
			u := fmt.Sprintf(
				"https://api.db.nomics.world/v22/series/%s/%s/%s?observations=1&limit=%d&offset=%d",
				url.PathEscape(providers[i]),
				url.PathEscape(datasets[i]),
				url.PathEscape(series[i]),
				pageSize,
				offset,
			)
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
			if err != nil {
				break
			}
			req.Header.Set("Accept", "application/json")

			resp, err := hc.Do(req)
			if err != nil {
				break
			}
			body, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil || resp.StatusCode != http.StatusOK {
				break
			}

			var parsed dbnomicsSeriesResp
			if err := json.Unmarshal(body, &parsed); err != nil {
				break
			}
			if len(parsed.Docs) == 0 {
				break
			}

			pre := len(all)
			for _, doc := range parsed.Docs {
				if doc.Value == "" || doc.Value == "." {
					continue
				}
				var val float64
				if _, err := fmt.Sscanf(doc.Value, "%f", &val); err != nil || val <= 0 {
					continue
				}
				all = append(all, store.PricePoint{
					Date:   doc.Period,
					Close:  val,
					Source: fmt.Sprintf("dbnomics:%s/%s", providers[i], datasets[i]),
				})
			}
			added := len(all) - pre
			// Stop when this page added fewer than pageSize valid rows.
			if added < pageSize {
				break
			}
		}
		if len(all) >= 1000 {
			return all, nil
		}
	}

	return nil, fmt.Errorf("no DBnomics gold series returned usable data")
}

// fetchYahooGold fetches gold price from Yahoo Finance chart API.
// Tries GC=F (COMEX gold futures, current canonical symbol) first;
// falls back to XAUUSD=X (delisted in 2026 but kept for historical
// resilience in case it returns) and GLD ETF as a last-resort proxy.
func fetchYahooGold(ctx context.Context) ([]store.PricePoint, error) {
	symbols := []string{"GC=F", "XAUUSD=X", "GLD"}
	var lastErr error
	for _, sym := range symbols {
		points, err := fetchYahooGoldSymbol(ctx, sym)
		if err == nil && len(points) > 0 {
			return points, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("yahoo gold: no symbol returned data")
	}
	return nil, lastErr
}

func fetchYahooGoldSymbol(ctx context.Context, symbol string) ([]store.PricePoint, error) {
	bars, err := dsyahoo.GetHistoryDays(ctx, symbol, goldMaxHistoryDays)
	if err != nil {
		return nil, err
	}

	points := make([]store.PricePoint, 0, len(bars))
	for _, b := range bars {
		if b.Close <= 0 {
			continue
		}
		points = append(points, store.PricePoint{
			Date:   b.Date,
			Close:  b.Close,
			Source: "yahoo:" + symbol,
		})
	}

	if len(points) == 0 {
		return nil, fmt.Errorf("yahoo gold: no valid closes")
	}
	return points, nil
}

func mergeGoldPoints(base, updates []store.PricePoint) []store.PricePoint {
	merged := make([]store.PricePoint, 0, len(base)+len(updates))
	merged = append(merged, base...)
	merged = append(merged, updates...)
	return store.NormalizePricePoints(merged)
}

// FetchGoldIncremental fetches only recent gold data for PriceStore updates.
func FetchGoldIncremental(ctx context.Context, lastDate string) ([]store.PricePoint, error) {
	if lastDate == "" {
		return FetchLondonGoldPricePoints(ctx)
	}
	last, err := time.Parse("2006-01-02", lastDate)
	if err != nil {
		return FetchLondonGoldPricePoints(ctx)
	}

	daysSince := int(time.Since(last).Hours() / 24)
	if daysSince <= 1 {
		return nil, nil // fresh enough
	}

	return FetchLondonGoldPricePoints(ctx)
}
