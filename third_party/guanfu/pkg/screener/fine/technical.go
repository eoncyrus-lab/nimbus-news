// Technical scoring — price trend, momentum, volume-price, categorical signals.
//
// Uses indicators from Longbridge Mode B extra_returns:
//   - turnover_rate, volume_rate, amplitude (price volatility)
//   - MACD (categorical: goldenfork/deadcross), RSI (categorical: overbought/oversold)
//   - KDJ, Bollinger band categorical signals
//   - Price change: prevchg, prevonemonthchg
//
// For value investors, the ideal technical setup is:
//   - Price near support (not parabolic)
//   - Volume drying up on decline (capitulation complete)
//   - RSI exiting oversold → momentum turning
//   - MACD golden cross → trend reversal confirmation
//
// Categorical indicators from Longbridge: these come as string labels
// in the Indicators map if requested separately. When available as float
// flags (0=no signal, 1=signal), we interpret:
//   macd_goldenfork=1 → bullish +10
//   macd_deadcross=1 → bearish -10
//   rsi_oversold=1 → reversal potential +8
//   rsi_overbought=1 → pullback risk -8
//
// UNVALIDATED PRIORS.

package fine

import (
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
)

// TechScore computes a technical analysis score (0-100, higher = better setup).
func TechScore(c *model.ScreenedCandidate) float64 {
	score := 50.0

	// 1. Price momentum — avoid chasing extended moves
	prevChg := c.Indicators["prevchg"]
	monthChg := c.Indicators["prevonemonthchg"]

	if prevChg > 5 {
		score -= 10
	} else if prevChg < -5 {
		score += 5
	}

	if monthChg > 30 {
		score -= 20
	} else if monthChg > 15 {
		score -= 10
	} else if monthChg < -20 {
		score += 15
	} else if monthChg < -10 {
		score += 8
	}

	// 2. Volume ratio
	volRatio := c.Indicators["volume_rate"]
	if volRatio > 2.0 {
		score += 8
	} else if volRatio > 1.5 {
		score += 5
	}

	// 3. Turnover rate
	turnover := c.Indicators["turnover_rate"]
	if turnover > 1.0 && turnover < 5.0 {
		score += 5
	} else if turnover > 5.0 {
		score -= 5
	}

	// 4. Volatility (amplitude)
	amplitude := c.Indicators["amplitude"]
	if amplitude > 0 && amplitude < 3 {
		score += 5
	} else if amplitude > 8 {
		score -= 5
	}

	// 5. Counter-trend bonus
	if monthChg < -10 && monthChg > -25 && amplitude > 0 && amplitude < 5 && volRatio < 1.2 {
		score += 10
	}

	// 6. Categorical signals (MACD/RSI from Longbridge when available)
	// MACD golden fork = bullish crossover
	if c.Indicators["macd_goldenfork"] > 0 {
		score += 10
	}
	// MACD dead cross = bearish
	if c.Indicators["macd_deadcross"] > 0 {
		score -= 10
	}
	// RSI oversold → potential bounce
	if c.Indicators["rsi_oversold"] > 0 {
		score += 8
	}
	// RSI overbought → pullback risk
	if c.Indicators["rsi_overbought"] > 0 {
		score -= 8
	}
	// KDJ golden fork
	if c.Indicators["kdj_goldenfork"] > 0 {
		score += 6
	}
	// KDJ dead cross
	if c.Indicators["kdj_deadcross"] > 0 {
		score -= 6
	}
	// Bollinger breakthrough up (breakout)
	if c.Indicators["boll_breakthrough_up"] > 0 {
		score += 5
	}
	// Bollinger breakthrough down (breakdown)
	if c.Indicators["boll_breakthrough_down"] > 0 {
		score -= 5
	}

	return clamp(score, 0, 100)
}
