package screener

import (
	"os"
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
)

func TestDuckDBPriceProvider_Load(t *testing.T) {
	// Create a temp JSONL file with realistic daily_prices format
	tmp, err := os.CreateTemp("", "prices-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())

	// Write sample data: 3 HK stocks, ~90 days each
	data := `{"symbol":"0700.HK","market":"HK","date":"2025-01-02","close":295.0}
{"symbol":"0700.HK","market":"HK","date":"2025-01-03","close":298.5}
{"symbol":"0700.HK","market":"HK","date":"2025-01-06","close":302.0}
{"symbol":"0001.HK","market":"HK","date":"2025-01-02","close":45.0}
{"symbol":"0001.HK","market":"HK","date":"2025-01-03","close":45.8}
{"symbol":"0001.HK","market":"HK","date":"2025-01-06","close":46.2}
{"symbol":"0005.HK","market":"HK","date":"2025-01-02","close":68.0}
{"symbol":"0005.HK","market":"HK","date":"2025-01-03","close":67.5}
{"symbol":"0005.HK","market":"HK","date":"2025-01-06","close":69.0}
`
	if _, err := tmp.WriteString(data); err != nil {
		t.Fatal(err)
	}
	tmp.Close()

	p, err := NewDuckDBPriceProvider(tmp.Name())
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	if p.SymbolCount() != 3 {
		t.Errorf("expected 3 symbols, got %d", p.SymbolCount())
	}

	// Test price lookup
	price, _, ok := p.Price("0700.HK", makeDate("2025-01-02"))
	if !ok {
		t.Fatal("price not found for 0700.HK on 2025-01-02")
	}
	if price != 295.0 {
		t.Errorf("expected 295.0, got %.2f", price)
	}

	// Test price on non-trading day (should return last available)
	price, _, ok = p.Price("0700.HK", makeDate("2025-01-05")) // Sunday
	if !ok {
		t.Fatal("should find previous trading day price")
	}
	if price != 298.5 { // Jan 3 is Friday, closest before Jan 5
		t.Errorf("expected 298.5 (last trading day), got %.2f", price)
	}

	// Test date range
	earliest, latest := p.DateRange()
	if earliest.Format("2006-01-02") != "2025-01-02" {
		t.Errorf("earliest = %s", earliest.Format("2006-01-02"))
	}
	if latest.Format("2006-01-02") != "2025-01-06" {
		t.Errorf("latest = %s", latest.Format("2006-01-02"))
	}

	// Test symbols
	symbols := p.Symbols()
	if len(symbols) != 3 {
		t.Errorf("expected 3 symbols, got %v", symbols)
	}
}

func TestDuckDBBacktest(t *testing.T) {
	// Create JSONL with 90 days of trending data
	tmp, err := os.CreateTemp("", "bt-prices-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())

	start := makeDate("2025-01-02")
	for d := start; !d.After(makeDate("2025-04-01")); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		days := d.Sub(start).Hours() / 24
		tmp.WriteString(formatPriceRec("VAL.HK", d, 100+days*0.15))
		tmp.WriteString(formatPriceRec("GRO.HK", d, 50+days*0.25))
		tmp.WriteString(formatPriceRec("BAD.HK", d, 200-days*0.10))
	}
	tmp.Close()

	p, err := NewDuckDBPriceProvider(tmp.Name())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	earliest, latest := p.DateRange()
	t.Logf("Loaded %d symbols, range %s → %s",
		p.SymbolCount(),
		earliest.Format("2006-01-02"), latest.Format("2006-01-02"))

	// Run backtest with this real-format data
	candidates := []model.ScreenedCandidate{
		{Symbol: "VAL.HK", Name: "Value Stock", Market: "HK", Price: 100, Indicators: map[string]float64{
			"pettm": 8, "pbmrq": 0.8, "roe": 15, "divyld": 5, "netmargin": 20, "la": 30, "total_amount": 5e8, "marketcap": 200,
		}},
		{Symbol: "GRO.HK", Name: "Growth Stock", Market: "HK", Price: 50, Indicators: map[string]float64{
			"pettm": 20, "pbmrq": 3, "roe": 25, "divyld": 1, "netmargin": 18, "la": 40, "total_amount": 3e8, "marketcap": 150,
		}},
		{Symbol: "BAD.HK", Name: "Declining Stock", Market: "HK", Price: 200, Indicators: map[string]float64{
			"pettm": 40, "pbmrq": 5, "roe": 5, "divyld": 0.5, "netmargin": 5, "la": 70, "total_amount": 1e8, "marketcap": 50,
		}},
	}

	profile := FindProfile(model.BuiltinStrategyProfiles(), "HK", "value")
	if profile == nil {
		t.Fatal("profile not found")
	}

	cfg := BacktestConfig{
		Name: "duckdb_hk_value", Profile: profile,
		StartDate: makeDate("2025-01-02"), EndDate: makeDate("2025-04-01"),
		TopN: 2, HoldDays: 30, RebalanceFreq: 30, InitialCapital: 1_000_000,
	}

	result, err := RunBacktest(cfg, p, NewMockCandidateProvider(candidates))
	if err != nil {
		t.Fatalf("backtest: %v", err)
	}

	t.Logf("DuckDB backtest: %d periods, annual=%.2f%%, maxDD=%.1f%%, winRate=%.0f%%",
		result.NumPeriods, result.AnnualReturn*100, result.MaxDrawdown*100, result.WinRate*100)
	for _, pr := range result.PeriodReturns {
		t.Logf("  %s → %s: ret=%.2f%% excess=%.2f%% %v",
			pr.StartDate, pr.EndDate, pr.Return*100, pr.Excess*100, pr.TopSymbols)
	}

	if result.NumPeriods == 0 {
		t.Error("expected at least 1 period")
	}
	// Top 2 should be VAL and GRO (both trending up), excluding BAD (declining)
	if result.AnnualReturn <= 0 {
		t.Errorf("expected positive annual return with trending stocks, got %.2f%%", result.AnnualReturn*100)
	}
}

func formatPriceRec(symbol string, date time.Time, close float64) string {
	return "{\"symbol\":\"" + symbol + "\",\"market\":\"HK\",\"date\":\"" +
		date.Format("2006-01-02") + "\",\"close\":" + formatFloat(close) + "}\n"
}

func formatFloat(f float64) string {
	v := int(f * 100)
	intPart := v / 100
	decPart := v % 100
	if decPart < 0 {
		decPart = -decPart
	}
	if decPart == 0 {
		return itoa(intPart) + ".0"
	}
	return itoa(intPart) + "." + itoa(decPart)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
