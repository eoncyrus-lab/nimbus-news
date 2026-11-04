package main

import "testing"

func TestSummarizeConsensusPluralityThreshold(t *testing.T) {
	// 3 eligible, 2 agree on upside → consensus (2/3 = 0.67 meets threshold)
	rows := []JointRow{
		{Asset: "btc", DominantScenario: "upside_continuation"},
		{Asset: "qqq", DominantScenario: "upside_continuation"},
		{Asset: "gold", DominantScenario: "range"},
	}
	r := summarizeConsensus(rows, 90)
	if r.Consensus != "upside_continuation" {
		t.Errorf("consensus = %q, want upside_continuation", r.Consensus)
	}
	if r.Agreement < 0.66 || r.Agreement > 0.68 {
		t.Errorf("agreement = %v, want ~0.667", r.Agreement)
	}
}

func TestSummarizeConsensusNoMajorityIsMixed(t *testing.T) {
	// 4 eligible, 2-1-1 split → max agreement 0.5 < 0.67 → mixed
	rows := []JointRow{
		{Asset: "btc", DominantScenario: "upside_continuation"},
		{Asset: "qqq", DominantScenario: "upside_continuation"},
		{Asset: "gold", DominantScenario: "range"},
		{Asset: "spy", DominantScenario: "downside_pressure"},
	}
	r := summarizeConsensus(rows, 90)
	if r.Consensus != "mixed" {
		t.Errorf("consensus = %q, want mixed", r.Consensus)
	}
}

func TestSummarizeConsensusExcludesHardBlocked(t *testing.T) {
	// Hard-blocked rows do not vote; remaining 2/2 agree → consensus.
	rows := []JointRow{
		{Asset: "btc", DominantScenario: "upside_continuation"},
		{Asset: "qqq", DominantScenario: "upside_continuation"},
		{Asset: "gold", DominantScenario: "downside_pressure", HardBlocked: true},
	}
	r := summarizeConsensus(rows, 90)
	if r.Consensus != "upside_continuation" {
		t.Errorf("hard-blocked should be excluded; consensus = %q", r.Consensus)
	}
	if r.Agreement != 1.0 {
		t.Errorf("2/2 eligible agreement = 1.0, got %v", r.Agreement)
	}
}

func TestSummarizeConsensusAllBlocked(t *testing.T) {
	rows := []JointRow{
		{Asset: "qqq", DominantScenario: "downside_pressure", HardBlocked: true},
		{Asset: "gold", DominantScenario: "range", HardBlocked: true},
	}
	r := summarizeConsensus(rows, 90)
	if r.Consensus != "mixed" {
		t.Errorf("all-blocked → consensus should be 'mixed', got %q", r.Consensus)
	}
	if r.Agreement != 0 {
		t.Errorf("all-blocked → agreement should be 0, got %v", r.Agreement)
	}
}

func TestScenarioCNLabels(t *testing.T) {
	cases := map[string]string{
		"upside_continuation": "上行延续",
		"range":               "区间震荡",
		"downside_pressure":   "下行压力",
		"unknown_key":         "unknown_key",
	}
	for in, want := range cases {
		if got := scenarioCN(in); got != want {
			t.Errorf("scenarioCN(%q) = %q, want %q", in, got, want)
		}
	}
}
