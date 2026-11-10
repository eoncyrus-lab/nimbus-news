package bootstrap

import (
	"context"
	"errors"
	"testing"

	"github.com/Ricaardo/nimbus-os/news/internal/config"
	"github.com/Ricaardo/nimbus-os/news/internal/filter"
	"github.com/Ricaardo/nimbus-os/news/internal/llm"
	"github.com/Ricaardo/nimbus-os/news/internal/model"
)

// blockingProvider 永远返回「应被拦截」的评分，用于验证 AI 门禁是否生效。
type blockingProvider struct{}

func (blockingProvider) Chat(_ context.Context, _ []llm.Message) (string, error) {
	return "1|无价值|无交易价值", nil
}
func (blockingProvider) ChatHeavy(_ context.Context, _ []llm.Message) (string, error) {
	return "1|无价值|无交易价值", nil
}
func (blockingProvider) ChatWithTools(_ context.Context, _ []llm.Message, _ []map[string]interface{}) (*llm.Message, error) {
	return nil, errors.New("not implemented")
}
func (blockingProvider) Think(_ context.Context, _ []llm.Message, _ string) (string, string, error) {
	return "1|无价值|无交易价值", "", nil
}

// 没配 AI，或 ai_filter.enabled=false 时，任何消息都不能被 AI 拦截。
// 这是「不可以影响推送」的回归护栏：LLM 缺失/关闭只影响增强与评分，
// 不改变消息是否送达。
func TestInitFilters_NeverBlocksDeliveryWhenAIIsUnavailableOrDisabled(t *testing.T) {
	msg := &model.Message{Title: "重要市场消息", Content: "央行意外降息", Source: "src-a", ID: "msg-1"}

	cases := []struct {
		name     string
		provider llm.Provider
		enabled  bool
	}{
		{name: "no LLM provider, enabled=false", provider: nil, enabled: false},
		{name: "no LLM provider, enabled=true", provider: nil, enabled: true},
		{name: "LLM provider present, enabled=false", provider: blockingProvider{}, enabled: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.PlatformConfig{}
			cfg.Filters.AIFilter = config.AIFilterConfig{
				Enabled:            tc.enabled,
				ThresholdScore:     6.5,
				MaxPerMinute:       30,
				GlobalMaxPerMinute: 60,
				BlockCategories:    []string{"无价值", "无关"},
				TargetSources:      []string{"src-a"},
			}

			chain, asyncEval := InitFilters(cfg, nil, tc.provider, nil)
			if chain.ShouldFilter("src-a", "wechat-main", msg) {
				t.Fatal("message was blocked by AI; delivery must not be gated by AI")
			}
			if asyncEval != nil {
				t.Fatal("async AI evaluator must not run when delivery is not gated by AI")
			}

			// 即使通过 typed routing 求值器，也不应产出「拦截」结论。
			for _, f := range chain.GetFilters() {
				if ai, ok := f.(*filter.AIFilter); ok {
					result := ai.EvaluateTyped(context.Background(), "src-a", msg)
					if result.Blocked {
						t.Fatalf("typed evaluation blocked delivery: %+v", result)
					}
				}
			}
		})
	}
}

// 只有显式启用且 LLM 可用时，AI 门禁才真正拦截低分消息。
func TestInitFilters_BlocksOnlyWhenExplicitlyEnabled(t *testing.T) {
	cfg := &config.PlatformConfig{}
	cfg.Filters.AIFilter = config.AIFilterConfig{
		Enabled:            true,
		ThresholdScore:     6.5,
		MaxPerMinute:       30,
		GlobalMaxPerMinute: 60,
		BlockCategories:    []string{"无价值", "无关"},
		TargetSources:      []string{"src-a"},
	}
	msg := &model.Message{Title: "低价值消息", Content: "无实质内容", Source: "src-a", ID: "msg-2"}

	chain, _ := InitFilters(cfg, nil, blockingProvider{}, nil)
	if !chain.ShouldFilter("src-a", "wechat-main", msg) {
		t.Fatal("expected low-value message to be blocked when AI gate is enabled")
	}
}
