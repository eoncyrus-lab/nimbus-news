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

func TestParseSinaHQ(t *testing.T) {
	body := `var hq_str_rt_hkHSI="HSI,恒生指数,24625.710,24481.859,24738.880,24421.110,24448.951,-32.910,-0.130,0.000,0.000,0,0,0.000,0.000,28056.100,22518.000,2026/11/09,16:08:26,,,,,";
var hq_str_hf_HSI="24520.810,,24515.000,24520.000,24529.000,24495.000,17:32:50,24519.000,24519.000,17142,4,9,2026-11-09,恒生指数期货,24";
var hq_str_rt_hkHSAHP="HSAHP,HSAHP,128.7200,129.5800,130.4700,128.5500,130.3700,0.7900,0.6097,0.0000,0.0000,0.0000,0,0.0000,0.0000,0.0000,0.0000,2026/11/09,16:08:26,,,,,";`
	quotes := parseSinaHQ(body)
	hsi, ok := quotes["rt_hkHSI"]
	if !ok || hsi.price != 24448.951 || hsi.pct != -0.13 {
		t.Fatalf("rt_hkHSI = %+v", hsi)
	}
	hf, ok := quotes["hf_HSI"]
	if !ok || hf.price != 24520.81 || hf.prev != 24519 {
		t.Fatalf("hf_HSI = %+v", hf)
	}
	if got := quotes["rt_hkHSAHP"].price; got != 130.37 {
		t.Fatalf("HSAHP price = %v", got)
	}
}
