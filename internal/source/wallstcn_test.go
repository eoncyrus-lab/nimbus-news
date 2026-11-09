package source

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/news/internal/model"
)

func TestWallStreetCNParsesChannelsAndFilters(t *testing.T) {
	published := time.Now().Add(-30 * time.Minute).Unix()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("channel"); got != "hk-stock-channel" && got != "a-stock-channel" {
			t.Errorf("unexpected channel %q", got)
		}
		_, _ = w.Write([]byte(`{"code":20000,"message":"OK","data":{"items":[
			{"id":1,"title":"中国10月CPI同比 0.7%","content_text":"中国10月CPI同比增0.7%，前值0.9%。","display_time":` + wscnTestInt(published) + `,"uri":"https://wallstreetcn.com/livenews/1","score":3},
			{"id":2,"title":"","content_text":"<p>央行开展<b>7天</b>逆回购操作&nbsp;净投放1000亿元</p>","display_time":` + wscnTestInt(published) + `,"uri":"https://wallstreetcn.com/livenews/2","score":2},
			{"id":3,"title":"常规个股异动","content_text":"某股涨停","display_time":` + wscnTestInt(published) + `,"uri":"https://wallstreetcn.com/livenews/3","score":1},
			{"id":4,"title":"提醒：日内请重点关注","content_text":"① 09:30 中国CPI","display_time":` + wscnTestInt(published) + `,"uri":"https://wallstreetcn.com/livenews/4","score":3},
			{"id":1,"title":"中国10月CPI同比 0.7%","content_text":"重复条目","display_time":` + wscnTestInt(published) + `,"uri":"https://wallstreetcn.com/livenews/1","score":3}
		]}}`))
	}))
	defer server.Close()

	src, err := NewWallStreetCNSource(Config{
		Name: "wallstcn-hk",
		Type: "wallstcn",
		URL:  server.URL,
		Options: map[string]interface{}{
			"channels":         []interface{}{"hk-stock-channel", "a-stock-channel"},
			"min_score":        2,
			"exclude_keywords": []interface{}{"提醒：日内请重点关注"},
			"max_age_hours":    6,
			"limit":            30,
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
		t.Fatalf("messages=%d, want 2 (score filter + dedupe + exclude): %+v", len(messages), messages)
	}
	byScore := make(map[int]*model.Message, len(messages))
	for _, msg := range messages {
		score, _ := msg.Metadata["wscn_score"].(int)
		byScore[score] = msg
	}
	cpi := byScore[3]
	if cpi == nil || cpi.Title != "中国10月CPI同比 0.7%" || cpi.Link != "https://wallstreetcn.com/livenews/1" {
		t.Fatalf("unexpected CPI message: %+v", cpi)
	}
	pboc := byScore[2]
	if pboc == nil {
		t.Fatalf("missing score-2 message: %+v", messages)
	}
	if pboc.Title != "央行开展7天逆回购操作 净投放1000亿元" {
		t.Fatalf("html strip/title fallback failed: %q", pboc.Title)
	}
	if strings.Contains(pboc.Content, "<") || strings.Contains(pboc.Content, "&nbsp;") {
		t.Fatalf("content not cleaned: %q", pboc.Content)
	}
	if pboc.SourceType != "wallstcn" {
		t.Fatalf("source type=%q", pboc.SourceType)
	}
}

func TestWallStreetCNRejectsEmptyChannels(t *testing.T) {
	if _, err := NewWallStreetCNSource(Config{Name: "x", Type: "wallstcn", Options: map[string]interface{}{
		"channels": []interface{}{},
	}}); err == nil {
		t.Fatal("empty channels accepted")
	}
}

func wscnTestInt(value int64) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	pos := len(digits)
	for value > 0 {
		pos--
		digits[pos] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[pos:])
}

func TestWallStreetCNIncludeKeywords(t *testing.T) {
	published := time.Now().Add(-10 * time.Minute).Unix()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":20000,"message":"OK","data":{"items":[
			{"id":11,"title":"某股涨停","content_text":"个股异动","display_time":` + wscnTestInt(published) + `,"uri":"https://wallstreetcn.com/livenews/11","score":2},
			{"id":12,"title":"证监会就减持规则答记者问","content_text":"监管表态","display_time":` + wscnTestInt(published) + `,"uri":"https://wallstreetcn.com/livenews/12","score":2}
		]}}`))
	}))
	defer server.Close()

	src, err := NewWallStreetCNSource(Config{
		Name: "wallstcn-a", Type: "wallstcn", URL: server.URL,
		Options: map[string]interface{}{
			"channels":         []interface{}{"a-stock-channel"},
			"min_score":        1,
			"include_keywords": []interface{}{"证监会", "国务院"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	messages, err := src.Fetch()
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].ID == "" || !strings.Contains(messages[0].Title, "证监会") {
		t.Fatalf("include filter failed: %+v", messages)
	}
}

func TestWallStreetCNTagsDeduplicated(t *testing.T) {
	src, err := NewWallStreetCNSource(Config{
		Name: "wallstcn-hk",
		Options: map[string]interface{}{
			"tags": []interface{}{"中国", "宏观", "自定义"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	tags := src.(*WallStreetCNSource).tags
	seen := map[string]int{}
	for _, tag := range tags {
		seen[tag]++
	}
	for tag, n := range seen {
		if n > 1 {
			t.Fatalf("tag %q duplicated %d times: %v", tag, n, tags)
		}
	}
	if seen["自定义"] != 1 {
		t.Fatalf("custom tag missing: %v", tags)
	}
}
