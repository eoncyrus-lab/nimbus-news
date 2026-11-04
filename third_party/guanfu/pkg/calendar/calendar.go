// Package calendar returns upcoming macro / crypto-cycle events for
// guanfu digest + watch use (Track F7).
//
// Philosophy: we DON'T fetch an external API. Event dates from FRED/BLS
// calendars have historically moved or been rate-limited. Instead:
//
//   - FOMC meetings: hardcoded per-year schedule (FOMC publishes ~1y out,
//     dates rarely change; refresh this table annually).
//   - CPI releases: rule-based (BLS publishes around the 10th-15th
//     of the following month). Approximate; user cross-checks date.
//   - BTC halving: deterministic from block height, known within ±week.
//   - Quarterly earnings peak: rule-based (mid-Jan/Apr/Jul/Oct).
//
// The output is a sorted list of events within a window; consumers
// (guanfu digest, SKILL) decide how to present.

package calendar

import (
	"sort"
	"time"
)

// Kind is a coarse event category.
type Kind string

const (
	KindFOMC     Kind = "fomc"     // FOMC rate decision
	KindCPI      Kind = "cpi"      // CPI release
	KindHalving  Kind = "halving"  // BTC halving
	KindEarnings Kind = "earnings" // quarterly earnings peak week
)

// Event is a single dated market-impact event.
type Event struct {
	Date time.Time `json:"date"`
	Kind Kind      `json:"kind"`
	Name string    `json:"name"`
	Note string    `json:"note,omitempty"`
}

// Upcoming returns events in [now, now+windowDays], sorted ascending.
// Deterministic: pure function of `now` and the hardcoded tables.
func Upcoming(now time.Time, windowDays int) []Event {
	if windowDays <= 0 {
		windowDays = 30
	}
	end := now.AddDate(0, 0, windowDays)
	var out []Event
	out = append(out, fomcEvents(now, end)...)
	out = append(out, cpiEvents(now, end)...)
	out = append(out, halvingEvents(now, end)...)
	out = append(out, earningsEvents(now, end)...)
	sort.Slice(out, func(i, j int) bool { return out[i].Date.Before(out[j].Date) })
	return out
}

// fomcSchedule holds known FOMC rate-decision dates (the second day
// of each two-day meeting). Source: federalreserve.gov/monetarypolicy/.
// Updated for 2026; extend this slice annually.
var fomcSchedule = []time.Time{
	mustDate("2026-01-28"),
	mustDate("2026-03-18"),
	mustDate("2026-04-29"),
	mustDate("2026-06-17"),
	mustDate("2026-07-29"),
	mustDate("2026-09-16"),
	mustDate("2026-10-28"),
	mustDate("2026-12-16"),
	// 2027 tentative (FOMC typically publishes a year in advance):
	mustDate("2027-01-27"),
	mustDate("2027-03-17"),
	mustDate("2027-04-28"),
	mustDate("2027-06-16"),
	mustDate("2027-07-28"),
	mustDate("2027-09-15"),
	mustDate("2027-10-27"),
	mustDate("2027-12-15"),
}

func fomcEvents(from, to time.Time) []Event {
	var out []Event
	for _, d := range fomcSchedule {
		if d.Before(from) || d.After(to) {
			continue
		}
		out = append(out, Event{
			Date: d,
			Kind: KindFOMC,
			Name: "FOMC rate decision",
			Note: "Forecast reliability lower in the 3 days before + 1 day after; avoid making horizon decisions on the announcement window",
		})
	}
	return out
}

// cpiEvents emits a CPI release event around the 12th of each month.
// BLS actual release dates range roughly day 10-15; the 12th is a
// serviceable approximation. User should verify the exact day via
// bls.gov/schedule if exact timing matters.
func cpiEvents(from, to time.Time) []Event {
	var out []Event
	// Walk month-by-month from `from`'s month through `to`'s month.
	cur := time.Date(from.Year(), from.Month(), 12, 0, 0, 0, 0, time.UTC)
	for !cur.After(to) {
		if !cur.Before(from) {
			out = append(out, Event{
				Date: cur,
				Kind: KindCPI,
				Name: "CPI release (approx)",
				Note: "Actual release varies day 10-15; cross-check bls.gov/schedule for exact timing",
			})
		}
		cur = cur.AddDate(0, 1, 0)
	}
	return out
}

// halvingEvents covers only BTC's next halving. We hardcode the date
// (derived from the expected block height schedule); accuracy is ±week.
var nextBTCHalving = time.Date(2028, 4, 20, 0, 0, 0, 0, time.UTC)

func halvingEvents(from, to time.Time) []Event {
	if nextBTCHalving.Before(from) || nextBTCHalving.After(to) {
		return nil
	}
	return []Event{{
		Date: nextBTCHalving,
		Kind: KindHalving,
		Name: "BTC halving (estimated)",
		Note: "±1 week uncertainty from block-time variance. Cycle phase transitions around this date.",
	}}
}

// earningsEvents flags the peak week of each quarterly earnings season
// (mid-Jan/Apr/Jul/Oct). Impacts QQQ/SPY broad sentiment regardless of
// individual names the user holds.
func earningsEvents(from, to time.Time) []Event {
	var out []Event
	// Peak weeks anchored on the 15th of Jan/Apr/Jul/Oct.
	for year := from.Year(); year <= to.Year(); year++ {
		for _, m := range []time.Month{time.January, time.April, time.July, time.October} {
			d := time.Date(year, m, 15, 0, 0, 0, 0, time.UTC)
			if d.Before(from) || d.After(to) {
				continue
			}
			out = append(out, Event{
				Date: d,
				Kind: KindEarnings,
				Name: "Quarterly earnings peak week",
				Note: "QQQ/SPY breadth can swing materially; horizon forecasts less reliable this week",
			})
		}
	}
	return out
}

func mustDate(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}
