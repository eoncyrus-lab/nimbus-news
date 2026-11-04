package cboe

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHistoricalCSVParsesDedupesSorts(t *testing.T) {
	oldA, oldR := ArchiveCSVURL, RecentCSVURL
	defer func() { ArchiveCSVURL, RecentCSVURL = oldA, oldR }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/archive":
			// header junk + rows; ratio is the last field. Overlap date 1/03/2024.
			w.Write([]byte("Date,Calls,Puts,Total,P/C Ratio\n1/02/2024,1,1,2,0.90\n1/03/2024,1,1,2,0.80\n"))
		case "/recent":
			// recent wins the overlap (1/03/2024 -> 0.85); adds 1/04/2024.
			w.Write([]byte("Date,Calls,Puts,Total,P/C Ratio\n1/03/2024,1,1,2,0.85\n1/04/2024,1,1,2,0.95\n"))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	ArchiveCSVURL = srv.URL + "/archive"
	RecentCSVURL = srv.URL + "/recent"

	pts, err := HistoricalCSV(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 3 {
		t.Fatalf("len = %d, want 3 (dedup overlap): %+v", len(pts), pts)
	}
	if pts[0].Date != "2024-01-02" || pts[0].Ratio != 0.90 {
		t.Fatalf("pts[0] = %+v", pts[0])
	}
	// recent overrides archive for the overlap date
	if pts[1].Date != "2024-01-03" || pts[1].Ratio != 0.85 {
		t.Fatalf("overlap not last-wins: pts[1] = %+v", pts[1])
	}
	if pts[2].Date != "2024-01-04" || pts[2].Ratio != 0.95 {
		t.Fatalf("pts[2] = %+v", pts[2])
	}
}

func TestDailyRangeScrapesBusinessDays(t *testing.T) {
	oldD := DailyBaseURL
	defer func() { DailyBaseURL = oldD }()

	var mu struct {
		seen map[string]bool
	}
	mu.seen = map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dt := r.URL.Query().Get("dt")
		// escaped-JSON ratio form
		w.Write([]byte(`...{\"label\":\"TOTAL PUT/CALL RATIO\",\"value\":\"0.88\"}...`))
		_ = dt
	}))
	defer srv.Close()
	DailyBaseURL = srv.URL

	// Wed 2024-01-03 .. Mon 2024-01-08 → business days 3,4,5,8 (skip 6,7 weekend).
	start, _ := time.Parse("2006-01-02", "2024-01-03")
	end, _ := time.Parse("2006-01-02", "2024-01-08")
	pts, err := DailyRange(context.Background(), start, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 4 {
		t.Fatalf("len = %d, want 4 business days: %+v", len(pts), pts)
	}
	for _, p := range pts {
		if p.Ratio != 0.88 {
			t.Fatalf("ratio = %v, want 0.88", p.Ratio)
		}
	}
	if pts[0].Date != "2024-01-03" || pts[3].Date != "2024-01-08" {
		t.Fatalf("dates = %s..%s", pts[0].Date, pts[3].Date)
	}
}

func TestParseDailyRatioTextFallback(t *testing.T) {
	// Non-escaped HTML form exercises the text-fallback regex.
	body := `<div>TOTAL PUT/CALL RATIO</div> <span>0.73</span>`
	v, ok := parseDailyRatio(body)
	if !ok || v != 0.73 {
		t.Fatalf("text fallback = %v %v, want 0.73 true", v, ok)
	}
	if _, ok := parseDailyRatio("<div>nothing here</div>"); ok {
		t.Fatal("expected no match on empty body")
	}
}

func TestHistoricalCSVEmpty(t *testing.T) {
	oldA, oldR := ArchiveCSVURL, RecentCSVURL
	defer func() { ArchiveCSVURL, RecentCSVURL = oldA, oldR }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Date,Calls,Puts,Total,Ratio\n")) // header only
	}))
	defer srv.Close()
	ArchiveCSVURL = srv.URL + "/a"
	RecentCSVURL = srv.URL + "/b"

	pts, err := HistoricalCSV(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if pts != nil {
		t.Fatalf("header-only should be nil, got %+v", pts)
	}
	_ = strings.TrimSpace
}

func TestGetQuote(t *testing.T) {
	old := QuotesBaseURL
	defer func() { QuotesBaseURL = old }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_VIX.json" {
			t.Fatalf("path %s", r.URL.Path)
		}
		w.Write([]byte(`{"timestamp":"t","symbol":"^VIX","data":{
			"symbol":"^VIX","current_price":18.41,"bid":18.3,"ask":18.5,
			"open":19.0,"high":19.2,"low":18.1,"close":18.6,"prev_day_close":18.7,
			"volume":0,"iv30":0,"last_trade_time":"2026-06-28T16:00:00"}}`))
	}))
	defer srv.Close()
	QuotesBaseURL = srv.URL

	q, err := GetQuote(context.Background(), "_VIX")
	if err != nil {
		t.Fatal(err)
	}
	if q.CurrentPrice != 18.41 || q.Bid != 18.3 || q.High != 19.2 {
		t.Fatalf("quote = %+v", q)
	}
	if q.Symbol != "^VIX" {
		t.Fatalf("symbol = %q", q.Symbol)
	}
}
