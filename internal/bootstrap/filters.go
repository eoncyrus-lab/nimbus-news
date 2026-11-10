package bootstrap

import (
	"log/slog"

	"github.com/Ricaardo/nimbus-os/news/internal/config"
	"github.com/Ricaardo/nimbus-os/news/internal/filter"
	"github.com/Ricaardo/nimbus-os/news/internal/llm"
	"github.com/Ricaardo/nimbus-os/news/internal/model"
	"github.com/Ricaardo/nimbus-os/news/internal/store"
)

// InitFilters builds the full filter chain and async evaluator.
func InitFilters(
	cfg *config.PlatformConfig,
	st store.Store,
	llmProvider llm.Provider,
	onAsyncHighScore func(*model.Message),
) (*filter.Chain, *filter.AsyncEvaluator) {
	chain := filter.NewChain()

	// Content filter
	if cfg.Filters.Content.Enabled {
		chain.Add(filter.NewContentFilter(filter.ContentFilterConfig{
			MinContentLength: cfg.Filters.Content.MinContentLength,
			BlockKeywords:    cfg.Filters.Content.BlockKeywords,
			StrictSources:    cfg.Filters.Content.StrictSources,
		}))
		slog.Info("content filter enabled", "min_length", cfg.Filters.Content.MinContentLength,
			"strict_sources", cfg.Filters.Content.StrictSources)
	}

	// Unified dedup: Exact ID → Content Hash → Semantic
	if cfg.Filters.Dedup.Enabled {
		ud, err := filter.NewUnifiedDedup(st, filter.UnifiedDedupConfig{
			Enabled:     true,
			TTL:         cfg.Filters.Dedup.TTL,
			DedupGroups: cfg.Filters.Dedup.Groups,
			SkipSinks:   cfg.Filters.Dedup.SkipSinks,
			Semantic: filter.SemanticSubConfig{
				Enabled:    cfg.Filters.Dedup.Semantic.Enabled,
				Threshold:  cfg.Filters.Dedup.Semantic.Threshold,
				TimeWindow: cfg.Filters.Dedup.Semantic.TimeWindow,
				CacheSize:  cfg.Filters.Dedup.Semantic.CacheSize,
			},
		})
		if err == nil {
			chain.Add(ud)
			slog.Info("unified dedup enabled", "groups", len(cfg.Filters.Dedup.Groups),
				"semantic", cfg.Filters.Dedup.Semantic.Enabled,
				"threshold", cfg.Filters.Dedup.Semantic.Threshold)
		} else {
			slog.Error("failed to init unified dedup", "error", err)
		}
	}

	// 未配置 LLM（llmProvider == nil）时绝不挂 AI 过滤器，推送不受 AI 影响。
	// 配了 LLM 时仍挂入 chain 供 typed routing 的 EvaluateTyped 复用；是否实际
	// 拦截由 Enabled 决定（ShouldFilter 在 false 时直接放行），LLM 报错/限流也
	// fail-open。日志如实标注启用状态，避免把「已挂载求值器」误读成「已开启拦截」。
	if llmProvider != nil {
		chain.Add(filter.NewAIFilter(llmProvider, filter.AIFilterConfig{
			Enabled:            cfg.Filters.AIFilter.Enabled,
			ThresholdScore:     cfg.Filters.AIFilter.ThresholdScore,
			MaxPerMinute:       cfg.Filters.AIFilter.MaxPerMinute,
			GlobalMaxPerMinute: cfg.Filters.AIFilter.GlobalMaxPerMinute,
			BlockCategories:    cfg.Filters.AIFilter.BlockCategories,
			TargetSources:      cfg.Filters.AIFilter.TargetSources,
		}))
		if cfg.Filters.AIFilter.Enabled {
			slog.Info("blocking ai_filter enabled", "threshold", cfg.Filters.AIFilter.ThresholdScore,
				"target_sources", len(cfg.Filters.AIFilter.TargetSources))
		} else {
			slog.Info("ai_filter evaluator mounted but disabled; news delivery is not gated by AI", "reason", "enabled=false")
		}
	} else {
		slog.Info("ai_filter not mounted; news delivery is not gated by AI", "reason", "LLM provider not configured")
	}

	// Rate limit
	if cfg.Filters.Ratelimit.Enabled {
		rules := make([]filter.RatelimitRule, len(cfg.Filters.Ratelimit.Rules))
		for i, r := range cfg.Filters.Ratelimit.Rules {
			rules[i] = filter.RatelimitRule{
				Source:       r.Source,
				Sink:         r.Sink,
				MaxPerMinute: r.MaxPerMinute,
			}
		}
		chain.Add(filter.NewRatelimitFilter(rules))
	}

	// Async AI evaluator
	var asyncEval *filter.AsyncEvaluator
	if cfg.Filters.AIFilter.Enabled && llmProvider != nil {
		asyncEval = filter.NewAsyncEvaluator(llmProvider, filter.AsyncEvalConfig{
			Enabled:         true,
			ThresholdScore:  cfg.Filters.AIFilter.ThresholdScore,
			Workers:         10,
			QueueSize:       1000,
			Timeout:         10,
			BlockCategories: cfg.Filters.AIFilter.BlockCategories,
			TargetSources:   cfg.Filters.AIFilter.TargetSources,
		}, onAsyncHighScore)
		slog.Info("async AI evaluator enabled", "threshold", cfg.Filters.AIFilter.ThresholdScore, "workers", 10)
	}

	return chain, asyncEval
}
