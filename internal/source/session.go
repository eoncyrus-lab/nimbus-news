package source

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Ricaardo/nimbus-os/news/internal/digest"
	"github.com/Ricaardo/nimbus-os/news/internal/llm"
	"github.com/Ricaardo/nimbus-os/news/internal/model"
)

// 按市场时段组装的综合简报：一个时段只推一条，把该时段相关的行情、宏观、
// 日历和资讯摘要全部塞进去，替代此前七八个各自定时、各自推送的源。
func init() {
	Register("session", NewSessionSource)
}

const sessionDefaultMaxChars = 1800

const (
	sessionMorning = "morning"  // A 股盘中多槽
	sessionClose   = "close"    // A 股收盘
	sessionUS      = "us"       // 美盘前瞻
	sessionUSClose = "us_close" // 美盘收盘/夜盘复盘
	sessionWeek    = "week"     // 周度版次（周日 20:00）
)

type sessionMember struct {
	label     string
	src       Source
	typ       string
	createErr error // 成员构造失败时保留占位，避免拖垮整条简报
}

// SessionSource 按市场时段组合的简报源
type SessionSource struct {
	name            string
	session         string
	title           string
	members         []sessionMember
	digestSt        digest.Store
	llmProv         llm.Provider
	force           bool   // 跳过交易日判断（手动 FetchOnly / 测试用）
	maxChars        int    // 单条消息上限（字符数），超出按小节分段推送
	titleFromMember bool   // 用行情成员的内在标题（随竞价/开盘/午盘相位变化）
	phase           string // 美盘相位：preopen/close，用美东时间门控冬夏令时
}

// NewSessionSource 创建时段简报源。members 里每一项用现有的源类型组装：
//
//	options:
//	  session: "morning"
//	  title: "🌅 盘前全景"
//	  members:
//	    - label: "隔夜与盘前"
//	      type: "market-briefing"
//	      options: {briefing_type: "pre_market"}
func NewSessionSource(cfg Config) (Source, error) {
	session, _ := cfg.Options["session"].(string)
	switch session {
	case sessionMorning, sessionClose, sessionUS, sessionUSClose, sessionWeek:
	default:
		return nil, fmt.Errorf("session: unknown session %q (want morning/close/us/us_close/week)", session)
	}
	s := &SessionSource{name: cfg.Name, session: session}
	s.title, _ = cfg.Options["title"].(string)
	if force, ok := cfg.Options["force"].(bool); ok {
		s.force = force
	}
	if fromMember, ok := cfg.Options["title_from_member"].(bool); ok {
		s.titleFromMember = fromMember
	}
	if phase, ok := cfg.Options["phase"].(string); ok {
		s.phase = strings.ToLower(strings.TrimSpace(phase))
	}
	s.maxChars = sessionDefaultMaxChars
	if v, ok := cfg.Options["max_chars"].(int); ok && v >= 0 {
		s.maxChars = v
	}
	if s.title == "" {
		s.title = map[string]string{
			sessionMorning: "🌅 盘前全景",
			sessionClose:   "📊 A股收盘",
			sessionUS:      "🌎 美盘前瞻",
			sessionUSClose: "🌎 美盘收盘",
			sessionWeek:    "🗓 周度版次",
		}[session]
	}

	rawMembers, ok := cfg.Options["members"].([]interface{})
	if !ok || len(rawMembers) == 0 {
		return nil, fmt.Errorf("session %s: members must not be empty", cfg.Name)
	}
	for i, raw := range rawMembers {
		member, ok := raw.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("session %s: member %d is not a mapping", cfg.Name, i)
		}
		memberType, _ := member["type"].(string)
		if memberType == "" {
			return nil, fmt.Errorf("session %s: member %d type is required", cfg.Name, i)
		}
		label, _ := member["label"].(string)
		if label == "" {
			label = fmt.Sprintf("%s#%d", memberType, i+1)
		}
		memberOptions, _ := member["options"].(map[string]interface{})
		url, _ := member["url"].(string)
		src, err := Create(Config{
			Name:    fmt.Sprintf("%s/%s", cfg.Name, label),
			Type:    memberType,
			URL:     url,
			Options: memberOptions,
		})
		if err != nil {
			// 缺密钥/依赖的成员（如未配 FRED key）只降级该段，整条时段简报照常生成。
			slog.Warn("session member create failed", "source", cfg.Name, "member", label, "error", err)
			s.members = append(s.members, sessionMember{label: label, typ: memberType, createErr: err})
			continue
		}
		s.members = append(s.members, sessionMember{label: label, src: src, typ: memberType})
	}
	return s, nil
}

func (s *SessionSource) Name() string { return s.name }
func (s *SessionSource) Type() string { return "session" }

// SetDigestStore 把 digest store 透传给需要租约的成员（聚合摘要）。
func (s *SessionSource) SetDigestStore(store digest.Store) {
	s.digestSt = store
	for _, member := range s.members {
		if setter, ok := member.src.(interface{ SetDigestStore(digest.Store) }); ok {
			setter.SetDigestStore(store)
		}
	}
}

// SetLLMProvider 实现 LLMSettable，透传给各成员的叙事段。
func (s *SessionSource) SetLLMProvider(provider llm.Provider) {
	s.llmProv = provider
	for _, member := range s.members {
		if setter, ok := member.src.(LLMSettable); ok {
			setter.SetLLMProvider(provider)
		}
	}
}

// SetEventReader 把事件模型注入 events 成员（版次段落）。
func (s *SessionSource) SetEventReader(reader EventReader) {
	for _, member := range s.members {
		if setter, ok := member.src.(EventReaderSetter); ok {
			setter.SetEventReader(reader)
		}
	}
}

func (s *SessionSource) Fetch() ([]*model.Message, error) {
	return s.FetchContext(context.Background())
}

func (s *SessionSource) FetchContext(parent context.Context) ([]*model.Message, error) {
	if !s.sessionActive(time.Now()) {
		return nil, nil
	}

	var parts []sessionPart
	var failed []string
	memberTitle := ""
	for _, member := range s.members {
		if member.createErr != nil {
			failed = append(failed, member.label)
			continue
		}
		messages, err := fetchMember(parent, member.src)
		if err != nil {
			// 单个成员失败只降级该段，不影响整条时段简报。
			slog.Warn("session member failed", "source", s.name, "member", member.label, "error", err)
			failed = append(failed, member.label)
			continue
		}
		for _, msg := range messages {
			if msg == nil {
				continue
			}
			// 同一时段源一天跑多槽（竞价/开盘/午盘/收盘/盘后），标题跟随行情成员相位，
			// 不能让所有槽都叫“盘前全景”。
			if memberTitle == "" && member.typ == "market-briefing" && strings.TrimSpace(msg.Title) != "" {
				memberTitle = strings.TrimSpace(msg.Title)
			}
			parts = append(parts, sessionPart{
				text:          s.renderMember(member, msg),
				leaseID:       msg.GetStringMetadata("digest_lease_id"),
				leaseBriefing: msg.GetStringMetadata("digest_briefing"),
				short:         msg.ShortContent,
				image:         msg.ImageURL,
			})
		}
	}
	if len(parts) == 0 && len(failed) == 0 {
		return nil, nil
	}
	if len(failed) > 0 {
		parts = append(parts, sessionPart{text: "⚠️ 数据源异常：" + strings.Join(failed, ", ")})
	}

	now := time.Now()
	chunks := packSessionParts(parts, s.maxChars)
	messages := make([]*model.Message, 0, len(chunks))
	baseTitle := fmt.Sprintf("%s · %s", s.title, now.Format("01-02"))
	if s.titleFromMember && memberTitle != "" {
		baseTitle = memberTitle
	}
	for i, chunk := range chunks {
		title := baseTitle
		if len(chunks) > 1 {
			title = fmt.Sprintf("%s（%d/%d）", baseTitle, i+1, len(chunks))
		}
		msg := &model.Message{
			Type:       model.TypeAnalysis,
			ID:         fmt.Sprintf("session_%s_%s_%s_%d", s.session, now.Format("20060102"), now.Format("1504"), i+1),
			Title:      title,
			Content:    chunk.text,
			Source:     s.name,
			SourceType: "session",
			CreateTime: now,
			FetchTime:  now,
			Tags:       []string{"简报", s.session},
		}
		if i == 0 {
			msg.ShortContent = chunk.short
			msg.ImageURL = chunk.image
		}
		// 聚合成员租到的 digest 桶必须随消息透传，否则租约无人 ack，下个时段会重复推送。
		// 分段时只挂在包含该成员的那一段上。
		if chunk.leaseID != "" {
			msg.SetMetadata("digest_lease_id", chunk.leaseID)
			msg.SetMetadata("digest_briefing", chunk.leaseBriefing)
		}
		displaySource := s.title
		if s.titleFromMember && memberTitle != "" {
			displaySource = memberTitle
		}
		msg.SetMetadata("display_source", displaySource)
		messages = append(messages, msg)
	}
	return messages, nil
}

// sessionPart 是合并前的一个成员段落，附带该段需要透传的元数据。
type sessionPart struct {
	text          string
	leaseID       string
	leaseBriefing string
	short         string
	image         string
}

type sessionChunk struct {
	text          string
	leaseID       string
	leaseBriefing string
	short         string
	image         string
}

// packSessionParts 按 maxChars 把成员段落打包成若干条消息：
// 优先在成员边界切分，单个成员超长时再按行硬切，避免一条简报超过渠道上限。
func packSessionParts(parts []sessionPart, maxChars int) []sessionChunk {
	if maxChars <= 0 {
		maxChars = sessionDefaultMaxChars
	}
	var chunks []sessionChunk
	var current sessionChunk
	var builder strings.Builder
	curLen := 0 // 已缓存的字符数（rune），渠道上限按字符算

	flush := func() {
		if strings.TrimSpace(builder.String()) == "" {
			return
		}
		current.text = strings.TrimSpace(builder.String())
		chunks = append(chunks, current)
		builder.Reset()
		current = sessionChunk{}
		curLen = 0
	}

	for _, part := range parts {
		text := strings.TrimSpace(part.text)
		if text == "" {
			continue
		}
		pieces := splitLongText(text, maxChars)
		for idx, piece := range pieces {
			pieceLen := len([]rune(piece))
			if curLen > 0 && curLen+2+pieceLen > maxChars {
				flush()
			}
			if curLen > 0 {
				builder.WriteString("\n\n")
				curLen += 2
			}
			builder.WriteString(piece)
			curLen += pieceLen
			// 租约只挂在段落的最后一段：整段推完才算送达，中途缺失仍会按 at-least-once 重投。
			if idx == len(pieces)-1 && current.leaseID == "" {
				current.leaseID = part.leaseID
				current.leaseBriefing = part.leaseBriefing
			}
			if current.short == "" {
				current.short = part.short
			}
			if current.image == "" {
				current.image = part.image
			}
			if curLen >= maxChars {
				flush()
			}
		}
	}
	flush()
	return chunks
}

// splitLongText 只在单段本身超限时按行切分；正常段落原样返回。
func splitLongText(text string, maxChars int) []string {
	if len([]rune(text)) <= maxChars {
		return []string{text}
	}
	var pieces []string
	var builder strings.Builder
	for _, line := range strings.Split(text, "\n") {
		lineLen := len([]rune(line))
		if builder.Len() > 0 && len([]rune(builder.String()))+1+lineLen > maxChars {
			pieces = append(pieces, strings.TrimSpace(builder.String()))
			builder.Reset()
		}
		if lineLen > maxChars {
			// 单行超长（无换行的长段落）：按字符硬切。
			runes := []rune(line)
			for len(runes) > maxChars {
				pieces = append(pieces, strings.TrimSpace(string(runes[:maxChars])))
				runes = runes[maxChars:]
			}
			builder.WriteString(string(runes))
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n")
		}
		builder.WriteString(line)
	}
	if strings.TrimSpace(builder.String()) != "" {
		pieces = append(pieces, strings.TrimSpace(builder.String()))
	}
	return pieces
}

func fetchMember(parent context.Context, src Source) ([]*model.Message, error) {
	type result struct {
		messages []*model.Message
		err      error
	}
	done := make(chan result, 1)
	go func() {
		if fetcher, ok := src.(ContextFetcher); ok {
			messages, err := fetcher.FetchContext(parent)
			done <- result{messages, err}
			return
		}
		messages, err := src.Fetch()
		done <- result{messages, err}
	}()
	select {
	case <-parent.Done():
		return nil, parent.Err()
	case r := <-done:
		return r.messages, r.err
	}
}

// renderMember 处理成员内容：market-briefing 的正文自带小节标题，直接拼接；
// script 类成员的主标题在 Title 里，需要补一行小节头。
func (s *SessionSource) renderMember(member sessionMember, msg *model.Message) string {
	content := strings.TrimSpace(msg.Content)
	if member.typ == "script" {
		if title := strings.TrimSpace(msg.Title); title != "" {
			return "── " + title + " ──\n" + content
		}
	}
	return content
}

func (s *SessionSource) sessionActive(now time.Time) bool {
	if s.force {
		return true
	}
	switch s.session {
	case sessionMorning, sessionClose:
		return IsAShareBroadlyActive(now)
	case sessionUS, sessionUSClose:
		et := now.In(newYorkLocation())
		if wd := et.Weekday(); wd == time.Saturday || wd == time.Sunday {
			return false
		}
		// 调度器只支持北京时间 HH:MM，所以同一相位排两个北京时间槽，
		// 由美东实际时刻决定哪个槽生效（夏令时差 12h、冬令时差 13h）。
		phase := s.phase
		if s.session == sessionUSClose {
			phase = "close"
		}
		minutes := et.Hour()*60 + et.Minute()
		switch phase {
		case "":
			return true // 旧配置未写 phase，保持原行为
		case "preopen":
			return minutes >= 8*60+50 && minutes < 9*60+10
		case "close":
			// 20:05 ET：常规收盘 + 盘后结束，此时才拿得到完整盘后数据。
			return minutes >= 20*60+5 && minutes < 20*60+25
		}
		return false
	case sessionWeek:
		// 调度器只支持 HH:MM；周度版次每天 20:00 触发，只在周日真正产出。
		return now.Weekday() == time.Sunday
	}
	return false
}

// newYorkLocation 统一美东时区；tzdata 缺失时退回固定 EST。
func newYorkLocation() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.FixedZone("America/New_York", -5*60*60)
	}
	return loc
}
