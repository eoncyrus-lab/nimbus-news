package source

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/news/internal/digest"
	"github.com/Ricaardo/nimbus-os/news/internal/model"
	"github.com/Ricaardo/nimbus-os/news/internal/store"
)

type briefingDigestStore struct {
	target digest.Briefing
	lease  *digest.Lease
}

type topicBriefingDigestStore struct {
	*briefingDigestStore
	topicTarget digest.Briefing
	topicLimit  int
	topicLease  *digest.Lease
}

func (*briefingDigestStore) Enqueue(context.Context, string, digest.Briefing, *model.Message) error {
	return nil
}
func (s *briefingDigestStore) Lease(_ context.Context, target digest.Briefing, _ int, _ time.Duration) (*digest.Lease, error) {
	s.target = target
	return s.lease, nil
}
func (*briefingDigestStore) Ack(context.Context, string) error { return nil }

func (s *topicBriefingDigestStore) LeaseTopics(_ context.Context, target digest.Briefing, limit int, _ time.Duration) (*digest.Lease, error) {
	s.topicTarget = target
	s.topicLimit = limit
	return s.topicLease, nil
}

func TestMarketBriefingAppendsTargetedDigestMetadata(t *testing.T) {
	ds := &briefingDigestStore{lease: &digest.Lease{
		ID:       "lease-1",
		Briefing: digest.USPreview,
		Items: []digest.Item{{
			ID: "item", Source: "wire",
			Message: &model.Message{Title: "Fed signals patience"},
		}},
	}}
	src := &MarketBriefingSource{briefingType: "us_preview", digestStore: ds}
	msg := &model.Message{Content: "base"}
	got, err := src.appendDigest(context.Background(), []*model.Message{msg})
	if err != nil {
		t.Fatal(err)
	}
	if ds.target != digest.USPreview {
		t.Fatalf("leased target=%s", ds.target)
	}
	if len(got) != 1 || got[0].GetStringMetadata("digest_lease_id") != "lease-1" {
		t.Fatalf("missing lease metadata: %+v", got)
	}
	if !strings.Contains(got[0].Content, "## 资讯摘要") || !strings.Contains(got[0].Content, "Fed signals patience") {
		t.Fatalf("digest not rendered: %s", got[0].Content)
	}
}

func TestMarketBriefingLeasesAndRendersTwelveTopics(t *testing.T) {
	topics := make([]digest.Topic, 0, digestBriefingItemLimit)
	for i := 0; i < digestBriefingItemLimit; i++ {
		topic := digest.Topic{
			Key: fmt.Sprintf("topic-%02d", i),
			Items: []digest.Item{{
				ID: fmt.Sprintf("item-%02d", i), Source: "wire-a",
				Message: &model.Message{Title: fmt.Sprintf("Topic %02d", i)},
			}},
		}
		if i == 0 {
			topic.Items = append(topic.Items, digest.Item{
				ID: "item-00-b", Source: "wire-b",
				Message: &model.Message{Title: "Duplicate member must not add a line"},
			})
		}
		topics = append(topics, topic)
	}
	ds := &topicBriefingDigestStore{
		briefingDigestStore: &briefingDigestStore{},
		topicLease: &digest.Lease{
			ID: "topic-lease", Briefing: digest.USPreview, Topics: topics,
		},
	}
	src := &MarketBriefingSource{briefingType: "us_preview", digestStore: ds}
	got, err := src.appendDigest(context.Background(), []*model.Message{{Content: "base"}})
	if err != nil {
		t.Fatal(err)
	}
	if ds.topicTarget != digest.USPreview || ds.topicLimit != digestBriefingItemLimit {
		t.Fatalf("topic lease target=%s limit=%d", ds.topicTarget, ds.topicLimit)
	}
	if ds.target != "" {
		t.Fatalf("legacy Lease called for TopicStore: target=%s", ds.target)
	}
	if strings.Count(got[0].Content, "\n- ") != digestBriefingItemLimit {
		t.Fatalf("rendered topic lines=%d, want %d:\n%s",
			strings.Count(got[0].Content, "\n- "), digestBriefingItemLimit, got[0].Content)
	}
	if strings.Contains(got[0].Content, "Duplicate member must not add a line") {
		t.Fatalf("multi-member topic rendered more than one line:\n%s", got[0].Content)
	}
	if !strings.Contains(got[0].Content, "wire-a、wire-b") {
		t.Fatalf("multi-member topic attribution missing:\n%s", got[0].Content)
	}
	if got[0].GetStringMetadata("digest_lease_id") != "topic-lease" {
		t.Fatalf("topic lease metadata missing: %+v", got[0])
	}
}

func TestMarketBriefingEmptyDigestLeavesBaseUntouched(t *testing.T) {
	ds := &briefingDigestStore{}
	src := &MarketBriefingSource{briefingType: "closing", digestStore: ds}
	msg := &model.Message{Content: "base"}
	got, err := src.appendDigest(context.Background(), []*model.Message{msg})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Content != "base" || got[0].GetStringMetadata("digest_lease_id") != "" {
		t.Fatalf("empty digest changed briefing: %+v", got)
	}
}

func TestMarketBriefingDigestOverflowRemainsPending(t *testing.T) {
	owner, err := store.NewBoltStore(filepath.Join(t.TempDir(), "digest.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	ds, err := store.NewDigestStore(owner.DB())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < digestBriefingItemLimit+3; i++ {
		msg := &model.Message{ID: fmt.Sprintf("item-%02d", i), Title: fmt.Sprintf("News %02d", i)}
		if err := ds.Enqueue(context.Background(), "wire", digest.USPreview, msg); err != nil {
			t.Fatal(err)
		}
	}
	src := &MarketBriefingSource{briefingType: "us_preview", digestStore: ds}
	base := &model.Message{Content: "base"}
	got, err := src.appendDigest(context.Background(), []*model.Message{base})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].GetStringMetadata("digest_lease_id") == "" {
		t.Fatal("first digest lease metadata missing")
	}
	next, err := ds.Lease(context.Background(), digest.USPreview, digestBriefingItemLimit, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if next == nil || len(next.Items) != 3 {
		t.Fatalf("overflow lease=%+v, want 3 pending items", next)
	}
}

// TestAggregateLeasesNewsAggregateBucket 验证聚合源:
// 租 news_aggregate 桶 + 打 digest_lease_id(走 durable digest 投递)。
// 脚本目录设为不存在路径 → blockbeats 快速失败,摘要仍正常产出。
func TestAggregateLeasesNewsAggregateBucket(t *testing.T) {
	ds := &topicBriefingDigestStore{topicLease: &digest.Lease{
		ID: "lease-agg-1",
		Topics: []digest.Topic{{
			Key:      "topic-1",
			Priority: 10,
			Items: []digest.Item{{
				Source:  "bloomberg-markets",
				Message: &model.Message{Title: "美联储官员讲话摘要"},
			}},
		}},
	}}
	src := &MarketBriefingSource{
		name:         "news-aggregate-evening",
		briefingType: "aggregate",
		digestStore:  ds,
	}
	messages, err := src.Fetch()
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	msg := messages[0]
	if ds.topicTarget != digest.NewsAggregate {
		t.Fatalf("leased bucket = %q, want %q", ds.topicTarget, digest.NewsAggregate)
	}
	if msg.GetStringMetadata("digest_lease_id") != "lease-agg-1" {
		t.Fatalf("digest_lease_id = %q", msg.GetStringMetadata("digest_lease_id"))
	}
	if !strings.Contains(msg.Content, "美联储官员讲话摘要") {
		t.Fatalf("digest summary missing from content: %s", msg.Content)
	}
	if strings.Contains(msg.Content, "BlockBeats") || strings.Contains(msg.Content, "BTC") {
		t.Fatalf("script failure content leaked into message: %s", msg.Content[:min(len(msg.Content), 200)])
	}
}

// TestAggregateEmptyEverythingReturnsNoMessages 聚合源无摘要且脚本失败 → 不产出。
func TestAggregateEmptyEverythingReturnsNoMessages(t *testing.T) {
	src := &MarketBriefingSource{
		name:         "news-aggregate-morning",
		briefingType: "aggregate",
		digestStore:  &topicBriefingDigestStore{}, // 空租约
	}
	messages, err := src.Fetch()
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("messages = %d, want 0", len(messages))
	}
}

// TestPreMarketSessionMapping verifies the intraday phases map correctly
// for Asia/Shanghai timezone boundary hours.
func TestPreMarketSessionMapping(t *testing.T) {
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	cases := []struct {
		hour, min int
		want      string
	}{
		{9, 0, "pre_market"},
		{9, 14, "pre_market"},
		{9, 15, "auction"},
		{9, 25, "auction"},
		{9, 30, "open"},
		{9, 59, "open"},
		{10, 0, "morning_session"},
		{11, 29, "morning_session"},
		{11, 30, "midday"},
		{11, 59, "midday"},
		{12, 59, "midday"},
		{13, 0, "afternoon_open"},
		{13, 59, "afternoon_open"},
		{14, 0, "late_session"},
		{14, 59, "late_session"},
		{15, 0, "afternoon_close"},
		{15, 5, "afternoon_close"},
		{15, 29, "afternoon_close"},
		{15, 30, "after_hours"},
		{16, 10, "after_hours"},
	}
	for _, c := range cases {
		ts := time.Date(2026, 8, 3, c.hour, c.min, 0, 0, shanghai)
		got := preMarketSession(ts)
		if got != c.want {
			t.Errorf("preMarketSession(%02d:%02d Asia/Shanghai) = %q, want %q", c.hour, c.min, got, c.want)
		}
	}
}
