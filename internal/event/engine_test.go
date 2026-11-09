package event

import (
	"context"
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/news/internal/model"
)

func testClock() time.Time {
	return time.Date(2026, 11, 9, 10, 0, 0, 0, time.FixedZone("CST", 8*3600))
}

func newTestEngine(t *testing.T, mode string, now time.Time) (*Engine, *MemoryStore) {
	t.Helper()
	store := NewMemoryStore()
	store.now = func() time.Time { return now }
	cfg := DefaultConfig()
	cfg.Mode = mode
	cfg.Location = "Asia/Shanghai"
	engine, err := NewEngine(cfg, DefaultRules(), store)
	if err != nil {
		t.Fatal(err)
	}
	engine.SetClock(func() time.Time { return now })
	return engine, store
}

func newsMessage(id, source, title, content, link string, now time.Time) *model.Message {
	return &model.Message{
		Type:       model.TypeNews,
		ID:         id,
		Title:      title,
		Content:    content,
		Link:       link,
		Source:     source,
		CreateTime: now.Add(-2 * time.Minute),
		FetchTime:  now,
	}
}

func TestObserveMergesSameCanonicalURL(t *testing.T) {
	now := testClock()
	engine, _ := newTestEngine(t, ModeActive, now)

	first, err := engine.Observe(context.Background(), "bwe-tradfi",
		newsMessage("1", "bwe-tradfi", "Fed cuts rates by 25bp", "rate decision", "https://example.com/a?utm_source=x", now))
	if err != nil || first == nil {
		t.Fatalf("first observe: %v %v", first, err)
	}
	if first.Decision.Lane != LaneInstant {
		t.Fatalf("first lane = %s, want instant", first.Decision.Lane)
	}

	second, err := engine.Observe(context.Background(), "forexlive-breaking",
		newsMessage("2", "forexlive-breaking", "Fed cuts rates by 25bp - update", "rate decision confirmed", "https://example.com/a?ref=feed", now.Add(time.Minute)))
	if err != nil || second == nil {
		t.Fatalf("second observe: %v %v", second, err)
	}
	if !second.Duplicate {
		t.Fatal("second observation should be a duplicate")
	}
	if second.Story.ID != first.Story.ID {
		t.Fatalf("story id %s != %s", second.Story.ID, first.Story.ID)
	}
	if second.Decision.Lane != LaneSilent {
		t.Fatalf("duplicate lane = %s, want silent", second.Decision.Lane)
	}
	if got := len(second.Story.Sources); got != 2 {
		t.Fatalf("sources = %d, want 2", got)
	}
}

func TestObserveMatchesTitleSimilarity(t *testing.T) {
	now := testClock()
	engine, _ := newTestEngine(t, ModeShadow, now)

	_, _ = engine.Observe(context.Background(), "bwe-tradfi",
		newsMessage("1", "bwe-tradfi", "Fed cuts rates by 25 basis points", "rate decision", "https://example.com/1", now))
	second, _ := engine.Observe(context.Background(), "bloomberg-markets",
		newsMessage("2", "bloomberg-markets", "Fed cuts rates 25 basis points", "rate decision", "https://example.com/2", now.Add(time.Minute)))
	if second == nil || !second.Duplicate {
		t.Fatalf("similar titles should merge, got %+v", second)
	}
}

func TestBudgetCapsSeventhInstant(t *testing.T) {
	now := testClock()
	engine, _ := newTestEngine(t, ModeActive, now)

	subjects := []string{"Fed", "ECB", "BOJ", "PBOC", "BOE", "Treasury", "OPEC"}
	for i, subject := range subjects {
		obs, err := engine.Observe(context.Background(), "bwe-tradfi",
			newsMessage(string(rune('a'+i)), "bwe-tradfi",
				subject+" rate decision story "+string(rune('A'+i)),
				subject+" balance sheet review", "https://example.com/"+subject, now.Add(time.Duration(i)*time.Minute)))
		if err != nil || obs == nil {
			t.Fatalf("observe %s: %v", subject, err)
		}
		if i < 6 && obs.Decision.Lane != LaneInstant {
			t.Fatalf("%s lane = %s, want instant", subject, obs.Decision.Lane)
		}
		if i == 6 {
			if obs.Decision.Lane != LaneDefer {
				t.Fatalf("seventh lane = %s, want defer", obs.Decision.Lane)
			}
			if obs.Decision.Reason != "budget:budget" {
				t.Fatalf("seventh reason = %q, want budget:budget", obs.Decision.Reason)
			}
		}
	}
	if engine.budget.Used != 6 {
		t.Fatalf("budget used = %d, want 6", engine.budget.Used)
	}
}

func TestQuietHoursDeferAndCriticalBypass(t *testing.T) {
	now := time.Date(2026, 11, 9, 23, 45, 0, 0, time.FixedZone("CST", 8*3600))
	engine, _ := newTestEngine(t, ModeActive, now)

	quiet, _ := engine.Observe(context.Background(), "kobeissi-letter",
		newsMessage("1", "kobeissi-letter", "US CPI due tomorrow", "data release preview", "https://example.com/cpi", now))
	if quiet == nil || quiet.Decision.Lane != LaneDefer {
		t.Fatalf("quiet hours lane = %+v, want defer", quiet)
	}
	if quiet.Decision.Reason != "budget:quiet_hours" {
		t.Fatalf("reason = %q, want budget:quiet_hours", quiet.Decision.Reason)
	}

	critical, _ := engine.Observe(context.Background(), "bwe-tradfi",
		newsMessage("2", "bwe-tradfi", "Emergency rate cut announced", "emergency meeting called", "https://example.com/emergency", now.Add(time.Minute)))
	if critical == nil || critical.Decision.Lane != LaneInstant {
		t.Fatalf("critical lane = %+v, want instant", critical)
	}
}

func TestDecisionsSurviveReload(t *testing.T) {
	now := testClock()
	engine, store := newTestEngine(t, ModeActive, now)
	obs, _ := engine.Observe(context.Background(), "bwe-tradfi",
		newsMessage("1", "bwe-tradfi", "Fed cuts rates by 25bp", "rate decision", "https://example.com/a", now))
	if obs == nil {
		t.Fatal("observe returned nil")
	}

	cfg := DefaultConfig()
	cfg.Mode = ModeActive
	cfg.Location = "Asia/Shanghai"
	reloaded, err := NewEngine(cfg, DefaultRules(), store)
	if err != nil {
		t.Fatal(err)
	}
	reloaded.SetClock(func() time.Time { return now })
	reloaded.load()

	items := reloaded.EventsBetween(now.Add(-time.Hour), now.Add(time.Hour), 10)
	if len(items) != 1 || items[0].StoryID != obs.Story.ID {
		t.Fatalf("reloaded events = %+v", items)
	}
}

func TestContributionsAggregatesDuplicatesAndSignals(t *testing.T) {
	now := testClock()
	engine, _ := newTestEngine(t, ModeActive, now)

	_, _ = engine.Observe(context.Background(), "bwe-tradfi",
		newsMessage("1", "bwe-tradfi", "Fed cuts rates by 25bp", "rate decision", "https://example.com/a", now))
	_, _ = engine.Observe(context.Background(), "forexlive-breaking",
		newsMessage("2", "forexlive-breaking", "Fed cuts rates by 25bp", "rate decision", "https://example.com/a", now.Add(time.Minute)))
	_, _ = engine.Observe(context.Background(), "bwe-tradfi",
		newsMessage("3", "bwe-tradfi", "Treasury announces new bond sale", "debt management", "https://example.com/b", now.Add(2*time.Minute)))

	reports := engine.Contributions(7)
	if len(reports) != 2 {
		t.Fatalf("reports = %d, want 2: %+v", len(reports), reports)
	}
	bySource := map[string]Contribution{}
	for _, report := range reports {
		bySource[report.Source] = report
	}
	bwe := bySource["bwe-tradfi"]
	if bwe.Items != 2 || bwe.NewStories != 2 {
		t.Fatalf("bwe stat = %+v", bwe)
	}
	forex := bySource["forexlive-breaking"]
	if forex.Items != 1 || forex.Duplicates != 1 || forex.RedundancyRate != 1 {
		t.Fatalf("forexlive stat = %+v", forex)
	}
	if bwe.FreshnessMedianS != 120 {
		t.Fatalf("freshness median = %d, want 120", bwe.FreshnessMedianS)
	}
}

func TestIdentityHelpers(t *testing.T) {
	if got := CanonicalURL("https://Example.com/a/?utm_source=x&ref=y#top"); got != "https://example.com/a" {
		t.Fatalf("canonical = %q", got)
	}
	if got := TitleCore("Breaking: 美联储降息 25 个基点 🕒 10:00"); got != "美联储降息 25 个基点" {
		t.Fatalf("title core = %q", got)
	}
	if Jaccard(TitleTokens("fed cuts rates"), TitleTokens("fed cuts rates")) != 1 {
		t.Fatal("identical titles should have jaccard 1")
	}
}
