// guanfu-similar compares one guanfu JSON panel against historical panel JSON files.
//
// Usage:
//
//	guanfu --json > current.json
//	guanfu-similar --current current.json                       # default --history-dir ~/.guanfu/panels
//	guanfu-similar --current current.json --history-dir path    # override
//	guanfu-similar --current current.json samples/panels/*.json # explicit files
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Ricaardo/nimbus-os/guanfu/pkg/model"
	"github.com/Ricaardo/nimbus-os/guanfu/pkg/version"
)

const defaultHistoryDir = "~/.guanfu/panels"

type compareResult struct {
	Date       string
	File       string
	Distance   float64
	Similarity float64
	Matched    int
}

func main() {
	currentPath := flag.String("current", "-", "current guanfu JSON panel path, or '-' for stdin")
	historyDir := flag.String("history-dir", defaultHistoryDir, "directory containing historical guanfu JSON panels")
	top := flag.Int("top", 5, "number of nearest panels to print")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		version.Print(os.Stdout, "guanfu-similar")
		return
	}

	current, err := readPanel(*currentPath)
	if err != nil {
		exitf("read current panel: %v", err)
	}

	files := append([]string(nil), flag.Args()...)
	if *historyDir != "" {
		expanded, err := expandHome(*historyDir)
		if err != nil {
			exitf("expand history dir: %v", err)
		}
		dirFiles, err := filepath.Glob(filepath.Join(expanded, "*.json"))
		if err != nil {
			exitf("read history dir: %v", err)
		}
		if len(dirFiles) == 0 && len(flag.Args()) == 0 {
			fmt.Fprintf(os.Stderr,
				"no panels found in %s.\n"+
					"  bootstrap the archive with:  guanfu --json > %s/$(date -u +%%F).json\n"+
					"  or pass explicit files / a different --history-dir.\n",
				expanded, expanded)
			os.Exit(2)
		}
		files = append(files, dirFiles...)
	}
	files = dedupeAndSort(files)
	if len(files) == 0 {
		exitf("no historical JSON panels provided")
	}

	results := make([]compareResult, 0, len(files))
	for _, file := range files {
		history, err := readPanel(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", file, err)
			continue
		}
		result := comparePanels(current, history)
		result.File = file
		result.Date = history.Date
		if result.Matched == 0 {
			fmt.Fprintf(os.Stderr, "skip %s: no shared q metrics\n", file)
			continue
		}
		results = append(results, result)
	}
	if len(results) == 0 {
		exitf("no comparable historical panels found")
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Distance == results[j].Distance {
			return results[i].File < results[j].File
		}
		return results[i].Distance < results[j].Distance
	})
	if *top > 0 && *top < len(results) {
		results = results[:*top]
	}

	fmt.Println("| rank | date | similarity | matched_q | distance | file |")
	fmt.Println("|---:|---|---:|---:|---:|---|")
	for i, result := range results {
		fmt.Printf("| %d | %s | %.1f%% | %d | %.4f | %s |\n",
			i+1, result.Date, result.Similarity, result.Matched, result.Distance, result.File)
	}
}

func exitf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func readPanel(path string) (*model.IndicatorPanel, error) {
	var b []byte
	var err error
	if path == "-" {
		b, err = io.ReadAll(os.Stdin)
	} else {
		b, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}

	var panel model.IndicatorPanel
	if err := json.Unmarshal(b, &panel); err != nil {
		return nil, err
	}
	return &panel, nil
}

func comparePanels(current, history *model.IndicatorPanel) compareResult {
	currentDomains := panelDomains(current)
	historyDomains := panelDomains(history)

	var sumSquares float64
	var matched int
	for domain, currentMetrics := range currentDomains {
		historyMetrics := historyDomains[domain]
		for key, currentIndicator := range currentMetrics {
			historyIndicator, ok := historyMetrics[key]
			if !ok || currentIndicator.Quantile <= 0 || historyIndicator.Quantile <= 0 {
				continue
			}
			diff := currentIndicator.Quantile - historyIndicator.Quantile
			sumSquares += diff * diff
			matched++
		}
	}

	if matched == 0 {
		return compareResult{Distance: math.Inf(1)}
	}

	distance := math.Sqrt(sumSquares / float64(matched))
	similarity := math.Max(0, 1-distance) * 100
	return compareResult{
		Distance:   distance,
		Similarity: similarity,
		Matched:    matched,
	}
}

func panelDomains(p *model.IndicatorPanel) map[string]map[string]model.Indicator {
	if p == nil {
		return nil
	}
	return map[string]map[string]model.Indicator{
		"cycle":       p.Cycle,
		"valuation":   p.Valuation,
		"network":     p.Network,
		"positioning": p.Positioning,
		"macro":       p.Macro,
		"flow":        p.Flow,
		"technical":   p.Technical,
		"cross_asset": p.CrossAsset,
	}
}

func expandHome(path string) (string, error) {
	if !strings.HasPrefix(path, "~/") && path != "~" {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if path == "~" {
		return home, nil
	}
	return filepath.Join(home, path[2:]), nil
}

func dedupeAndSort(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
