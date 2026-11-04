// Fund flow & volume-price scoring.
//
// Uses indicators from Longbridge Mode B:
//   - total_amount: daily turnover (万)
//   - balance: order book imbalance (net buy vs sell)
//   - circulating_marketcap: free-float market cap
//   - turnover_rate: daily turnover / free float
//
// For value investors, ideal flow signals:
//   - Moderate to high turnover (liquidity exists)
//   - Net buying pressure (balance > 0)
//   - Not too hot (turnover_rate < 10%)
//   - Small-cap with increasing volume (accumulation signal)
//
// UNVALIDATED PRIORS.

package fine

import (
	"math"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
)

// FlowScore computes a fund flow score (0-100, higher = better flow).
func FlowScore(c *model.ScreenedCandidate) float64 {
	score := 50.0

	amount := c.Indicators["total_amount"]     // 万 (10k units)
	balance := c.Indicators["balance"]           // order book net
	turnover := c.Indicators["turnover_rate"]    // %
	circMktCap := c.Indicators["circulating_marketcap"] // 亿
	mktCap := c.Indicators["marketcap"]          // 亿

	// 1. Liquidity score — higher turnover = easier entry/exit
	if amount > 1e7 { // > 1亿 turnover
		score += 15 // excellent liquidity
	} else if amount > 5e6 { // > 5000万
		score += 10
	} else if amount > 1e6 { // > 1000万
		score += 5
	} else if amount < 1e5 { // < 10万
		score -= 20 // illiquid, avoid for value positions
	} else if amount < 5e5 {
		score -= 10
	}

	// 2. Turnover rate quality
	if turnover > 0.5 && turnover < 3.0 {
		score += 8 // healthy turnover: 0.5-3%
	} else if turnover >= 3.0 && turnover < 8.0 {
		score += 3 // moderate
	} else if turnover >= 8.0 {
		score -= 10 // too hot, potential distribution
	}

	// 3. Net buying pressure (order book imbalance)
	if balance > 0 {
		// Normalize by market cap: balance ratio
		capForRatio := mktCap
		if capForRatio <= 0 {
			capForRatio = circMktCap
		}
		if capForRatio > 0 {
			balRatio := math.Abs(balance) / capForRatio
			if balance > 0 && balRatio > 0.01 {
				score += 10 // strong net buying
			} else if balance > 0 {
				score += 5
			}
		}
	} else if balance < 0 {
		score -= 5 // net selling
	}

	// 4. Size-appropriate liquidity
	// Small cap (< 50亿) with reasonable volume = accumulation signal
	if mktCap > 0 && mktCap < 50 && amount > 5e5 {
		score += 8 // small cap with volume = active accumulation
	}
	// Large cap (> 500亿) expects higher volume
	if mktCap > 500 && amount < 1e6 {
		score -= 5 // large cap with thin volume = ignored
	}

	// 5. Free float ratio bonus
	if circMktCap > 0 && mktCap > 0 {
		floatRatio := circMktCap / mktCap
		if floatRatio > 0.5 && floatRatio < 0.9 {
			score += 3 // good float ratio: liquid but not fully diluted
		}
	}

	return clamp(score, 0, 100)
}
