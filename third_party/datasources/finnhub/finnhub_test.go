package finnhub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMarketNews(t *testing.T) {
	old := BaseURL
	defer func() { BaseURL = old }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/news" {
			t.Fatalf("path %s", r.URL.Path)
		}
		if r.URL.Query().Get("category") != "general" || r.URL.Query().Get("token") != "k" {
			t.Fatalf("query %s", r.URL.RawQuery)
		}
		w.Write([]byte(`[{"id":1,"headline":"H1","source":"Reuters","datetime":100,"url":"u1"},
			{"id":2,"headline":"H2","source":"Bloomberg","datetime":200,"url":"u2"}]`))
	}))
	defer srv.Close()
	BaseURL = srv.URL

	ns, err := MarketNews(context.Background(), "k", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ns) != 2 || ns[0].Headline != "H1" || ns[1].Source != "Bloomberg" {
		t.Fatalf("news = %+v", ns)
	}
}

func TestCompanyNews(t *testing.T) {
	old := BaseURL
	defer func() { BaseURL = old }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/company-news" {
			t.Fatalf("path %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("symbol") != "AAPL" || q.Get("from") != "2026-06-20" || q.Get("to") != "2026-06-27" {
			t.Fatalf("query %s", r.URL.RawQuery)
		}
		w.Write([]byte(`[{"id":9,"headline":"Apple thing","related":"AAPL","datetime":300}]`))
	}))
	defer srv.Close()
	BaseURL = srv.URL

	ns, err := CompanyNews(context.Background(), "k", "AAPL", "2026-06-20", "2026-06-27")
	if err != nil {
		t.Fatal(err)
	}
	if len(ns) != 1 || ns[0].Related != "AAPL" || ns[0].Datetime != 300 {
		t.Fatalf("news = %+v", ns)
	}
}

func TestRawPassthrough(t *testing.T) {
	old := BaseURL
	defer func() { BaseURL = old }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stock/profile2" || r.URL.Query().Get("symbol") != "MSFT" {
			t.Fatalf("req %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		w.Write([]byte(`{"name":"Microsoft","marketCapitalization":3000000}`))
	}))
	defer srv.Close()
	BaseURL = srv.URL

	// path without leading slash should be normalized
	raw, err := Raw(context.Background(), "k", "stock/profile2", map[string]string{"symbol": "MSFT"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["name"] != "Microsoft" {
		t.Fatalf("raw = %s", string(raw))
	}
}

func TestRawNoKey(t *testing.T) {
	if _, err := Raw(context.Background(), "", "/quote", nil); err == nil || !strings.Contains(err.Error(), "missing api key") {
		t.Fatalf("want missing-key error, got %v", err)
	}
}
