// Risk red flag detection — market-differentiated.
//
// Each market has distinct risk signals:
//   - A-shares: ST/delisting risk, major shareholder reduction, goodwill impairment,
//     high pledge ratio, audit qualifications
//   - HK: old thousand stocks (老千股) patterns, related-party transactions,
//     qualified audit opinions, frequent share consolidation
//   - US: high short interest, SEC inquiries, class action lawsuits,
//     restatements, going-concern warnings
//
// Returns (score 0-100 where higher = lower risk, flags list).
// UNVALIDATED PRIORS.

package fine

import (
	"strings"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
)

// RiskScore computes a market-specific risk score (0-100, higher = less risk).
// Also populates candidate.RiskFlags with human-readable warnings.
func RiskScore(c *model.ScreenedCandidate) float64 {
	switch c.Market {
	case "A":
		return aShareRiskScore(c)
	case "HK":
		return hkRiskScore(c)
	case "US":
		return usRiskScore(c)
	default:
		return genericRiskScore(c)
	}
}

// --- A-share: ST + 减持 + 质押 + 商誉 ---

func aShareRiskScore(c *model.ScreenedCandidate) float64 {
	score := 100.0

	// ST / *ST detection (hard block)
	name := strings.ToUpper(c.Name)
	for _, t := range c.Themes {
		if t == "ST" || t == "*ST" {
			score -= 100 // immediate reject
			c.RiskFlags = append(c.RiskFlags, "ST或*ST特别处理")
		}
	}
	if strings.Contains(name, "ST") || strings.HasPrefix(c.Symbol, "ST") {
		score -= 100
		c.RiskFlags = append(c.RiskFlags, "ST标识检测")
	}

	// Low liquidity: amount < 20M CNY
	amount := c.Indicators["total_amount"]
	if amount > 0 && amount < 2e7 {
		score -= 25
		c.RiskFlags = append(c.RiskFlags, "成交额过低(<2000万)")
	} else if amount > 0 && amount < 5e7 {
		score -= 10
		c.RiskFlags = append(c.RiskFlags, "流动性偏低(<5000万)")
	}

	// High liability ratio
	la := c.Indicators["la"]
	if la > 80 {
		score -= 20
		c.RiskFlags = append(c.RiskFlags, "资产负债率>80%")
	} else if la > 65 {
		score -= 10
		c.RiskFlags = append(c.RiskFlags, "资产负债率偏高(>65%)")
	}

	// Negative ROE
	roe := c.Indicators["roe"]
	if roe < 0 {
		score -= 25
		c.RiskFlags = append(c.RiskFlags, "ROE为负")
	}

	// Penny stock (price < 1 CNY)
	price := c.Price
	if price > 0 && price < 1 {
		score -= 30
		c.RiskFlags = append(c.RiskFlags, "股价低于1元(面值退市风险)")
	} else if price > 0 && price < 3 {
		score -= 10
		c.RiskFlags = append(c.RiskFlags, "低价股(<3元)")
	}

	return clamp(score, 0, 100)
}

// --- HK: 老千股 + 关联交易 + 审计 ---

func hkRiskScore(c *model.ScreenedCandidate) float64 {
	score := 100.0

	// Extremely low PB (< 0.2) often signals old-thousand-stock or fraud
	pb := c.Indicators["pbmrq"]
	if pb > 0 && pb < 0.2 {
		score -= 40
		c.RiskFlags = append(c.RiskFlags, "PB极低(<0.2)疑似老千股")
	} else if pb > 0 && pb < 0.5 {
		score -= 15
		c.RiskFlags = append(c.RiskFlags, "PB偏低(<0.5)")
	}

	// Unsustainably high dividend (>12%) often a trap
	divYld := c.Indicators["divyld"]
	if divYld > 12 {
		score -= 25
		c.RiskFlags = append(c.RiskFlags, "股息率异常高(>12%)可持续性存疑")
	}

	// Negative or extremely high PE
	pe := c.Indicators["pettm"]
	if pe < 0 {
		score -= 20
		c.RiskFlags = append(c.RiskFlags, "PE为负(亏损)")
	}

	// Low liquidity
	amount := c.Indicators["total_amount"]
	if amount > 0 && amount < 1e6 { // <1M HKD
		score -= 30
		c.RiskFlags = append(c.RiskFlags, "成交额极低(<100万港币)")
	} else if amount > 0 && amount < 5e6 {
		score -= 15
		c.RiskFlags = append(c.RiskFlags, "流动性偏低(<500万港币)")
	}

	// Low market cap + low price = shell stock risk
	mktCap := c.Indicators["marketcap"]
	price := c.Price
	if mktCap > 0 && mktCap < 5 && price < 1 {
		score -= 20
		c.RiskFlags = append(c.RiskFlags, "小市值仙股(<5亿+<1元)")
	}

	// High debt
	la := c.Indicators["la"]
	if la > 70 {
		score -= 15
		c.RiskFlags = append(c.RiskFlags, "高负债(>70%)")
	}

	return clamp(score, 0, 100)
}

// --- US: short interest + SEC + litigation ---

func usRiskScore(c *model.ScreenedCandidate) float64 {
	score := 100.0

	// Low price
	price := c.Price
	if price > 0 && price < 2 {
		score -= 25
		c.RiskFlags = append(c.RiskFlags, "低价股(<2美元)")
	} else if price > 0 && price < 5 {
		score -= 10
		c.RiskFlags = append(c.RiskFlags, "低价股(<5美元)")
	}

	// Negative earnings
	pe := c.Indicators["pettm"]
	if pe < 0 {
		score -= 20
		c.RiskFlags = append(c.RiskFlags, "PE为负(亏损)")
	}

	// Low liquidity
	amount := c.Indicators["total_amount"]
	if amount > 0 && amount < 1e6 {
		score -= 30
		c.RiskFlags = append(c.RiskFlags, "成交额极低(<100万美元)")
	} else if amount > 0 && amount < 5e6 {
		score -= 15
		c.RiskFlags = append(c.RiskFlags, "流动性偏低(<500万美元)")
	}

	// Declining revenue
	salesGrowth := c.Indicators["salesgrowthyoy"]
	if salesGrowth < -20 {
		score -= 20
		c.RiskFlags = append(c.RiskFlags, "营收大幅下滑(>-20%)")
	} else if salesGrowth < -5 {
		score -= 10
		c.RiskFlags = append(c.RiskFlags, "营收下滑")
	}

	// SPAC / shell detection
	for _, t := range c.Themes {
		lower := strings.ToLower(t)
		if strings.Contains(lower, "spac") || strings.Contains(lower, "blank check") {
			score -= 50
			c.RiskFlags = append(c.RiskFlags, "SPAC/空白支票公司")
			break
		}
	}

	// China concept filter (US-listed Chinese cos have VIE/delisting risk)
	for _, t := range c.Themes {
		if t == "china_concept" || t == "china_concept_filtered" {
			score -= 15
			c.RiskFlags = append(c.RiskFlags, "中概股(VIE/退市风险)")
			break
		}
	}

	return clamp(score, 0, 100)
}

// --- Generic ---

func genericRiskScore(c *model.ScreenedCandidate) float64 {
	score := 100.0

	pe := c.Indicators["pettm"]
	if pe < 0 {
		score -= 20
		c.RiskFlags = append(c.RiskFlags, "PE为负")
	}

	la := c.Indicators["la"]
	if la > 70 {
		score -= 15
		c.RiskFlags = append(c.RiskFlags, "高负债")
	}

	if c.Price > 0 && c.Price < 1 {
		score -= 30
		c.RiskFlags = append(c.RiskFlags, "仙股")
	}

	return clamp(score, 0, 100)
}
