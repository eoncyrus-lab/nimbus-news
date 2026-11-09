package source

import (
	"strings"
	"testing"
)

func TestRenderAshareMappingStockChains(t *testing.T) {
	changes := map[string]float64{"^IXIC": 1.3, "SOXX": 3.5, "KWEB": -2.0, "USDCNH=X": 0.01}
	stocks := map[string]float64{"NVDA": 3.2, "AAPL": 0.2, "TSLA": -3.0}
	lines := renderAshareMapping(changes, stocks, nil)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"纳指 +1.30%", "费城半导体 +3.50%", "北方华创", "中概互联网 -2.00%", "英伟达 +3.20%", "中际旭创", "特斯拉 -3.00%", "拓普集团"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("mapping missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "苹果") {
		t.Fatalf("threshold should skip AAPL +0.2%%:\n%s", joined)
	}
	if strings.Contains(joined, "离岸人民币") {
		t.Fatalf("threshold should skip USDCNH +0.01%%:\n%s", joined)
	}

	override := renderAshareMapping(map[string]float64{"SOXX": 2}, nil, map[string]string{"SOXX": "自定义链：示例股份"})
	if len(override) != 1 || !strings.Contains(override[0], "示例股份") {
		t.Fatalf("override chain not used: %v", override)
	}
}
