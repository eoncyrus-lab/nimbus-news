package client

import (
	"context"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
	"github.com/Ricaardo/nimbus-os/datasources/cboe"
)

const (
	cboePutCallStorageKey       = "stooq_putcall"
	cboePutCallRecentStart      = "2019-10-07"
	cboePutCallRecentLookback   = 420
	cboePutCallSourceHistorical = "cboe:volume_and_call_put_ratios"
	cboePutCallSourceDaily      = "cboe:daily_market_statistics"
)

// CBOEPutCallSource fetches CBOE total Put/Call ratio without an API key via the
// shared cboe package.
//
// The storage key remains "stooq_putcall" for compatibility with existing
// forecast features and local archives. New data is sourced directly from
// CBOE official historical CSVs and Daily Market Statistics pages.
type CBOEPutCallSource struct{}

func (CBOEPutCallSource) Key() string { return cboePutCallStorageKey }
func (CBOEPutCallSource) DisplayName() string {
	return "stooq_putcall (CBOE official total P/C, no key)"
}

// Refresh uses CBOE historical CSVs for old data and CBOE Daily Market
// Statistics for recent/current data. CBOE does not expose a documented bulk
// post-2019 CSV, so first-run refresh limits page scraping to a recent window
// that is long enough for the 252-observation percentile feature.
func (s CBOEPutCallSource) Refresh(ctx context.Context, ps *store.PriceStore) (*RefreshResult, error) {
	stale, lastDate := staleThreshold(ps, s.Key())
	if !stale {
		return freshSkipResult(s.Key(), s.DisplayName(), lastDate, ps), nil
	}

	mode := "full"
	if lastDate != "" {
		mode = "incremental"
	}

	points := make([]store.PricePoint, 0, 6000)
	if lastDate == "" || dateBefore(lastDate, "2019-10-04") {
		hist, err := cboe.HistoricalCSV(ctx)
		if err != nil {
			return nil, err
		}
		for _, h := range hist {
			points = append(points, store.PricePoint{Date: h.Date, Close: h.Ratio, Source: cboePutCallSourceHistorical})
		}
	}

	dailyStart := cboeDailyStartDate(lastDate, time.Now())
	recent, err := cboe.DailyRange(ctx, dailyStart, time.Now())
	if err != nil {
		return nil, err
	}
	for _, r := range recent {
		points = append(points, store.PricePoint{Date: r.Date, Close: r.Ratio, Source: cboePutCallSourceDaily})
	}

	if len(points) == 0 {
		count, _ := ps.Count(s.Key())
		return &RefreshResult{
			Key:         s.Key(),
			DisplayName: s.DisplayName(),
			Mode:        "skip",
			SkipReason:  "no_new_data",
			Stale:       stale,
			Action:      "ignore",
			Total:       count,
			LastDate:    lastDate,
		}, nil
	}

	var added int
	if lastDate == "" {
		if err := ps.Save(s.Key(), points); err != nil {
			return nil, err
		}
		added = len(store.NormalizePricePoints(points))
	} else {
		before, _ := ps.Count(s.Key())
		if err := ps.Append(s.Key(), points); err != nil {
			return nil, err
		}
		after, _ := ps.Count(s.Key())
		added = after - before
	}

	total, _ := ps.Count(s.Key())
	last, _ := ps.LastDate(s.Key())
	return &RefreshResult{
		Key:         s.Key(),
		DisplayName: s.DisplayName(),
		Mode:        mode,
		Added:       added,
		Total:       total,
		LastDate:    last,
	}, nil
}

// cboeDailyStartDate clamps the daily-scrape window to the recent lookback (and
// CBOE's recent-CSV start), advancing past lastDate on incremental refresh.
func cboeDailyStartDate(lastDate string, now time.Time) time.Time {
	recentStart := dateOnly(now).AddDate(0, 0, -cboePutCallRecentLookback)
	minRecent, _ := time.Parse("2006-01-02", cboePutCallRecentStart)
	if recentStart.Before(minRecent) {
		recentStart = minRecent
	}
	if lastDate == "" {
		return recentStart
	}
	last, err := time.Parse("2006-01-02", lastDate)
	if err != nil {
		return recentStart
	}
	next := last.AddDate(0, 0, 1)
	if next.Before(recentStart) {
		return recentStart
	}
	return next
}

func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func dateBefore(a, b string) bool {
	ta, errA := time.Parse("2006-01-02", a)
	tb, errB := time.Parse("2006-01-02", b)
	return errA == nil && errB == nil && ta.Before(tb)
}
