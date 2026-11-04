package binance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDepth(t *testing.T) {
	old := spotBase
	defer func() { spotBase = old }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/v3/depth") {
			t.Fatalf("path %s", r.URL.Path)
		}
		if r.URL.Query().Get("symbol") != "BTCUSDT" || r.URL.Query().Get("limit") != "10" {
			t.Fatalf("query %s", r.URL.RawQuery)
		}
		w.Write([]byte(`{"lastUpdateId":42,
			"bids":[["100.0","2.0"],["99.5","4.0"]],
			"asks":[["100.5","1.0"],["101.0","3.0"]]}`))
	}))
	defer srv.Close()
	spotBase = srv.URL

	ob, err := Depth(context.Background(), "BTCUSDT", 10)
	if err != nil {
		t.Fatal(err)
	}
	if ob.LastUpdateID != 42 || len(ob.Bids) != 2 || len(ob.Asks) != 2 {
		t.Fatalf("ob = %+v", ob)
	}
	if bb, ok := ob.BestBid(); !ok || bb.Price != 100.0 || bb.Qty != 2.0 {
		t.Fatalf("bestBid = %+v ok=%v", bb, ok)
	}
	if ba, ok := ob.BestAsk(); !ok || ba.Price != 100.5 {
		t.Fatalf("bestAsk = %+v ok=%v", ba, ok)
	}
	if mid := ob.MidPrice(); mid != 100.25 {
		t.Fatalf("mid = %v, want 100.25", mid)
	}
	// spread 0.5 / mid 100.25 * 1e4 ≈ 49.875 bps
	if bps := ob.SpreadBps(); bps < 49.8 || bps > 49.9 {
		t.Fatalf("spreadBps = %v, want ~49.875", bps)
	}
	// bid notional 100*2 + 99.5*4 = 598
	if n := ob.BidNotional(); n != 598.0 {
		t.Fatalf("bidNotional = %v, want 598", n)
	}
	// ask notional 100.5*1 + 101*3 = 403.5
	if n := ob.AskNotional(); n != 403.5 {
		t.Fatalf("askNotional = %v, want 403.5", n)
	}
}

func TestOrderBookEmptySides(t *testing.T) {
	ob := &OrderBook{Symbol: "X"}
	if _, ok := ob.BestBid(); ok {
		t.Fatal("empty bids should be ok=false")
	}
	if ob.MidPrice() != 0 || ob.SpreadBps() != 0 {
		t.Fatal("empty book should yield 0 mid/spread")
	}
	// one-sided book: guard fires on the empty side, not on mid==0
	half := &OrderBook{Bids: []Level{{Price: 100, Qty: 1}}}
	if half.MidPrice() != 0 || half.SpreadBps() != 0 {
		t.Fatal("one-sided book should yield 0 mid/spread")
	}
}

func TestSpreadBpsCrossed(t *testing.T) {
	// crossed book: best ask below best bid → negative spread
	ob := &OrderBook{
		Bids: []Level{{Price: 101, Qty: 1}},
		Asks: []Level{{Price: 100, Qty: 1}},
	}
	if bps := ob.SpreadBps(); bps >= 0 {
		t.Fatalf("crossed book spreadBps = %v, want negative", bps)
	}
}
