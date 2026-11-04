package cmc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetGlobalAndQuote(t *testing.T) {
	oldBase := BaseURL
	defer func() { BaseURL = oldBase }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-CMC_PRO_API_KEY") != "test-key" {
			t.Fatalf("missing/wrong CMC api key header: %q", r.Header.Get("X-CMC_PRO_API_KEY"))
		}
		switch r.URL.Path {
		case "/v1/global-metrics/quotes/latest":
			w.Write([]byte(`{"status":{"error_code":0},"data":{"btc_dominance":58.12,"eth_dominance":9.31,"quote":{"USD":{"total_market_cap":3.5e12,"total_volume_24h":1.2e11,"altcoin_market_cap":1.4e12,"altcoin_volume_24h":7e10,"last_updated":"2026-05-10T12:34:56Z"}}}}`))
		case "/v2/cryptocurrency/quotes/latest":
			w.Write([]byte(`{"status":{"error_code":0},"data":{"1":{"id":1,"symbol":"BTC","quote":{"USD":{"price":104000,"volume_24h":4.2e10,"market_cap":2.05e12,"last_updated":"2026-05-10T12:35:10Z"}}}}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	BaseURL = srv.URL

	g, err := GetGlobalMetrics(context.Background(), "test-key")
	if err != nil {
		t.Fatal(err)
	}
	if g.TotalMarketCapUSD != 3.5e12 || g.BTCDominance != 58.12 || g.LastUpdated != "2026-05-10T12:34:56Z" {
		t.Fatalf("global mismatch: %+v", g)
	}
	q, err := GetQuote(context.Background(), "test-key", 1)
	if err != nil {
		t.Fatal(err)
	}
	if q.PriceUSD != 104000 || q.Symbol != "BTC" || q.MarketCapUSD != 2.05e12 {
		t.Fatalf("quote mismatch: %+v", q)
	}
}
