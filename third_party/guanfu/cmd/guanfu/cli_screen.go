// guanfu screen — equity screener subcommand.
//
// Usage:
//
//	guanfu screen                          # default: HK value
//	guanfu screen --market HK --strategy value
//	guanfu screen --market US --strategy growth
//	guanfu screen --market A --strategy value
//	guanfu screen --list-profiles          # list available profiles
//	guanfu screen --json                   # JSON output
//	guanfu screen --verdict-only           # verdict only
//	guanfu screen --top 5                  # show top N picks

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/screener"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/screener/fine"
)

func runScreen(market, style string, jsonOut, pretty, verdictOnly bool, topN int, listProfiles bool, timeout time.Duration) {
	if listProfiles {
		printScreenProfiles()
		return
	}

	// Find matching profile
	var profile *model.StrategyProfile
	for _, p := range model.BuiltinStrategyProfiles() {
		if strings.EqualFold(p.Market, market) && strings.EqualFold(p.Style, style) {
			profile = &p
			break
		}
	}
	if profile == nil {
		fmt.Fprintf(os.Stderr, "screen: no profile for market=%s style=%s\n", market, style)
		fmt.Fprintf(os.Stderr, "  available profiles:\n")
		printScreenProfiles()
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	asset := screener.NewScreenedEquitiesAsset(market, style, profile)

	snap, err := asset.FetchSnapshot(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "screen: fetch failed: %v\n", err)
		mcpURL := os.Getenv("LONGBRIDGE_MCP_URL")
		if mcpURL == "" {
			mcpURL = "https://openapi.longbridge.com/mcp"
		}
		fmt.Fprintf(os.Stderr, "(Longbridge MCP: %s, set LONGBRIDGE_API_KEY for auth)\n", mcpURL)
		os.Exit(1)
	}

	// Fallback: read from cache if FetchSnapshot fails to reach Longbridge
	candidates, err := asset.Candidates()
	if err != nil || len(candidates) == 0 {
		fmt.Fprintf(os.Stderr, "screen: no candidates available (cache miss + Longbridge unreachable)\n")
		os.Exit(1)
	}

	// Re-score (fresh scores based on latest indicators)
	fine.ScoreAll(candidates, profile)
	buckets := fine.Bucketize(candidates)

	// Build panel and verdict
	panel, err := asset.BuildPanel(snap)
	if err != nil {
		fmt.Fprintf(os.Stderr, "screen: build panel: %v\n", err)
		os.Exit(1)
	}
	verdict := asset.BuildVerdict(panel)

	if jsonOut || pretty {
		out := struct {
			Profile    string                       `json:"profile"`
			Market     string                       `json:"market"`
			Verdict    interface{}                  `json:"verdict"`
			Candidates []model.ScreenedCandidate    `json:"candidates"`
			Buckets    map[string]int               `json:"buckets"`
		}{
			Profile:    profile.Name,
			Market:     market,
			Verdict:    verdict,
			Candidates: limitCandidates(candidates, topN),
			Buckets: map[string]int{
				"core":    len(buckets[fine.BucketCore]),
				"watch":   len(buckets[fine.BucketWatch]),
				"reserve": len(buckets[fine.BucketReserve]),
				"reject":  len(buckets[fine.BucketReject]),
			},
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

	if verdictOnly {
		printScreenVerdict(profile, verdict, buckets)
		return
	}

	// Human output
	printScreenBanner(profile, verdict, buckets)
	printScreenTopPicks(candidates, topN)
	printScreenVerdict(profile, verdict, buckets)
}

func printScreenProfiles() {
	profiles := model.BuiltinStrategyProfiles()
	// Group by market
	markets := []struct {
		key, title string
	}{{"HK", "🇭🇰 港股"}, {"US", "🇺🇸 美股"}, {"A", "🇨🇳 A股"}, {"ALL", "🌏 跨市场"}}

	for _, m := range markets {
		count := 0
		for _, p := range profiles {
			if p.Market == m.key {
				count++
			}
		}
		if count == 0 {
			continue
		}
		fmt.Printf("\n%s (%d strategies):\n", m.title, count)
		for _, p := range profiles {
			if p.Market != m.key {
				continue
			}
			fmt.Printf("  %-14s style=%-12s coarse=%-3s  %s\n",
				p.Name, p.Style, p.CoarseStrategyID, p.Description)
		}
	}
	fmt.Println()
}

func printScreenBanner(profile *model.StrategyProfile, v interface{}, buckets map[string][]model.ScreenedCandidate) {
	core := buckets[fine.BucketCore]
	fmt.Printf("观复 · 选股  %s (%s)  %s\n", profile.Name, profile.Market, time.Now().Format("2006-01-02"))
	fmt.Printf("├─ 策略: %s  粗筛: Longbridge #%s\n", profile.Description, profile.CoarseStrategyID)
	fmt.Printf("├─ 候选: %d 只  core=%d watch=%d reserve=%d reject=%d\n",
		len(core)+len(buckets[fine.BucketWatch])+len(buckets[fine.BucketReserve])+len(buckets[fine.BucketReject]),
		len(core), len(buckets[fine.BucketWatch]), len(buckets[fine.BucketReserve]), len(buckets[fine.BucketReject]))
}

func printScreenTopPicks(candidates []model.ScreenedCandidate, topN int) {
	if topN <= 0 {
		topN = 10
	}
	if len(candidates) < topN {
		topN = len(candidates)
	}
	fmt.Println()
	fmt.Println("⭐ 精选标的:")
	fmt.Println("  #  Symbol        Name           Final  Val  Qual  Pot  Risk  Reasons")
	for i := 0; i < topN; i++ {
		c := candidates[i]
		fmt.Printf("  %-2d %-12s %-14s %5.0f  %4.0f  %4.0f  %4.0f  %4.0f  %s\n",
			i+1, c.Symbol, truncate(c.Name, 14), c.FinalScore,
			c.ValScore, c.QualScore, c.PotScore, c.RiskScore,
			truncate(joinReasons(c.Reasons), 50))
	}
}

func printScreenVerdict(profile *model.StrategyProfile, v interface{}, buckets map[string][]model.ScreenedCandidate) {
	// Use type assertion to access verdict fields
	vr, ok := v.(interface {
		GetStance() string
	})
	_ = vr
	_ = ok

	fmt.Println()
	fmt.Println("⚖ 读盘结论:")
	core := len(buckets[fine.BucketCore])
	watch := len(buckets[fine.BucketWatch])

	switch {
	case core >= 10:
		fmt.Println("  Stance: cautious_accumulate — 价值池充裕,可逐步建仓")
	case core >= 5:
		fmt.Println("  Stance: selective_accumulate — 精选核心标的分散配置")
	case core >= 1:
		fmt.Println("  Stance: nibble — 仅1-2只值得关注,控制仓位")
	default:
		fmt.Println("  Stance: standby — 当前市场无足够价值标的,等待")
	}
	fmt.Printf("  Buckets: core=%d watch=%d reserve=%d reject=%d\n",
		core, watch, len(buckets[fine.BucketReserve]), len(buckets[fine.BucketReject]))
	fmt.Println("  (完整读盘: guanfu screen --verdict-only)")
}

func limitCandidates(candidates []model.ScreenedCandidate, n int) []model.ScreenedCandidate {
	if n <= 0 || n > len(candidates) {
		n = len(candidates)
	}
	return candidates[:n]
}

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-1]) + "…"
}

func joinReasons(reasons []string) string {
	if len(reasons) == 0 {
		return "-"
	}
	result := ""
	for i, r := range reasons {
		if i > 0 {
			result += "; "
		}
		result += r
	}
	return result
}
