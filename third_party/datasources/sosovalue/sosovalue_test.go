package sosovalue

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHistoricalInflowChartArrayShape(t *testing.T) {
	oldBase := BaseURL
	defer func() { BaseURL = oldBase }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/openapi/v2/etf/historicalInflowChart" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("x-soso-api-key") != "k123" {
			t.Fatalf("missing api key header")
		}
		body, _ := io.ReadAll(r.Body)
		var got struct {
			Type string `json:"type"`
		}
		json.Unmarshal(body, &got)
		if got.Type != "us-btc-spot" {
			t.Fatalf("body type = %q", got.Type)
		}
		w.Write([]byte(`{"code":0,"msg":"ok","data":[
			{"date":"2026-06-02","totalNetInflow":200000000,"totalNetAssets":120000000000,"cumNetInflow":50000000000},
			{"date":"2026-06-01","totalNetInflow":100000000,"totalNetAssets":119000000000,"cumNetInflow":49800000000}
		]}`))
	}))
	defer srv.Close()
	BaseURL = srv.URL

	flows, err := HistoricalInflowChart(context.Background(), "k123", "us-btc-spot")
	if err != nil {
		t.Fatal(err)
	}
	if len(flows) != 2 {
		t.Fatalf("len = %d, want 2", len(flows))
	}
	// oldest-first
	if flows[0].Date.After(flows[1].Date) {
		t.Fatalf("not oldest-first: %v %v", flows[0].Date, flows[1].Date)
	}
	if flows[1].NetInflowUSD != 200000000 || flows[1].TotalAssetsUSD != 120000000000 {
		t.Fatalf("flows[1] = %+v", flows[1])
	}
}

func TestHistoricalInflowChartWrappedListShape(t *testing.T) {
	oldBase := BaseURL
	defer func() { BaseURL = oldBase }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":0,"data":{"list":[
			{"date":"2026-06-01","totalNetInflow":100000000,"totalNetAssets":119000000000,"cumNetInflow":49800000000}
		]}}`))
	}))
	defer srv.Close()
	BaseURL = srv.URL

	flows, err := HistoricalInflowChart(context.Background(), "", "us-btc-spot")
	if err != nil {
		t.Fatal(err)
	}
	if len(flows) != 1 || flows[0].NetInflowUSD != 100000000 {
		t.Fatalf("flows = %+v", flows)
	}
}

func TestHistoricalInflowChartErrorCode(t *testing.T) {
	oldBase := BaseURL
	defer func() { BaseURL = oldBase }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":40001,"msg":"rate limited","data":null}`))
	}))
	defer srv.Close()
	BaseURL = srv.URL

	_, err := HistoricalInflowChart(context.Background(), "", "us-btc-spot")
	if err == nil {
		t.Fatal("expected error on code!=0")
	}
}

func TestHistoricalInflowChartEmpty(t *testing.T) {
	oldBase := BaseURL
	defer func() { BaseURL = oldBase }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":0,"data":[]}`))
	}))
	defer srv.Close()
	BaseURL = srv.URL

	flows, err := HistoricalInflowChart(context.Background(), "", "us-btc-spot")
	if err != nil {
		t.Fatalf("empty should not error: %v", err)
	}
	if flows != nil {
		t.Fatalf("empty should return nil, got %v", flows)
	}
}
