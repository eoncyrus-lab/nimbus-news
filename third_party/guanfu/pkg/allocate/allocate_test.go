package allocate

import (
	"encoding/json"
	"testing"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/store"
)

func TestPortfolioDefinitions(t *testing.T) {
	for _, pf := range AllPortfolios {
		if pf.Name == "" {
			t.Fatal("portfolio has no name")
		}
		sum := 0.0
		for _, w := range pf.Assets {
			sum += w
		}
		if sum < 0.99 || sum > 1.01 {
			t.Fatalf("%s: weights sum to %.2f, want 1.0", pf.Name, sum)
		}
	}
}

func TestAnalyzePortfolio(t *testing.T) {
	status, err := Analyze(Portfolio6040)
	if err != nil {
		t.Fatalf("Analyze 60/40: %v", err)
	}
	if status.Portfolio != "60/40" {
		t.Fatalf("expected 60/40, got %s", status.Portfolio)
	}
	if len(status.Assets) != 2 {
		t.Fatalf("expected 2 assets, got %d", len(status.Assets))
	}
	for _, a := range status.Assets {
		if a.Asset == "" {
			t.Fatal("asset has no name")
		}
		t.Logf("  %s: target=%.0f%% price=$%.2f zone=%s hint=%s",
			a.Asset, a.TargetPct, a.CurrentPrice, a.Zone, a.Hint)
	}

	// JSON roundtrip
	b, _ := json.Marshal(status)
	var decoded Status
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("json: %v", err)
	}
}

func TestAnalyzeDoesNotFabricateDriftFromPrices(t *testing.T) {
	s := &store.PriceStore{Dir: t.TempDir()}
	if err := s.Save("spy", []store.PricePoint{{Date: "2026-05-01", Close: 500}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save("tlt", []store.PricePoint{{Date: "2026-05-01", Close: 90}}); err != nil {
		t.Fatal(err)
	}

	status, err := analyzeWithStore(Portfolio6040, s)
	if err != nil {
		t.Fatalf("analyzeWithStore: %v", err)
	}
	if status.DriftMax != 0 {
		t.Fatalf("drift should be unknown/zero without actual holdings, got %.1f", status.DriftMax)
	}
	if status.RebalanceNeeded {
		t.Fatal("rebalance should not trigger from target weights alone")
	}
	for _, a := range status.Assets {
		if a.DriftPct != 0 {
			t.Fatalf("%s drift should be zero without actual holdings, got %.1f", a.Asset, a.DriftPct)
		}
	}
}

func TestAnalyzeAllWeather(t *testing.T) {
	status, err := Analyze(PortfolioAllWeather)
	if err != nil {
		t.Fatalf("Analyze All-Weather: %v", err)
	}
	if len(status.Assets) != 6 {
		t.Fatalf("expected 6 assets, got %d", len(status.Assets))
	}
	t.Logf("All-Weather: zone=%s maxDrift=%.1f%% rebalance=%v",
		status.OverallZone, status.DriftMax, status.RebalanceNeeded)
}

func TestAllPortfolios(t *testing.T) {
	for _, pf := range AllPortfolios {
		status, err := Analyze(pf)
		if err != nil {
			t.Logf("%s: skipped (%v)", pf.Name, err)
			continue
		}
		t.Logf("%s: assets=%d zone=%s", pf.Name, len(status.Assets), status.OverallZone)
	}
}

func TestMultiAssetOverview(t *testing.T) {
	ov, err := MultiAssetOverview()
	if err != nil {
		t.Fatalf("MultiAssetOverview: %v", err)
	}
	if len(ov.Items) == 0 {
		t.Log("no assets in PriceStore (expected on clean system)")
		return
	}
	for _, item := range ov.Items {
		if item.Asset == "" || item.Price <= 0 {
			t.Fatalf("invalid item: %+v", item)
		}
	}
	t.Logf("overview: %d assets", len(ov.Items))
}

func TestConsensusScan(t *testing.T) {
	cs, err := ConsensusScan()
	if err != nil {
		t.Fatalf("ConsensusScan: %v", err)
	}
	if cs == nil {
		t.Fatal("expected result")
	}
	t.Logf("consensus: direction=%s confidence=%.0f%% signals=%d",
		cs.Direction, cs.Confidence*100, len(cs.Signals))
	for _, s := range cs.Signals {
		t.Logf("  %s: $%.2f mom=%+.1f%% %s", s.Asset, s.Price, s.Momentum, s.Signal)
	}

	// JSON roundtrip
	b, _ := json.Marshal(cs)
	var decoded ConsensusResult
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("json: %v", err)
	}
}
