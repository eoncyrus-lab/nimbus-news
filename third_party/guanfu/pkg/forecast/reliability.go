// Per-asset, per-horizon reliability wrappers — surfaced into forecast output
// so the consumer sees which horizons have been historically meaningful and
// which are essentially coin flips. The source table now lives in
// pkg/assetprofile so reading, forecast, skill, and backtest contracts share
// one policy source.
//
// The numbers below come from TestBacktestBundles (pkg/engine) on the
// post-refresh dataset (2026-05-11). They are the directional hit rate
// over the n_tests sliding-window backtests at that horizon. We treat
// dir_hit < 0.55 as "approaching random" — not a hard threshold but a
// reasonable fence for "don't draw conclusions from this".
//
// Updating: when TestBacktestBundles output changes materially, refresh
// the table here and the AsOf date. Don't auto-derive from a backtest
// run at panel-build time (would be slow and IO-heavy).

package forecast

import (
	"fmt"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/assetprofile"
)

// HorizonReliability is the static historical performance of a forecast
// at one (asset, horizon) cell.
type HorizonReliability struct {
	DirHit float64 `json:"dir_hit"` // directional hit rate, 0-1
	NTests int     `json:"n_tests"` // backtest sample count
	AsOf   string  `json:"as_of"`   // YYYY-MM-DD when these numbers were measured
}

// reliabilityThreshold is the dir_hit cutoff below which the caveat fires.
// 0.55 = ~5pp better than coin flip — anything weaker is treated as noise.
const reliabilityThreshold = 0.55

// hardBlockThreshold is the dir_hit cutoff below which we refuse to emit
// numeric forecast summaries at all — only a "signal strength below random"
// warning. 0.50 = no better than a coin flip; pretending a prediction is
// meaningful at this level is dishonest to the user.
const hardBlockThreshold = 0.50

// minSamplesForClaim is the minimum n_tests we need before reporting any
// reliability number. Below this, we say "untested" instead of pretending
// the dir_hit means something.
const minSamplesForClaim = 10

// ReliabilityFor returns the recorded reliability cell for an
// (asset, days) pair. ok=false when no data is recorded — treat as
// "untested, no claim".
func ReliabilityFor(asset string, days int) (HorizonReliability, bool) {
	r, ok := assetprofile.ReliabilityFor(asset, days)
	if !ok {
		return HorizonReliability{}, false
	}
	return HorizonReliability{DirHit: r.DirHit, NTests: r.NTests, AsOf: r.AsOf}, true
}

// HorizonCaveat returns a non-empty caveat string when the (asset, days)
// historical performance is poor or thin enough that a consumer should
// down-weight the forecast. Empty string when reliable, or when no data
// is recorded for this cell (we don't manufacture warnings).
func HorizonCaveat(asset string, days int) string {
	r, ok := ReliabilityFor(asset, days)
	if !ok {
		return ""
	}
	if r.NTests < minSamplesForClaim {
		return fmt.Sprintf("⚠ 仅 %d 个回测样本，可靠性不足", r.NTests)
	}
	if r.DirHit < hardBlockThreshold {
		return fmt.Sprintf("⚠ 历史命中率 %.0f%% (n=%d, 截至 %s)，信号强度低于随机阈值，请忽略数值预测，仅参考原始指标",
			r.DirHit*100, r.NTests, r.AsOf)
	}
	if r.DirHit < reliabilityThreshold {
		return fmt.Sprintf("⚠ 历史命中率 %.0f%% (n=%d, 截至 %s)，接近随机",
			r.DirHit*100, r.NTests, r.AsOf)
	}
	return ""
}

// IsHardBlocked reports whether the (asset, horizon) cell's dir_hit is at or
// below the coin-flip threshold. Consumers that display numeric predictions
// should suppress or visually dim them when true — emitting a specific
// "90d p10 -5% / p90 +12%" on a 45% dir_hit horizon invites users to treat
// noise as signal. Untested cells (no entry) return false: we don't have
// evidence to block them.
func IsHardBlocked(asset string, days int) bool {
	r, ok := ReliabilityFor(asset, days)
	if !ok {
		return false
	}
	if r.NTests < minSamplesForClaim {
		return false
	}
	return r.DirHit < hardBlockThreshold
}
