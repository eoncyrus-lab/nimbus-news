package engine

import (
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

func TestEnrichGlobalInvestorMacroAddsFXAndCentralBankContext(t *testing.T) {
	ps := &store.PriceStore{Dir: t.TempDir()}
	today := time.Now().UTC().Format("2006-01-02")
	past := time.Now().AddDate(0, 0, -70).UTC().Format("2006-01-02")
	rateDate := time.Now().AddDate(0, 0, -2).UTC().Format("2006-01-02")
	if err := ps.Save("usd_cny", []store.PricePoint{
		{Date: past, Close: 7.00, Source: "yahoo:CNY=X"},
		{Date: today, Close: 7.21, Source: "yahoo:CNY=X"},
	}); err != nil {
		t.Fatal(err)
	}
	for key, v := range map[string]float64{
		"fred_fed_funds":           4.25,
		"fred_ecb_deposit_rate":    2.00,
		"fred_boj_call_rate":       0.73,
		"fred_pboc_interbank_rate": 2.90,
	} {
		if err := ps.Save(key, []store.PricePoint{{Date: rateDate, Close: v, Source: "fred:test"}}); err != nil {
			t.Fatalf("save %s: %v", key, err)
		}
	}

	panel := &model.IndicatorPanel{Macro: map[string]model.Indicator{}}
	EnrichGlobalInvestorMacro(panel, ps)

	if got := panel.Macro["usd_cny"]; got.Value != 7.21 || got.Missing {
		t.Fatalf("unexpected usd_cny indicator: %+v", got)
	}
	if got := panel.Macro["usd_cny_60d_trend_pct"]; got.Value != 3 || got.Missing {
		t.Fatalf("unexpected usd_cny trend: %+v", got)
	}
	if got := panel.Macro["global_rate_us_fed_pct"]; got.Value != 4.25 || got.Missing {
		t.Fatalf("unexpected Fed rate: %+v", got)
	}
	if got := panel.Macro["global_rate_spread_us_cn_pct"]; got.Value != 1.35 || got.Missing {
		t.Fatalf("unexpected US-CN spread: %+v", got)
	}
	if got := panel.Macro["global_dm_policy_rate_avg_pct"]; got.Value != 2.33 || got.Missing {
		t.Fatalf("unexpected DM policy avg: %+v", got)
	}

	if got, ok := testSourceHealth(panel.SourceHealth, investorFXSourceName); !ok || got.Status != "ok" || got.AsOf != today {
		t.Fatalf("unexpected FX source health ok=%v health=%+v", ok, got)
	}
	if got, ok := testSourceHealth(panel.SourceHealth, globalCentralBankRateSource); !ok || got.Status != "ok" || got.AsOf != rateDate {
		t.Fatalf("unexpected central bank source health ok=%v health=%+v", ok, got)
	}
}

func TestEnrichGlobalInvestorMacroFallsBackToTBillWhenFedFundsMissing(t *testing.T) {
	ps := &store.PriceStore{Dir: t.TempDir()}
	rateDate := time.Now().AddDate(0, 0, -2).UTC().Format("2006-01-02")
	if err := ps.Save("fred_dgs3mo", []store.PricePoint{{Date: rateDate, Close: 4.10, Source: "fred:DGS3MO"}}); err != nil {
		t.Fatal(err)
	}

	panel := &model.IndicatorPanel{}
	EnrichGlobalInvestorMacro(panel, ps)

	got, ok := panel.Macro["global_rate_us_fed_pct"]
	if !ok || got.Value != 4.10 || got.Source != "fred:DGS3MO" {
		t.Fatalf("expected DGS3MO fallback, got ok=%v ind=%+v", ok, got)
	}

	health, ok := testSourceHealth(panel.SourceHealth, globalCentralBankRateSource)
	if !ok || health.Status != "partial" || health.AsOf != rateDate {
		t.Fatalf("expected partial central-bank source health, got ok=%v health=%+v", ok, health)
	}
}

func TestEnrichGlobalInvestorMacroAddsCNYReturnLens(t *testing.T) {
	ps := &store.PriceStore{Dir: t.TempDir()}
	today := time.Now().UTC().Format("2006-01-02")
	past90 := time.Now().AddDate(0, 0, -100).UTC().Format("2006-01-02")
	if err := ps.Save("usd_cny", []store.PricePoint{
		{Date: past90, Close: 7.00, Source: "yahoo:CNY=X"},
		{Date: today, Close: 7.20, Source: "yahoo:CNY=X"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := ps.Save("qqq", []store.PricePoint{
		{Date: past90, Close: 100, Source: "test"},
		{Date: today, Close: 110, Source: "test"},
	}); err != nil {
		t.Fatal(err)
	}

	panel := &model.IndicatorPanel{Asset: "qqq", Macro: map[string]model.Indicator{}}
	EnrichGlobalInvestorMacro(panel, ps)

	if got := panel.Macro["asset_price_cny"]; got.Value != 792 {
		t.Fatalf("asset_price_cny = %+v, want 792", got)
	}
	if got := panel.Macro["asset_return_usd_90d"]; got.Value != 10 {
		t.Fatalf("asset_return_usd_90d = %+v, want 10", got)
	}
	if got := panel.Macro["asset_return_cny_90d"]; got.Value < 13.1 || got.Value > 13.2 {
		t.Fatalf("asset_return_cny_90d = %+v, want about 13.14", got)
	}
}

func TestEnrichGlobalInvestorMacroAddsEquityPutCall(t *testing.T) {
	ps := &store.PriceStore{Dir: t.TempDir()}
	start := time.Now().AddDate(0, 0, -300).UTC()
	points := make([]store.PricePoint, 301)
	for i := range points {
		points[i] = store.PricePoint{
			Date:   start.AddDate(0, 0, i).Format("2006-01-02"),
			Close:  0.8 + float64(i%50)/100,
			Source: "stooq:^PC",
		}
	}
	points[len(points)-1].Close = 1.25
	if err := ps.Save("stooq_putcall", points); err != nil {
		t.Fatal(err)
	}

	panel := &model.IndicatorPanel{Asset: "qqq", Macro: map[string]model.Indicator{}}
	EnrichGlobalInvestorMacro(panel, ps)

	if got := panel.Positioning["put_call_ratio"]; got.Value != 1.25 || got.Missing {
		t.Fatalf("unexpected put_call_ratio: %+v", got)
	}
	if _, ok := panel.Positioning["put_call_252d_percentile"]; !ok {
		t.Fatal("expected put_call_252d_percentile")
	}
	if _, ok := testSourceHealth(panel.SourceHealth, "forecast_bundle_stooq_putcall"); !ok {
		t.Fatal("expected stooq_putcall source health")
	}
}

func testSourceHealth(items []model.SourceHealth, source string) (model.SourceHealth, bool) {
	for _, item := range items {
		if item.Source == source {
			return item, true
		}
	}
	return model.SourceHealth{}, false
}
