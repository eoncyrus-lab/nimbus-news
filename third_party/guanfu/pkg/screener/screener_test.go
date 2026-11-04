// End-to-end screener demo test — runs the full coarse→fine→verdict pipeline
// with mock data representing realistic multi-market candidates.

package screener

import (
	"os"
	"testing"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/screener/fine"
)

// realisticCandidates returns 25 mock candidates across HK/US/A markets
// with realistic indicator values for testing the full pipeline.
func realisticCandidates() []model.ScreenedCandidate {
	return []model.ScreenedCandidate{
		// ── HK deep value ──
		{Symbol: "0001.HK", Name: "长和", Market: "HK", Industry: "综合企业", Price: 45,
			Indicators: m("pettm", 6.5, "pbmrq", 0.4, "roe", 8, "divyld", 7.2, "netmargin", 15, "la", 35, "total_amount", 2e8, "marketcap", 180)},
		{Symbol: "0011.HK", Name: "恒生银行", Market: "HK", Industry: "银行", Price: 95,
			Indicators: m("pettm", 9, "pbmrq", 1.1, "roe", 12, "divyld", 5.5, "netmargin", 45, "la", 90, "total_amount", 3e8, "marketcap", 1800)},
		{Symbol: "0012.HK", Name: "恒基地产", Market: "HK", Industry: "房地产", Price: 22,
			Indicators: m("pettm", 8, "pbmrq", 0.35, "roe", 5, "divyld", 8, "netmargin", 40, "la", 30, "total_amount", 1e8, "marketcap", 1000)},
		{Symbol: "0388.HK", Name: "港交所", Market: "HK", Industry: "金融", Price: 300,
			Indicators: m("pettm", 32, "pbmrq", 7, "roe", 25, "divyld", 2.5, "netmargin", 60, "la", 85, "total_amount", 5e9, "marketcap", 3800)},
		{Symbol: "0823.HK", Name: "领展", Market: "HK", Industry: "房地产", Price: 38,
			Indicators: m("pettm", 15, "pbmrq", 0.55, "roe", 5, "divyld", 6.8, "netmargin", 80, "la", 25, "total_amount", 4e8, "marketcap", 1000)},

		// ── HK growth ──
		{Symbol: "0700.HK", Name: "腾讯", Market: "HK", Industry: "互联网", Price: 380,
			Indicators: m("pettm", 18.5, "pbmrq", 3.2, "roe", 22, "divyld", 0.4, "netmargin", 28, "la", 45, "netincomegrowthyoy", 30, "salesgrowthyoy", 10, "total_amount", 8e9, "marketcap", 3600)},
		{Symbol: "9988.HK", Name: "阿里巴巴", Market: "HK", Industry: "互联网", Price: 85,
			Indicators: m("pettm", 12, "pbmrq", 1.3, "roe", 15, "divyld", 1.5, "netmargin", 18, "la", 38, "netincomegrowthyoy", 45, "salesgrowthyoy", 8, "total_amount", 6e9, "marketcap", 1700)},
		{Symbol: "2269.HK", Name: "药明生物", Market: "HK", Industry: "制药", Price: 18,
			Indicators: m("pettm", 35, "pbmrq", 2.5, "roe", 10, "divyld", 0, "netmargin", 22, "la", 40, "netincomegrowthyoy", -15, "salesgrowthyoy", 5, "total_amount", 2e9, "marketcap", 750)},

		// ── HK turnaround ──
		{Symbol: "0175.HK", Name: "吉利汽车", Market: "HK", Industry: "汽车", Price: 12,
			Indicators: m("pettm", 18, "pbmrq", 0.9, "roe", 6, "divyld", 2, "netmargin", 5, "la", 55, "netincomegrowthyoy", 120, "salesgrowthyoy", 35, "total_amount", 1.5e9, "marketcap", 1200)},
		{Symbol: "2333.HK", Name: "长城汽车", Market: "HK", Industry: "汽车", Price: 10,
			Indicators: m("pettm", 15, "pbmrq", 0.8, "roe", 8, "divyld", 3, "netmargin", 7, "la", 60, "netincomegrowthyoy", 85, "salesgrowthyoy", 28, "total_amount", 1e9, "marketcap", 900)},

		// ── HK risk flags ──
		{Symbol: "1234.HK", Name: "老千股示例", Market: "HK", Industry: "综合企业", Price: 0.15,
			Indicators: m("pettm", 2, "pbmrq", 0.08, "roe", -5, "divyld", 15, "netmargin", -10, "la", 95, "total_amount", 5e4, "marketcap", 1.5)},

		// ── US value ──
		{Symbol: "BRK.B.US", Name: "Berkshire Hathaway", Market: "US", Industry: "Insurance", Price: 450,
			Indicators: m("pettm", 12, "pbmrq", 1.5, "roe", 18, "netmargin", 25, "la", 55, "salesgrowthyoy", 8, "netincomegrowthyoy", 15, "total_amount", 2e10, "marketcap", 9800)},
		{Symbol: "JPM.US", Name: "JPMorgan Chase", Market: "US", Industry: "Banks", Price: 200,
			Indicators: m("pettm", 11, "pbmrq", 1.8, "roe", 16, "netmargin", 35, "la", 90, "salesgrowthyoy", 10, "netincomegrowthyoy", 12, "total_amount", 3e10, "marketcap", 5800)},

		// ── US growth/quality ──
		{Symbol: "AAPL.US", Name: "Apple Inc.", Market: "US", Industry: "Technology", Price: 220,
			Indicators: m("pettm", 32, "pbmrq", 45, "roe", 140, "netmargin", 26, "la", 82, "salesgrowthyoy", -2, "netincomegrowthyoy", 5, "total_amount", 1.2e10, "marketcap", 34000)},
		{Symbol: "MSFT.US", Name: "Microsoft", Market: "US", Industry: "Software", Price: 450,
			Indicators: m("pettm", 35, "pbmrq", 13, "roe", 45, "netmargin", 36, "la", 50, "salesgrowthyoy", 16, "netincomegrowthyoy", 20, "total_amount", 8e9, "marketcap", 33000)},
		{Symbol: "NVDA.US", Name: "NVIDIA", Market: "US", Industry: "Semiconductors", Price: 130,
			Indicators: m("pettm", 45, "pbmrq", 25, "roe", 90, "netmargin", 55, "la", 35, "salesgrowthyoy", 80, "netincomegrowthyoy", 120, "total_amount", 5e10, "marketcap", 3200)},

		// ── US risk ──
		{Symbol: "SPAC.US", Name: "SPAC Acquisition Corp", Market: "US", Industry: "Shell Companies", Price: 3.5,
			Themes: []string{"spac"},
			Indicators: m("pettm", -10, "pbmrq", 0.5, "roe", -30, "netmargin", -50, "la", 20, "total_amount", 5e5, "marketcap", 2)},

		// ── A-share value ──
		{Symbol: "600036.SH", Name: "招商银行", Market: "A", Industry: "银行", Price: 38,
			Indicators: m("pettm", 6, "pbmrq", 0.9, "roe", 16, "divyld", 5, "netmargin", 45, "la", 91, "netincomegrowthyoy", 6, "salesgrowthyoy", 3, "total_amount", 8e9, "marketcap", 9500)},
		{Symbol: "601318.SH", Name: "中国平安", Market: "A", Industry: "保险", Price: 48,
			Indicators: m("pettm", 9, "pbmrq", 0.95, "roe", 13, "divyld", 4.5, "netmargin", 12, "la", 89, "netincomegrowthyoy", 25, "salesgrowthyoy", 8, "total_amount", 5e9, "marketcap", 8700)},

		// ── A-share growth ──
		{Symbol: "300750.SZ", Name: "宁德时代", Market: "A", Industry: "电力设备", Price: 220,
			Indicators: m("pettm", 22, "pbmrq", 4.5, "roe", 22, "divyld", 0.8, "netmargin", 12, "la", 65, "netincomegrowthyoy", 35, "salesgrowthyoy", 25, "total_amount", 1.2e10, "marketcap", 9600)},
		{Symbol: "002594.SZ", Name: "比亚迪", Market: "A", Industry: "汽车", Price: 280,
			Indicators: m("pettm", 28, "pbmrq", 5, "roe", 20, "divyld", 0.3, "netmargin", 5, "la", 72, "netincomegrowthyoy", 50, "salesgrowthyoy", 40, "total_amount", 8e9, "marketcap", 8000)},

		// ── A-share dividend ──
		{Symbol: "601088.SH", Name: "中国神华", Market: "A", Industry: "煤炭", Price: 35,
			Indicators: m("pettm", 8, "pbmrq", 1.2, "roe", 15, "divyld", 7, "netmargin", 20, "la", 30, "netincomegrowthyoy", -5, "salesgrowthyoy", -8, "total_amount", 3e9, "marketcap", 7000)},

		// ── A-share ST (should be rejected) ──
		{Symbol: "000005.SZ", Name: "*ST星源", Market: "A", Industry: "环保", Price: 0.8,
			Themes: []string{"*ST"},
			Indicators: m("pettm", -5, "pbmrq", 0.3, "roe", -15, "divyld", 0, "netmargin", -30, "la", 85, "total_amount", 3e6, "marketcap", 8)},
	}
}

func m(pairs ...interface{}) map[string]float64 {
	result := make(map[string]float64, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		key := pairs[i].(string)
		var val float64
		switch v := pairs[i+1].(type) {
		case float64:
			val = v
		case int:
			val = float64(v)
		default:
			val = 0
		}
		result[key] = val
	}
	return result
}

func TestFullPipeline_Demo(t *testing.T) {
	candidates := realisticCandidates()
	t.Logf("Demo pipeline: %d candidates across 3 markets", len(candidates))

	// Run each market's value profile
	type testCase struct {
		market string
		style  string
		profile *model.StrategyProfile
	}
	tests := []testCase{
		{"HK", "value", FindProfile(model.BuiltinStrategyProfiles(), "HK", "value")},
		{"HK", "growth", FindProfile(model.BuiltinStrategyProfiles(), "HK", "growth")},
		{"HK", "turnaround", FindProfile(model.BuiltinStrategyProfiles(), "HK", "turnaround")},
		{"US", "value", FindProfile(model.BuiltinStrategyProfiles(), "US", "value")},
		{"US", "quality", FindProfile(model.BuiltinStrategyProfiles(), "US", "quality")},
		{"US", "balanced", FindProfile(model.BuiltinStrategyProfiles(), "US", "balanced")},
		{"A", "value", FindProfile(model.BuiltinStrategyProfiles(), "A", "value")},
		{"A", "growth", FindProfile(model.BuiltinStrategyProfiles(), "A", "growth")},
		{"A", "dividend", FindProfile(model.BuiltinStrategyProfiles(), "A", "dividend")},
		{"A", "potential", FindProfile(model.BuiltinStrategyProfiles(), "A", "potential")},
	}

	for _, tc := range tests {
		if tc.profile == nil {
			t.Fatalf("profile not found: %s/%s", tc.market, tc.style)
		}
		// Filter candidates for this market
		var marketCandidates []model.ScreenedCandidate
		for _, c := range candidates {
			if c.Market == tc.market {
				marketCandidates = append(marketCandidates, c)
			}
		}
		if len(marketCandidates) == 0 {
			continue
		}

		scored := fine.ScoreAll(marketCandidates, tc.profile)
		buckets := fine.Bucketize(scored)

		t.Logf("  %s: %d candidates → core=%d watch=%d reserve=%d reject=%d",
			tc.profile.Name, len(marketCandidates),
			len(buckets[fine.BucketCore]), len(buckets[fine.BucketWatch]),
			len(buckets[fine.BucketReserve]), len(buckets[fine.BucketReject]))

		// Print top picks
		topN := 3
		if len(scored) < topN {
			topN = len(scored)
		}
		for i := 0; i < topN; i++ {
			c := scored[i]
			t.Logf("    #%d %s %s final=%.1f val=%.1f qual=%.1f pot=%.1f risk=%.1f %v",
				i+1, c.Symbol, c.Name, c.FinalScore, c.ValScore, c.QualScore, c.PotScore, c.RiskScore, c.Reasons)
		}

		// Verify basic invariants
		if len(buckets[fine.BucketCore]) > 0 {
			// Top core pick should have FinalScore >= threshold
			if buckets[fine.BucketCore][0].FinalScore < tc.profile.CoreCandidateMin {
				t.Errorf("%s: core pick below threshold (%.1f < %.0f)",
					tc.profile.Name, buckets[fine.BucketCore][0].FinalScore, tc.profile.CoreCandidateMin)
			}
		}
	}

	// Verify cross-cutting concerns
	t.Run("ST rejection", func(t *testing.T) {
		profile := FindProfile(model.BuiltinStrategyProfiles(), "A", "value")
		var stStock *model.ScreenedCandidate
		for _, c := range candidates {
			if c.Symbol == "000005.SZ" {
				stStock = &c
				break
			}
		}
		if stStock == nil {
			t.Fatal("ST stock not found")
		}
		fine.ScoreCandidate(stStock, profile)
		if stStock.FinalScore >= 50 {
			t.Errorf("ST stock should be rejected, got final=%.1f", stStock.FinalScore)
		}
		if len(stStock.RiskFlags) == 0 {
			t.Error("ST stock should have risk flags")
		}
	})

	t.Run("SPAC rejection", func(t *testing.T) {
		profile := FindProfile(model.BuiltinStrategyProfiles(), "US", "value")
		var spac *model.ScreenedCandidate
		for _, c := range candidates {
			if c.Symbol == "SPAC.US" {
				spac = &c
				break
			}
		}
		if spac == nil {
			t.Fatal("SPAC stock not found")
		}
		fine.ScoreCandidate(spac, profile)
		if spac.FinalScore >= 50 {
			t.Errorf("SPAC should have low score, got final=%.1f", spac.FinalScore)
		}
	})

	t.Run("HK junk rejection", func(t *testing.T) {
		profile := FindProfile(model.BuiltinStrategyProfiles(), "HK", "value")
		var junk *model.ScreenedCandidate
		for _, c := range candidates {
			if c.Symbol == "1234.HK" {
				junk = &c
				break
			}
		}
		if junk == nil {
			t.Fatal("junk stock not found")
		}
		fine.ScoreCandidate(junk, profile)
		if junk.FinalScore >= 50 {
			t.Errorf("junk stock should be rejected, got final=%.1f", junk.FinalScore)
		}
		if len(junk.RiskFlags) < 3 {
			t.Errorf("junk stock should have ≥3 risk flags, got %d: %v", len(junk.RiskFlags), junk.RiskFlags)
		}
	})
}

func TestYAMLProfiles(t *testing.T) {
	// Verify YAML file parses and matches builtins
	yamlPath := "strategies.yaml"
	if _, err := os.Stat(yamlPath); os.IsNotExist(err) {
		yamlPath = "/Users/x/nimbus-os/guanfu/pkg/screener/strategies.yaml"
	}
	profiles, err := LoadProfiles(yamlPath)
	if err != nil {
		t.Skipf("YAML file not found (expected in repo): %v", err)
		return
	}
	t.Logf("Loaded %d profiles from %s", len(profiles), yamlPath)

	builtins := model.BuiltinStrategyProfiles()
	if len(profiles) != len(builtins) {
		t.Errorf("YAML profiles count (%d) != builtins (%d)", len(profiles), len(builtins))
	}

	for i, p := range profiles {
		if p.Name == "" || p.Market == "" || p.Style == "" {
			t.Errorf("YAML profile %d: missing required field", i)
		}
		w := p.Weights
		sum := w.Valuation + w.Quality + w.Potential + w.Technical + w.Flow + w.Risk
		if sum < 0.99 || sum > 1.01 {
			t.Errorf("YAML profile %s: weights sum to %.2f", p.Name, sum)
		}
	}
}
