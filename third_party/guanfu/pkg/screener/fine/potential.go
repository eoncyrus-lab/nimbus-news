// Potential detection — market-differentiated turnaround & catalyst signals.
//
// Each market has distinct potential signals:
//   - A-shares: policy catalyst, earnings turnaround, asset injection/restructuring
//   - HK: HK Connect inclusion/upgrade, privatization bids, earnings recovery
//   - US: earnings surprise momentum, new product cycle, industry consolidation
//
// All return 0-100 scores where higher = more potential.
// UNVALIDATED PRIORS.

package fine

import (
	"math"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
)

// PotScore computes a market-specific potential score.
func PotScore(c *model.ScreenedCandidate) float64 {
	switch c.Market {
	case "A":
		return aSharePotScore(c)
	case "HK":
		return hkPotScore(c)
	case "US":
		return usPotScore(c)
	default:
		return genericPotScore(c)
	}
}

// --- A-share: policy themes + earnings turnaround ---

func aSharePotScore(c *model.ScreenedCandidate) float64 {
	netIncomeGrowth := c.Indicators["netincomegrowthyoy"]
	salesGrowth := c.Indicators["salesgrowthyoy"]
	roe := c.Indicators["roe"]

	// Earnings turnaround: strong signal in A-shares
	turnaroundScore := 50.0
	if netIncomeGrowth > 0 {
		if netIncomeGrowth > 50 {
			turnaroundScore = 100 // explosive recovery
		} else {
			turnaroundScore = 50 + netIncomeGrowth/50*50
		}
	} else if netIncomeGrowth < 0 {
		// Declining but could be bottoming
		turnaroundScore = clamp(50+netIncomeGrowth/50*50, 10, 50)
	}

	// Revenue growth momentum
	revenueScore := 50.0
	if salesGrowth > 0 {
		if salesGrowth > 30 {
			revenueScore = 100
		} else {
			revenueScore = 50 + salesGrowth/30*50
		}
	}

	// Theme/concept bonus: thematic stocks have higher potential in A-shares
	themeBonus := 0.0
	hotThemes := map[string]bool{
		"AI": true, "人工智能": true, "芯片": true, "半导体": true,
		"新能源": true, "光伏": true, "储能": true, "机器人": true,
		"创新药": true, "低空经济": true, "量子": true,
	}
	for _, t := range c.Themes {
		if hotThemes[t] {
			themeBonus = 15
			break
		}
	}

	// ROE improvement potential: low current ROE means more room to improve
	roeScore := 50.0
	if roe > 0 {
		if roe < 5 {
			roeScore = 80 // deep value turnaround potential
		} else if roe < 10 {
			roeScore = 65
		} else {
			roeScore = clamp(50-(roe-10)/15*30, 20, 50)
		}
	}

	return clamp(turnaroundScore*0.30+revenueScore*0.25+roeScore*0.25+themeBonus, 0, 100)
}

// --- HK: HK Connect inclusion + earnings recovery ---

func hkPotScore(c *model.ScreenedCandidate) float64 {
	netIncomeGrowth := c.Indicators["netincomegrowthyoy"]
	salesGrowth := c.Indicators["salesgrowthyoy"]
	roe := c.Indicators["roe"]

	// Earnings turnaround
	turnaroundScore := 50.0
	if netIncomeGrowth > 0 {
		if netIncomeGrowth > 50 {
			turnaroundScore = 100
		} else {
			turnaroundScore = 50 + netIncomeGrowth/50*50
		}
	} else {
		turnaroundScore = clamp(50+netIncomeGrowth/50*50, 10, 50)
	}

	// Revenue growth
	revenueScore := 50.0
	if salesGrowth > 0 {
		if salesGrowth > 30 {
			revenueScore = 100
		} else {
			revenueScore = 50 + salesGrowth/30*50
		}
	}

	// ROE recovery potential
	roeScore := 50.0
	if roe > 0 && roe < 8 {
		roeScore = 75 // low ROE = turnaround potential in HK
	} else if roe >= 8 && roe < 15 {
		roeScore = 60
	}

	return clamp(turnaroundScore*0.35+revenueScore*0.30+roeScore*0.35, 0, 100)
}

// --- US: earnings momentum + product cycle ---

func usPotScore(c *model.ScreenedCandidate) float64 {
	netIncomeGrowth := c.Indicators["netincomegrowthyoy"]
	salesGrowth := c.Indicators["salesgrowthyoy"]
	roe := c.Indicators["roe"]

	// Earnings momentum: accelerating growth
	earnScore := 50.0
	if netIncomeGrowth > 0 {
		if netIncomeGrowth > 40 {
			earnScore = 100
		} else {
			earnScore = 50 + netIncomeGrowth/40*50
		}
	} else {
		earnScore = clamp(50+math.Abs(netIncomeGrowth)/30*50, 10, 50)
	}

	// Revenue acceleration
	revScore := 50.0
	if salesGrowth > 0 {
		if salesGrowth > 25 {
			revScore = 100
		} else {
			revScore = 50 + salesGrowth/25*50
		}
	}

	// Efficiency potential: low ROE with high margin = operational leverage
	effScore := 50.0
	margin := c.Indicators["netmargin"]
	if roe > 0 && roe < 15 && margin > 15 {
		effScore = 75 // high margin but low ROE = capital structure or one-time drag
	}

	// Heat momentum bonus: stocks rising in popularity have short-term potential
	heatBonus := 0.0
	if c.HeatRising {
		heatBonus = 10
	}
	if c.DiscussHot {
		heatBonus += 5
	}

	return clamp(earnScore*0.35+revScore*0.25+effScore*0.25+heatBonus, 0, 100)
}

// --- Generic ---

func genericPotScore(c *model.ScreenedCandidate) float64 {
	netIncomeGrowth := c.Indicators["netincomegrowthyoy"]
	salesGrowth := c.Indicators["salesgrowthyoy"]

	earnScore := 50.0
	if netIncomeGrowth > 0 {
		earnScore = clamp(50+netIncomeGrowth/40*50, 0, 100)
	} else {
		earnScore = clamp(50+netIncomeGrowth/30*50, 10, 50)
	}

	revScore := 50.0
	if salesGrowth > 0 {
		revScore = clamp(50+salesGrowth/25*50, 0, 100)
	}

	return earnScore*0.55 + revScore*0.45
}
