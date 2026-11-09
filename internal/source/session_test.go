package source

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Ricaardo/nimbus-os/news/internal/model"
)

type fakeSessionMember struct {
	messages []*model.Message
	err      error
}

func (f *fakeSessionMember) Name() string { return "fake-member" }
func (f *fakeSessionMember) Type() string { return "fake" }
func (f *fakeSessionMember) Fetch() ([]*model.Message, error) {
	return f.messages, f.err
}

func TestSessionSourceComposesMembers(t *testing.T) {
	header := &model.Message{
		ID: "h", Title: "🌅 盘前全景", Content: "── 行情 ──\nSPY +1.2%",
	}
	leased := &model.Message{
		ID: "d", Content: "## 资讯摘要\n- 中国 CPI 0.7%",
	}
	leased.SetMetadata("digest_lease_id", "lease-1")
	leased.SetMetadata("digest_briefing", "news_aggregate")
	scripted := &model.Message{ID: "e", Title: "📅 今日数据", Content: "21:00 美国 CPI"}

	s := &SessionSource{
		name:    "session-morning",
		session: sessionMorning,
		title:   "🌅 盘前全景",
		force:   true,
		members: []sessionMember{
			{label: "行情", src: &fakeSessionMember{messages: []*model.Message{header}}, typ: "market-briefing"},
			{label: "摘要", src: &fakeSessionMember{messages: []*model.Message{leased}}, typ: "market-briefing"},
			{label: "数据", src: &fakeSessionMember{messages: []*model.Message{scripted}}, typ: "script"},
			{label: "失败", src: &fakeSessionMember{err: errors.New("boom")}, typ: "market-briefing"},
		},
	}
	messages, err := s.Fetch()
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages=%d, want 1 composed message", len(messages))
	}
	msg := messages[0]
	if !strings.HasPrefix(msg.Title, "🌅 盘前全景 · ") {
		t.Fatalf("title=%q", msg.Title)
	}
	for _, want := range []string{"SPY +1.2%", "中国 CPI 0.7%", "── 📅 今日数据 ──", "21:00 美国 CPI"} {
		if !strings.Contains(msg.Content, want) {
			t.Fatalf("content missing %q:\n%s", want, msg.Content)
		}
	}
	if got := msg.GetStringMetadata("digest_lease_id"); got != "lease-1" {
		t.Fatalf("digest lease not propagated: %q", got)
	}
	if got := msg.GetStringMetadata("digest_briefing"); got != "news_aggregate" {
		t.Fatalf("digest briefing not propagated: %q", got)
	}
	if !strings.HasPrefix(msg.ID, "session_morning_") {
		t.Fatalf("id=%q", msg.ID)
	}
}

func TestSessionSourceReturnsNilWhenAllMembersEmpty(t *testing.T) {
	s := &SessionSource{
		name: "session-close", session: sessionClose, force: true,
		members: []sessionMember{{label: "空", src: &fakeSessionMember{}, typ: "market-briefing"}},
	}
	messages, err := s.Fetch()
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 0 {
		t.Fatalf("expected no message, got %+v", messages)
	}
}

func TestSessionSourceRejectsUnknownSessionAndEmptyMembers(t *testing.T) {
	if _, err := NewSessionSource(Config{Name: "x", Options: map[string]interface{}{"session": "noon"}}); err == nil {
		t.Fatal("unknown session accepted")
	}
	if _, err := NewSessionSource(Config{Name: "x", Options: map[string]interface{}{"session": "morning"}}); err == nil {
		t.Fatal("empty members accepted")
	}
}

func TestSessionSourceBuildsMembersFromConfig(t *testing.T) {
	Register("session-test-member", func(cfg Config) (Source, error) {
		return &fakeSessionMember{messages: []*model.Message{{ID: cfg.Name, Content: "ok"}}}, nil
	})
	src, err := NewSessionSource(Config{
		Name: "session-morning",
		Options: map[string]interface{}{
			"session": "morning",
			"force":   true,
			"members": []interface{}{
				map[string]interface{}{"label": "行情", "type": "session-test-member"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	session, ok := src.(*SessionSource)
	if !ok {
		t.Fatalf("type=%T", src)
	}
	if len(session.members) != 1 || session.members[0].label != "行情" {
		t.Fatalf("members=%+v", session.members)
	}
	if session.title != "🌅 盘前全景" {
		t.Fatalf("default title=%q", session.title)
	}
	messages, err := src.(ContextFetcher).FetchContext(context.Background())
	if err != nil || len(messages) != 1 {
		t.Fatalf("messages=%d err=%v", len(messages), err)
	}
}

func TestSessionSourceSplitsLongBriefing(t *testing.T) {
	long := strings.Repeat("消息内容很长。", 200) // 1200 字符
	leased := &model.Message{ID: "d", Content: long}
	leased.SetMetadata("digest_lease_id", "lease-9")
	leased.SetMetadata("digest_briefing", "news_aggregate")
	image := &model.Message{ID: "i", Content: long, ImageURL: "https://x/y.png"}

	s := &SessionSource{
		name: "session-close", session: sessionClose, title: "📊 A股收盘", force: true, maxChars: 600,
		members: []sessionMember{
			{label: "摘要", src: &fakeSessionMember{messages: []*model.Message{leased}}, typ: "market-briefing"},
			{label: "图", src: &fakeSessionMember{messages: []*model.Message{image}}, typ: "market-briefing"},
		},
	}
	messages, err := s.Fetch()
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) < 2 {
		t.Fatalf("messages=%d, want at least 2 segments", len(messages))
	}
	total := 0
	leaseSegment := -1
	for i, msg := range messages {
		if len([]rune(msg.Content)) > 600 {
			t.Fatalf("segment %d has %d runes, over max_chars", i, len([]rune(msg.Content)))
		}
		total += len([]rune(msg.Content))
		if !strings.Contains(msg.Title, "(") && !strings.Contains(msg.Title, "（") {
			t.Fatalf("segmented title missing index: %q", msg.Title)
		}
		if msg.GetStringMetadata("digest_lease_id") != "" {
			if leaseSegment != -1 {
				t.Fatalf("digest lease attached to more than one segment")
			}
			leaseSegment = i
		}
		if msg.ImageURL != "" && i != 0 {
			t.Fatalf("image attached to segment %d, want first only", i)
		}
	}
	if leaseSegment < 0 {
		t.Fatal("digest lease lost while splitting")
	}
	if total < len([]rune(long)) {
		t.Fatalf("total runes=%d, content lost (source %d)", total, len([]rune(long)))
	}
	// 分段消息 ID 必须唯一，否则下游去重会把后一段当成重复吞掉。
	seen := map[string]bool{}
	for _, msg := range messages {
		if seen[msg.ID] {
			t.Fatalf("duplicate message id %q", msg.ID)
		}
		seen[msg.ID] = true
	}
}

func TestPackSessionPartsKeepsSingleShortBriefing(t *testing.T) {
	chunks := packSessionParts([]sessionPart{{text: "一句话"}, {text: "第二段"}}, sessionDefaultMaxChars)
	if len(chunks) != 1 {
		t.Fatalf("chunks=%d, want 1", len(chunks))
	}
	if chunks[0].text != "一句话\n\n第二段" {
		t.Fatalf("text=%q", chunks[0].text)
	}
}

func TestPanicLevelThresholds(t *testing.T) {
	cases := map[int]string{0: "平静", 24: "平静", 25: "正常", 49: "正常", 50: "紧张", 74: "紧张", 75: "恐慌", 100: "恐慌"}
	for score, want := range cases {
		if got := panicLevel(score); got != want {
			t.Fatalf("panicLevel(%d)=%q want %q", score, got, want)
		}
	}
}

func TestSessionSourceDegradesMemberCreateError(t *testing.T) {
	Register("session-test-ok", func(cfg Config) (Source, error) {
		return &fakeSessionMember{messages: []*model.Message{{ID: "ok", Content: "正常段"}}}, nil
	})
	src, err := NewSessionSource(Config{
		Name: "session-morning",
		Options: map[string]interface{}{
			"session": "morning",
			"force":   true,
			"members": []interface{}{
				map[string]interface{}{"label": "正常", "type": "session-test-ok"},
				map[string]interface{}{"label": "缺密钥", "type": "us-macro-report"},
			},
		},
	})
	if err != nil {
		t.Fatalf("member create error should not fail session: %v", err)
	}
	messages, err := src.(ContextFetcher).FetchContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages=%d, want 1", len(messages))
	}
	if !strings.Contains(messages[0].Content, "正常段") {
		t.Fatalf("working member dropped: %s", messages[0].Content)
	}
	if !strings.Contains(messages[0].Content, "数据源异常：缺密钥") {
		t.Fatalf("missing degradation note: %s", messages[0].Content)
	}
}
