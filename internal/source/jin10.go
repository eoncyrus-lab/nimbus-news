package source

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Ricaardo/nimbus-os/news/internal/model"
)

// 金十数据 7x24 快讯（公开 JSON API）。相比英文通讯社，中文一手宏观/政策/商品
// 快讯更快、跨源重复更低；相比华尔街见闻，金十的宏观与商品覆盖面更宽。
//
// API 每次返回最近约 20 条，用上一页最后一条的 time 作为 max_time 继续翻页。
// 只有 important=1 或命中宏观关键词的条目才保留，个股公告类噪音默认丢弃。
const (
	defaultJin10Endpoint = "https://flash-api.jin10.com/get_flash_list"
	jin10MaxResponseBody = 4 << 20
	jin10RequestTimeout  = 20 * time.Second
)

func init() {
	Register("jin10", NewJin10Source)
}

type jin10Flash struct {
	ID        string `json:"id"`
	Important int    `json:"important"`
	Time      string `json:"time"`
	Data      struct {
		Content string `json:"content"`
		Title   string `json:"title"`
	} `json:"data"`
}

type jin10Response struct {
	Status  int          `json:"status"`
	Message string       `json:"message"`
	Data    []jin10Flash `json:"data"`
}

// Jin10Source 金十数据快讯源
type Jin10Source struct {
	name            string
	endpoint        string
	limit           int
	pages           int
	maxAge          time.Duration
	minImportant    int
	includeKeywords []string
	excludeKeywords []string
	tags            []string
	client          *http.Client

	// 个股公告噪音不进实时推送，按天攒成一条汇总。
	noiseSummary  bool
	summaryMinute int // 汇总发车时刻（当天分钟数），默认 08:30
	summaryLimit  int // 汇总里最多列出的条数
	noise         map[string]jin10Noise
	lastSummary   string // 已汇总的本地日期，进程内保证一天一次
	now           func() time.Time
	mu            sync.Mutex
}

type jin10Noise struct {
	ID      string
	Title   string
	Keyword string
	Time    time.Time
}

// 默认保留的宏观/政策/商品关键词。个股公告通常不含这些词，会被过滤掉。
var jin10DefaultInclude = []string{
	"央行", "美联储", "利率", "降息", "加息", "降准", "lpr", "mlf", "逆回购",
	"cpi", "ppi", "pmi", "gdp", "非农", "通胀", "通缩", "经济数据", "就业", "失业",
	"关税", "制裁", "停火", "战争", "冲突", "伊朗", "以色列", "俄罗斯", "乌克兰", "台海", "霍尔木兹",
	"证监会", "国务院", "财政部", "发改委", "商务部", "外交部", "人民银行", "政策", "监管",
	"人民币", "汇率", "国债", "收益率", "美元", "欧元", "日元",
	"原油", "opec", "黄金", "铜", "商品", "美股", "港股", "a股", "熔断", "暴跌", "暴涨",
	"财政", "债务", "违约", "破产", "并购", "收购", "预算",
}

// 个股公告类噪音。命中即丢，即使带 important 标记。
var jin10DefaultExclude = []string{
	"公告", "减持", "增持", "解禁", "股权激励", "业绩预告", "业绩快报",
	"招股", "调研", "互动平台", "拟投资", "签订合同", "项目投资",
}

func NewJin10Source(cfg Config) (Source, error) {
	s := &Jin10Source{
		name:            cfg.Name,
		endpoint:        cfg.URL,
		limit:           40,
		pages:           6,
		maxAge:          3 * time.Hour,
		minImportant:    1,
		includeKeywords: append([]string(nil), jin10DefaultInclude...),
		excludeKeywords: append([]string(nil), jin10DefaultExclude...),
		tags:            []string{"中国", "宏观", "金十"},
		client:          &http.Client{Timeout: jin10RequestTimeout},
		noiseSummary:    true,
		summaryMinute:   8*60 + 30,
		summaryLimit:    20,
		noise:           make(map[string]jin10Noise),
		now:             time.Now,
	}
	if s.endpoint == "" {
		s.endpoint = defaultJin10Endpoint
	}
	if v := optionInt(cfg.Options, "limit"); v > 0 {
		s.limit = v
	}
	if v := optionInt(cfg.Options, "pages"); v > 0 {
		s.pages = v
	}
	if v := optionInt(cfg.Options, "max_age_hours"); v > 0 {
		s.maxAge = time.Duration(v) * time.Hour
	}
	// optionInt 对缺省键返回 0，不能直接赋值，否则默认 min_important=1 会被抹成 0。
	if _, ok := cfg.Options["min_important"]; ok {
		if v := optionInt(cfg.Options, "min_important"); v >= 0 {
			s.minImportant = v
		}
	}
	if keywords := optionStrings(cfg.Options, "include_keywords"); len(keywords) > 0 {
		s.includeKeywords = keywords
	}
	if keywords := optionStrings(cfg.Options, "exclude_keywords"); len(keywords) > 0 {
		s.excludeKeywords = keywords
	}
	if _, ok := cfg.Options["noise_daily_summary"]; ok {
		s.noiseSummary = optionBool(cfg.Options, "noise_daily_summary")
	}
	if value, ok := cfg.Options["noise_summary_time"].(string); ok {
		if minute, ok := parseClockMinute(value); ok {
			s.summaryMinute = minute
		}
	}
	if v := optionInt(cfg.Options, "noise_summary_limit"); v > 0 {
		s.summaryLimit = v
	}
	if raw, ok := cfg.Options["tags"].([]interface{}); ok {
		s.tags = nil
		for _, value := range raw {
			if tag, ok := value.(string); ok && strings.TrimSpace(tag) != "" && !containsString(s.tags, tag) {
				s.tags = append(s.tags, strings.TrimSpace(tag))
			}
		}
	}
	return s, nil
}

func (s *Jin10Source) Name() string { return s.name }
func (s *Jin10Source) Type() string { return "jin10" }

func (s *Jin10Source) Fetch() ([]*model.Message, error) {
	ctx, cancel := context.WithTimeout(context.Background(), jin10RequestTimeout)
	defer cancel()
	return s.FetchContext(ctx)
}

func (s *Jin10Source) FetchContext(ctx context.Context) ([]*model.Message, error) {
	now := s.now()
	seen := make(map[string]struct{})
	var flashes []jin10Flash
	maxTime := ""

	for page := 0; page < s.pages; page++ {
		batch, err := s.fetchPage(ctx, maxTime)
		if err != nil {
			if page == 0 {
				return nil, err
			}
			slog.Warn("jin10 page failed", "source", s.name, "page", page, "error", err)
			break
		}
		if len(batch) == 0 {
			break
		}
		reachedOld := false
		for _, item := range batch {
			if item.ID == "" {
				continue
			}
			published := parseJin10Time(item.Time)
			if !published.IsZero() && now.Sub(published) > s.maxAge {
				reachedOld = true
				continue
			}
			if _, ok := seen[item.ID]; ok {
				continue
			}
			seen[item.ID] = struct{}{}
			flashes = append(flashes, item)
		}
		if reachedOld || len(flashes) >= s.limit {
			break
		}
		maxTime = batch[len(batch)-1].Time
		if maxTime == "" {
			break
		}
	}

	sort.SliceStable(flashes, func(i, j int) bool {
		return parseJin10Time(flashes[i].Time).Before(parseJin10Time(flashes[j].Time))
	})

	messages := make([]*model.Message, 0, len(flashes))
	for _, item := range flashes {
		cleaned := htmlToPlainText(item.Data.Content)
		if cleaned == "" {
			continue
		}
		if !s.accept(cleaned, item.Important) {
			// 个股公告不进实时推送，但按天留给汇总。
			if keyword := firstMatchedKeyword(strings.ToLower(cleaned), s.excludeKeywords); keyword != "" {
				s.recordNoise(item.ID, parseJin10Time(item.Time), cleaned, keyword, now)
			}
			continue
		}
		title, body := splitJin10Title(cleaned)
		published := parseJin10Time(item.Time)
		if published.IsZero() {
			published = now
		}
		msg := &model.Message{
			Type:       model.TypeNews,
			ID:         hashString("jin10:" + item.ID),
			Title:      title,
			Content:    body,
			Link:       "https://flash.jin10.com/detail/" + item.ID,
			CreateTime: published,
			FetchTime:  now,
			Source:     s.name,
			SourceType: "jin10",
			Tags:       s.tags,
		}
		msg.SetMetadata("display_source", "金十数据")
		msg.SetMetadata("important", item.Important)
		messages = append(messages, msg)
		if len(messages) >= s.limit {
			break
		}
	}

	// 公告汇总按本地时间的固定窗口发一次，不跟随实时推送。
	pendingNoise := s.noiseCount()
	if summary := s.takeNoiseSummary(now); summary != nil {
		messages = append(messages, summary)
	}

	slog.Info("jin10 fetched", "source", s.name, "messages", len(messages), "flashes", len(flashes),
		"noise_pending", pendingNoise)
	return messages, nil
}

func (s *Jin10Source) noiseCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.noise)
}

// recordNoise 把被丢弃的个股公告放进当天待汇总集合；同一 ID 只记一次。
func (s *Jin10Source) recordNoise(id string, published time.Time, content, keyword string, now time.Time) {
	if id == "" {
		return
	}
	if published.IsZero() {
		published = now
	}
	title, _ := splitJin10Title(content)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.noise == nil {
		s.noise = make(map[string]jin10Noise)
	}
	if _, ok := s.noise[id]; ok {
		return
	}
	if len(s.noise) >= 1000 {
		oldestID := ""
		var oldest time.Time
		for key, item := range s.noise {
			if oldestID == "" || item.Time.Before(oldest) {
				oldestID, oldest = key, item.Time
			}
		}
		delete(s.noise, oldestID)
	}
	s.noise[id] = jin10Noise{ID: id, Title: title, Keyword: keyword, Time: published}
}

// takeNoiseSummary 在汇总窗口内取出待汇总的公告并清空；一天最多一次。
func (s *Jin10Source) takeNoiseSummary(now time.Time) *model.Message {
	if !s.noiseSummary {
		return nil
	}
	loc := jin10Location()
	local := now.In(loc)
	if !inClockWindow(local, s.summaryMinute, 30) {
		return nil
	}
	day := local.Format("2006-01-02")
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastSummary == day || len(s.noise) == 0 {
		return nil
	}
	items := make([]jin10Noise, 0, len(s.noise))
	for _, item := range s.noise {
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Time.Before(items[j].Time) })
	s.noise = make(map[string]jin10Noise)
	s.lastSummary = day
	return s.buildNoiseSummary(items, local)
}

func (s *Jin10Source) buildNoiseSummary(items []jin10Noise, now time.Time) *model.Message {
	counts := make(map[string]int)
	for _, item := range items {
		counts[item.Keyword]++
	}
	keywords := make([]string, 0, len(counts))
	for keyword := range counts {
		keywords = append(keywords, keyword)
	}
	sort.SliceStable(keywords, func(i, j int) bool {
		if counts[keywords[i]] != counts[keywords[j]] {
			return counts[keywords[i]] > counts[keywords[j]]
		}
		return keywords[i] < keywords[j]
	})
	parts := make([]string, 0, len(keywords))
	for _, keyword := range keywords {
		parts = append(parts, fmt.Sprintf("%s %d", keyword, counts[keyword]))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "── 个股公告噪音 · 近24小时 · 共 %d 条 ──\n", len(items))
	b.WriteString("按类：" + strings.Join(parts, " · ") + "\n")
	loc := jin10Location()
	listed := 0
	for _, item := range items {
		if listed >= s.summaryLimit {
			fmt.Fprintf(&b, "…另有 %d 条未列出\n", len(items)-listed)
			break
		}
		title := item.Title
		if title == "" {
			title = item.Keyword
		}
		fmt.Fprintf(&b, "- %s %s\n", item.Time.In(loc).Format("15:04"), truncateRunes(title, 60))
		listed++
	}

	msg := &model.Message{
		Type:       model.TypeAnalysis, // 不进事件引擎，不做实时判定
		ID:         hashString("jin10-noise:" + now.Format("20060102")),
		Title:      fmt.Sprintf("📋 金十公告汇总 · 近24小时（%d 条）", len(items)),
		Content:    b.String(),
		Source:     s.name,
		SourceType: "jin10",
		CreateTime: now,
		FetchTime:  now,
		Tags:       []string{"公告", "每日汇总"},
	}
	msg.SetMetadata("display_source", "金十公告汇总")
	msg.SetMetadata("noise_summary", "1")
	return msg
}

// firstMatchedKeyword 返回命中的第一个关键词，用于汇总按类分桶。
func firstMatchedKeyword(text string, keywords []string) string {
	for _, keyword := range keywords {
		if keyword != "" && strings.Contains(text, strings.ToLower(keyword)) {
			return keyword
		}
	}
	return ""
}

func parseClockMinute(value string) (int, bool) {
	parts := strings.SplitN(strings.TrimSpace(value), ":", 2)
	if len(parts) != 2 {
		return 0, false
	}
	hour, err1 := strconv.Atoi(parts[0])
	minute, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}

// inClockWindow 判断 now 是否落在 [start, start+window) 的分钟窗口内，支持跨零点。
func inClockWindow(now time.Time, start, window int) bool {
	minutes := now.Hour()*60 + now.Minute()
	end := start + window
	if end <= 24*60 {
		return minutes >= start && minutes < end
	}
	return minutes >= start || minutes < end-24*60
}

func jin10Location() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*60*60)
	}
	return loc
}

func (s *Jin10Source) fetchPage(ctx context.Context, maxTime string) ([]jin10Flash, error) {
	params := url.Values{}
	params.Set("channel", "-8200")
	params.Set("vip", "1")
	if maxTime != "" {
		params.Set("max_time", maxTime)
	}
	endpoint := s.endpoint
	if endpoint == "" {
		endpoint = defaultJin10Endpoint
	}
	separator := "?"
	if strings.Contains(endpoint, "?") {
		separator = "&"
	}
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint+separator+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)")
	req.Header.Set("Accept", "application/json")
	// 金十公开接口要求的客户端标识，缺失时返回 403。
	req.Header.Set("x-app-id", "bVBF4FyRTn5NJF5n")
	req.Header.Set("x-version", "1.0.0")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jin10 request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jin10 http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, jin10MaxResponseBody))
	if err != nil {
		return nil, fmt.Errorf("jin10 read failed: %w", err)
	}
	var payload jin10Response
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("jin10 decode failed: %w", err)
	}
	if payload.Status != 200 {
		return nil, fmt.Errorf("jin10 status %d: %s", payload.Status, payload.Message)
	}
	return payload.Data, nil
}

func (s *Jin10Source) accept(content string, important int) bool {
	lower := strings.ToLower(content)
	if matchesAnyKeyword(lower, s.excludeKeywords) {
		return false
	}
	if important >= s.minImportant {
		return true
	}
	if len(s.includeKeywords) == 0 {
		return true
	}
	return matchesAnyKeyword(lower, s.includeKeywords)
}

func matchesAnyKeyword(text string, keywords []string) bool {
	for _, keyword := range keywords {
		if keyword != "" && strings.Contains(text, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

// splitJin10Title 优先取【标题】；没有则用第一句（截断到 60 字符）。
func splitJin10Title(content string) (string, string) {
	text := strings.TrimSpace(content)
	if strings.HasPrefix(text, "【") {
		if end := strings.Index(text, "】"); end > len("【") {
			title := strings.TrimSpace(text[len("【"):end])
			body := strings.TrimSpace(text[end+len("】"):])
			if body == "" {
				body = text
			}
			return title, body
		}
	}
	first := text
	if idx := strings.IndexAny(first, "。！？!?"); idx > 0 {
		first = first[:idx]
	}
	first = strings.TrimSpace(first)
	runes := []rune(first)
	if len(runes) > 60 {
		first = string(runes[:60]) + "…"
	}
	return first, text
}

func parseJin10Time(value string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(value), jin10Location())
	if err != nil {
		return time.Time{}
	}
	return t
}
