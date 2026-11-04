// mempool.space 客户端 — 拿 BTC 网络数据：哈希率、难度、mempool 拥堵
//
// 全部免费，无需 API key。

package client

import (
	"context"
	"fmt"
	"time"

	"github.com/Ricaardo/nimbus-os/datasources/mempool"
)

// MempoolData 网络数据汇总
type MempoolData struct {
	HashRate3yEHs       []HashRatePoint `json:"-"`                 // 3 年哈希率序列（用于 hash ribbons 计算）
	HashRateNowEHs      float64         `json:"hash_rate_now_ehs"` // 当前哈希率（EH/s）
	HashRibbons30vs60   string          `json:"hash_ribbons"`      // "上行" / "下行" / "交叉中"
	DifficultyChangePct float64         `json:"difficulty_change_pct"`
	MempoolMB           float64         `json:"mempool_mb"`
	MempoolCount        int             `json:"mempool_count"`
	AsOf                time.Time       `json:"-"`
	Warnings            []string        `json:"-"`
}

// HashRatePoint 单点哈希率
type HashRatePoint struct {
	Timestamp int64
	HashEHs   float64
}

// FetchMempoolData 一次性拉取 mempool.space 三个端点（走共享 datasources/mempool 包）
func FetchMempoolData(ctx context.Context) (*MempoolData, error) {
	out := &MempoolData{}

	// 1. 哈希率（3y 序列）
	if hr, err := mempool.Hashrate3y(ctx); err == nil {
		pts := make([]HashRatePoint, len(hr))
		for i, p := range hr {
			pts[i] = HashRatePoint{Timestamp: p.Timestamp, HashEHs: p.HashEHs}
		}
		out.HashRate3yEHs = pts
		if len(pts) > 0 {
			out.HashRateNowEHs = pts[len(pts)-1].HashEHs
			out.HashRibbons30vs60 = computeHashRibbons(pts)
			out.AsOf = time.Unix(pts[len(pts)-1].Timestamp, 0).UTC()
		}
	} else {
		out.Warnings = append(out.Warnings, fmt.Sprintf("mempool hash rate fetch failed: %v", err))
	}

	// 2. 难度
	if dc, err := mempool.DifficultyChangePct(ctx); err == nil {
		out.DifficultyChangePct = dc
	} else {
		out.Warnings = append(out.Warnings, fmt.Sprintf("mempool difficulty fetch failed: %v", err))
	}

	// 3. Mempool 拥堵
	if vsize, count, err := mempool.MempoolDepth(ctx); err == nil {
		out.MempoolMB = float64(vsize) / 1024 / 1024
		out.MempoolCount = count
		if out.AsOf.IsZero() {
			out.AsOf = time.Now().UTC()
		}
	} else {
		out.Warnings = append(out.Warnings, fmt.Sprintf("mempool depth fetch failed: %v", err))
	}

	return out, nil
}

// computeHashRibbons 30d MA vs 60d MA of daily hash rate.
//
// Uses the last 180 days of data for stable moving averages (3 × 60d),
// not just 60 points, to avoid short-term noise causing false signals.
// Returns "上行" (30 > 60 : miner expansion) / "下行" (30 < 60 : miner capitulation) / "交叉中".
func computeHashRibbons(hr []HashRatePoint) string {
	const (
		ma30Window = 30
		ma60Window = 60
		minData    = 180 // need at least 3 × 60d for a stable 60d MA
	)

	if len(hr) < minData {
		// Fallback: use what we have, but require at least 60 points
		if len(hr) < ma60Window {
			return "n/a"
		}
	}

	// Work with the last N points, up to 180
	tail := hr
	if len(hr) > minData {
		tail = hr[len(hr)-minData:]
	}

	// 30d MA: average of last 30 points
	ma30 := 0.0
	start30 := len(tail) - ma30Window
	for _, p := range tail[start30:] {
		ma30 += p.HashEHs
	}
	ma30 /= float64(ma30Window)

	// 60d MA: average of all available tail points (up to 180, min 60)
	ma60 := 0.0
	for _, p := range tail {
		ma60 += p.HashEHs
	}
	ma60 /= float64(len(tail))

	diff := (ma30 - ma60) / ma60
	switch {
	case diff > 0.02:
		return "上行（矿工扩张）"
	case diff < -0.02:
		return "下行（矿工投降信号 ⚠）"
	default:
		return "交叉中"
	}
}
