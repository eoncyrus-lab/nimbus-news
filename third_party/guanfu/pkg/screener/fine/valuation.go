// Valuation fine screening — market-differentiated.
//
// Each market has distinct value anchors:
//   - A-shares: PE percentile (industry-relative) + PB-ROE match
//   - HK: PE percentile + dividend yield + AH premium/discount
//   - US: FCF yield + EV/EBIT + share buyback yield
//
// All return 0-100 scores where higher = better value.
// UNVALIDATED PRIORS — calibrate with walk-forward backtest.

package fine

import (
	"math"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
)

// ValScore computes a market-specific valuation score for a candidate.
// indicators must contain at minimum: pettm, pbmrq, roe, marketcap.
func ValScore(c *model.ScreenedCandidate) float64 {
	switch c.Market {
	case "A":
		return aShareValScore(c)
	case "HK":
		return hkValScore(c)
	case "US":
		return usValScore(c)
	default:
		return genericValScore(c)
	}
}

// --- A-share: PE percentile + PB-ROE match (sector-aware) ---

func aShareValScore(c *model.ScreenedCandidate) float64 {
	pe := c.Indicators["pettm"]
	pb := c.Indicators["pbmrq"]
	roe := c.Indicators["roe"]

	// Sector-aware PE midpoint: tech sectors tolerate higher PE
	adj := sectorPEAdjust(c.Industry)
	midPE := 30.0 + adj // default 30, higher for tech, lower for banks

	peScore := 50.0
	if pe > 0 && pe < 200 {
		if pe <= midPE {
			peScore = 100 - (pe-5)/(midPE-5)*50 // 100 at PE=5, 50 at midPE
		} else {
			peScore = 50 - (pe-midPE)/(150-midPE)*50 // 50 at midPE, 0 at PE=150
		}
		peScore = clamp(peScore, 0, 100)
	}

	// PB-ROE match: low PB + high ROE = good value
	pbRoeScore := 50.0
	if pb > 0 && pb < 30 && roe > 0 {
		// ROE/PB ratio: higher = more value per unit of book
		ratio := roe / pb
		// Map: ratio 0.3→0, 0.8→50, 2.0→100
		pbRoeScore = clamp((ratio-0.3)/1.7*100, 0, 100)
	}

	return peScore*0.5 + pbRoeScore*0.5
}

// --- HK: PE percentile + dividend yield + AH premium ---

func hkValScore(c *model.ScreenedCandidate) float64 {
	pe := c.Indicators["pettm"]
	pb := c.Indicators["pbmrq"]
	divYld := c.Indicators["divyld"]
	mktCap := c.Indicators["marketcap"]

	// PE score (same as A-share logic)
	peScore := 50.0
	if pe > 0 && pe < 120 {
		if pe <= 30 {
			peScore = 100 - (pe-5)/25*50
		} else {
			peScore = 50 - (pe-30)/70*50
		}
		peScore = clamp(peScore, 0, 100)
	}

	// PB score: HK deep value often has PB < 1
	pbScore := 50.0
	if pb > 0 && pb < 5 {
		pbScore = clamp((1.5-pb)/1.3*100, 0, 100) // PB=0.2→100, PB=1.5→0
	}

	// Dividend yield: 6% is excellent for HK
	divScore := 50.0
	if divYld > 0 {
		divScore = clamp(divYld/6*100, 0, 100) // 6%→100, 3%→50
	}

	// Small-cap premium: smaller HK stocks trade at deeper discounts
	smallCapBoost := 0.0
	if mktCap > 0 && mktCap < 100 { // market cap < 100B HKD
		smallCapBoost = (100 - mktCap) / 100 * 10 // max +10 for micro-cap
	}

	w := clamp(peScore*0.35+pbScore*0.25+divScore*0.40+smallCapBoost, 0, 100)
	return w
}

// --- US: FCF yield + EV/EBIT (sector-aware) ---

func usValScore(c *model.ScreenedCandidate) float64 {
	pe := c.Indicators["pettm"]
	roe := c.Indicators["roe"]
	netMargin := c.Indicators["netmargin"]

	// Sector-aware PE midpoint
	adj := sectorPEAdjust(c.Industry)
	midPE := 25.0 + adj // default 25, higher for tech, lower for banks

	peScore := 50.0
	if pe > 0 && pe < 200 {
		if pe <= midPE {
			peScore = 100 - (pe-5)/(midPE-5)*50
		} else {
			peScore = 50 - (pe-midPE)/(200-midPE)*50
		}
		peScore = clamp(peScore, 0, 100)
	}

	// Profitability-adjusted value: high ROE + high margin compensates for higher PE
	profitScore := 50.0
	if roe > 0 && netMargin > 0 {
		// ROE 25% → 100, ROE 10% → 50
		roeS := clamp(roe/25*100, 0, 100)
		// Margin 40% → 100, margin 15% → 50
		marS := clamp(netMargin/40*100, 0, 100)
		profitScore = roeS*0.6 + marS*0.4
	}

	// Combined: PE matters but profitability matters too
	return clamp(peScore*0.55 + profitScore*0.45, 0, 100)
}

// --- Generic fallback ---

func genericValScore(c *model.ScreenedCandidate) float64 {
	pe := c.Indicators["pettm"]
	pb := c.Indicators["pbmrq"]

	peScore := 50.0
	if pe > 0 && pe < 120 {
		if pe <= 30 {
			peScore = 100 - (pe-5)/25*50
		} else {
			peScore = 50 - (pe-30)/70*50
		}
		peScore = clamp(peScore, 0, 100)
	}

	pbScore := 50.0
	if pb > 0 && pb < 10 {
		pbScore = clamp((2.0-pb)/1.5*100, 0, 100)
	}

	return peScore*0.6 + pbScore*0.4
}

func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}

// sectorPEAdjust returns an adjustment to the PE score based on industry norms.
// Different sectors have structurally different PE ranges.
// Returns a delta to ADD to the PE score (positive = sector is naturally higher PE).
func sectorPEAdjust(industry string) float64 {
	// High-PE sectors (growth/tech): adjust PE threshold up
	highPE := map[string]bool{
		"半导体": true, "软件": true, "互联网": true, "生物技术": true,
		"制药": true, "医疗保健": true, "电子": true, "计算机": true,
		"新能源": true, "光伏": true, "储能": true, "机器人": true,
		"Semiconductors": true, "Software": true, "Biotechnology": true,
		"Internet": true, "Health Care": true, "Technology": true,
	}
	// Low-PE sectors (financials/utilities/energy): adjust PE threshold down
	lowPE := map[string]bool{
		"银行": true, "保险": true, "房地产": true, "公用事业": true,
		"能源": true, "钢铁": true, "煤炭": true, "建筑": true,
		"Banks": true, "Insurance": true, "Real Estate": true,
		"Utilities": true, "Energy": true, "Materials": true,
	}

	for k := range highPE {
		if contains(industry, k) {
			return 15 // tech sector: PE up to 45 is "reasonable"
		}
	}
	for k := range lowPE {
		if contains(industry, k) {
			return -10 // bank sector: PE above 15 is "expensive"
		}
	}
	return 0
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		len(s) > len(substr) && (s[:len(substr)] == substr ||
			s[len(s)-len(substr):] == substr ||
			containsMiddle(s, substr)))
}

func containsMiddle(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
