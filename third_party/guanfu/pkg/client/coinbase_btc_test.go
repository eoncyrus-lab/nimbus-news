package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
	"github.com/Ricaardo/nimbus-os/datasources/coinbase"
)

func TestCoinbaseBTCSourceInterface(t *testing.T) {
	var s Source = CoinbaseBTCSource{}
	if s.Key() != "coinbase_btc" {
		t.Errorf("Key = %q, want coinbase_btc", s.Key())
	}
	if s.DisplayName() == "" {
		t.Error("DisplayName empty")
	}
}

func TestCoinbaseBTCSourceRefreshStoresCloses(t *testing.T) {
	oldBase := coinbase.BaseURL
	defer func() { coinbase.BaseURL = oldBase }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/products/BTC-USD/candles" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		// [time, low, high, open, close, volume]; one row has close=0 (filtered).
		w.Write([]byte(`[
			[1700000000, 35000, 38000, 36000, 37000, 1234.5],
			[1699913600, 0, 0, 0, 0, 0]
		]`))
	}))
	defer srv.Close()
	coinbase.BaseURL = srv.URL

	ps := &store.PriceStore{Dir: t.TempDir()}
	res, err := CoinbaseBTCSource{}.Refresh(context.Background(), ps)
	if err != nil {
		t.Fatal(err)
	}
	// close<=0 row filtered → exactly one stored point.
	if res.Added != 1 {
		t.Fatalf("Added = %d, want 1 (close<=0 filtered): %+v", res.Added, res)
	}
	got, ok := ps.Latest("coinbase_btc")
	if !ok || got.Close != 37000 || got.Source != "coinbase:BTC-USD" {
		t.Fatalf("latest = %+v ok=%v", got, ok)
	}
}
