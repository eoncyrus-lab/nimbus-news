package coinbase

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCandles(t *testing.T) {
	oldBase := BaseURL
	defer func() { BaseURL = oldBase }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/products/BTC-USD/candles" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("granularity") != "86400" {
			t.Fatalf("granularity = %s", r.URL.Query().Get("granularity"))
		}
		// [time, low, high, open, close, volume]
		w.Write([]byte(`[
			[1700000000, 35000, 38000, 36000, 37000, 1234.5],
			[1699913600, 34000, 36000, 35000, 35500, 1000.0]
		]`))
	}))
	defer srv.Close()
	BaseURL = srv.URL

	cs, err := Candles(context.Background(), "BTC-USD", 86400,
		time.Unix(1699913600, 0), time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 {
		t.Fatalf("len = %d, want 2", len(cs))
	}
	if cs[0].Close != 37000 || cs[0].Volume != 1234.5 || cs[0].Low != 35000 {
		t.Fatalf("cs[0] = %+v", cs[0])
	}
}

func TestDailyCandlesChunksAndDedupes(t *testing.T) {
	oldBase := BaseURL
	defer func() { BaseURL = oldBase }()

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		// Every window returns the same single day → exercises dedupe + sort.
		w.Write([]byte(`[[1700000000, 35000, 38000, 36000, 37000, 1234.5]]`))
	}))
	defer srv.Close()
	BaseURL = srv.URL

	// 700-day range → 3 windows of 300/300/100 days.
	end := time.Now().UTC()
	start := end.AddDate(0, 0, -700)
	cs, err := DailyCandles(context.Background(), "BTC-USD", start, end)
	if err != nil {
		t.Fatal(err)
	}
	if calls < 3 {
		t.Fatalf("expected ≥3 windowed calls, got %d", calls)
	}
	if len(cs) != 1 {
		t.Fatalf("dedupe failed: len = %d, want 1", len(cs))
	}
	if cs[0].Close != 37000 {
		t.Fatalf("cs[0].Close = %v", cs[0].Close)
	}
}

func TestCandlesEmpty(t *testing.T) {
	oldBase := BaseURL
	defer func() { BaseURL = oldBase }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	BaseURL = srv.URL

	cs, err := Candles(context.Background(), "BTC-USD", 86400, time.Unix(1, 0), time.Unix(2, 0))
	if err != nil {
		t.Fatalf("empty should not error: %v", err)
	}
	if cs != nil {
		t.Fatalf("empty should return nil, got %v", cs)
	}
}
