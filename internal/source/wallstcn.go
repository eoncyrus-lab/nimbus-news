package source

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Ricaardo/nimbus-os/news/internal/model"
)

// WallStreetCN 7x24 快讯。国内宏观/政策/AH 覆盖此前只有英文源，中文一手内容缺位；
// 该源直接消费华尔街见闻的频道 API（公开 JSON），按频道与重要度分级取用。
//
// 频道实测（2026-11-09）：
//   - hk-stock-channel   21 条/日，含中国 CPI/PPI、部委表态、港股监管，噪声低 → 默认
//   - a-stock-channel   178 条/日，盘中个股异动为主，噪声高
//   - global-channel    275 条/日，全量快讯，噪声最高
const (
	defaultWSCNEndpoint = "https://api-one-wscn.awtmt.com/apiv1/content/lives"
	wscnMaxResponseBody = 4 << 20
	wscnRequestTimeout  = 20 * time.Second
)

var wscnTagRE = regexp.MustCompile(`(?s)<[^>]*>`)

func init() {
	Register("wallstcn", NewWallStreetCNSource)
}

type wallstcnItem struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	ContentText string `json:"content_text"`
	ContentMore string `json:"content_more"`
	DisplayTime int64  `json:"display_time"`
	URI         string `json:"uri"`
	Score       int    `json:"score"`
}

type wallstcnResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Items []wallstcnItem `json:"items"`
	} `json:"data"`
}

// WallStreetCNSource 华尔街见闻快讯源
type WallStreetCNSource struct {
	name            string
	endpoint        string
	channels        []string
	limit           int
	minScore        int
	maxAge          time.Duration
	includeKeywords []string
	excludeKeywords []string
	tags            []string
	client          *http.Client
}

// NewWallStreetCNSource 创建华尔街见闻快讯源
func NewWallStreetCNSource(cfg Config) (Source, error) {
	s := &WallStreetCNSource{
		name:     cfg.Name,
		endpoint: cfg.URL,
		channels: []string{"hk-stock-channel"},
		limit:    30,
		minScore: 1,
		maxAge:   6 * time.Hour,
		tags:     []string{"中国", "港股", "宏观"},
		client:   &http.Client{Timeout: wscnRequestTimeout},
	}
	if s.endpoint == "" {
		s.endpoint = defaultWSCNEndpoint
	}
	if raw, ok := cfg.Options["channels"].([]interface{}); ok {
		s.channels = nil
		for _, value := range raw {
			if ch, ok := value.(string); ok && ch != "" {
				s.channels = append(s.channels, ch)
			}
		}
	}
	if len(s.channels) == 0 {
		return nil, fmt.Errorf("wallstcn: channels must not be empty")
	}
	if v, ok := cfg.Options["limit"].(int); ok && v > 0 {
		s.limit = v
	}
	if v, ok := cfg.Options["min_score"].(int); ok && v >= 0 {
		s.minScore = v
	}
	if v, ok := cfg.Options["max_age_hours"].(int); ok && v > 0 {
		s.maxAge = time.Duration(v) * time.Hour
	}
	if raw, ok := cfg.Options["include_keywords"].([]interface{}); ok {
		for _, value := range raw {
			if kw, ok := value.(string); ok && kw != "" {
				s.includeKeywords = append(s.includeKeywords, kw)
			}
		}
	}
	if raw, ok := cfg.Options["exclude_keywords"].([]interface{}); ok {
		for _, value := range raw {
			if kw, ok := value.(string); ok && kw != "" {
				s.excludeKeywords = append(s.excludeKeywords, kw)
			}
		}
	}
	if raw, ok := cfg.Options["tags"].([]interface{}); ok {
		for _, value := range raw {
			if tag, ok := value.(string); ok && tag != "" && !containsString(s.tags, tag) {
				s.tags = append(s.tags, tag)
			}
		}
	}
	return s, nil
}

func (s *WallStreetCNSource) Name() string { return s.name }
func (s *WallStreetCNSource) Type() string { return "wallstcn" }

func (s *WallStreetCNSource) Fetch() ([]*model.Message, error) {
	ctx, cancel := context.WithTimeout(context.Background(), wscnRequestTimeout)
	defer cancel()
	return s.FetchContext(ctx)
}

func (s *WallStreetCNSource) FetchContext(ctx context.Context) ([]*model.Message, error) {
	var (
		mu       sync.Mutex
		messages []*model.Message
		firstErr error
		wg       sync.WaitGroup
	)
	for _, channel := range s.channels {
		wg.Add(1)
		go func(channel string) {
			defer wg.Done()
			items, err := s.fetchChannel(ctx, channel)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				return
			}
			messages = append(messages, items...)
		}(channel)
	}
	wg.Wait()

	// 同一快讯常同时挂在多个频道下（如 hk-stock 与 a-stock 都带中国 CPI），
	// 按 ID 去重，避免同一条消息在两个频道各占一次直推配额。
	deduped := make([]*model.Message, 0, len(messages))
	seen := make(map[string]struct{}, len(messages))
	for _, msg := range messages {
		if _, ok := seen[msg.ID]; ok {
			continue
		}
		seen[msg.ID] = struct{}{}
		deduped = append(deduped, msg)
	}

	// 全部频道失败才算失败；单频道失败由健康检查的连续失败计数兜底。
	if len(deduped) == 0 && firstErr != nil {
		return nil, firstErr
	}
	slog.Info("wallstcn fetched", "source", s.name, "channels", s.channels, "messages", len(deduped))
	return deduped, nil
}

func (s *WallStreetCNSource) fetchChannel(ctx context.Context, channel string) ([]*model.Message, error) {
	url := fmt.Sprintf("%s?channel=%s&limit=%d", s.endpoint, channel, s.limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wallstcn %s: %w", channel, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("wallstcn %s: http %d", channel, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, wscnMaxResponseBody))
	if err != nil {
		return nil, fmt.Errorf("wallstcn %s: read body: %w", channel, err)
	}
	var payload wallstcnResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("wallstcn %s: decode: %w", channel, err)
	}
	// 上游成功码是 20000（不是 HTTP 语义的 200）；0 兼容旧版返回。
	if payload.Code != 20000 && payload.Code != 0 {
		return nil, fmt.Errorf("wallstcn %s: api code %d %s", channel, payload.Code, payload.Message)
	}

	cutoff := time.Now().Add(-s.maxAge)
	seen := make(map[string]struct{}, len(payload.Data.Items))
	messages := make([]*model.Message, 0, len(payload.Data.Items))
	for _, item := range payload.Data.Items {
		if item.ID == 0 || item.Score < s.minScore {
			continue
		}
		published := time.Unix(item.DisplayTime, 0)
		if published.Before(cutoff) {
			continue
		}
		title := strings.TrimSpace(item.Title)
		content := wallstcnText(item.ContentText)
		if title == "" {
			title = truncateRunes(content, 60)
		}
		text := title + " " + content
		if title == "" || s.excluded(text) || !s.included(text) {
			continue
		}

		id := hashString(fmt.Sprintf("wscn:%d", item.ID))
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}

		if more := wallstcnText(item.ContentMore); more != "" && !strings.Contains(content, more) {
			content = strings.TrimSpace(content + "\n\n" + more)
		}

		msg := &model.Message{
			Type:       model.TypeNews,
			ID:         id,
			Title:      title,
			Content:    content,
			Link:       item.URI,
			CreateTime: published,
			FetchTime:  time.Now(),
			Source:     s.name,
			SourceType: "wallstcn",
			Tags:       append([]string(nil), s.tags...),
		}
		msg.SetMetadata("display_source", "华尔街见闻")
		msg.SetMetadata("wscn_score", item.Score)
		msg.SetMetadata("wscn_channel", channel)
		messages = append(messages, msg)
	}
	return messages, nil
}

// included 为空表示不过滤；配置后标题或正文命中任一关键词才保留。
// 用于接入高噪声频道（如 a-stock-channel）时只放行政策/监管类条目。
func (s *WallStreetCNSource) included(text string) bool {
	if len(s.includeKeywords) == 0 {
		return true
	}
	for _, keyword := range s.includeKeywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

func (s *WallStreetCNSource) excluded(text string) bool {
	for _, keyword := range s.excludeKeywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
	return false
}

// wallstcnText 去掉内容里的 HTML 标签与多余空白。
func wallstcnText(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	text := wscnTagRE.ReplaceAllString(raw, "")
	text = html.UnescapeString(text)
	text = strings.ReplaceAll(text, " ", " ")
	lines := strings.Split(text, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" {
			cleaned = append(cleaned, line)
		}
	}
	return strings.Join(cleaned, "\n")
}

func containsString(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}
