package fine

import (
	"testing"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
)

func TestScoreCandidate_HKValue(t *testing.T) {
	profile := &model.StrategyProfile{
		Name:    "港股价值",
		Market:  "HK",
		Weights: model.HKWeights(),
	}
	c := model.ScreenedCandidate{
		Symbol: "700.HK", Name: "腾讯控股", Market: "HK",
		Price: 380.0,
		Indicators: map[string]float64{
			"pettm":    18.5,
			"pbmrq":    3.2,
			"roe":      22.0,
			"divyld":   0.4,
			"netmargin": 28.0,
			"la":       45.0,
			"total_amount": 8e9,
			"marketcap": 3600,
		},
	}
	bucket := ScoreCandidate(&c, profile)

	// 腾讯 should not be rejected
	if bucket == BucketReject {
		t.Errorf("700.HK should not be rejected, got bucket=%s final=%.1f", bucket, c.FinalScore)
	}
	if c.ValScore <= 0 || c.ValScore > 100 {
		t.Errorf("val_score %.1f out of range", c.ValScore)
	}
	if c.QualScore <= 0 || c.QualScore > 100 {
		t.Errorf("qual_score %.1f out of range", c.QualScore)
	}
	if c.FinalScore <= 0 || c.FinalScore > 100 {
		t.Errorf("final_score %.1f out of range", c.FinalScore)
	}
	if len(c.RiskFlags) > 0 {
		t.Logf("tencent risk flags: %v", c.RiskFlags)
	}
	t.Logf("Tencent: val=%.1f qual=%.1f pot=%.1f risk=%.1f final=%.1f bucket=%s",
		c.ValScore, c.QualScore, c.PotScore, c.RiskScore, c.FinalScore, bucket)
}

func TestScoreCandidate_HKDeepValue(t *testing.T) {
	profile := &model.StrategyProfile{
		Name:    "港股价值",
		Market:  "HK",
		Weights: model.HKWeights(),
	}
	c := model.ScreenedCandidate{
		Symbol: "0001.HK", Name: "长和", Market: "HK",
		Price: 45.0,
		Indicators: map[string]float64{
			"pettm":    6.5,
			"pbmrq":    0.4,
			"roe":      8.0,
			"divyld":   7.2,
			"netmargin": 15.0,
			"la":       35.0,
			"total_amount": 2e8,
			"marketcap": 180,
		},
	}
	bucket := ScoreCandidate(&c, profile)

	if bucket != BucketCore {
		t.Errorf("deep value stock should be core_candidate, got %s (final=%.1f)", bucket, c.FinalScore)
	}
	if c.ValScore < 70 {
		t.Errorf("deep value val_score should be >=70, got %.1f", c.ValScore)
	}
	t.Logf("CK Hutchison: val=%.1f qual=%.1f pot=%.1f risk=%.1f final=%.1f bucket=%s",
		c.ValScore, c.QualScore, c.PotScore, c.RiskScore, c.FinalScore, bucket)
}

func TestScoreCandidate_AShareST(t *testing.T) {
	profile := &model.StrategyProfile{
		Name:    "A股价值",
		Market:  "A",
		Weights: model.AShareWeights(),
	}
	c := model.ScreenedCandidate{
		Symbol: "000005.SZ", Name: "*ST星源", Market: "A",
		Price: 0.8,
		Themes: []string{"*ST", "环保"},
		Indicators: map[string]float64{
			"pettm":     -5.0,
			"pbmrq":    0.3,
			"roe":      -15.0,
			"netmargin": -30.0,
			"la":       85.0,
			"total_amount": 3e6,
			"marketcap": 8,
		},
	}
	bucket := ScoreCandidate(&c, profile)

	if bucket != BucketReject {
		t.Errorf("ST stock should be reject, got %s", bucket)
	}
	if c.FinalScore >= 50 {
		t.Errorf("ST stock should have final < 50, got %.1f", c.FinalScore)
	}
	t.Logf("ST stock: val=%.1f qual=%.1f pot=%.1f risk=%.1f final=%.1f bucket=%s flags=%v",
		c.ValScore, c.QualScore, c.PotScore, c.RiskScore, c.FinalScore, bucket, c.RiskFlags)
}

func TestScoreCandidate_USValue(t *testing.T) {
	profile := &model.StrategyProfile{
		Name:    "美股价值",
		Market:  "US",
		Weights: model.USWeights(),
	}
	c := model.ScreenedCandidate{
		Symbol: "AAPL.US", Name: "Apple Inc.", Market: "US",
		Price: 220.0,
		Indicators: map[string]float64{
			"pettm":              32.0,
			"pbmrq":              45.0,
			"roe":                140.0,
			"netmargin":          26.0,
			"netincomegrowthyoy": 5.0,
			"salesgrowthyoy":     -2.0,
			"la":                 82.0,
			"total_amount":       1.2e10,
			"marketcap":          34000,
		},
	}
	bucket := ScoreCandidate(&c, profile)

	t.Logf("Apple: val=%.1f qual=%.1f pot=%.1f risk=%.1f final=%.1f bucket=%s flags=%v",
		c.ValScore, c.QualScore, c.PotScore, c.RiskScore, c.FinalScore, bucket, c.RiskFlags)
}

func TestAllStrategyProfiles(t *testing.T) {
	profiles := model.BuiltinStrategyProfiles()
	t.Logf("Total profiles: %d", len(profiles))

	markets := map[string]int{}
	styles := map[string]int{}
	for _, p := range profiles {
		markets[p.Market]++
		styles[p.Style]++

		// Validate required fields
		if p.Name == "" {
			t.Errorf("profile %s/%s: empty name", p.Market, p.Style)
		}
		if p.Market == "" {
			t.Errorf("profile %s: empty market", p.Name)
		}
		if p.Style == "" {
			t.Errorf("profile %s: empty style", p.Name)
		}
		if p.CoarseStrategyID == "" && p.Market != "ALL" {
			t.Logf("profile %s: no coarse_strategy_id (Mode B custom)", p.Name)
		}
		w := p.Weights
		sum := w.Valuation + w.Quality + w.Potential + w.Technical + w.Flow + w.Risk
		if sum < 0.99 || sum > 1.01 {
			t.Errorf("profile %s: weights sum to %.2f, expected 1.0", p.Name, sum)
		}
		if p.CoreCandidateMin <= 0 || p.WatchlistMin <= 0 {
			t.Errorf("profile %s: invalid decision thresholds", p.Name)
		}
	}

	t.Logf("Market coverage: %v", markets)
	t.Logf("Style coverage: %v", styles)

	if len(profiles) < 14 {
		t.Errorf("expected >=14 profiles, got %d", len(profiles))
	}
}

func TestSectorAwareScoring(t *testing.T) {
	// Same PE but different sectors → tech should score relatively higher
	techCandidate := model.ScreenedCandidate{
		Symbol: "TECH.HK", Name: "TechCo", Market: "HK", Industry: "半导体",
		Price: 100,
		Indicators: map[string]float64{
			"pettm": 35, "pbmrq": 3.0, "roe": 18, "divyld": 1.0,
			"netmargin": 20, "la": 35, "total_amount": 5e8, "marketcap": 500,
		},
	}
	bankCandidate := model.ScreenedCandidate{
		Symbol: "BANK.HK", Name: "BankCo", Market: "HK", Industry: "银行",
		Price: 50,
		Indicators: map[string]float64{
			"pettm": 35, "pbmrq": 1.0, "roe": 12, "divyld": 4.0,
			"netmargin": 40, "la": 88, "total_amount": 1e9, "marketcap": 1000,
		},
	}

	techVal := aShareValScore(&techCandidate) // tech sector → higher PE tolerance
	bankVal := aShareValScore(&bankCandidate) // bank sector → lower PE tolerance

	t.Logf("Tech (PE=35,半导体): val=%.1f", techVal)
	t.Logf("Bank (PE=35,银行): val=%.1f", bankVal)

	// Both at PE=35, tech should score better than bank because PE=35 is normal for tech
	if techVal <= bankVal {
		t.Errorf("PE=35: tech (%.1f) should score > bank (%.1f)", techVal, bankVal)
	}
}

func TestScoreAll_MultipleCandidates(t *testing.T) {
	profile := &model.StrategyProfile{
		Name:              "港股价值",
		Market:            "HK",
		Weights:           model.HKWeights(),
		CoreCandidateMin:  65,
		WatchlistMin:      50,
	}
	candidates := []model.ScreenedCandidate{
		{Symbol: "0700.HK", Name: "腾讯", Market: "HK", Price: 380, Indicators: map[string]float64{
			"pettm": 18.5, "pbmrq": 3.2, "roe": 22, "divyld": 0.4,
			"netmargin": 28, "la": 45, "total_amount": 8e9, "marketcap": 3600,
		}},
		{Symbol: "0001.HK", Name: "长和", Market: "HK", Price: 45, Indicators: map[string]float64{
			"pettm": 6.5, "pbmrq": 0.4, "roe": 8, "divyld": 7.2,
			"netmargin": 15, "la": 35, "total_amount": 2e8, "marketcap": 180,
		}},
		{Symbol: "0005.HK", Name: "汇丰", Market: "HK", Price: 68, Indicators: map[string]float64{
			"pettm": 8.0, "pbmrq": 0.9, "roe": 12, "divyld": 6.5,
			"netmargin": 35, "la": 88, "total_amount": 3e9, "marketcap": 1200,
		}},
	}

	scored := ScoreAll(candidates, profile)
	buckets := Bucketize(scored)

	t.Logf("Scored %d candidates", len(scored))
	for _, c := range scored {
		t.Logf("  %s: final=%.1f val=%.1f qual=%.1f pot=%.1f risk=%.1f reasons=%v flags=%v",
			c.Symbol, c.FinalScore, c.ValScore, c.QualScore, c.PotScore, c.RiskScore, c.Reasons, c.RiskFlags)
	}
	t.Logf("Buckets: core=%d watch=%d reserve=%d reject=%d",
		len(buckets[BucketCore]), len(buckets[BucketWatch]),
		len(buckets[BucketReserve]), len(buckets[BucketReject]))

	if len(buckets[BucketCore]) == 0 {
		t.Error("expected at least one core_candidate")
	}
	// Verify sorted by FinalScore descending
	for i := 1; i < len(scored); i++ {
		if scored[i].FinalScore > scored[i-1].FinalScore {
			t.Errorf("not sorted: [%d] %s %.1f > [%d] %s %.1f",
				i, scored[i].Symbol, scored[i].FinalScore,
				i-1, scored[i-1].Symbol, scored[i-1].FinalScore)
		}
	}
}
