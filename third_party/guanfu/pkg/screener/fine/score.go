// Composite scoring — weighted combination with decision bucketing.
//
// Applies StrategyProfile weights to valuation/quality/potential/risk scores.
// Outputs FinalScore, decision buckets (core_candidate/watchlist/reserve/reject),
// and human-readable reasons.
//
// UNVALIDATED PRIORS — calibrate with walk-forward backtest.

package fine

import (
	"fmt"
	"sort"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
)

// Decision bucket labels.
const (
	BucketCore     = "core_candidate" // >= CoreCandidateMin — 核心标的
	BucketWatch    = "watchlist"      // >= WatchlistMin — 观察名单
	BucketReserve  = "reserve"        // below WatchlistMin — 储备
	BucketReject   = "reject"         // RiskScore < 20 — 排除
)

// ScoreCandidate computes the composite score for a single candidate
// using the given profile's weights, and sets all score fields + reasons + flags.
// Returns the decision bucket.
func ScoreCandidate(c *model.ScreenedCandidate, profile *model.StrategyProfile) string {
	c.ValScore = ValScore(c)
	c.QualScore = QualScore(c)
	c.PotScore = PotScore(c)
	c.TechScore = TechScore(c)
	c.FlowScore = FlowScore(c)
	c.RiskScore = RiskScore(c) // this also populates RiskFlags

	w := profile.Weights
	c.FinalScore = c.ValScore*w.Valuation +
		c.QualScore*w.Quality +
		c.PotScore*w.Potential +
		c.TechScore*w.Technical +
		c.FlowScore*w.Flow +
		c.RiskScore*w.Risk

	// Build reasons
	c.Reasons = buildReasons(c)

	// Decision
	return decide(c, profile)
}

// ScoreAll scores all candidates and returns them sorted by FinalScore descending.
func ScoreAll(candidates []model.ScreenedCandidate, profile *model.StrategyProfile) []model.ScreenedCandidate {
	for i := range candidates {
		ScoreCandidate(&candidates[i], profile)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].FinalScore > candidates[j].FinalScore
	})
	return candidates
}

// Bucketize splits scored candidates into decision buckets.
func Bucketize(candidates []model.ScreenedCandidate) map[string][]model.ScreenedCandidate {
	buckets := map[string][]model.ScreenedCandidate{
		BucketCore:    {},
		BucketWatch:   {},
		BucketReserve: {},
		BucketReject:  {},
	}
	for _, c := range candidates {
		if len(c.RiskFlags) > 0 {
			// Check for hard-reject flags
			hardReject := false
			for _, f := range c.RiskFlags {
				if f == "ST或*ST特别处理" || f == "ST标识检测" || f == "SPAC/空白支票公司" {
					hardReject = true
					break
				}
			}
			if hardReject {
				buckets[BucketReject] = append(buckets[BucketReject], c)
				continue
			}
		}
		switch {
		case c.FinalScore >= 65:
			buckets[BucketCore] = append(buckets[BucketCore], c)
		case c.FinalScore >= 50:
			buckets[BucketWatch] = append(buckets[BucketWatch], c)
		default:
			buckets[BucketReserve] = append(buckets[BucketReserve], c)
		}
	}
	return buckets
}

// buildReasons generates human-readable reasons for a candidate's inclusion.
func buildReasons(c *model.ScreenedCandidate) []string {
	var reasons []string

	if c.ValScore >= 75 {
		reasons = append(reasons, fmt.Sprintf("估值分%.0f: 显著低估", c.ValScore))
	} else if c.ValScore >= 60 {
		reasons = append(reasons, fmt.Sprintf("估值分%.0f: 估值合理偏低", c.ValScore))
	}
	if c.QualScore >= 75 {
		reasons = append(reasons, fmt.Sprintf("质量分%.0f: 盈利质量优秀", c.QualScore))
	} else if c.QualScore >= 60 {
		reasons = append(reasons, fmt.Sprintf("质量分%.0f: 基本面稳健", c.QualScore))
	}
	if c.PotScore >= 70 {
		reasons = append(reasons, fmt.Sprintf("潜力分%.0f: 拐点/催化信号强", c.PotScore))
	}
	if c.HeatRank > 0 && c.HeatRank <= 50 {
		reasons = append(reasons, fmt.Sprintf("热度#%d: 市场关注度高", c.HeatRank))
	}
	if c.HeatRising {
		reasons = append(reasons, "热度上升中")
	}

	// Fallback
	if len(reasons) == 0 {
		if c.FinalScore >= 65 {
			reasons = append(reasons, "综合评分达标")
		} else if c.FinalScore >= 50 {
			reasons = append(reasons, "综合评分中等")
		}
	}

	return reasons
}

// decide returns the decision bucket for a scored candidate.
func decide(c *model.ScreenedCandidate, profile *model.StrategyProfile) string {
	// Hard reject: RiskScore < 20 or hard-reject flags
	if c.RiskScore < 20 {
		return BucketReject
	}
	for _, f := range c.RiskFlags {
		if f == "ST或*ST特别处理" || f == "ST标识检测" || f == "SPAC/空白支票公司" {
			return BucketReject
		}
	}

	// Apply profile thresholds (with defaults)
	coreMin := profile.CoreCandidateMin
	if coreMin == 0 {
		coreMin = 65
	}
	watchMin := profile.WatchlistMin
	if watchMin == 0 {
		watchMin = 50
	}

	if c.FinalScore >= coreMin {
		return BucketCore
	}
	if c.FinalScore >= watchMin {
		return BucketWatch
	}
	return BucketReserve
}
