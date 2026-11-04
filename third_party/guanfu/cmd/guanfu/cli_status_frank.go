// status --frank (N4): per-asset per-horizon reliability categorization
// based on pkg/forecast/reliability.go. Scans the reliability table and
// prints 3 buckets:
//
//   ✓ 可靠    dir_hit ≥ 0.55 AND n ≥ 10
//   ⚠ 可疑    0.50 ≤ dir_hit < 0.55
//   ✗ 不建议  dir_hit < 0.50 OR n < 10
//
// Goal: a single glance answer to "which (asset, horizon) cells should
// I actually believe?". Pairs with N5 README failure-modes doc.

package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/forecast"
)

// runStatusFrank prints the reliability categorization.
// It deliberately does NOT fetch live data — purely a table dump of
// recorded backtest reliability, so it is fast and offline-safe.
type frankCell struct {
	Asset   string  `json:"asset"`
	Horizon int     `json:"horizon"`
	DirHit  float64 `json:"dir_hit"`
	N       int     `json:"n_tests"`
	Verdict string  `json:"verdict"` // reliable / approaching_random / hard_blocked / thin
	Note    string  `json:"note"`
}

func runStatusFrank(jsonOut, pretty, plain bool) {
	assets := []string{"btc", "qqq", "spy", "gold"}
	horizons := []int{30, 60, 63, 90, 120, 180, 252}
	var reliable, suspect, blocked []frankCell

	for _, a := range assets {
		for _, h := range horizons {
			r, ok := forecast.ReliabilityFor(a, h)
			if !ok {
				continue
			}
			c := frankCell{
				Asset:   a,
				Horizon: h,
				DirHit:  r.DirHit,
				N:       r.NTests,
				Note:    forecast.HorizonCaveat(a, h),
			}
			switch {
			case forecast.IsHardBlocked(a, h):
				c.Verdict = "hard_blocked"
				blocked = append(blocked, c)
			case r.NTests < 10:
				c.Verdict = "thin"
				suspect = append(suspect, c)
			case r.DirHit < 0.55:
				c.Verdict = "approaching_random"
				suspect = append(suspect, c)
			default:
				c.Verdict = "reliable"
				reliable = append(reliable, c)
			}
		}
	}

	if jsonOut || pretty {
		out := map[string]any{
			"reliable": reliable,
			"suspect":  suspect,
			"blocked":  blocked,
		}
		var b []byte
		if pretty {
			b, _ = json.MarshalIndent(out, "", "  ")
		} else {
			b, _ = json.Marshal(out)
		}
		fmt.Println(string(b))
		return
	}

	printFrankBucket(reliable, "✓ 可靠(dir_hit ≥ 55%, n ≥ 10)", plain)
	fmt.Println()
	printFrankBucket(suspect, "⚠ 可疑(接近随机 / 样本不足)", plain)
	fmt.Println()
	printFrankBucket(blocked, "✗ 不建议(dir_hit < 50%, hard-blocked)", plain)
	fmt.Println()
	fmt.Println("  使用建议:")
	fmt.Println("    - 只有'可靠'行 kNN 前向收益分布值得当数值看")
	fmt.Println("    - '可疑'行的 p10/p90 需附 caveat,不能作决策依据")
	fmt.Println("    - '不建议'行 guanfu 自动 hard-block,不输出数值 — 只用原始指标")
}

func printFrankBucket(cells []frankCell, header string, plain bool) {
	if plain {
		header = strings.ReplaceAll(header, "✓ ", "OK ")
		header = strings.ReplaceAll(header, "⚠ ", "WARN ")
		header = strings.ReplaceAll(header, "✗ ", "BLOCK ")
	}
	fmt.Println(header)
	if len(cells) == 0 {
		fmt.Println("  (none)")
		return
	}
	sort.Slice(cells, func(i, j int) bool {
		if cells[i].Asset == cells[j].Asset {
			return cells[i].Horizon < cells[j].Horizon
		}
		return cells[i].Asset < cells[j].Asset
	})
	for _, c := range cells {
		fmt.Printf("  %-6s %3dd   dir_hit %.0f%%  n=%d",
			strings.ToUpper(c.Asset), c.Horizon, c.DirHit*100, c.N)
		if c.Note != "" {
			fmt.Printf("   %s", c.Note)
		}
		fmt.Println()
	}
}
