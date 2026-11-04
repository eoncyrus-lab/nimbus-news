package client

import (
	"context"
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/fred"
	"github.com/shopspring/decimal"
)

func TestFetchMacroDataWithoutAPIKeyIsOptional(t *testing.T) {
	t.Setenv("FRED_API_KEY", "")

	got, err := FetchMacroData(context.Background(), nil)
	if err != nil {
		t.Fatalf("FetchMacroData without key returned error: %v", err)
	}
	if got != nil {
		t.Fatalf("FetchMacroData without key = %+v, want nil optional macro data", got)
	}
}

func TestComputeBTCSPXCorrelation30dMatchesSPXDates(t *testing.T) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	spxObs := make([]fred.Observation, 31)
	btcHistory := make([]decimal.Decimal, 40)

	for i := range btcHistory {
		btcHistory[i] = decimal.NewFromFloat(1000 + float64(39-i)*10)
	}
	for i := range spxObs {
		date := today.AddDate(0, 0, -i)
		spxObs[i] = fred.Observation{
			Date:  date.Format("2006-01-02"),
			Value: decimal.NewFromFloat(4000 + float64(30-i)*20).String(),
		}
	}

	corr, asOf, ok := computeBTCSPXCorrelation30d(spxObs, btcHistory)
	if !ok {
		t.Fatal("expected dated BTC/SPX observations to produce correlation")
	}
	if asOf != today.Format("2006-01-02") {
		t.Fatalf("asOf = %q, want today", asOf)
	}
	if corr < 0.99 {
		t.Fatalf("corr = %.6f, want strongly positive", corr)
	}
}
