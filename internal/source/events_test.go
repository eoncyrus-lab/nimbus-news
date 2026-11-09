package source

import (
	"strings"
	"testing"
	"time"

	"github.com/Ricaardo/nimbus-os/news/internal/event"
)

type stubEventReader struct {
	active bool
	items  []event.EventItem
}

func (s stubEventReader) Active() bool { return s.active }
func (s stubEventReader) EventsBetween(time.Time, time.Time, int) []event.EventItem {
	return s.items
}

func TestEventsSourceRendersEdition(t *testing.T) {
	src, err := NewEventsSource(Config{
		Name: "events",
		Options: map[string]interface{}{
			"window_hours": 12,
			"limit":        5,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	eventsSrc := src.(*EventsSource)
	eventsSrc.SetEventReader(stubEventReader{active: true, items: []event.EventItem{
		{Lane: event.LaneInstant, Score: 92, Class: event.ClassCentralBank, Title: "Fed cuts rates", Source: "bwe-tradfi", Sources: []string{"bwe-tradfi", "forexlive-breaking"}},
		{Lane: event.LaneDefer, Score: 55, Class: event.ClassDataSurprise, Title: "China CPI preview", Source: "wallstcn-hk", Sources: []string{"wallstcn-hk"}},
	}})

	messages, err := eventsSrc.Fetch()
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	content := messages[0].Content
	for _, want := range []string{"── 事件版次 · 近12小时 ──", "⚡ [92] 央行 · Fed cuts rates（bwe-tradfi +1）", "· [55] 数据 · China CPI preview（wallstcn-hk）"} {
		if !strings.Contains(content, want) {
			t.Fatalf("content missing %q:\n%s", want, content)
		}
	}
}

func TestEventsSourceInactiveReturnsNothing(t *testing.T) {
	src, err := NewEventsSource(Config{Name: "events"})
	if err != nil {
		t.Fatal(err)
	}
	src.(*EventsSource).SetEventReader(stubEventReader{active: false, items: []event.EventItem{
		{Lane: event.LaneInstant, Score: 90, Title: "x"},
	}})
	messages, err := src.Fetch()
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 0 {
		t.Fatalf("shadow/off mode should not render events, got %d", len(messages))
	}
}
