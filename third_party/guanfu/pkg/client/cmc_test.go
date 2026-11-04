package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
	"github.com/Ricaardo/nimbus-os/datasources/cmc"
)

func TestCMCMarketContextSourceRefreshStoresMetrics(t *testing.T) {
	oldBase := cmc.BaseURL
	defer func() { cmc.BaseURL = oldBase }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-CMC_PRO_API_KEY") != "test-key" {
			t.Fatalf("missing CMC api key header")
		}
		switch r.URL.Path {
		case "/v1/global-metrics/quotes/latest":
			w.Write([]byte(`{
				"status":{"error_code":0},
				"data":{
					"btc_dominance":58.12,
					"eth_dominance":9.31,
					"quote":{"USD":{
						"total_market_cap":3500000000000,
						"total_volume_24h":120000000000,
						"altcoin_market_cap":1400000000000,
						"altcoin_volume_24h":70000000000,
						"last_updated":"2026-05-10T12:34:56Z"
					}}
				}
			}`))
		case "/v2/cryptocurrency/quotes/latest":
			w.Write([]byte(`{
				"status":{"error_code":0},
				"data":{"1":{
					"id":1,
					"symbol":"BTC",
					"quote":{"USD":{
						"price":104000,
						"volume_24h":42000000000,
						"market_cap":2050000000000,
						"last_updated":"2026-05-10T12:35:10Z"
					}}
				}}
			}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	cmc.BaseURL = srv.URL

	t.Setenv(cmcAPIKeyEnv, "test-key")
	ps := &store.PriceStore{Dir: t.TempDir()}
	res, err := CMCMarketContextSource{}.Refresh(context.Background(), ps)
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != "full" || res.Added == 0 {
		t.Fatalf("unexpected refresh result: %+v", res)
	}

	for key, want := range map[string]float64{
		"cmc_total_market_cap_usd": 3500000000000,
		"cmc_btc_dominance_pct":    58.12,
		"cmc_btc_price_usd":        104000,
	} {
		got, ok := ps.Latest(key)
		if !ok || got.Close != want || got.Date != "2026-05-10" {
			t.Fatalf("%s latest = %+v ok=%v, want close=%v date=2026-05-10", key, got, ok, want)
		}
	}
}

func TestCMCMarketContextSourceSkipsWithoutKey(t *testing.T) {
	t.Setenv(cmcAPIKeyEnv, "")
	t.Setenv(cmcAPIKeyEnvAlt, "")
	t.Setenv("GUANFU_ENV_FILE", t.TempDir()+"/missing-env")
	ps := &store.PriceStore{Dir: t.TempDir()}
	res, err := CMCMarketContextSource{}.Refresh(context.Background(), ps)
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != "skip" || res.SkipReason != "config" || !strings.Contains(res.Error, cmcAPIKeyEnv) {
		t.Fatalf("unexpected skip result: %+v", res)
	}
}

func TestReadEnvFileValueSupportsExportSyntax(t *testing.T) {
	path := t.TempDir() + "/env"
	if err := os.WriteFile(path, []byte("export CMC_API_KEY='abc123'\nOTHER=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readEnvFileValue(path, cmcAPIKeyEnv); got != "abc123" {
		t.Fatalf("readEnvFileValue = %q, want abc123", got)
	}
}
