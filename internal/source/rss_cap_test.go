package source

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 源侧条数上限不能卡在 10 条：老 feed 前 10 条往往全是已推送过的旧闻，
// 下游去重会把它们全丢掉，导致第 11 条起的新条目永远推不出去。
func TestRSSFetchDoesNotCapAtTenBeforeDedupe(t *testing.T) {
	var items strings.Builder
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&items, "<item><title>Story %d</title><link>https://example.com/%d</link><pubDate>%s</pubDate></item>",
			i, i, base.Add(-time.Duration(i)*time.Minute).Format(time.RFC1123Z))
	}
	feed := `<?xml version="1.0"?><rss><channel>` + items.String() + `</channel></rss>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(feed))
	}))
	defer srv.Close()

	src := &RSSSource{name: "test-rss", url: srv.URL, maxAge: 24 * time.Hour}
	msgs, err := src.Fetch()
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 20 {
		t.Fatalf("messages=%d, want 20 (11th+ must not be dropped)", len(msgs))
	}
}

// 超过硬上限时保留最新的 N 条（按发布时间倒序），而不是 feed 里的原始顺序。
func TestRSSFetchKeepsNewestWhenOverHardCap(t *testing.T) {
	var items strings.Builder
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < maxRSSItemsPerFetch+10; i++ {
		// 原始顺序为从旧到新，最新的一条在最后。
		fmt.Fprintf(&items, "<item><title>Story %d</title><link>https://example.com/%d</link><pubDate>%s</pubDate></item>",
			i, i, base.Add(-time.Duration(maxRSSItemsPerFetch+10-i)*time.Minute).Format(time.RFC1123Z))
	}
	feed := `<?xml version="1.0"?><rss><channel>` + items.String() + `</channel></rss>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(feed))
	}))
	defer srv.Close()

	src := &RSSSource{name: "test-rss", url: srv.URL, maxAge: 24 * time.Hour}
	msgs, err := src.Fetch()
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != maxRSSItemsPerFetch {
		t.Fatalf("messages=%d, want %d", len(msgs), maxRSSItemsPerFetch)
	}
	if msgs[0].Title != fmt.Sprintf("Story %d", maxRSSItemsPerFetch+9) {
		t.Fatalf("first kept item=%q, want the newest", msgs[0].Title)
	}
}
