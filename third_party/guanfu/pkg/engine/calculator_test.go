package engine

import (
	"math"
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"

	"github.com/shopspring/decimal"
)

func TestCalculateDcaCostUsesFixedAmountCostBasis(t *testing.T) {
	history := []decimal.Decimal{
		decimal.NewFromInt(100),
		decimal.NewFromInt(200),
	}

	got, ok := calculateDcaCost(history, 0, 2)
	if !ok {
		t.Fatal("expected DCA cost to be calculated")
	}

	want := 2.0 / (1.0/100.0 + 1.0/200.0)
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("DCA cost = %f, want %f", got, want)
	}
}

func TestCalcAhr999AdaptiveScoreDistinguishesValuationRegimes(t *testing.T) {
	asOf := time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)
	baseHistory := syntheticBTCHistory(asOf, ahrFitWindowDays)

	cheapHistory := cloneDecimalSlice(baseHistory)
	cheapHistory[0] = cheapHistory[0].Mul(decimal.NewFromFloat(0.35))
	cheapSnap := &model.MarketSnapshot{
		Date:            asOf,
		BTCPrice:        cheapHistory[0],
		BTCPriceHistory: cheapHistory,
	}

	expensiveHistory := cloneDecimalSlice(baseHistory)
	expensiveHistory[0] = expensiveHistory[0].Mul(decimal.NewFromFloat(3.0))
	expensiveSnap := &model.MarketSnapshot{
		Date:            asOf,
		BTCPrice:        expensiveHistory[0],
		BTCPriceHistory: expensiveHistory,
	}

	calculator := &Calculator{}
	cheapScore, _, cheapQ, cheapOK := calculator.calcAhr999Detailed(cheapSnap)
	expensiveScore, _, expensiveQ, expensiveOK := calculator.calcAhr999Detailed(expensiveSnap)

	if !cheapOK || !expensiveOK {
		t.Fatalf("expected adaptive AHR999 to be available, cheapOK=%v expensiveOK=%v", cheapOK, expensiveOK)
	}

	if cheapScore.LessThan(decimal.NewFromFloat(0.5)) {
		t.Fatalf("cheap score = %s, want >= 0.5", cheapScore)
	}
	if expensiveScore.GreaterThan(decimal.NewFromFloat(-0.5)) {
		t.Fatalf("expensive score = %s, want <= -0.5", expensiveScore)
	}
	if cheapQ >= expensiveQ {
		t.Fatalf("cheap q = %f, expensive q = %f; want cheap < expensive", cheapQ, expensiveQ)
	}
}

func TestBuildPanelUsesAhrQuantileFromAhrDistribution(t *testing.T) {
	asOf := time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)
	history := syntheticBTCHistory(asOf, ahrFitWindowDays)
	history[0] = history[0].Mul(decimal.NewFromFloat(1.4))
	snap := &model.MarketSnapshot{
		Date:            asOf,
		BTCPrice:        history[0],
		BTCPriceHistory: history,
	}

	calculator := NewCalculator(&model.Config{})
	_, raw, q, ok := calculator.calcAhr999Detailed(snap)
	if !ok {
		t.Fatal("expected detailed AHR999 to be available")
	}

	panel := calculator.BuildPanel(snap)
	ahr, ok := panel.Valuation["ahr999"]
	if !ok {
		t.Fatal("expected ahr999 indicator")
	}
	if math.Abs(ahr.Value-f(raw)) > 1e-9 {
		t.Fatalf("ahr999 value = %f, want %f", ahr.Value, f(raw))
	}
	if math.Abs(ahr.Quantile-displayQuantile(q)) > 1e-9 {
		t.Fatalf("ahr999 q = %f, want %f", ahr.Quantile, displayQuantile(q))
	}
}

func TestDisplayQuantileSuppressesInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		in   float64
		want float64
	}{
		{name: "negative sentinel", in: -1, want: 0},
		{name: "above range", in: 1.01, want: 0},
		{name: "nan", in: math.NaN(), want: 0},
		{name: "inf", in: math.Inf(1), want: 0},
		{name: "valid low", in: 0.2, want: 0.2},
		{name: "valid high", in: 1, want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := displayQuantile(tt.in); got != tt.want {
				t.Fatalf("displayQuantile(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestCalc3DScoreUsesNewestFirstHistory(t *testing.T) {
	history := make([]decimal.Decimal, 300)
	for i := range history {
		history[i] = decimal.NewFromInt(100)
	}
	history[10] = decimal.NewFromInt(125)
	for i := 200; i < len(history); i++ {
		history[i] = decimal.NewFromInt(10)
	}
	snap := &model.MarketSnapshot{
		Date:            time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC),
		BTCPrice:        decimal.NewFromInt(100),
		BTCPriceHistory: history,
	}

	_, _, mayer, drawdown, ok := NewCalculator(&model.Config{}).calc3DScore(snap)
	if !ok {
		t.Fatal("expected 3D score to be available")
	}
	wantMayer := 100.0 / ((199*100.0 + 125.0) / 200.0)
	if math.Abs(mayer-wantMayer) > 1e-9 {
		t.Fatalf("d3 mayer = %f, want %f", mayer, wantMayer)
	}
	if math.Abs(drawdown-(-0.2)) > 1e-9 {
		t.Fatalf("d3 drawdown = %f, want -0.2", drawdown)
	}
}

func TestBuildPanelIncludesOnchainValuation(t *testing.T) {
	snap := &model.MarketSnapshot{
		Date:                    time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC),
		OnchainValuationFetched: true,
		OnchainValuationAsOf:    "2026-05-01",
		CapMVRVCur:              decimal.NewFromFloat(1.8),
		MVRVZScore:              decimal.NewFromFloat(2.5),
		NUPL:                    decimal.NewFromFloat(0.4444),
		MVRVQuantile:            decimal.NewFromFloat(0.7),
		NUPLQuantile:            decimal.NewFromFloat(0.72),
	}

	panel := NewCalculator(&model.Config{}).BuildPanel(snap)
	mvrv, ok := panel.Valuation["mvrv"]
	if !ok {
		t.Fatal("expected mvrv indicator")
	}
	if mvrv.Value != 1.8 {
		t.Fatalf("mvrv value = %f", mvrv.Value)
	}
	if mvrv.Quantile != 0.7 {
		t.Fatalf("mvrv q = %f", mvrv.Quantile)
	}
	if mvrv.UpdatedAt != "2026-05-01T00:00:00Z" {
		t.Fatalf("mvrv updated_at = %s", mvrv.UpdatedAt)
	}
	if _, ok := panel.Valuation["nupl"]; !ok {
		t.Fatal("expected nupl indicator")
	}
	if _, ok := panel.Valuation["mvrv_z_score"]; !ok {
		t.Fatal("expected mvrv_z_score indicator")
	}
}

func TestBuildPanelMarksAltcoinSeasonMissingWhenTop50Unavailable(t *testing.T) {
	panel := NewCalculator(&model.Config{}).BuildPanel(&model.MarketSnapshot{
		Date: time.Date(2026, 5, 3, 0, 0, 0, 0, time.UTC),
	})

	alt, ok := panel.Positioning["altcoin_season"]
	if !ok {
		t.Fatal("expected altcoin_season placeholder")
	}
	if !alt.Missing {
		t.Fatalf("expected missing altcoin_season when top50 history is unavailable, got %+v", alt)
	}
}

func TestSourceTimestampNormalizesDates(t *testing.T) {
	if got := sourceTimestamp("2026-05-01", "fallback"); got != "2026-05-01T00:00:00Z" {
		t.Fatalf("date timestamp = %s", got)
	}
	if got := sourceTimestamp("", "fallback"); got != "fallback" {
		t.Fatalf("fallback timestamp = %s", got)
	}
}

func syntheticBTCHistory(asOf time.Time, days int) []decimal.Decimal {
	history := make([]decimal.Decimal, days)
	ageNow := bitcoinAgeDays(asOf)

	for i := 0; i < days; i++ {
		date := asOf.AddDate(0, 0, -i)
		age := bitcoinAgeDays(date)
		trend := 65000.0 * math.Pow(age/ageNow, 2.2)
		cycle := math.Exp(
			0.55*math.Sin(float64(i)*2*math.Pi/1460.0) +
				0.20*math.Sin(float64(i)*2*math.Pi/365.0),
		)
		history[i] = decimal.NewFromFloat(trend * cycle)
	}

	return history
}

func cloneDecimalSlice(values []decimal.Decimal) []decimal.Decimal {
	out := make([]decimal.Decimal, len(values))
	copy(out, values)
	return out
}
