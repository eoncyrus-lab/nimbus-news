package edgar

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCIKForTicker(t *testing.T) {
	oldBase := SECBase
	defer func() { SECBase = oldBase }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/files/company_tickers.json" {
			t.Fatalf("path %s", r.URL.Path)
		}
		if !strings.Contains(r.Header.Get("User-Agent"), "nimbus-os") {
			t.Fatalf("missing SEC UA: %q", r.Header.Get("User-Agent"))
		}
		w.Write([]byte(`{"0":{"cik_str":320193,"ticker":"AAPL","title":"Apple Inc."},
			"1":{"cik_str":789019,"ticker":"MSFT","title":"Microsoft"}}`))
	}))
	defer srv.Close()
	SECBase = srv.URL

	cik, err := CIKForTicker(context.Background(), "msft")
	if err != nil {
		t.Fatal(err)
	}
	if cik != 789019 {
		t.Fatalf("cik = %d, want 789019", cik)
	}
	if _, err := CIKForTicker(context.Background(), "NOPE"); err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestRecentFilings(t *testing.T) {
	oldData := SECDataBase
	oldBase := SECBase
	defer func() { SECDataBase, SECBase = oldData, oldBase }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"filings":{"recent":{
			"form":["10-K","8-K","4"],
			"filingDate":["2026-02-01","2026-01-15","2026-01-10"],
			"accessionNumber":["0000320193-26-000001","0000320193-26-000002","0000320193-26-000003"],
			"primaryDocument":["aapl-10k.htm","aapl-8k.htm","form4.xml"]
		}}}`))
	}))
	defer srv.Close()
	SECDataBase = srv.URL
	SECBase = srv.URL

	fs, err := RecentFilings(context.Background(), 320193, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 2 {
		t.Fatalf("len = %d, want 2 (limit)", len(fs))
	}
	if fs[0].Form != "10-K" || fs[0].FilingDate != "2026-02-01" {
		t.Fatalf("fs[0] = %+v", fs[0])
	}
	wantURL := srv.URL + "/Archives/edgar/data/320193/000032019326000001/aapl-10k.htm"
	if fs[0].URL != wantURL {
		t.Fatalf("url = %q, want %q", fs[0].URL, wantURL)
	}
}

func TestParseInfoTableMergesByIssuer(t *testing.T) {
	xml := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<informationTable xmlns="http://www.sec.gov/edgar/document/thirteenf/informationtable">
  <infoTable><nameOfIssuer>APPLE INC</nameOfIssuer><value>1000</value>
    <shrsOrPrnAmt><sshPrnamt>50</sshPrnamt></shrsOrPrnAmt></infoTable>
  <infoTable><nameOfIssuer>APPLE INC</nameOfIssuer><value>500</value>
    <shrsOrPrnAmt><sshPrnamt>25</sshPrnamt></shrsOrPrnAmt></infoTable>
  <infoTable><nameOfIssuer>MICROSOFT CORP</nameOfIssuer><value>2000</value>
    <shrsOrPrnAmt><sshPrnamt>10</sshPrnamt></shrsOrPrnAmt></infoTable>
</informationTable>`)
	hs := parseInfoTable(xml)
	if len(hs) != 2 {
		t.Fatalf("len = %d, want 2 (merged): %+v", len(hs), hs)
	}
	byName := map[string]Holding{}
	for _, h := range hs {
		byName[h.Name] = h
	}
	if byName["APPLE INC"].Value != 1500 || byName["APPLE INC"].Shares != 75 {
		t.Fatalf("APPLE = %+v, want value=1500 shares=75", byName["APPLE INC"])
	}
	if byName["MICROSOFT CORP"].Value != 2000 || byName["MICROSOFT CORP"].Shares != 10 {
		t.Fatalf("MSFT = %+v", byName["MICROSOFT CORP"])
	}
}

func TestHoldings13FEndToEnd(t *testing.T) {
	oldData, oldBase := SECDataBase, SECBase
	defer func() { SECDataBase, SECBase = oldData, oldBase }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/submissions/"):
			w.Write([]byte(`{"filings":{"recent":{
				"form":["13F-HR","8-K","13F-HR"],
				"filingDate":["2026-05-15","2026-04-01","2026-02-14"],
				"reportDate":["2026-03-31","","2025-12-31"],
				"accessionNumber":["0001067983-26-000050","x","0001067983-26-000020"]
			}}}`))
		case strings.HasSuffix(r.URL.Path, "/index.json"):
			w.Write([]byte(`{"directory":{"item":[
				{"name":"primary_doc.xml"},{"name":"form13fInfoTable.xml"}]}}`))
		case strings.HasSuffix(r.URL.Path, "form13fInfoTable.xml"):
			w.Write([]byte(`<informationTable xmlns="http://x">
				<infoTable><nameOfIssuer>BERK</nameOfIssuer><value>900</value>
				  <shrsOrPrnAmt><sshPrnamt>3</sshPrnamt></shrsOrPrnAmt></infoTable>
				<infoTable><nameOfIssuer>COKE</nameOfIssuer><value>1500</value>
				  <shrsOrPrnAmt><sshPrnamt>9</sshPrnamt></shrsOrPrnAmt></infoTable></informationTable>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	SECDataBase = srv.URL
	SECBase = srv.URL

	periods, err := Holdings13F(context.Background(), "0001067983", 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(periods) != 2 {
		t.Fatalf("periods = %d, want 2 (two 13F-HR)", len(periods))
	}
	// holdings sorted by value desc → COKE(1500) before BERK(900)
	h := periods[0].Holdings
	if len(h) != 2 || h[0].Name != "COKE" || h[0].Value != 1500 || h[1].Name != "BERK" {
		t.Fatalf("holdings = %+v", h)
	}
	if periods[0].ReportDate != "2026-03-31" {
		t.Fatalf("reportDate = %q", periods[0].ReportDate)
	}
}
