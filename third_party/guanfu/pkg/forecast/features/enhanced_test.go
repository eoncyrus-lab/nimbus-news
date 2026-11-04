package features

import (
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

func TestPutCallRatioExtractor(t *testing.T) {
	ps := &store.PriceStore{Dir: t.TempDir()}
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	points := make([]store.PricePoint, 300)
	for i := range points {
		points[i] = store.PricePoint{
			Date:   start.AddDate(0, 0, i).Format("2006-01-02"),
			Close:  0.8 + float64(i%50)/100,
			Source: "stooq:^PC",
		}
	}
	if err := ps.Save("stooq_putcall", points); err != nil {
		t.Fatal(err)
	}

	ex := PutCallRatioExtractorWithMode(ps, PutCallAll)
	if ex == nil {
		t.Fatal("expected put/call extractor")
	}
	pricePoints := make([]forecast.Point, 300)
	for i := range pricePoints {
		pricePoints[i] = forecast.Point{
			Date:  start.AddDate(0, 0, i).Format("2006-01-02"),
			Close: 100 + float64(i),
		}
	}
	values, ok := ex(pricePoints, len(pricePoints)-1)
	if !ok {
		t.Fatal("expected put/call features")
	}
	names := map[string]bool{}
	for _, v := range values {
		names[v.Name] = true
		if v.Weight <= 0 {
			t.Fatalf("invalid feature weight: %+v", v)
		}
	}
	for _, name := range []string{"put_call_ratio", "put_call_30d_change", "put_call_252d_percentile"} {
		if !names[name] {
			t.Fatalf("missing %s in %+v", name, values)
		}
	}
}

func TestPutCallRatioExtractorModes(t *testing.T) {
	ps := &store.PriceStore{Dir: t.TempDir()}
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	points := make([]store.PricePoint, 300)
	for i := range points {
		points[i] = store.PricePoint{
			Date:   start.AddDate(0, 0, i).Format("2006-01-02"),
			Close:  0.8 + float64(i%50)/100,
			Source: "cboe:test",
		}
	}
	if err := ps.Save("stooq_putcall", points); err != nil {
		t.Fatal(err)
	}
	pricePoints := make([]forecast.Point, 300)
	for i := range pricePoints {
		pricePoints[i] = forecast.Point{
			Date:  start.AddDate(0, 0, i).Format("2006-01-02"),
			Close: 100 + float64(i),
		}
	}
	cases := []struct {
		mode PutCallFeatureMode
		want int
	}{
		{PutCallRatioOnly, 1},
		{PutCallRatioAndChange, 2},
		{PutCallAll, 3},
	}
	for _, tc := range cases {
		ex := PutCallRatioExtractorWithMode(ps, tc.mode)
		if ex == nil {
			t.Fatalf("mode %v returned nil", tc.mode)
		}
		values, ok := ex(pricePoints, len(pricePoints)-1)
		if !ok || len(values) != tc.want {
			t.Fatalf("mode %v got ok=%v len=%d, want %d", tc.mode, ok, len(values), tc.want)
		}
	}
}

func TestPutCallRatioExtractorRejectsStaleForwardFill(t *testing.T) {
	ps := &store.PriceStore{Dir: t.TempDir()}
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	points := make([]store.PricePoint, 300)
	for i := range points {
		points[i] = store.PricePoint{
			Date:   start.AddDate(0, 0, i).Format("2006-01-02"),
			Close:  0.8 + float64(i%50)/100,
			Source: "cboe:test",
		}
	}
	if err := ps.Save("stooq_putcall", points); err != nil {
		t.Fatal(err)
	}

	ex := PutCallRatioExtractorWithMode(ps, PutCallAll)
	if ex == nil {
		t.Fatal("expected put/call extractor")
	}
	staleStart := start.AddDate(1, 0, 0)
	pricePoints := make([]forecast.Point, 300)
	for i := range pricePoints {
		pricePoints[i] = forecast.Point{
			Date:  staleStart.AddDate(0, 0, i).Format("2006-01-02"),
			Close: 100 + float64(i),
		}
	}
	if values, ok := ex(pricePoints, len(pricePoints)-1); ok || len(values) != 0 {
		t.Fatalf("expected stale CBOE data to be rejected, got ok=%v values=%+v", ok, values)
	}
}
