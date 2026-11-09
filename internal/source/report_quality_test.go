package source

import (
	"context"
	"strings"
	"testing"

	"github.com/Ricaardo/nimbus-os/news/internal/llm"
)

func TestFormatMacroValueUsesFREDSeriesUnits(t *testing.T) {
	tests := map[string]string{
		"WALCL":    "6.74万亿美元",
		"M2SL":     "21.50万亿美元",
		"CPIAUCSL": "323.1（指数）",
		"PCEPI":    "125.4（指数）",
		"T10Y2Y":   "0.42个百分点",
	}
	values := map[string]float64{
		"WALCL": 6_738_000, "M2SL": 21_500, "CPIAUCSL": 323.1,
		"PCEPI": 125.4, "T10Y2Y": 0.42,
	}
	for series, want := range tests {
		if got := formatMacroValue(series, values[series]); got != want {
			t.Errorf("formatMacroValue(%s) = %q, want %q", series, got, want)
		}
	}
}

func TestNewUSMacroPointSkipsMissingAndSameDateRevision(t *testing.T) {
	point, ok := newUSMacroPoint("CPIAUCSL", []fredObs{
		{Date: "2026-07-01", Value: "."},
		{Date: "2026-06-01", Value: "320"},
		{Date: "2026-06-01", Value: "319"},
		{Date: "2026-05-01", Value: "316"},
	})
	if !ok {
		t.Fatal("expected valid point")
	}
	if point.Date != "2026-06-01" || point.Value != 320 {
		t.Fatalf("latest valid point = %#v", point)
	}
	if !point.HasChange || point.ChangePct < 1.26 || point.ChangePct > 1.27 {
		t.Fatalf("change should compare against prior date, got %#v", point)
	}
}

func TestUSMacroRenderLabelsIndexAndPriorChange(t *testing.T) {
	s := &USMacroReportSource{}
	content := s.render([]*usMacroPoint{{
		SeriesID: "CPIAUCSL", NameCN: "CPI", Category: "通胀",
		Value: 323.1, ChangePct: 0.25, HasChange: true,
	}}, nil, nil, nil, nil, "", "")
	if !strings.Contains(content, "CPI 323.1（指数） (较前值 +0.25%)") {
		t.Fatalf("unexpected macro rendering:\n%s", content)
	}
	if strings.Contains(content, "同比") {
		t.Fatalf("index level must not be labeled year-over-year:\n%s", content)
	}
}

func TestClosingNarrativePromptDoesNotContainRawJSON(t *testing.T) {
	provider := &captureLLMProvider{}
	s := &MarketBriefingSource{name: "closing-test", llmProvider: provider}
	sen := &marketSentimentReport{}
	sen.Indices = append(sen.Indices, struct {
		Name      string  `json:"name"`
		Close     float64 `json:"close"`
		ChangePct float64 `json:"change_pct"`
	}{Name: "上证指数", Close: 3600, ChangePct: 1.2})
	s.closingNarrative(context.Background(), sen, &hotSectorEveningReport{},
		&lhbEveningReport{}, &capitalFlowEveningReport{})
	assertNoClosingRawJSON(t, provider.prompt)
	if !strings.Contains(provider.prompt, "上证指数 +1.20%") {
		t.Fatalf("prompt missing index line:\n%s", provider.prompt)
	}
}

func TestClosingRenderSkipsEmptyOverview(t *testing.T) {
	content := (&MarketBriefingSource{}).renderClosing(
		&marketSentimentReport{}, &hotSectorEveningReport{}, &lhbEveningReport{},
		&capitalFlowEveningReport{}, nil, "", nil)
	if strings.Contains(content, "市场总览") {
		t.Fatalf("empty overview header rendered:\n%s", content)
	}
}

func TestClosingRenderMarksFailedSections(t *testing.T) {
	content := (&MarketBriefingSource{}).renderClosing(
		&marketSentimentReport{}, &hotSectorEveningReport{}, &lhbEveningReport{},
		&capitalFlowEveningReport{}, nil, "", []string{"sentiment", "lhb"})
	if !strings.Contains(content, "数据源异常") || !strings.Contains(content, "sentiment") {
		t.Fatalf("failed sections not surfaced:\n%s", content)
	}
}

func assertNoClosingRawJSON(t *testing.T, text string) {
	t.Helper()
	for _, forbidden := range []string{"seal_amount", "raw_secret", `"candidates"`, `{"`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("output leaked raw JSON field %q:\n%s", forbidden, text)
		}
	}
}

type captureLLMProvider struct{ prompt string }

func (p *captureLLMProvider) Chat(context.Context, []llm.Message) (string, error) { return "", nil }
func (p *captureLLMProvider) ChatHeavy(context.Context, []llm.Message) (string, error) {
	return "", nil
}
func (p *captureLLMProvider) ChatWithTools(context.Context, []llm.Message, []map[string]interface{}) (*llm.Message, error) {
	return nil, nil
}
func (p *captureLLMProvider) Think(_ context.Context, messages []llm.Message, _ string) (string, string, error) {
	if len(messages) > 0 {
		p.prompt = messages[0].Content
	}
	return "ok", "", nil
}

func TestClosingRenderUsesScriptUnits(t *testing.T) {
	sen := &marketSentimentReport{}
	sen.Breadth.UpCount = 2242
	sen.Breadth.DownCount = 3148
	sen.Limits.ZtCount = 54
	sen.Limits.DtCount = 1
	sen.Limits.ZbRate = 44.9 // 脚本输出百分数
	sen.Volume.Total = 14946 // 脚本输出“亿”
	content := (&MarketBriefingSource{}).renderClosing(
		sen, &hotSectorEveningReport{}, &lhbEveningReport{},
		&capitalFlowEveningReport{}, nil, "", nil)
	if !strings.Contains(content, "炸板率 45%") {
		t.Fatalf("broken ratio unit wrong:\n%s", content)
	}
	if !strings.Contains(content, "成交额: 14946亿") {
		t.Fatalf("volume unit wrong:\n%s", content)
	}
}
