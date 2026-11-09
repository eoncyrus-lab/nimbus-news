package source

import (
	"strings"
	"testing"
)

func TestFormatBulletContentSplitsLongText(t *testing.T) {
	text := "Qatar says US-Iran talks alive as Trump meets Qatari PM. Oil swings on mixed signals. " +
		"China October CPI +0.7% y/y (expected +0.8%, prior +0.9%). " +
		"WSJ: Japan spent a record $50 billion in one day to defend the yen, data confirms. " +
		"Markets price three more BoE hikes, but analysts see a shallower path after move to 4%. " +
		"U.S. CPI is due tomorrow and the market is already pricing a 78% chance of a hike in December. " +
		"--- " +
		"中国人民银行开展 5000 亿元逆回购操作，中标利率为 1.50%。市场流动性保持合理充裕。" +
		"美联储官员表示，目前不应急于下调关键利率。"

	out := formatBulletContent(text, 200)
	lines := strings.Split(out, "\n")
	bullets := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "- ") {
			bullets++
		}
	}
	if bullets < 6 {
		t.Fatalf("expected at least 6 bullets, got %d:\n%s", bullets, out)
	}
	for _, line := range lines {
		if strings.TrimSpace(line) == "- U.S." {
			// U.S. 是缩写，不能单独成条
			t.Fatalf("U.S. abbreviation split into its own bullet:\n%s", out)
		}
	}
	if !strings.Contains(out, "U.S. CPI is due tomorrow") {
		t.Fatalf("abbreviation boundary broke sentence:\n%s", out)
	}
	if !strings.Contains(out, "中国人民银行开展 5000 亿元逆回购操作") {
		t.Fatalf("Chinese content lost:\n%s", out)
	}
}

func TestFormatBulletContentKeepsShortText(t *testing.T) {
	text := "Fed's Daly: jobs report is good news. Markets are calm."
	if got := formatBulletContent(text, 400); got != text {
		t.Fatalf("short text should be unchanged, got %q", got)
	}
}
