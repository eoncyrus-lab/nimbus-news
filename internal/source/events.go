package source

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Ricaardo/nimbus-os/news/internal/event"
	"github.com/Ricaardo/nimbus-os/news/internal/model"
)

// EventReader 是事件模型的只读视图，由 core 在构造源之后注入。
type EventReader interface {
	Active() bool
	EventsBetween(from, to time.Time, limit int) []event.EventItem
}

// EventReaderSetter 由需要事件版次的源实现（session 会向成员转发）。
type EventReaderSetter interface {
	SetEventReader(reader EventReader)
}

func init() {
	Register("events", NewEventsSource)
}

// EventsSource 把事件引擎里进入 instant/defer 的 Story 渲染成版次段落。
// 只读；mode=shadow 时 Active()=false，不改变推送。
type EventsSource struct {
	name     string
	window   time.Duration
	limit    int
	minScore float64
	reader   EventReader
}

func NewEventsSource(cfg Config) (Source, error) {
	s := &EventsSource{name: cfg.Name, window: 12 * time.Hour, limit: 8}
	if v := optionInt(cfg.Options, "window_hours"); v > 0 {
		s.window = time.Duration(v) * time.Hour
	}
	if v := optionInt(cfg.Options, "limit"); v > 0 {
		s.limit = v
	}
	if v, ok := cfg.Options["min_score"].(float64); ok {
		s.minScore = v
	}
	return s, nil
}

func (s *EventsSource) Name() string { return s.name }
func (s *EventsSource) Type() string { return "events" }

func (s *EventsSource) SetEventReader(reader EventReader) { s.reader = reader }

func (s *EventsSource) Fetch() ([]*model.Message, error) {
	return s.FetchContext(context.Background())
}

func (s *EventsSource) FetchContext(_ context.Context) ([]*model.Message, error) {
	if s.reader == nil || !s.reader.Active() {
		return nil, nil
	}
	now := time.Now()
	items := s.reader.EventsBetween(now.Add(-s.window), now, s.limit*3)
	if len(items) == 0 {
		return nil, nil
	}
	var lines []string
	for _, item := range items {
		if item.Score < s.minScore {
			continue
		}
		lines = append(lines, renderEventItem(item))
		if len(lines) >= s.limit {
			break
		}
	}
	if len(lines) == 0 {
		return nil, nil
	}
	label := fmt.Sprintf("事件版次 · 近%s", humanWindow(s.window))
	content := "── " + label + " ──\n" + strings.Join(lines, "\n")
	msg := &model.Message{
		Type:       model.TypeAnalysis,
		ID:         fmt.Sprintf("events_%s_%d", s.name, now.Unix()),
		Title:      label,
		Content:    content,
		Source:     s.name,
		SourceType: "events",
		CreateTime: now,
		FetchTime:  now,
		Tags:       []string{"版次", "事件"},
	}
	msg.SetMetadata("display_source", "事件版次")
	return []*model.Message{msg}, nil
}

func renderEventItem(item event.EventItem) string {
	icon := "·"
	if item.Lane == event.LaneInstant {
		icon = "⚡"
	}
	source := item.Source
	if len(item.Sources) > 1 {
		source = fmt.Sprintf("%s +%d", source, len(item.Sources)-1)
	}
	return fmt.Sprintf("%s [%.0f] %s · %s（%s）", icon, item.Score, classLabel(item.Class), item.Title, source)
}

func classLabel(class string) string {
	switch class {
	case event.ClassCentralBank:
		return "央行"
	case event.ClassDataSurprise:
		return "数据"
	case event.ClassGeopolitics:
		return "地缘"
	case event.ClassSystemicCo:
		return "公司"
	case event.ClassMarketRegime:
		return "市场"
	case event.ClassAnalysis:
		return "分析"
	case event.ClassDomesticPolit:
		return "政治"
	default:
		return "其他"
	}
}

func humanWindow(window time.Duration) string {
	if window >= 7*24*time.Hour && window%(7*24*time.Hour) == 0 {
		return fmt.Sprintf("%d天", int(window/(24*time.Hour)))
	}
	if window >= time.Hour && window%time.Hour == 0 {
		return fmt.Sprintf("%d小时", int(window/time.Hour))
	}
	return window.String()
}
