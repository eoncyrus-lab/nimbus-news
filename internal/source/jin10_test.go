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
