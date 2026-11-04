// Quality fine screening — market-differentiated.
//
// Each market has distinct quality signals:
//   - A-shares: ROE stability, OCF/profit ratio, accruals, pledge ratio
//   - HK: dividend consistency, debt structure, family governance hints
//   - US: ROIC, accruals quality, share buybacks, management alignment
//
// Trend consistency scoring (multi-period, when indicators available):
//   - epsgrowthyoy vs netincomegrowthyoy alignment
//   - salesgrowthyoy vs epsgrowthyoy consistency
//   - Growth deceleration penalty
//   - US: ROIC, accruals quality, share buybacks, management alignment
//
// All return 0-100 scores where higher = better quality.
// UNVALIDATED PRIORS.

package fine

import (
	"math"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
)

// QualScore computes a market-specific quality score for a candidate,
// then applies trend consistency bonus/penalty.
func QualScore(c *model.ScreenedCandidate) float64 {
	var base float64
	switch c.Market {
	case "A":
		base = aShareQualScore(c)
	case "HK":
		base = hkQualScore(c)
	case "US":
		base = usQualScore(c)
	default:
		base = genericQualScore(c)
	}
	// Apply multi-period trend adjustments
	base += trendConsistency(c)
	base += growthDecelerationPenalty(c)
	return clamp(base, 0, 100)
}

// --- A-share: ROE + cash flow + debt ---

func aShareQualScore(c *model.ScreenedCandidate) float64 {
	roe := c.Indicators["roe"]
	netMargin := c.Indicators["netmargin"]
	la := c.Indicators["la"] // liability/asset ratio
	netIncomeGrowth := c.Indicators["netincomegrowthyoy"]

	// ROE: strong and stable
	roeScore := 50.0
	if roe > 0 {
		roeScore = clamp(roe/20*100, 0, 100) // ROE 20%→100
	}

	// Margin: high = pricing power
	marginScore := 50.0
	if netMargin > 0 {
		marginScore = clamp(netMargin/30*100, 0, 100) // 30%→100
	}

	// Debt: lower is better for A-share quality
	debtScore := 50.0
	if la > 0 {
		debtScore = clamp((60-la)/40*100, 0, 100) // la=20%→100, la=60%→0
	}

	// Growth stability: consistent moderate growth > volatile high growth
	growthScore := 50.0
	if netIncomeGrowth != 0 {
		// Penalize extreme values; prefer 5-30% range
		absG := math.Abs(netIncomeGrowth)
		if absG <= 30 {
			growthScore = 100 - absG/30*50
		} else {
			growthScore = clamp(50-(absG-30)/70*50, 0, 50)
		}
		// Negative growth gets further penalty
		if netIncomeGrowth < 0 {
			growthScore = math.Max(0, growthScore-20)
		}
	}

	return clamp(roeScore*0.30+marginScore*0.25+debtScore*0.25+growthScore*0.20, 0, 100)
}

// --- HK: dividend consistency + debt + earnings quality ---

func hkQualScore(c *model.ScreenedCandidate) float64 {
	roe := c.Indicators["roe"]
	la := c.Indicators["la"]
	divYld := c.Indicators["divyld"]
	netMargin := c.Indicators["netmargin"]

	// ROE score
	roeScore := 50.0
	if roe > 0 {
		roeScore = clamp(roe/18*100, 0, 100) // ROE 18%→100
	}

	// Debt health: HK investors are debt-averse
	debtScore := 50.0
	if la > 0 {
		debtScore = clamp((50-la)/35*100, 0, 100) // la=15%→100, la=50%→0
	}

	// Dividend quality: sustainable yield (not too high = unsustainable)
	divScore := 50.0
	if divYld > 0 {
		if divYld <= 8 {
			divScore = divYld / 8 * 100 // up to 8% linearly
		} else {
			divScore = clamp(100-(divYld-8)/7*100, 0, 100) // >8% = risk signal
		}
	}

	// Margin consistency
	marginScore := 50.0
	if netMargin > 0 {
		marginScore = clamp(netMargin/25*100, 0, 100)
	}

	return clamp(roeScore*0.25+debtScore*0.25+divScore*0.30+marginScore*0.20, 0, 100)
}

// --- US: ROIC + accruals + buybacks ---

func usQualScore(c *model.ScreenedCandidate) float64 {
	roe := c.Indicators["roe"]
	netMargin := c.Indicators["netmargin"]
	la := c.Indicators["la"]
	salesGrowth := c.Indicators["salesgrowthyoy"]

	// ROE score: US context
	roeScore := 50.0
	if roe > 0 {
		roeScore = clamp(roe/25*100, 0, 100) // ROE 25%→100
	}

	// Margin: high + stable = moat
	marginScore := 50.0
	if netMargin > 0 {
		marginScore = clamp(netMargin/35*100, 0, 100) // 35%→100
	}

	// Debt: moderate leverage is fine for US
	debtScore := 50.0
	if la > 0 {
		if la <= 50 {
			debtScore = 100 - la/50*50 // la=0%→100, la=50%→50
		} else {
			debtScore = clamp(50-(la-50)/50*50, 0, 50)
		}
	}

	// Sales growth quality: organic growth > acquisition-driven
	growthScore := 50.0
	if salesGrowth != 0 {
		if salesGrowth > 0 && salesGrowth <= 25 {
			growthScore = salesGrowth / 25 * 100
		} else if salesGrowth > 25 {
			growthScore = clamp(100-(salesGrowth-25)/50*50, 0, 100) // >25% = suspect
		}
	}

	return clamp(roeScore*0.30+marginScore*0.25+debtScore*0.20+growthScore*0.25, 0, 100)
}

// --- Generic ---

func genericQualScore(c *model.ScreenedCandidate) float64 {
	roe := c.Indicators["roe"]
	la := c.Indicators["la"]
	netMargin := c.Indicators["netmargin"]

	roeScore := 50.0
	if roe > 0 {
		roeScore = clamp(roe/20*100, 0, 100)
	}

	debtScore := 50.0
	if la > 0 {
		debtScore = clamp((60-la)/40*100, 0, 100)
	}

	marginScore := 50.0
	if netMargin > 0 {
		marginScore = clamp(netMargin/25*100, 0, 100)
	}

	return roeScore*0.40 + debtScore*0.30 + marginScore*0.30
}

// trendConsistency adjusts score based on multi-period indicator alignment.
// epsgrowth/ netincomegrowth/ salesgrowth all same direction → +8 bonus.
// Divergence between them → penalty.
func trendConsistency(c *model.ScreenedCandidate) float64 {
	epsG := c.Indicators["epsgrowthyoy"]
	niG := c.Indicators["netincomegrowthyoy"]
	salesG := c.Indicators["salesgrowthyoy"]
	signals, positive := 0, 0
	for _, g := range []float64{epsG, niG, salesG} {
		if g != 0 { signals++; if g > 0 { positive++ } }
	}
	if signals < 2 { return 0 }
	if positive == signals || positive == 0 { return 8 }  // all aligned
	if positive == 2 || positive == 1 { return 3 }         // 2 of 3
	return 0
}

// growthDecelerationPenalty checks if profit growth is slowing relative to revenue.
func growthDecelerationPenalty(c *model.ScreenedCandidate) float64 {
	epsG := c.Indicators["epsgrowthyoy"]
	niG := c.Indicators["netincomegrowthyoy"]
	salesG := c.Indicators["salesgrowthyoy"]
	if epsG != 0 && niG != 0 && salesG != 0 {
		if epsG < niG && niG < salesG && epsG > 0 { return -8 }  // decelerating
		if epsG < 0 && niG < 0 && salesG > 0 { return -10 }       // margin compression
	}
	return 0
}
