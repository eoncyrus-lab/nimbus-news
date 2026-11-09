package source

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func jin10FlashItem(id string, important int, when time.Time, content string) jin10Flash {
	item := jin10Flash{ID: id, Important: important, Time: when.Format("2006-01-02 15:04:05")}
	item.Data.Content = content
	return item
}

func TestJin10FetchFiltersAndExtractsTitles(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().In(loc)

	page1 := jin10Response{Status: 200, Data: []jin10Flash{
		jin10FlashItem("1", 1, now.Add(-1*time.Minute),
			"【央行开展5000亿元逆回购操作】金十数据11月9日讯，中标利率1.50%。"),
		jin10FlashItem("2", 0, now.Add(-2*time.Minute),
			"【丰原药业：获得药品注册证书】金十数据11月9日讯，丰原药业公告，公司收到药品注册证书。"),
	}}
	page2 := jin10Response{Status: 200, Data: []jin10Flash{
		jin10FlashItem("3", 0, now.Add(-3*time.Minute),
			"美联储戴利：目前不应急于下调关键利率，需要更多通胀回落证据。"),
	}}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-app-id") == "" {
			http.Error(w, "missing app id", http.StatusForbidden)
			return
		}
		payload := page1
		if r.URL.Query().Get("max_time") != "" {
			payload = page2
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer server.Close()

	src, err := NewJin10Source(Config{
		Name: "jin10",
		URL:  server.URL,
		Options: map[string]interface{}{
			"max_age_hours": 6,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	messages, err := src.Fetch()
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		titles := make([]string, 0, len(messages))
		for _, msg := range messages {
			titles = append(titles, msg.Title)
		}
		t.Fatalf("expected 2 messages (important + macro keyword, announcement dropped), got %d: %v", len(messages), titles)
	}

	titles := map[string]bool{}
	for _, msg := range messages {
		titles[msg.Title] = true
		if msg.GetStringMetadata("display_source") != "金十数据" {
			t.Fatalf("display_source = %q", msg.GetStringMetadata("display_source"))
		}
		if !strings.HasPrefix(msg.Link, "https://flash.jin10.com/detail/") {
			t.Fatalf("unexpected link %q", msg.Link)
		}
	}
	if !titles["央行开展5000亿元逆回购操作"] {
		t.Fatalf("bracket title not extracted: %v", titles)
	}
	if titles["丰原药业：获得药品注册证书"] {
		t.Fatalf("stock announcement should be filtered: %v", titles)
	}
	if !titles["美联储戴利：目前不应急于下调关键利率，需要更多通胀回落证据"] {
		t.Fatalf("first-sentence title not extracted: %v", titles)
	}
}

func TestJin10NoiseDailySummary(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	src, err := NewJin10Source(Config{Name: "jin10", Options: map[string]interface{}{
		"noise_summary_time": "08:30",
	}})
	if err != nil {
		t.Fatal(err)
	}
	s := src.(*Jin10Source)

	day := time.Date(2026, 11, 8, 16, 0, 0, 0, loc)
	s.recordNoise("n1", day, "【某公司：股东拟减持不超过2%】金十数据11月8日讯", "减持", day)
	s.recordNoise("n1", day, "【某公司：股东拟减持不超过2%】金十数据11月8日讯", "减持", day) // 重复 ID
	s.recordNoise("n2", day.Add(time.Hour), "【某公司：限售股解禁】金十数据11月8日讯", "解禁", day)

	// 窗口外不产出。
	if msg := s.takeNoiseSummary(time.Date(2026, 11, 9, 12, 0, 0, 0, loc)); msg != nil {
		t.Fatalf("summary emitted outside window: %+v", msg)
	}

	next := time.Date(2026, 11, 9, 8, 31, 0, 0, loc)
	msg := s.takeNoiseSummary(next)
	if msg == nil {
		t.Fatal("expected daily noise summary")
	}
	if !strings.Contains(msg.Title, "2 条") || !strings.Contains(msg.Content, "减持 1") || !strings.Contains(msg.Content, "解禁 1") {
		t.Fatalf("unexpected summary title=%q content=%q", msg.Title, msg.Content)
	}
	if msg.Type != "analysis" || msg.GetStringMetadata("noise_summary") != "1" {
		t.Fatalf("summary must stay out of realtime news: type=%s metadata=%v", msg.Type, msg.Metadata)
	}
	if msg.Source != "jin10" {
		t.Fatalf("summary should keep the source name, got %q", msg.Source)
	}

	// 同一天第二次拉取不再产出。
	if again := s.takeNoiseSummary(next.Add(2 * time.Minute)); again != nil {
		t.Fatalf("summary emitted twice in one day: %+v", again)
	}

	// 汇总后待处理集合已清空；次日新公告进入下一份汇总。
	if s.noiseCount() != 0 {
		t.Fatalf("noise bucket not cleared: %d", s.noiseCount())
	}
	s.recordNoise("n3", next, "【某公司：业绩预告】金十数据11月9日讯", "业绩预告", next)
	nextDay := time.Date(2026, 11, 10, 8, 31, 0, 0, loc)
	msg = s.takeNoiseSummary(nextDay)
	if msg == nil || !strings.Contains(msg.Content, "业绩预告") {
		t.Fatalf("next-day summary missing: %+v", msg)
	}
}

func TestJin10FetchRecordsExcludedAnnouncements(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().In(loc)
	page := jin10Response{Status: 200, Data: []jin10Flash{
		jin10FlashItem("1", 1, now.Add(-1*time.Minute), "【央行开展5000亿元逆回购操作】金十数据11月9日讯，中标利率1.50%。"),
		jin10FlashItem("2", 0, now.Add(-2*time.Minute), "【丰原药业：获得药品注册证书】金十数据11月9日讯，丰原药业公告，公司收到药品注册证书。"),
	}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(page)
	}))
	defer server.Close()

	src, err := NewJin10Source(Config{Name: "jin10", URL: server.URL, Options: map[string]interface{}{
		"max_age_hours": 6,
		// 把窗口放在当前时刻之外，避免本次抓取就发汇总。
		"noise_summary_time": "03:00",
	}})
	if err != nil {
		t.Fatal(err)
	}
	messages, err := src.Fetch()
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("announcement should stay out of realtime push, got %d messages", len(messages))
	}
	if count := src.(*Jin10Source).noiseCount(); count != 1 {
		t.Fatalf("dropped announcement should be recorded for daily summary, got %d", count)
	}
}
