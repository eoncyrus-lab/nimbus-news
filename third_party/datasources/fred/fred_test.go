package fred

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestObservationsLatestDesc(t *testing.T) {
	oldBase := BaseURL
	defer func() { BaseURL = oldBase }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fred/series/observations" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("series_id") != "DTWEXBGS" || q.Get("api_key") != "k1" || q.Get("file_type") != "json" {
			t.Fatalf("bad params: %v", q)
		}
		if q.Get("sort_order") != "desc" || q.Get("limit") != "3" {
			t.Fatalf("sort/limit not set: %v", q)
		}
		w.Write([]byte(`{"observations":[
			{"date":"2026-06-26","value":"120.5"},
			{"date":"2026-06-25","value":"."},
			{"date":"2026-06-24","value":"119.8"}
		]}`))
	}))
	defer srv.Close()
	BaseURL = srv.URL

	obs, err := Observations(context.Background(), "k1", Query{Series: "DTWEXBGS", Limit: 3, SortDesc: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 3 {
		t.Fatalf("len = %d, want 3", len(obs))
	}
	if obs[0].Date != "2026-06-26" || obs[0].Value != "120.5" {
		t.Fatalf("obs[0] = %+v", obs[0])
	}
	// "." passed through verbatim — consumer decides.
	if obs[1].Value != "." {
		t.Fatalf("missing value not preserved: %q", obs[1].Value)
	}
}

func TestObservationsRange(t *testing.T) {
	oldBase := BaseURL
	defer func() { BaseURL = oldBase }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("observation_start") != "2026-06-01" || q.Get("observation_end") != "2026-06-03" {
			t.Fatalf("range params missing: %v", q)
		}
		if q.Get("sort_order") != "" {
			t.Fatalf("range query should use natural ascending order, got sort=%s", q.Get("sort_order"))
		}
		w.Write([]byte(`{"observations":[
			{"date":"2026-06-01","value":"1.0"},
			{"date":"2026-06-02","value":"1.1"}
		]}`))
	}))
	defer srv.Close()
	BaseURL = srv.URL

	obs, err := Observations(context.Background(), "k1",
		Query{Series: "DGS10", Start: "2026-06-01", End: "2026-06-03", Limit: 100000})
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 2 || obs[0].Date != "2026-06-01" {
		t.Fatalf("obs = %+v", obs)
	}
}

func TestObservationsEmpty(t *testing.T) {
	oldBase := BaseURL
	defer func() { BaseURL = oldBase }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"observations":[]}`))
	}))
	defer srv.Close()
	BaseURL = srv.URL

	obs, err := Observations(context.Background(), "k1", Query{Series: "DGS10"})
	if err != nil {
		t.Fatalf("empty should not error: %v", err)
	}
	if obs != nil {
		t.Fatalf("empty should return nil, got %v", obs)
	}
}
