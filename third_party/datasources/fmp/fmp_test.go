package fmp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The free tier 402s when a `limit` param is sent — the package must never
// send it, and must slice client-side instead.
func TestNeverSendsLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("limit") != "" {
			w.WriteHeader(http.StatusPaymentRequired) // mirror real FMP behavior
			return
		}
		if r.URL.Query().Get("apikey") != "k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		rows := []map[string]any{{"date": "2026-01-01"}, {"date": "2025-01-01"}, {"date": "2024-01-01"}}
		json.NewEncoder(w).Encode(rows)
	}))
	defer srv.Close()
	old := BaseURL
	BaseURL = srv.URL
	defer func() { BaseURL = old }()

	rows, err := Statement(context.Background(), "k", "income-statement", "AAPL", "annual", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0]["date"] != "2026-01-01" {
		t.Fatalf("client-side slice failed: %v", rows)
	}

	all, err := Dividends(context.Background(), "k", "KO", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("limit=0 should return all rows, got %d", len(all))
	}
}

func TestMissingKey(t *testing.T) {
	if _, err := Stable(context.Background(), "", "dividends", nil); err == nil {
		t.Fatal("missing key should error before any HTTP call")
	}
}
