package client

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/sosovalue"
)

func TestFetchBTCETFDataAggregates7dAnd30d(t *testing.T) {
	oldBase := sosovalue.BaseURL
	defer func() { sosovalue.BaseURL = oldBase }()

	// 10 consecutive days ending today, each +100e6 net inflow. 7d sum = 700e6,
	// 30d sum = 1000e6 (only 10 days exist). newest-first in the response.
	var rows []string
	for i := 0; i < 10; i++ {
		d := time.Now().UTC().AddDate(0, 0, -i).Format("2006-01-02")
		rows = append(rows, fmt.Sprintf(
			`{"date":%q,"totalNetInflow":100000000,"totalNetAssets":120000000000,"cumNetInflow":50000000000}`, d))
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":0,"data":[` + strings.Join(rows, ",") + `]}`))
	}))
	defer srv.Close()
	sosovalue.BaseURL = srv.URL

	etf, err := FetchBTCETFData(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if etf.NetInflow7dUSD != 700000000 {
		t.Errorf("7d = %v, want 7e8", etf.NetInflow7dUSD)
	}
	if etf.NetInflow30dUSD != 1000000000 {
		t.Errorf("30d = %v, want 1e9", etf.NetInflow30dUSD)
	}
	if etf.NetInflowMA7dUSD != 100000000 {
		t.Errorf("ma7 = %v, want 1e8", etf.NetInflowMA7dUSD)
	}
	if etf.TotalAssetsUSD != 120000000000 || etf.StaleDays != 0 {
		t.Errorf("totalAssets=%v staleDays=%d", etf.TotalAssetsUSD, etf.StaleDays)
	}
}
