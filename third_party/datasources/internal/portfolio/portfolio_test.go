package portfolio

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBuildSnapshotGoldenAndExactDecimals(t *testing.T) {
	asOf := time.Date(2026, 7, 23, 4, 5, 6, 123456789, time.UTC)
	holdings := []Holding{
		{Version: Version, Symbol: "US:MSFT", Quantity: "0.1", UnitPrice: "0.2", Currency: "USD", AsOf: asOf, Provenance: []string{"prices"}},
		{Version: Version, Symbol: "HK:00700", Quantity: "3", UnitPrice: "400.25", Currency: "HKD", AsOf: asOf, Provenance: []string{"broker"}, Stale: true},
	}
	rates := []FXRate{{Version: Version, FromCurrency: "HKD", ToCurrency: "USD", Rate: "0.125", AsOf: asOf, Provenance: []string{"fx"}}}
	snapshot, err := BuildSnapshot("USD", asOf, holdings, rates)
	if err != nil {
		t.Fatalf("BuildSnapshot() error = %v", err)
	}
	if snapshot.TotalValue != "150.11375" || snapshot.Holdings[1].BaseValue != "0.02" {
		t.Fatalf("snapshot decimals = total %s, second %s", snapshot.TotalValue, snapshot.Holdings[1].BaseValue)
	}
	encoded, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent() error = %v", err)
	}
	encoded = append(encoded, '\n')
	want, err := os.ReadFile("testdata/snapshot.golden.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(encoded) != string(want) {
		t.Fatalf("snapshot golden mismatch\ngot:\n%s\nwant:\n%s", encoded, want)
	}
	second, err := BuildSnapshot("USD", asOf, []Holding{holdings[1], holdings[0]}, rates)
	if err != nil || second.ID != snapshot.ID {
		t.Fatalf("permuted snapshot ID = %q, %v; want %q", second.ID, err, snapshot.ID)
	}
}

func TestBuildSnapshotValidation(t *testing.T) {
	asOf := time.Date(2026, 7, 23, 4, 5, 6, 0, time.UTC)
	valid := Holding{Version: Version, Symbol: "US:AAPL", Quantity: "1", UnitPrice: "2", Currency: "USD", AsOf: asOf, Provenance: []string{"prices"}}
	cases := []Holding{
		{Version: Version, Symbol: "AAPL", Quantity: "1", UnitPrice: "2", Currency: "USD", AsOf: asOf, Provenance: []string{"prices"}},
		{Version: Version, Symbol: "US:AAPL", Quantity: "NaN", UnitPrice: "2", Currency: "USD", AsOf: asOf, Provenance: []string{"prices"}},
		{Version: Version, Symbol: "US:AAPL", Quantity: "1e2", UnitPrice: "2", Currency: "USD", AsOf: asOf, Provenance: []string{"prices"}},
		{Version: Version, Symbol: "US:AAPL", Quantity: "1", UnitPrice: "2", Currency: "usd", AsOf: asOf, Provenance: []string{"prices"}},
		{Version: Version, Symbol: "US:AAPL", Quantity: "1", UnitPrice: "2", Currency: "USD", AsOf: asOf.In(time.FixedZone("UTC+0", 0)), Provenance: []string{"prices"}},
	}
	for index, item := range cases {
		if _, err := BuildSnapshot("USD", asOf, []Holding{item}, nil); err == nil {
			t.Fatalf("case %d unexpectedly succeeded", index)
		}
	}
	foreign := valid
	foreign.Currency = "HKD"
	if _, err := BuildSnapshot("USD", asOf, []Holding{foreign}, nil); err == nil {
		t.Fatal("missing FX rate unexpectedly succeeded")
	}
}

func TestBuildSnapshotRejectsDuplicateCanonicalIdentityInAnyOrder(t *testing.T) {
	asOf := time.Date(2026, 7, 23, 4, 5, 6, 0, time.UTC)
	left := Holding{Version: Version, Symbol: "US:AAPL", Quantity: "1", UnitPrice: "100", Currency: "USD", AsOf: asOf, Provenance: []string{"broker-a"}}
	right := Holding{Version: Version, Symbol: "US:AAPL", Quantity: "2", UnitPrice: "101", Currency: "USD", AsOf: asOf, Provenance: []string{"broker-b"}}
	for index, holdings := range [][]Holding{{left, right}, {right, left}} {
		if _, err := BuildSnapshot("USD", asOf, holdings, nil); err == nil {
			t.Fatalf("duplicate identity order %d unexpectedly succeeded", index)
		}
	}
}

func TestDecimalResourceLimits(t *testing.T) {
	asOf := time.Date(2026, 7, 23, 4, 5, 6, 0, time.UTC)
	holding := Holding{Version: Version, Symbol: "US:AAPL", Quantity: "1", UnitPrice: "1", Currency: "USD", AsOf: asOf, Provenance: []string{"prices"}}
	holding.Quantity = "1." + strings.Repeat("0", maxDecimalScale+1)
	if _, err := BuildSnapshot("USD", asOf, []Holding{holding}, nil); err == nil {
		t.Fatal("over-scale decimal unexpectedly succeeded")
	}
	holding.Quantity = strings.Repeat("9", maxDecimalDigits+1)
	if _, err := BuildSnapshot("USD", asOf, []Holding{holding}, nil); err == nil {
		t.Fatal("over-length decimal unexpectedly succeeded")
	}
	holding.Quantity = "0." + strings.Repeat("1", maxDecimalScale)
	if _, err := BuildSnapshot("USD", asOf, []Holding{holding}, nil); err != nil {
		t.Fatalf("decimal at scale limit error = %v", err)
	}
}
