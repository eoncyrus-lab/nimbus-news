package defillama

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStablecoinChartAll(t *testing.T) {
	oldBase := BaseURL
	defer func() { BaseURL = oldBase }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stablecoincharts/all" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Write([]byte(`[
			{"date":"1700000000","totalCirculatingUSD":{"peggedUSD":150000000000,"peggedEUR":3000000000}},
			{"date":1600000000,"totalCirculatingUSD":{"peggedUSD":120000000000}}
		]`))
	}))
	defer srv.Close()
	BaseURL = srv.URL

	pts, err := StablecoinChartAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 2 {
		t.Fatalf("len = %d, want 2", len(pts))
	}
	// oldest-first
	if !pts[0].Time.Before(pts[1].Time) {
		t.Fatalf("not sorted oldest-first: %v %v", pts[0].Time, pts[1].Time)
	}
	if got := pts[0].TotalUSD(); got != 120e9 {
		t.Fatalf("pts[0].TotalUSD = %v, want 120e9", got)
	}
	if got := pts[1].TotalUSD(); got != 153e9 {
		t.Fatalf("pts[1].TotalUSD = %v, want 153e9", got)
	}
	if pts[1].Time.Year() != 2023 {
		t.Fatalf("pts[1].Time year = %d, want 2023", pts[1].Time.Year())
	}
}

func TestStablecoinChartAllEmpty(t *testing.T) {
	oldBase := BaseURL
	defer func() { BaseURL = oldBase }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	BaseURL = srv.URL

	pts, err := StablecoinChartAll(context.Background())
	if err != nil {
		t.Fatalf("empty response should not error: %v", err)
	}
	if pts != nil {
		t.Fatalf("empty response should return nil, got %v", pts)
	}
}

func TestParseDate(t *testing.T) {
	if tm, ok := parseDate(float64(1700000000)); !ok || tm.Year() != 2023 {
		t.Errorf("float unix failed: %v %v", tm, ok)
	}
	if tm, ok := parseDate("2025-03-15"); !ok || tm.Year() != 2025 || tm.Month() != 3 {
		t.Errorf("iso date failed: %v %v", tm, ok)
	}
	if tm, ok := parseDate("1700000000"); !ok || tm.Year() != 2023 {
		t.Errorf("string unix failed: %v %v", tm, ok)
	}
	if _, ok := parseDate([]string{"nope"}); ok {
		t.Error("garbage should not parse")
	}
}

func TestTotalUSDSkipsNonNumeric(t *testing.T) {
	p := StablecoinPoint{Pegged: map[string]any{
		"peggedUSD": float64(150e9),
		"peggedEUR": float64(3e9),
		"bogus":     "string not number",
	}}
	if got := p.TotalUSD(); got != 153e9 {
		t.Errorf("TotalUSD = %v, want 153e9", got)
	}
}
