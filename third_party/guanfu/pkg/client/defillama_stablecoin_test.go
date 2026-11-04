package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
	"github.com/Ricaardo/nimbus-os/datasources/defillama"
)

func TestDefillamaStablecoinSourceInterface(t *testing.T) {
	var s Source = DefillamaStablecoinSource{}
	if s.Key() == "" {
		t.Error("Key must not be empty")
	}
	if s.DisplayName() == "" {
		t.Error("DisplayName must not be empty")
	}
}

func TestDefillamaStablecoinSourceRefreshStoresSeries(t *testing.T) {
	oldBase := defillama.BaseURL
	defer func() { defillama.BaseURL = oldBase }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stablecoincharts/all" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Write([]byte(`[
			{"date":1600000000,"totalCirculatingUSD":{"peggedUSD":120000000000}},
			{"date":"1700000000","totalCirculatingUSD":{"peggedUSD":150000000000,"peggedEUR":3000000000}}
		]`))
	}))
	defer srv.Close()
	defillama.BaseURL = srv.URL

	ps := &store.PriceStore{Dir: t.TempDir()}
	res, err := DefillamaStablecoinSource{}.Refresh(context.Background(), ps)
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != "full" || res.Added != 2 {
		t.Fatalf("unexpected refresh result: %+v", res)
	}
	got, ok := ps.Latest("defillama_stablecoin_supply")
	if !ok || got.Close != 153e9 || got.Date != "2023-11-14" {
		t.Fatalf("latest = %+v ok=%v, want close=153e9 date=2023-11-14", got, ok)
	}
	if got.Source != "defillama:stablecoin_supply" {
		t.Fatalf("source = %q", got.Source)
	}
}
