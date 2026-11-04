package okx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetOrderBook(t *testing.T) {
	old := base
	defer func() { base = old }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v5/market/books") {
			t.Fatalf("path %s", r.URL.Path)
		}
		if r.URL.Query().Get("instId") != "BTC-USDT" {
			t.Fatalf("query %s", r.URL.RawQuery)
		}
		// OKX levels are [price, size, liquidatedOrders, numOrders]
		w.Write([]byte(`{"code":"0","msg":"","data":[{
			"ts":"1700000000000",
			"bids":[["100.0","2.0","0","1"],["99.5","4.0","0","2"]],
			"asks":[["100.5","1.0","0","1"],["101.0","3.0","0","1"]]}]}`))
	}))
	defer srv.Close()
	base = srv.URL

	ob, err := GetOrderBook(context.Background(), "BTC-USDT", 50)
	if err != nil {
		t.Fatal(err)
	}
	if ob.Time != 1700000000000 || len(ob.Bids) != 2 || len(ob.Asks) != 2 {
		t.Fatalf("ob = %+v", ob)
	}
	if bb, ok := ob.BestBid(); !ok || bb.Price != 100.0 || bb.Qty != 2.0 {
		t.Fatalf("bestBid = %+v ok=%v", bb, ok)
	}
	if mid := ob.MidPrice(); mid != 100.25 {
		t.Fatalf("mid = %v, want 100.25", mid)
	}
	if bps := ob.SpreadBps(); bps < 49.8 || bps > 49.9 {
		t.Fatalf("spreadBps = %v, want ~49.875", bps)
	}
	if n := ob.BidNotional(); n != 598.0 {
		t.Fatalf("bidNotional = %v, want 598", n)
	}
	if n := ob.AskNotional(); n != 403.5 {
		t.Fatalf("askNotional = %v, want 403.5", n)
	}
}

func TestGetOrderBookClampsSz(t *testing.T) {
	old := base
	defer func() { base = old }()
	var gotSz string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSz = r.URL.Query().Get("sz")
		w.Write([]byte(`{"code":"0","msg":"","data":[{"ts":"1","bids":[],"asks":[]}]}`))
	}))
	defer srv.Close()
	base = srv.URL

	if _, err := GetOrderBook(context.Background(), "BTC-USDT", 9999); err != nil {
		t.Fatal(err)
	}
	if gotSz != "400" {
		t.Fatalf("sz = %q, want 400 (clamped)", gotSz)
	}
}

func TestGetOrderBookEmptyData(t *testing.T) {
	old := base
	defer func() { base = old }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":"0","msg":"","data":[]}`))
	}))
	defer srv.Close()
	base = srv.URL

	if _, err := GetOrderBook(context.Background(), "NOPE-USDT", 50); err == nil {
		t.Fatal("expected error on empty data")
	}
}
