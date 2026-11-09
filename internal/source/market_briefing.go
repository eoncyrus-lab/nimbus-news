package source

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Ricaardo/nimbus-os/news/internal/digest"
	"github.com/Ricaardo/nimbus-os/news/internal/llm"
	"github.com/Ricaardo/nimbus-os/news/internal/model"

	"github.com/Ricaardo/nimbus-os/datasources/market"
	dsyahoo "github.com/Ricaardo/nimbus-os/datasources/yahoo"
)

func init() {
	Register("market-briefing", NewMarketBriefingSource)
}

// MarketBriefingSource 盘前/收盘/美盘前瞻 合并简报
// 将多个独立脚本的输出合并为一条消息，通过 LLM 生成叙事
type MarketBriefingSource struct {
	name          string
	briefingType  string // "pre_market", "closing", "us_preview", "aggregate"
	pythonCmd     string
	scriptsDir    string
	marketService market.Service
	llmProvider   llm.Provider
	digestStore   digest.Store

	// 时段组装后的裁剪开关：默认全开，配置显式关闭才跳过对应段落/脚本。
	includeFedwatch bool
	closingSections map[string]bool

	// 美股收盘增量内容
	includeEarnings bool
	earningsMinCap  float64           // 财报只列市值 ≥ 该值的公司（美元）
	ashareStocks    map[string]string // 覆盖默认的 A 股产业链映射
}

func (s *MarketBriefingSource) sectionEnabled(name string) bool {
	if len(s.closingSections) == 0 {
		return true
	}
	return s.closingSections[name]
}

const digestBriefingItemLimit = 12

func (s *MarketBriefingSource) SetDigestStore(store digest.Store) {
	s.digestStore = store
}

// SetLLMProvider 实现 LLMSettable
func (s *MarketBriefingSource) SetLLMProvider(p llm.Provider) {
	s.llmProvider = p
}

func NewMarketBriefingSource(cfg Config) (Source, error) {
	s := &MarketBriefingSource{
		name:            cfg.Name,
		briefingType:    "closing", // default
		pythonCmd:       "python3",
		scriptsDir:      "scripts",
		marketService:   market.NewMarketService(),
		includeFedwatch: true,
		includeEarnings: true,
		earningsMinCap:  20e9,
	}
	if v, ok := cfg.Options["briefing_type"].(string); ok && v != "" {
		s.briefingType = v
	}
	if v, ok := cfg.Options["python_cmd"].(string); ok && v != "" {
		s.pythonCmd = v
	}
	if v, ok := cfg.Options["scripts_dir"].(string); ok && v != "" {
		s.scriptsDir = v
	}
	if v, ok := cfg.Options["include_fedwatch"].(bool); ok {
		s.includeFedwatch = v
	}
	if v, ok := cfg.Options["include_earnings"].(bool); ok {
		s.includeEarnings = v
	}
	// 单位：亿美元，默认 200 亿美元。
	if v, ok := cfg.Options["earnings_min_cap_100m"].(float64); ok && v > 0 {
		s.earningsMinCap = v * 1e8
	}
	if v, ok := cfg.Options["earnings_min_cap_100m"].(int); ok && v > 0 {
		s.earningsMinCap = float64(v) * 1e8
	}
	if raw, ok := cfg.Options["ashare_map"].(map[string]interface{}); ok {
		s.ashareStocks = make(map[string]string, len(raw))
		for key, value := range raw {
			if text, ok := value.(string); ok && text != "" {
				s.ashareStocks[strings.ToUpper(key)] = text
			}
		}
	}
	if raw, ok := cfg.Options["sections"].([]interface{}); ok {
		s.closingSections = make(map[string]bool, len(raw))
		for _, value := range raw {
			if name, ok := value.(string); ok && name != "" {
				s.closingSections[name] = true
			}
		}
	}
	return s, nil
}

func (s *MarketBriefingSource) Name() string { return s.name }
func (s *MarketBriefingSource) Type() string { return "market-briefing" }

// Fetch 无父 ctx(兼容旧调用);FetchContext 供引擎/API 传入父 ctx 继承超时。
func (s *MarketBriefingSource) Fetch() ([]*model.Message, error) {
	return s.FetchContext(context.Background())
}

// FetchContext 根据 briefingType 分发到不同逻辑。
// parent 为上层调用方 ctx(API 主动拉取的 330s 兜底 / 调度链路源生命周期 ctx),
// 各 fetch 方法内部以 parent 派生自身超时,父 ctx 到期会级联取消。
func (s *MarketBriefingSource) FetchContext(parent context.Context) ([]*model.Message, error) {
	var (
		messages []*model.Message
		err      error
	)
	switch s.briefingType {
	case "pre_market":
		messages, err = s.fetchPreMarket(parent)
	case "closing":
		messages, err = s.fetchClosing(parent)
	case "us_preview":
		messages, err = s.fetchUSPreview(parent)
	case "us_close":
		messages, err = s.fetchUSClose(parent)
	case "us_overnight":
		messages, err = s.fetchUSOvernight(parent)
	case "hk_close":
		messages, err = s.fetchHKClose(parent)
	case "aggregate":
		messages, err = s.fetchAggregate(parent)
	default:
		return nil, fmt.Errorf("unknown briefing_type: %s", s.briefingType)
	}
	// 兼容旧配置:us_preview 仍可租 us_preview 桶(现配置 briefing_target 已
	// 全部改为 news_aggregate,该桶为空,租约返回空不产生内容)。
	if err != nil || len(messages) == 0 || s.digestStore == nil || s.briefingType != "us_preview" {
		return messages, err
	}
	return s.appendDigest(parent, messages)
}

func (s *MarketBriefingSource) appendDigest(parent context.Context, messages []*model.Message) ([]*model.Message, error) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	briefing := digest.Briefing(s.briefingType)
	var (
		lease      *digest.Lease
		err        error
		topicAware bool
	)
	if topicStore, ok := s.digestStore.(digest.TopicStore); ok {
		lease, err = topicStore.LeaseTopics(ctx, briefing, digestBriefingItemLimit, 30*time.Minute)
		topicAware = true
	} else {
		lease, err = s.digestStore.Lease(ctx, briefing, digestBriefingItemLimit, 30*time.Minute)
	}
	if err != nil {
		return nil, fmt.Errorf("lease digest: %w", err)
	}
	if lease == nil {
		return messages, nil
	}
	var summary string
	if topicAware {
		summary = digest.RenderTopics(lease.Topics, digestBriefingItemLimit)
	} else {
		summary = digest.Render(lease.Items, digestBriefingItemLimit)
	}
	if summary == "" {
		return messages, nil
	}
	for _, msg := range messages {
		msg.Content = strings.TrimSpace(msg.Content) + "\n\n## 资讯摘要\n" + summary
		msg.SetMetadata("digest_lease_id", lease.ID)
		msg.SetMetadata("digest_briefing", s.briefingType)
	}
	return messages, nil
}

// us2cnSector maps US tickers to CN sector keywords for overseas trigger detection
var us2cnSector = map[string][]string{
	"SOXX":  {"半导体", "芯片"},
	"NVDA":  {"AI", "人工智能", "半导体"},
	"AMD":   {"AI", "半导体"},
	"TSLA":  {"新能源车", "锂电池"},
	"LCID":  {"新能源车"},
	"TAN":   {"光伏"},
	"XBI":   {"创新药", "生物医药", "医药"},
	"IBB":   {"创新药", "医药"},
	"KWEB":  {"互联网", "平台经济"},
	"BABA":  {"互联网", "电商"},
	"GDX":   {"贵金属", "黄金"},
	"GC=F":  {"贵金属", "黄金"},
	"XLE":   {"能源", "石油"},
	"XLF":   {"金融", "银行"},
	"XLY":   {"消费"},
	"XLI":   {"工业"},
	"MSFT":  {"云计算", "AI"},
	"GOOGL": {"云计算", "AI"},
}

type overseasSignal struct {
	Ticker    string
	ChangePct float64
	Sectors   []string
}

// ── pre_market: 盘前全景 (08:45) ──
// 海外隔夜行情 + 今日日历 + AI 叙事

// preMarketSession returns the trading session phase based on Asia/Shanghai time.
func preMarketSession(t time.Time) string {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		shanghai = time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	local := t.In(shanghai)
	minutes := local.Hour()*60 + local.Minute()
	switch {
	case minutes < 9*60+15:
		return "pre_market"
	case minutes < 9*60+30:
		return "auction" // 09:15–09:30 集合竞价
	case minutes < 10*60:
		return "open" // 09:30–10:00 开盘
	case minutes < 11*60+30:
		return "morning_session" // 10:00–11:30 盘中
	case minutes < 13*60:
		return "midday" // 11:30–13:00 午盘
	case minutes < 14*60:
		return "afternoon_open" // 13:00–14:00 午后开盘
	case minutes < 15*60:
		return "late_session" // 14:00–15:00 尾盘
	case minutes < 15*60+30:
		return "afternoon_close" // 15:00–15:30 收盘（含收盘集合竞价）
	default:
		return "after_hours" // 15:30 之后盘后固定价交易
	}
}

type preMarketSessionMeta struct {
	session      string
	emoji        string
	titleLabel   string
	sectionLabel string
	aiLabel      string
	aiRole       string
	aiMaxWords   string
	triggers     bool
}

func preMarketMeta(session string) preMarketSessionMeta {
	switch session {
	case "auction":
		return preMarketSessionMeta{
			session: "auction", emoji: "⏰", titleLabel: "集合竞价",
			sectionLabel: "隔夜行情", aiLabel: "AI 竞价研判",
			aiRole: "A 股竞价分析师", aiMaxWords: "200",
			triggers: false,
		}
	case "open":
		return preMarketSessionMeta{
			session: "open", emoji: "🔔", titleLabel: "开盘",
			sectionLabel: "实时行情", aiLabel: "AI 开盘研判",
			aiRole: "A 股开盘分析师", aiMaxWords: "200",
			triggers: false,
		}
	case "morning_session":
		return preMarketSessionMeta{
			session: "morning_session", emoji: "📈", titleLabel: "盘中",
			sectionLabel: "实时行情", aiLabel: "AI 盘中点评",
			aiRole: "A 股盘中分析师", aiMaxWords: "200",
			triggers: false,
		}
	case "midday":
		return preMarketSessionMeta{
			session: "midday", emoji: "🌆", titleLabel: "午盘",
			sectionLabel: "实时行情", aiLabel: "AI 午盘点评",
			aiRole: "A 股午间分析师", aiMaxWords: "200",
			triggers: false,
		}
	case "afternoon_open":
		return preMarketSessionMeta{
			session: "afternoon_open", emoji: "🏙", titleLabel: "午后开盘",
			sectionLabel: "实时行情", aiLabel: "AI 午后研判",
			aiRole: "A 股午后分析师", aiMaxWords: "200",
			triggers: false,
		}
	case "late_session":
		return preMarketSessionMeta{
			session: "late_session", emoji: "🌇", titleLabel: "尾盘",
			sectionLabel: "实时行情", aiLabel: "AI 尾盘研判",
			aiRole: "A 股尾盘分析师", aiMaxWords: "200",
			triggers: false,
		}
	case "after_hours":
		return preMarketSessionMeta{
			session: "after_hours", emoji: "🌃", titleLabel: "盘后",
			sectionLabel: "实时行情", aiLabel: "AI 盘后点评",
			aiRole: "A 股盘后分析师", aiMaxWords: "200",
			triggers: false,
		}
	case "afternoon_close":
		return preMarketSessionMeta{
			session: "afternoon_close", emoji: "🌇", titleLabel: "收盘行情",
			sectionLabel: "实时行情", aiLabel: "AI 收盘点评",
			aiRole: "A 股收盘分析师", aiMaxWords: "200",
			triggers: false,
		}
	default: // pre_market
		return preMarketSessionMeta{
			session: "pre_market", emoji: "🌅", titleLabel: "盘前全景",
			sectionLabel: "隔夜行情", aiLabel: "AI 盘前研判",
			aiRole: "A 股盘前分析师", aiMaxWords: "200",
			triggers: true,
		}
	}
}

func (s *MarketBriefingSource) fetchPreMarket(parent context.Context) ([]*model.Message, error) {
	if !IsAShareBroadlyActive(time.Now()) {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(parent, 4*time.Minute)
	defer cancel()

	now := time.Now()
	session := preMarketSession(now)
	meta := preMarketMeta(session)

	// 1. 行情 quotes（海外 + 商品 + A股大盘）
	overseasSymbols := []string{"SPY", "QQQ", "^VIX", "^HSI", "^HSTECH", "CL=F", "GC=F", "SI=F", "BTC-USD", "000001.SS", "399001.SZ", "399006.SZ"}
	overseasData := s.fetchQuotesBlock(ctx, overseasSymbols)

	// 2. 隔夜异动触发（仅盘前时段）
	var triggerSignals []overseasSignal
	if meta.triggers {
		triggerSignals = s.overseasTriggerCheck(ctx)
	}

	goldSilverLine := s.fetchGoldSilverRatio(ctx)
	forexLines := s.fetchForexLines(ctx)

	// 4. AI 叙事
	narrative := s.preMarketNarrative(ctx, session, overseasData)
	if len(overseasData) == 0 && len(triggerSignals) == 0 && strings.TrimSpace(narrative) == "" {
		return nil, nil
	}

	// 5. 渲染
	content := s.renderPreMarket(meta, overseasData, triggerSignals, goldSilverLine, forexLines, narrative)
	// ID 含运行时刻 HHMM:冷启动 catch-up 补跑旧槽时 session 由 time.Now() 判定会漂移,
	// 若只用 session+日期,补跑消息会与后续真实槽位 ID 撞车被 Stage1 去重误杀;
	// 带 HHMM 后补跑与真实槽各得唯一 ID(补跑消息标签可能偏差,但真实推送不被吞)。
	id := fmt.Sprintf("pre_market_%s_%s_%s", session, now.Format("20060102"), now.Format("1504"))
	tags := []string{"A股", meta.titleLabel}
	msg := &model.Message{
		Type:       model.TypeAnalysis,
		ID:         id,
		Title:      fmt.Sprintf("%s %s · %s", meta.emoji, meta.titleLabel, now.Format("01-02")),
		Content:    content,
		Source:     s.name,
		SourceType: "market-briefing",
		CreateTime: now,
		FetchTime:  now,
		Tags:       tags,
	}
	msg.SetMetadata("display_source", meta.titleLabel)
	return []*model.Message{msg}, nil
}

func (s *MarketBriefingSource) renderPreMarket(
	meta preMarketSessionMeta,
	overseas []string,
	triggerSignals []overseasSignal,
	goldSilverLine string,
	forexLines []string,
	narrative string,
) string {
	var b strings.Builder

	// 行情块（隔夜/实时）
	b.WriteString(fmt.Sprintf("── %s ──\n", meta.sectionLabel))
	if len(overseas) > 0 {
		for _, line := range overseas {
			b.WriteString(line + "\n")
		}
	} else {
		b.WriteString("暂无数据\n")
	}
	b.WriteString("\n")

	// 隔夜异动触发（仅盘前有数据,自然过滤）
	if len(triggerSignals) > 0 {
		b.WriteString("── 隔夜异动触发 ──\n")
		for _, sig := range triggerSignals {
			icon := "🟢"
			if sig.ChangePct < 0 {
				icon = "🔴"
			}
			b.WriteString(fmt.Sprintf("%s %s %+.2f%% → %s\n",
				icon, sig.Ticker, sig.ChangePct, strings.Join(sig.Sectors, ", ")))
		}
		b.WriteString("\n")
	}

	// 金银比 + 汇率

	if goldSilverLine != "" {
		b.WriteString(goldSilverLine + "\n")
	}
	if len(forexLines) > 0 {
		for _, l := range forexLines {
			b.WriteString(l + "\n")
		}
	}
	b.WriteString("\n")

	if narrative != "" {
		b.WriteString(FormatLLMSection("🤖 "+meta.aiLabel, narrative))
	}
	return b.String()
}

func (s *MarketBriefingSource) preMarketNarrative(
	ctx context.Context,
	session string,
	overseas []string,
) string {
	if s.llmProvider == nil {
		return ""
	}

	meta := preMarketMeta(session)

	var lines []string
	if session == "pre_market" {
		lines = append(lines, "【隔夜海外行情】")
	} else {
		lines = append(lines, "【实时行情】")
	}
	lines = append(lines, overseas...)

	var prompt string
	if session == "pre_market" {
		prompt = fmt.Sprintf(`你是资深 A 股盘前分析师，请基于以下数据做 %s 字以内盘前研判:

%s

要求:
1) 隔夜海外对 A 股的影响判断 (利好/中性/压力)
2) 今日关键日历事件对大盘的影响
3) 今日重点关注方向和风险提示
4) 直接输出，不要前置寒暄。`, meta.aiMaxWords, strings.Join(lines, "\n"))
	} else {
		prompt = fmt.Sprintf(`你是资深%s，请基于以下实时行情数据做%s字以内%s:

%s

要求:
1) 当前行情概览与主要走向
2) 板块与风格特征
3) 下午/次日关键观察和风险提示
4) 直接输出，不要前置寒暄。`, meta.aiRole, meta.aiMaxWords, meta.aiLabel, strings.Join(lines, "\n"))
	}

	return LLMSummarizeWithSource(ctx, s.llmProvider, s.name, prompt, "thinking", 40*time.Second)
}

// ── closing: 收盘总览 (16:10) ──
// 市场总览 + 板块轮动 + 资金面 + 龙虎榜 + 情绪 + AI 复盘

func (s *MarketBriefingSource) fetchClosing(parent context.Context) ([]*model.Message, error) {
	if !IsAShareBroadlyActive(time.Now()) {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()

	var specs []ScriptSpec
	if s.sectionEnabled("sentiment") {
		specs = append(specs, ScriptSpec{Key: "sentiment", Cmd: s.pythonCmd,
			Args:    []string{s.scriptsDir + "/market_sentiment.py", "--json-only"},
			Timeout: 3 * time.Minute, Optional: true})
	}
	if s.sectionEnabled("hot_sector") {
		specs = append(specs, ScriptSpec{Key: "hot_sector", Cmd: s.pythonCmd,
			Args:    []string{s.scriptsDir + "/hot_sector_push.py", "--json-only", "--top", "5"},
			Timeout: 3 * time.Minute, Optional: true})
	}
	if s.sectionEnabled("capital_flow") {
		specs = append(specs, ScriptSpec{Key: "capital_flow", Cmd: s.pythonCmd,
			Args:    []string{s.scriptsDir + "/capital_flow.py", "--json-only"},
			Timeout: 2 * time.Minute, Optional: true})
	}
	if s.sectionEnabled("lhb") {
		specs = append(specs, ScriptSpec{Key: "lhb", Cmd: s.pythonCmd,
			Args:    []string{s.scriptsDir + "/lhb.py", "--json-only"},
			Timeout: 2 * time.Minute, Optional: true})
	}
	results := RunScripts(ctx, SetSpecsSourceName(specs, s.name))

	var sentiment marketSentimentReport
	var hotSector hotSectorEveningReport
	var capFlow capitalFlowEveningReport
	var lhb lhbEveningReport

	_ = GetJSON(results, "sentiment", &sentiment)
	_ = GetJSON(results, "hot_sector", &hotSector)
	_ = GetJSON(results, "capital_flow", &capFlow)
	_ = GetJSON(results, "lhb", &lhb)

	// A/H 恐慌温度：官方波指缺位，用 20 日已实现波动率自算
	panicLines := s.renderPanic(ctx)

	// AI 收盘叙事
	narrative := s.closingNarrative(ctx, &sentiment, &hotSector, &lhb, &capFlow)
	if len(sentiment.Indices) == 0 && sentiment.Breadth.UpCount == 0 &&
		sentiment.Breadth.DownCount == 0 && sentiment.Volume.Total == 0 &&
		sentiment.Sentiment.Level == "" && len(hotSector.Sectors) == 0 &&
		len(lhb.Stocks) == 0 && capFlow.Margin == nil && len(capFlow.MainCapitalInflow) == 0 &&
		len(panicLines) == 0 && strings.TrimSpace(narrative) == "" {
		return nil, nil
	}

	// 可选段脚本失败不能静默：明确标出哪一段缺数据，避免误读为"今天没行情"。
	var failedSections []string
	for _, key := range []string{"sentiment", "hot_sector", "capital_flow", "lhb"} {
		if r, ok := results[key]; ok && r.Err != nil {
			failedSections = append(failedSections, key)
			slog.Warn("closing section failed", "source", s.name, "section", key,
				"latency", r.Latency.Round(time.Millisecond), "error", r.Err)
		}
	}
	content := s.renderClosing(&sentiment, &hotSector, &lhb, &capFlow, panicLines, narrative, failedSections)
	now := time.Now()
	id := fmt.Sprintf("closing_briefing_%s", now.Format("20060102"))
	msg := &model.Message{
		Type:       model.TypeAnalysis,
		ID:         id,
		Title:      fmt.Sprintf("📊 收盘总览 · %s", now.Format("01-02")),
		Content:    content,
		Source:     s.name,
		SourceType: "market-briefing",
		CreateTime: now,
		FetchTime:  now,
		Tags:       []string{"A股", "收盘", "总览", "复盘"},
	}
	msg.SetMetadata("display_source", "收盘总览")
	return []*model.Message{msg}, nil
}

func (s *MarketBriefingSource) renderClosing(
	sen *marketSentimentReport,
	hs *hotSectorEveningReport,
	lhb *lhbEveningReport,
	cf *capitalFlowEveningReport,
	panicLines []string,
	narrative string,
	failedSections []string,
) string {
	var b strings.Builder

	// 市场总览（脚本失败时整段省略，不留空标题）
	hasOverview := len(sen.Indices) > 0 || sen.Breadth.UpCount > 0 || sen.Breadth.DownCount > 0 ||
		sen.Volume.Total > 0 || sen.Sentiment.Level != ""
	if hasOverview {
		b.WriteString("── 市场总览 ──\n")
	}
	if len(sen.Indices) > 0 {
		parts := make([]string, 0, len(sen.Indices))
		for _, ix := range sen.Indices {
			parts = append(parts, fmt.Sprintf("%s %+.2f%%", ix.Name, ix.ChangePct))
		}
		b.WriteString("指数: " + strings.Join(parts, " / ") + "\n")
	}
	if sen.Breadth.UpCount > 0 || sen.Breadth.DownCount > 0 {
		b.WriteString(fmt.Sprintf("涨跌比: %d / %d | 涨停 %d / 跌停 %d | 炸板率 %.0f%%\n",
			sen.Breadth.UpCount, sen.Breadth.DownCount,
			sen.Limits.ZtCount, sen.Limits.DtCount, sen.Limits.ZbRate))
	}
	if sen.Volume.Total > 0 {
		b.WriteString(fmt.Sprintf("成交额: %.0f亿 (%+.1f%%)\n", sen.Volume.Total, sen.Volume.ChangePct))
	}
	if sen.Sentiment.Level != "" {
		b.WriteString(fmt.Sprintf("情绪: %s (%.1f)\n", sen.Sentiment.Level, sen.Sentiment.Score))
	}
	if hasOverview {
		b.WriteString("\n")
	}

	// 热门板块
	if len(hs.Sectors) > 0 {
		b.WriteString("── 板块轮动 ──\n")
		for i, sec := range hs.Sectors {
			if i >= 5 {
				break
			}
			b.WriteString(fmt.Sprintf("🔥 %s (+%.2f%%)\n", sec.Name, sec.ChangePct))
			for j, l := range sec.Leaders {
				if j >= 2 {
					break
				}
				b.WriteString(fmt.Sprintf("   · [%s] %s %+.2f%%\n", l.Code, l.Name, l.ChangePct))
			}
		}
		b.WriteString("\n")
	}

	// 资金面
	hasMargin := cf.Margin != nil && cf.Margin.MarginBalance > 0
	if hasMargin || len(cf.MainCapitalInflow) > 0 {
		b.WriteString("── 资金面 ──\n")
		if hasMargin {
			b.WriteString(fmt.Sprintf("两融余额: %.0f亿 (%+.2f%%)\n",
				cf.Margin.MarginBalance, cf.Margin.TotalChangePct))
		}
		if len(cf.MainCapitalInflow) > 0 {
			b.WriteString("主力净流入 Top5:\n")
			for i, c := range cf.MainCapitalInflow {
				if i >= 5 {
					break
				}
				b.WriteString(fmt.Sprintf("   · [%s] %s +%.1f亿\n", c.Code, c.Name, c.NetInflow))
			}
		}
		b.WriteString("\n")
	}

	// 龙虎榜
	if len(lhb.Stocks) > 0 {
		b.WriteString("── 龙虎榜 ──\n")
		for i, st := range lhb.Stocks {
			if i >= 5 {
				break
			}
			b.WriteString(fmt.Sprintf("• [%s] %s %+.2f%% 净%s%.1f亿",
				st.Code, st.Name, st.ChangePct, netSign(st.NetAmount), st.NetAmount/1e8))
			if st.Reason != "" {
				b.WriteString(" · " + st.Reason)
			}
			b.WriteString("\n")
		}
		if len(lhb.Institution.BuyStocks) > 0 {
			b.WriteString("机构买入: ")
			parts := make([]string, 0, len(lhb.Institution.BuyStocks))
			for i, st := range lhb.Institution.BuyStocks {
				if i >= 3 {
					break
				}
				parts = append(parts, st.Name)
			}
			b.WriteString(strings.Join(parts, ", ") + "\n")
		}
		b.WriteString("\n")
	}

	// A/H 恐慌温度（自算 RV20，官方波指缺位）
	if len(panicLines) > 0 {
		b.WriteString("── A/H 恐慌温度 ──\n")
		for _, line := range panicLines {
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	if narrative != "" {
		b.WriteString(FormatLLMSection("🤖 AI 复盘", narrative))
	}
	if len(failedSections) > 0 {
		b.WriteString("\n⚠️ 数据源异常：" + strings.Join(failedSections, ", ") + "\n")
	}
	return b.String()
}

func (s *MarketBriefingSource) closingNarrative(
	ctx context.Context,
	sen *marketSentimentReport,
	hs *hotSectorEveningReport,
	lhb *lhbEveningReport,
	cf *capitalFlowEveningReport,
) string {
	if s.llmProvider == nil {
		return ""
	}

	var lines []string

	if len(sen.Indices) > 0 {
		idx := make([]string, 0, len(sen.Indices))
		for _, ix := range sen.Indices {
			idx = append(idx, fmt.Sprintf("%s %+.2f%%", ix.Name, ix.ChangePct))
		}
		lines = append(lines, "指数: "+strings.Join(idx, " / "))
	}
	if sen.Breadth.UpCount > 0 || sen.Breadth.DownCount > 0 {
		lines = append(lines, fmt.Sprintf("涨跌: %d/%d, 涨停 %d / 跌停 %d, 炸板率 %.0f%%",
			sen.Breadth.UpCount, sen.Breadth.DownCount,
			sen.Limits.ZtCount, sen.Limits.DtCount, sen.Limits.ZbRate))
	}
	if sen.Sentiment.Level != "" {
		lines = append(lines, fmt.Sprintf("情绪: %s (%.1f)", sen.Sentiment.Level, sen.Sentiment.Score))
	}
	if len(hs.Sectors) > 0 {
		topSec := make([]string, 0, 3)
		for i, sec := range hs.Sectors {
			if i >= 3 {
				break
			}
			topSec = append(topSec, fmt.Sprintf("%s(%+.2f%%)", sec.Name, sec.ChangePct))
		}
		lines = append(lines, "热门板块: "+strings.Join(topSec, ", "))
	}
	if lhb != nil && len(lhb.Stocks) > 0 {
		lines = append(lines, fmt.Sprintf("龙虎榜 Top: %s (净%s%.1f亿)",
			lhb.Stocks[0].Name, netSign(lhb.Stocks[0].NetAmount), lhb.Stocks[0].NetAmount/1e8))
	}
	if cf != nil && cf.Margin != nil && cf.Margin.MarginBalance > 0 {
		lines = append(lines, fmt.Sprintf("两融 %.0f亿 (%+.2f%%)",
			cf.Margin.MarginBalance, cf.Margin.TotalChangePct))
	}

	if len(lines) == 0 {
		return ""
	}

	prompt := fmt.Sprintf(`你是资深 A 股分析师，请基于今日收盘数据做一份 300 字以内收盘复盘:

%s

要求:
1) 概括今日主要走势与风格特征 (1-2 句话)
2) 板块轮动方向: 领涨板块是否有延续性，领跌板块是否有反弹基础
3) 资金面分析: 机构/游资/散户情绪，两融变化信号
4) 龙虎榜活跃度及机构动向
5) 明日关键观察和风险提示
6) 直接输出，不要前置寒暄。`, strings.Join(lines, "\n"))

	return LLMSummarizeWithSource(ctx, s.llmProvider, s.name, prompt, "thinking", 60*time.Second)
}

// ── aggregate: 新闻聚合简报 (digest news_aggregate 桶) ──

func (s *MarketBriefingSource) fetchAggregate(parent context.Context) ([]*model.Message, error) {
	ctx, cancel := context.WithTimeout(parent, 4*time.Minute)
	defer cancel()

	// 2. 租 digest news_aggregate 桶
	var summary string
	var leaseID string
	if s.digestStore != nil {
		var lease *digest.Lease
		var err error
		if topicStore, ok := s.digestStore.(digest.TopicStore); ok {
			lease, err = topicStore.LeaseTopics(ctx, digest.NewsAggregate, digestBriefingItemLimit, 30*time.Minute)
		} else {
			lease, err = s.digestStore.Lease(ctx, digest.NewsAggregate, digestBriefingItemLimit, 30*time.Minute)
		}
		if err != nil {
			return nil, fmt.Errorf("lease digest: %w", err)
		}
		if lease != nil {
			summary = digest.RenderTopics(lease.Topics, digestBriefingItemLimit)
			leaseID = lease.ID
		}
	}

	// 3. 拼装
	if summary == "" {
		return nil, nil
	}
	now := time.Now()
	var b strings.Builder
	b.WriteString("## 资讯摘要\n" + summary + "\n")
	msg := &model.Message{
		Type:       model.TypeAnalysis,
		ID:         fmt.Sprintf("news_aggregate_%s", now.Format("20060102")),
		Title:      fmt.Sprintf("📰 新闻聚合 · %s", now.Format("01-02")),
		Content:    b.String(),
		Source:     s.name,
		SourceType: "market-briefing",
		CreateTime: now,
		FetchTime:  now,
		Tags:       []string{"新闻", "聚合"},
	}
	// 租到 digest 桶则走 durable digest 投递(与 us-preview 相同路径)
	if leaseID != "" {
		msg.SetMetadata("digest_lease_id", leaseID)
		msg.SetMetadata("digest_briefing", string(digest.NewsAggregate))
	}
	msg.SetMetadata("display_source", "新闻聚合")
	return []*model.Message{msg}, nil
}

// ── us_preview: 美盘前瞻 (21:10) ──
// fedwatch + VIX + 关键标的行情 + AI 叙事

func (s *MarketBriefingSource) fetchUSPreview(parent context.Context) ([]*model.Message, error) {
	if !usWeekday(time.Now()) {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(parent, 4*time.Minute)
	defer cancel()

	// 1. 宏观脚本
	var specs []ScriptSpec
	if s.includeFedwatch {
		specs = append(specs, ScriptSpec{Key: "fedwatch", Cmd: s.pythonCmd,
			Args:    []string{s.scriptsDir + "/fedwatch.py", "--json-only"},
			Timeout: 90 * time.Second, Optional: true})
	}
	specs = append(specs, ScriptSpec{Key: "vix_term", Cmd: s.pythonCmd,
		Args:    []string{s.scriptsDir + "/vix_term.py", "--json-only"},
		Timeout: 60 * time.Second, Optional: true})
	results := RunScripts(ctx, SetSpecsSourceName(specs, s.name))

	var fedwatchRaw string
	var vixTermRaw string
	if r, ok := results["fedwatch"]; ok && r.Err == nil {
		fedwatchRaw = string(r.Raw)
	}
	if r, ok := results["vix_term"]; ok && r.Err == nil {
		vixTermRaw = string(r.Raw)
	}
	// 2. 关键标的行情（含夜盘期货、汇率与中概）
	ussymbols := []string{"SPY", "QQQ", "KWEB", "FXI", "ES=F", "NQ=F", "GC=F", "CL=F", "BTC-USD", "USDCNH=X"}
	quoteLines := s.fetchQuotesBlock(ctx, ussymbols)
	preLines := s.fetchExtendedLines(ctx, []string{"SPY", "QQQ", "KWEB", "FXI"}, "盘前")
	rateLines := fetchRatesBlock(ctx)
	chinaNight := chinaNightLines(ctx, "")

	// 3. AI 叙事
	narrative := s.usPreviewNarrative(ctx, quoteLines, fedwatchRaw, vixTermRaw)
	if len(quoteLines) == 0 && strings.TrimSpace(fedwatchRaw) == "" &&
		strings.TrimSpace(vixTermRaw) == "" && strings.TrimSpace(narrative) == "" {
		return nil, nil
	}

	content := s.renderUSPreview(preLines, quoteLines, rateLines, chinaNight, fedwatchRaw, vixTermRaw, narrative)
	now := time.Now()
	id := fmt.Sprintf("us_preview_%s", now.Format("20060102"))
	msg := &model.Message{
		Type:       model.TypeAnalysis,
		ID:         id,
		Title:      fmt.Sprintf("🌎 美盘前瞻 · %s", now.Format("01-02")),
		Content:    content,
		Source:     s.name,
		SourceType: "market-briefing",
		CreateTime: now,
		FetchTime:  now,
		Tags:       []string{"美股", "盘前", "宏观", "前瞻"},
	}
	msg.SetMetadata("display_source", "美盘前瞻")
	return []*model.Message{msg}, nil
}

func (s *MarketBriefingSource) renderUSPreview(
	preLines []string,
	quotes []string,
	rateLines []string,
	chinaNight []string,
	fedwatchRaw string,
	vixTermRaw string,
	narrative string,
) string {
	var b strings.Builder

	if len(preLines) > 0 {
		b.WriteString("── 盘前 ──\n")
		for _, line := range preLines {
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	// 关键标的行情
	b.WriteString("── 关键标的 ──\n")
	if len(quotes) > 0 {
		for _, line := range quotes {
			b.WriteString(line + "\n")
		}
	} else {
		b.WriteString("暂无数据\n")
	}
	b.WriteString("\n")

	if len(chinaNight) > 0 {
		b.WriteString("── 中国夜盘 ──\n")
		for _, line := range chinaNight {
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	if len(rateLines) > 0 {
		b.WriteString("── 利率与美元 ──\n")
		for _, line := range rateLines {
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	// FedWatch 利率预期 (formatted for chat)
	if fw := formatFedWatch(fedwatchRaw); fw != "" {
		b.WriteString("── 利率路径 ──\n")
		b.WriteString(fw + "\n\n")
	}

	// VIX 期限结构 (formatted for chat)
	if vt := formatVIXTerm(vixTermRaw); vt != "" {
		b.WriteString("── VIX ──\n")
		b.WriteString(vt + "\n\n")
	}

	if narrative != "" {
		b.WriteString(FormatLLMSection("🤖 AI 前瞻研判", narrative))
	}
	return b.String()
}

// ── us_close: 美盘收盘 + 盘后 + 夜盘 + A股映射（美东 20:05 后）──

type usCloseData struct {
	indexLines    []string
	rateLines     []string
	sectorLines   []string
	afterLines    []string
	nightLines    []string
	chinaLines    []string
	earningsLines []string
	mappingLines  []string
}

func (s *MarketBriefingSource) fetchUSClose(parent context.Context) ([]*model.Message, error) {
	if !usWeekday(time.Now()) {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()

	indexLines := s.fetchQuotesBlock(ctx, []string{"^GSPC", "^IXIC", "^DJI", "^VIX"})
	rateLines := fetchRatesBlock(ctx)
	nightLines := s.fetchQuotesBlock(ctx, []string{"ES=F", "NQ=F", "YM=F", "GC=F", "CL=F", "HG=F", "BTC-USD", "USDCNH=X"})
	sectorChanges := s.quoteChanges(ctx, spdrSectorSymbols)
	sectorLines := append(summarizeSectors(sectorChanges), s.breadthLines(ctx, sectorChanges)...)
	chinaLines := s.fetchQuotesBlock(ctx, []string{"KWEB", "FXI", "SOXX"})
	chinaLines = append(chinaLines, chinaNightLines(ctx, "收盘")...)
	var earningsLines []string
	if s.includeEarnings {
		earningsLines = fetchEarningsBlock(ctx, time.Now().In(newYorkLocation()).Format("2006-01-02"), s.earningsMinCap)
	}
	afterLines := s.fetchExtendedLines(ctx, []string{"SPY", "QQQ", "KWEB", "FXI"}, "盘后")
	mappingLines := s.ashareMappingLines(ctx)

	data := usCloseData{
		indexLines: indexLines, rateLines: rateLines, sectorLines: sectorLines,
		afterLines: afterLines, nightLines: nightLines, chinaLines: chinaLines,
		earningsLines: earningsLines, mappingLines: mappingLines,
	}
	narrative := s.usCloseNarrative(ctx, data)
	if len(indexLines) == 0 && len(nightLines) == 0 && len(afterLines) == 0 &&
		strings.TrimSpace(narrative) == "" {
		return nil, nil
	}

	content := renderUSClose(data, narrative)
	now := time.Now()
	// 收盘简报在北京早上发出，标美东交易日才对得上"哪一天的收盘"。
	ny := now.In(newYorkLocation())
	msg := &model.Message{
		Type:       model.TypeAnalysis,
		ID:         fmt.Sprintf("us_close_%s", ny.Format("20060102")),
		Title:      fmt.Sprintf("🌎 美盘收盘 · %s（含盘后）", ny.Format("01-02")),
		Content:    content,
		Source:     s.name,
		SourceType: "market-briefing",
		CreateTime: now,
		FetchTime:  now,
		Tags:       []string{"美股", "收盘", "盘后", "夜盘", "映射"},
	}
	msg.SetMetadata("display_source", "美盘收盘")
	return []*model.Message{msg}, nil
}

func renderUSClose(data usCloseData, narrative string) string {
	var b strings.Builder
	writeLines := func(title string, lines []string, empty string) {
		b.WriteString(title + "\n")
		if len(lines) == 0 {
			b.WriteString(empty + "\n")
		}
		for _, line := range lines {
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}
	writeLines("── 收盘指数 ──", data.indexLines, "暂无数据")
	writeLines("── 利率与美元 ──", data.rateLines, "暂无数据")
	writeLines("── 板块轮动 ──", data.sectorLines, "暂无数据")
	writeLines("── 盘后/盘前（扩展时段）──", data.afterLines, "扩展时段无成交或数据源不可用")
	writeLines("── 夜盘期货与汇率 ──", data.nightLines, "暂无数据")
	writeLines("── 中国与半导体 ──", data.chinaLines, "暂无数据")
	if len(data.earningsLines) > 0 {
		writeLines("── 今日盘后财报（大市值）──", data.earningsLines, "")
	}
	b.WriteString("注：个股盘后与 Blue Ocean 夜盘不在行情源覆盖内，\n")
	b.WriteString("ETF 盘后用 Nasdaq 收盘后报价，期货与汇率代理隔夜方向。\n\n")
	if len(data.mappingLines) > 0 {
		writeLines("── 对明日 A 股/港股映射（仅方向参考）──", data.mappingLines, "")
	}
	if narrative != "" {
		b.WriteString(FormatLLMSection("🤖 AI 收盘复盘", narrative))
	}
	return b.String()
}

func (s *MarketBriefingSource) usCloseNarrative(ctx context.Context, data usCloseData) string {
	if s.llmProvider == nil {
		return ""
	}
	var lines []string
	appendBlock := func(title string, items []string) {
		if len(items) == 0 {
			return
		}
		lines = append(lines, "【"+title+"】")
		lines = append(lines, items...)
	}
	appendBlock("收盘指数", data.indexLines)
	appendBlock("利率与美元", data.rateLines)
	appendBlock("板块轮动", data.sectorLines)
	appendBlock("盘后", data.afterLines)
	appendBlock("夜盘期货与汇率", data.nightLines)
	appendBlock("中国与半导体", data.chinaLines)
	appendBlock("今日盘后财报", data.earningsLines)
	appendBlock("对A股/港股映射", data.mappingLines)
	prompt := fmt.Sprintf(`你是资深美股收盘分析师，请基于以下数据做 280 字以内收盘复盘:

%s

要求:
1) 指数与风格（科技/价值/防御）表现
2) 盘后与夜盘期货指向，是否预示明日开盘方向
3) VIX 与风险偏好
4) 结合"对A股/港股映射"给条件式判断：只说明哪些板块受影响、方向如何，不要点名个股、不要预测点位
5) 直接输出，不要前置寒暄。`, strings.Join(lines, "\n"))
	return LLMSummarizeWithSource(ctx, s.llmProvider, s.name+"-us-close", prompt, "thinking", 50*time.Second)
}

// ── us_overnight: 中国白天美股夜盘快照（挂在 A 股收盘简报里）──

func (s *MarketBriefingSource) fetchUSOvernight(parent context.Context) ([]*model.Message, error) {
	if !usWeekday(time.Now()) {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()

	nightLines := s.fetchQuotesBlock(ctx, []string{"ES=F", "NQ=F", "USDCNH=X", "GC=F", "CL=F"})
	extLines := s.fetchExtendedLines(ctx, []string{"SPY", "QQQ", "KWEB", "FXI"}, "夜盘")
	mapping := s.ashareMappingLines(ctx)
	if len(nightLines) == 0 && len(extLines) == 0 && len(mapping) == 0 {
		return nil, nil
	}
	var b strings.Builder
	b.WriteString("── 夜盘期货与汇率 ──\n")
	for _, line := range nightLines {
		b.WriteString(line + "\n")
	}
	if len(extLines) > 0 {
		b.WriteString("\n── ETF 夜盘/盘前 ──\n")
		for _, line := range extLines {
			b.WriteString(line + "\n")
		}
	}
	if len(mapping) > 0 {
		b.WriteString("\n── 对明日 A 股映射（仅方向参考）──\n")
		for _, line := range mapping {
			b.WriteString(line + "\n")
		}
	}
	b.WriteString("\n注：个股 Blue Ocean 夜盘不在行情源覆盖内，用期货与 ETF 代理。\n")

	now := time.Now()
	msg := &model.Message{
		Type:       model.TypeAnalysis,
		ID:         fmt.Sprintf("us_overnight_%s", now.In(newYorkLocation()).Format("20060102")),
		Title:      fmt.Sprintf("🌃 美股夜盘 · %s", now.In(newYorkLocation()).Format("01-02")),
		Content:    b.String(),
		Source:     s.name,
		SourceType: "market-briefing",
		CreateTime: now,
		FetchTime:  now,
		Tags:       []string{"美股", "夜盘", "映射"},
	}
	msg.SetMetadata("display_source", "美股夜盘")
	return []*model.Message{msg}, nil
}

// ── 盘前/盘后/夜盘扩展时段报价（Nasdaq 公开接口，失败静默降级）──

var spdrSectorSymbols = []string{"XLK", "XLF", "XLE", "XLV", "XLY", "XLP", "XLI", "XLU", "XLB", "XLRE", "XLC"}

var spdrSectorNames = map[string]string{
	"XLK": "科技", "XLF": "金融", "XLE": "能源", "XLV": "医疗", "XLY": "可选消费",
	"XLP": "必需消费", "XLI": "工业", "XLU": "公用", "XLB": "材料", "XLRE": "地产", "XLC": "通信",
}

type nasdaqQuotePayload struct {
	Data struct {
		MarketStatus string `json:"marketStatus"`
		PrimaryData  struct {
			LastSalePrice      string `json:"lastSalePrice"`
			PercentageChange   string `json:"percentageChange"`
			LastTradeTimestamp string `json:"lastTradeTimestamp"`
		} `json:"primaryData"`
		SecondaryData struct {
			LastSalePrice string `json:"lastSalePrice"`
		} `json:"secondaryData"`
	} `json:"data"`
}

func (s *MarketBriefingSource) fetchExtendedLines(ctx context.Context, symbols []string, label string) []string {
	lines := make([]string, len(symbols))
	var wg sync.WaitGroup
	for i, sym := range symbols {
		wg.Add(1)
		go func(i int, sym string) {
			defer wg.Done()
			price, changePct, status, ok := fetchNasdaqExtended(ctx, sym)
			if !ok {
				return
			}
			sessionLabel := label
			switch strings.ToLower(status) {
			case "pre-market":
				sessionLabel = "盘前"
			case "after-hours":
				sessionLabel = "盘后"
			}
			icon := "🔹"
			if changePct > 0.05 {
				icon = "🟢"
			} else if changePct < -0.05 {
				icon = "🔴"
			}
			name := quoteDisplayNames[sym]
			if name == "" {
				name = sym
			}
			lines[i] = fmt.Sprintf("%s %s %s %.2f (%+.2f%%)", icon, sessionLabel, name, price, changePct)
		}(i, sym)
	}
	wg.Wait()
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func fetchNasdaqExtended(ctx context.Context, symbol string) (float64, float64, string, bool) {
	assetClass := "etf"
	endpoint := fmt.Sprintf("https://api.nasdaq.com/api/quote/%s/info?assetclass=%s", symbol, assetClass)
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return 0, 0, "", false
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/120 Safari/537.36")
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, 0, "", false
	}
	var payload nasdaqQuotePayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0, 0, "", false
	}
	price := parseMoney(payload.Data.PrimaryData.LastSalePrice)
	prev := parseMoney(payload.Data.SecondaryData.LastSalePrice)
	if price <= 0 || prev <= 0 || price == prev {
		return 0, 0, "", false
	}
	change := parsePercent(payload.Data.PrimaryData.PercentageChange)
	if change == 0 && price != prev {
		change = (price - prev) / prev * 100
	}
	return price, change, payload.Data.MarketStatus, true
}

// formatEPS 把 Nasdaq 的 (0.05) / $1.43 转成中文可读的 -0.05 / 1.43。
func formatEPS(value string) string {
	value = strings.TrimSpace(value)
	negative := strings.HasPrefix(value, "(") && strings.HasSuffix(value, ")")
	value = strings.Trim(value, "()")
	value = strings.TrimPrefix(value, "$")
	if negative && !strings.HasPrefix(value, "-") {
		value = "-" + value
	}
	return value
}

func parseMoney(value string) float64 {
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "$"))
	value = strings.ReplaceAll(value, ",", "")
	v, _ := strconv.ParseFloat(value, 64)
	return v
}

func parsePercent(value string) float64 {
	value = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(value), "%"))
	value = strings.TrimPrefix(value, "+")
	v, _ := strconv.ParseFloat(value, 64)
	return v
}

func (s *MarketBriefingSource) quoteChanges(ctx context.Context, symbols []string) map[string]float64 {
	changes := make(map[string]float64, len(symbols))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, sym := range symbols {
		wg.Add(1)
		go func(sym string) {
			defer wg.Done()
			q, err := s.marketService.GetMarketQuote(ctx, sym)
			if err != nil || q == nil {
				return
			}
			mu.Lock()
			changes[sym] = q.ChangePercent
			mu.Unlock()
		}(sym)
	}
	wg.Wait()
	return changes
}

func summarizeSectors(changes map[string]float64) []string {
	type row struct {
		symbol string
		change float64
	}
	rows := make([]row, 0, len(changes))
	for sym, change := range changes {
		rows = append(rows, row{sym, change})
	}
	if len(rows) == 0 {
		return nil
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].change > rows[j].change })
	format := func(rows []row) string {
		parts := make([]string, 0, len(rows))
		for _, r := range rows {
			name := spdrSectorNames[r.symbol]
			if name == "" {
				name = r.symbol
			}
			parts = append(parts, fmt.Sprintf("%s %+.2f%%", name, r.change))
		}
		return strings.Join(parts, " · ")
	}
	top := rows
	if len(top) > 3 {
		top = top[:3]
	}
	bottom := rows
	if len(bottom) > 3 {
		bottom = bottom[len(bottom)-3:]
		for i, j := 0, len(bottom)-1; i < j; i, j = i+1, j-1 {
			bottom[i], bottom[j] = bottom[j], bottom[i]
		}
	}
	return []string{"领涨：" + format(top), "领跌：" + format(bottom)}
}

// breadthLines 自己算宽度：11 个 SPDR 板块的涨跌家数，以及等权 vs 市值的差值。
// 免费源没有稳定的 NYSE/Nasdaq 涨跌家数，板块宽度是当前可得的最可靠代理。
func (s *MarketBriefingSource) breadthLines(ctx context.Context, sectors map[string]float64) []string {
	up, down := 0, 0
	for _, change := range sectors {
		switch {
		case change > 0:
			up++
		case change < 0:
			down++
		}
	}
	if up+down == 0 {
		return nil
	}
	lines := []string{fmt.Sprintf("板块宽度：%d 涨 %d 跌（共 %d 个板块）", up, down, up+down)}
	changes := s.quoteChanges(ctx, []string{"RSP", "SPY"})
	rsp, okRSP := changes["RSP"]
	spy, okSPY := changes["SPY"]
	if okRSP && okSPY {
		jump := "中性"
		if rsp-spy > 0.3 {
			jump = "偏强（等权跑赢市值）"
		} else if rsp-spy < -0.3 {
			jump = "偏弱（市值跑赢等权）"
		}
		lines = append(lines, fmt.Sprintf("等权 %+.2f%% vs 市值 %+.2f%% → 宽度%s", rsp, spy, jump))
	}
	return lines
}

// ── 今日盘后财报（Nasdaq 日历，非官方接口，失败静默降级）──

type nasdaqEarningsPayload struct {
	Data struct {
		Rows []struct {
			Time        string `json:"time"`
			Symbol      string `json:"symbol"`
			Name        string `json:"name"`
			MarketCap   string `json:"marketCap"`
			EPSForecast string `json:"epsForecast"`
		} `json:"rows"`
	} `json:"data"`
}

func fetchEarningsBlock(ctx context.Context, date string, minCap float64) []string {
	endpoint := fmt.Sprintf("https://api.nasdaq.com/api/calendar/earnings?date=%s", date)
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/120 Safari/537.36")
	req.Header.Set("Accept", "application/json")
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var payload nasdaqEarningsPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil
	}
	var out []string
	for _, row := range payload.Data.Rows {
		if row.Time != "time-after-hours" {
			continue
		}
		cap := parseMoney(row.MarketCap)
		if cap < minCap {
			continue
		}
		eps := strings.TrimSpace(row.EPSForecast)
		if eps == "" || eps == "N/A" {
			eps = "暂无"
		}
		out = append(out, fmt.Sprintf("盘后 %s（市值 %.0f 亿美元，预期每股收益 %s 美元）", row.Symbol, cap/1e8, formatEPS(eps)))
		if len(out) >= 6 {
			break
		}
	}
	return out
}

// ashareMappingLines 把美股锚点映射到 A 股/港股板块与产业链方向。
func (s *MarketBriefingSource) ashareMappingLines(ctx context.Context) []string {
	changes := s.quoteChanges(ctx, []string{"^IXIC", "SOXX", "KWEB", "CL=F", "GC=F", "HG=F", "USDCNH=X"})
	stocks := s.quoteChanges(ctx, []string{"NVDA", "AAPL", "TSLA"})
	out := renderAshareMapping(changes, stocks, s.ashareStocks)
	// 美债用收益率变化（百分点）判断，ChangePercent 是收益率的百分比，不适合直接比。
	if q, err := dsyahoo.GetQuote(ctx, "^TNX"); err == nil && q != nil && q.PrevClose > 0 {
		delta := q.Last - q.PrevClose
		if delta >= 0.05 {
			out = append(out, fmt.Sprintf("美债 10Y +%.2fpp → 成长股估值承压、红利相对占优", delta))
		} else if delta <= -0.05 {
			out = append(out, fmt.Sprintf("美债 10Y %.2fpp → 成长股估值压力缓解", delta))
		}
	}
	if len(out) == 0 {
		out = append(out, "各锚点变动均未达到阈值，暂无明确方向映射")
	}
	return out
}

var defaultAshareChains = map[string]string{
	"SOXX": "半导体设备/材料：北方华创、中微公司、沪硅产业",
	"KWEB": "港股互联网：腾讯、阿里、美团",
	"CL=F": "石化/油服：中国石油、中国海油、中海油服",
	"GC=F": "黄金：山东黄金、紫金矿业、中金黄金",
	"HG=F": "铜：紫金矿业、江西铜业、铜陵有色",
	"NVDA": "算力/光模块/PCB：中际旭创、新易盛、沪电股份、工业富联",
	"AAPL": "苹果链：立讯精密、歌尔股份、蓝思科技",
	"TSLA": "特斯拉链：拓普集团、三花智控、旭升集团",
}

func renderAshareMapping(changes, stocks map[string]float64, overrides map[string]string) []string {
	type rule struct {
		symbol    string
		threshold float64
		label     string
		up        string
		down      string
		chain     bool
	}
	rules := []rule{
		{"^IXIC", 0.5, "纳指", "创业板/科创、AI 算力情绪偏暖", "创业板/科创、AI 算力承压", false},
		{"SOXX", 0.8, "费城半导体", "A 股半导体/设备/PCB 偏暖", "A 股半导体/设备/PCB 承压", true},
		{"KWEB", 0.8, "中概互联网", "港股互联网、A 股相关映射偏暖", "港股互联网、A 股相关映射承压", true},
		{"CL=F", 1.5, "原油", "石油石化/油服偏强", "石油石化/油服偏弱", true},
		{"GC=F", 0.8, "黄金", "黄金股偏强", "黄金股偏弱", true},
		{"HG=F", 1.0, "铜", "有色/铜产业链偏强", "有色/铜产业链偏弱", true},
		{"USDCNH=X", 0.2, "美元/离岸人民币", "人民币走弱，出口链相对受益、北向承压", "人民币走强，出口链承压、北向偏暖", false},
	}
	var out []string
	for _, r := range rules {
		change, ok := changes[r.symbol]
		if !ok || absFloat(change) < r.threshold {
			continue
		}
		text := r.down
		if change > 0 {
			text = r.up
		}
		line := fmt.Sprintf("%s %+.2f%% → %s", r.label, change, text)
		if r.chain {
			line += "（" + chainFor(r.symbol, overrides) + "）"
		}
		out = append(out, line)
	}

	stockRules := []struct {
		symbol    string
		threshold float64
		label     string
	}{
		{"NVDA", 2.0, "英伟达"},
		{"AAPL", 1.5, "苹果"},
		{"TSLA", 2.5, "特斯拉"},
	}
	for _, r := range stockRules {
		change, ok := stocks[r.symbol]
		if !ok || absFloat(change) < r.threshold {
			continue
		}
		out = append(out, fmt.Sprintf("%s %+.2f%% → %s", r.label, change, chainFor(r.symbol, overrides)))
	}
	return out
}

func chainFor(symbol string, overrides map[string]string) string {
	if overrides != nil {
		if text, ok := overrides[symbol]; ok && text != "" {
			return text
		}
	}
	if text, ok := defaultAshareChains[symbol]; ok {
		return text
	}
	return "产业链关注"
}

func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// usWeekday 按美东日期判断是否美股交易日：北京周六对应美东周五（应推送），
// 北京周一对应美东周日（应跳过），所以不能用本地星期。
func usWeekday(now time.Time) bool {
	wd := now.In(newYorkLocation()).Weekday()
	return wd != time.Saturday && wd != time.Sunday
}

func (s *MarketBriefingSource) usPreviewNarrative(
	ctx context.Context,
	quotes []string,
	fedwatchRaw string,
	vixTermRaw string,
) string {
	if s.llmProvider == nil {
		return ""
	}

	var lines []string
	lines = append(lines, "【关键标的行情】")
	lines = append(lines, quotes...)

	if fedwatchRaw != "" {
		lines = append(lines, fmt.Sprintf("【FedWatch利率预期】%s", truncateStr(fedwatchRaw, 600)))
	}
	if vixTermRaw != "" {
		lines = append(lines, fmt.Sprintf("【VIX期限结构】%s", truncateStr(vixTermRaw, 600)))
	}

	prompt := fmt.Sprintf(`你是资深美股盘前分析师，请基于以下数据做 250 字以内美盘前瞻研判:

%s

要求:
1) 盘前核心矛盾与驱动因素
2) 利率预期对市场的影响（科技/价值/防御风格轮动）
3) VIX 反映的市场恐慌/贪婪信号
4) 今晚关键风险/事件关注
5) 直接输出，不要前置寒暄。`, strings.Join(lines, "\n"))

	return LLMSummarizeWithSource(ctx, s.llmProvider, s.name+"-us", prompt, "thinking", 50*time.Second)
}

// ── 工具方法 ──

// ── 港股收盘与夜期（新浪公开行情，非官方接口，失败静默降级）──

type sinaQuote struct {
	price float64
	prev  float64
	pct   float64
}

func parseSinaHQ(body string) map[string]sinaQuote {
	out := make(map[string]sinaQuote)
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "var hq_str_") {
			continue
		}
		eq := strings.Index(line, "=\"")
		if eq < 0 {
			continue
		}
		code := strings.TrimPrefix(line[:eq], "var hq_str_")
		rest := line[eq+2:]
		if end := strings.LastIndex(rest, "\""); end >= 0 {
			rest = rest[:end]
		}
		fields := strings.Split(rest, ",")
		if strings.HasPrefix(code, "hf_") {
			if len(fields) < 8 {
				continue
			}
			price := parseFloat(fields[0])
			prev := parseFloat(fields[7])
			if price <= 0 {
				continue
			}
			pct := 0.0
			if prev > 0 {
				pct = (price - prev) / prev * 100
			}
			out[code] = sinaQuote{price: price, prev: prev, pct: pct}
			continue
		}
		if len(fields) < 9 {
			continue
		}
		price := parseFloat(fields[6])
		prev := parseFloat(fields[3])
		if price <= 0 {
			continue
		}
		out[code] = sinaQuote{price: price, prev: prev, pct: parseFloat(fields[8])}
	}
	return out
}

func parseFloat(value string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return v
}

func fetchSinaQuotes(ctx context.Context, codes []string) map[string]sinaQuote {
	endpoint := "https://hq.sinajs.cn/list=" + strings.Join(codes, ",")
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Referer", "https://finance.sina.com.cn")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)")
	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil
	}
	return parseSinaHQ(string(body))
}

func sinaLine(q sinaQuote, label string) string {
	icon := "🔹"
	if q.pct > 0.05 {
		icon = "🟢"
	} else if q.pct < -0.05 {
		icon = "🔴"
	}
	return fmt.Sprintf("%s %s: %.2f (%+.2f%%)", icon, label, q.price, q.pct)
}

// fetchHKClose 港股收盘：恒指/国企/恒生科技/AH 溢价。
func (s *MarketBriefingSource) fetchHKClose(parent context.Context) ([]*model.Message, error) {
	if !hkWeekday(time.Now()) {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	quotes := fetchSinaQuotes(ctx, []string{"rt_hkHSI", "rt_hkHSCEI", "rt_hkHSTECH", "rt_hkHSAHP"})
	if len(quotes) == 0 {
		return nil, nil
	}
	var b strings.Builder
	b.WriteString("── 港股收盘 ──\n")
	for _, item := range []struct {
		code  string
		label string
	}{
		{"rt_hkHSI", "恒生指数"},
		{"rt_hkHSCEI", "国企指数"},
		{"rt_hkHSTECH", "恒生科技"},
	} {
		if q, ok := quotes[item.code]; ok {
			b.WriteString(sinaLine(q, item.label) + "\n")
		}
	}
	if q, ok := quotes["rt_hkHSAHP"]; ok {
		b.WriteString(fmt.Sprintf("\nAH 溢价指数: %.2f (%+.2f%%)，越高说明 A 股相对 H 股越贵\n", q.price, q.pct))
	}
	now := time.Now()
	msg := &model.Message{
		Type:       model.TypeAnalysis,
		ID:         fmt.Sprintf("hk_close_%s", now.Format("20060102")),
		Title:      fmt.Sprintf("🇭🇰 港股收盘 · %s", now.Format("01-02")),
		Content:    b.String(),
		Source:     s.name,
		SourceType: "market-briefing",
		CreateTime: now,
		FetchTime:  now,
		Tags:       []string{"港股", "收盘", "AH溢价"},
	}
	msg.SetMetadata("display_source", "港股收盘")
	return []*model.Message{msg}, nil
}

// chinaNightLines 返回恒指夜期与富时中国 A50 夜盘。suffix 用于收盘后的"收盘"标注。
func chinaNightLines(ctx context.Context, suffix string) []string {
	quotes := fetchSinaQuotes(ctx, []string{"hf_HSI", "hf_CHA50CFD"})
	var out []string
	if q, ok := quotes["hf_HSI"]; ok {
		out = append(out, sinaLine(q, "恒指夜期"+suffix))
	}
	if q, ok := quotes["hf_CHA50CFD"]; ok {
		out = append(out, sinaLine(q, "A50夜盘"+suffix))
	}
	return out
}

func hkWeekday(now time.Time) bool {
	loc, err := time.LoadLocation("Asia/Hong_Kong")
	if err != nil {
		loc = time.FixedZone("HKT", 8*60*60)
	}
	wd := now.In(loc).Weekday()
	return wd != time.Saturday && wd != time.Sunday
}

// quoteDisplayNames 让关键标的在简报里显示可读名称，而不是被截断的 Yahoo 全名。
var quoteDisplayNames = map[string]string{
	"ES=F": "标普期货", "NQ=F": "纳指期货", "YM=F": "道指期货",
	"GC=F": "黄金", "CL=F": "原油", "HG=F": "铜",
	"BTC-USD": "比特币", "USDCNH=X": "离岸人民币",
	"KWEB": "中概互联网ETF", "FXI": "中国大盘ETF", "RSP": "标普等权ETF", "SOXX": "费城半导体ETF",
	"SPY": "标普500ETF", "QQQ": "纳指100ETF", "^GSPC": "标普500", "^IXIC": "纳斯达克",
	"^DJI": "道指", "^VIX": "恐慌指数VIX", "DX-Y.NYB": "美元指数",
	"^HSI": "恒生指数", "^HSTECH": "恒生科技ETF", "SI=F": "白银",
	"000001.SS": "上证指数", "399001.SZ": "深证成指", "399006.SZ": "创业板指",
}

// fetchRatesBlock 用 Yahoo 原始报价取美债收益率：行情服务的 CBOE 适配器会把
// ^TNX 放大 10 倍，直接显示会变成 48.59 这种值。
func fetchRatesBlock(ctx context.Context) []string {
	var out []string
	for _, item := range []struct {
		symbol string
		name   string
	}{{"^TNX", "美债10Y"}, {"^FVX", "美债5Y"}, {"^TYX", "美债30Y"}} {
		q, err := dsyahoo.GetQuote(ctx, item.symbol)
		if err != nil || q == nil || q.Last == 0 {
			continue
		}
		delta := q.Last - q.PrevClose
		icon := "🔹"
		if delta > 0.001 {
			icon = "🔴" // 收益率上行
		} else if delta < -0.001 {
			icon = "🟢"
		}
		out = append(out, fmt.Sprintf("%s %s: %.2f%% (%+.2fpp)", icon, item.name, q.Last, delta))
	}
	if q, err := dsyahoo.GetQuote(ctx, "DX-Y.NYB"); err == nil && q != nil && q.Last > 0 {
		pct := 0.0
		if q.PrevClose > 0 {
			pct = (q.Last - q.PrevClose) / q.PrevClose * 100
		}
		icon := "🔹"
		if pct > 0.05 {
			icon = "🟢"
		} else if pct < -0.05 {
			icon = "🔴"
		}
		out = append(out, fmt.Sprintf("%s 美元指数: %.2f (%+.2f%%)", icon, q.Last, pct))
	}
	return out
}

// fetchQuotesBlock 批量获取行情，返回格式化行
func (s *MarketBriefingSource) fetchQuotesBlock(ctx context.Context, symbols []string) []string {
	// 并发拉取：串行时末位符号(如 BTC-USD)会因共享 ctx 超时被饿死。
	// 结果按下标回填,保持符号顺序稳定。
	lines := make([]string, len(symbols))
	var wg sync.WaitGroup
	for i, sym := range symbols {
		wg.Add(1)
		go func(i int, sym string) {
			defer wg.Done()
			q, err := s.marketService.GetMarketQuote(ctx, sym)
			if err != nil || q == nil || q.Price == 0 {
				return
			}

			icon := "🔹"
			if q.ChangePercent > 0 {
				icon = "🟢"
			} else if q.ChangePercent < 0 {
				icon = "🔴"
			}

			name := q.Name
			if alias, ok := quoteDisplayNames[sym]; ok {
				name = alias
			}
			if name == "" {
				name = sym
			}
			if len([]rune(name)) > 12 {
				name = string([]rune(name)[:12])
			}

			lines[i] = fmt.Sprintf("%s %s: %.2f (%+.2f%%)", icon, name, q.Price, q.ChangePercent)
		}(i, sym)
	}
	wg.Wait()

	var out []string
	for _, line := range lines {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// truncateStr 截断字符串到 maxLen 字节，避免日志炸弹
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// overseasTriggerCheck 检测隔夜美股触发异动，返回超过阈值的信号列表
func (s *MarketBriefingSource) overseasTriggerCheck(ctx context.Context) []overseasSignal {
	var signals []overseasSignal
	for ticker, sectors := range us2cnSector {
		q, err := s.marketService.GetMarketQuote(ctx, ticker)
		if err != nil || q == nil {
			continue
		}
		if abs(q.ChangePercent) >= 5.0 {
			signals = append(signals, overseasSignal{
				Ticker:    ticker,
				ChangePct: q.ChangePercent,
				Sectors:   sectors,
			})
		}
	}
	// sort by absolute change descending
	sort.Slice(signals, func(i, j int) bool {
		return abs(signals[i].ChangePct) > abs(signals[j].ChangePct)
	})
	return signals
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// ─── JSON → human-readable formatters (for Discord / WeChat) ───

type fedwatchData struct {
	EFFR     float64 `json:"effr"`
	Meetings []struct {
		Date    string             `json:"date"`
		Implied float64            `json:"implied"`
		DeltaBP float64            `json:"delta_bp"`
		Probs   map[string]float64 `json:"probs"`
	} `json:"meetings"`
}

func formatFedWatch(raw string) string {
	var d fedwatchData
	if err := json.Unmarshal([]byte(raw), &d); err != nil || len(d.Meetings) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("EFFR %.2f%%", d.EFFR))
	for _, m := range d.Meetings {
		// bias emoji
		bias := "hold"
		for k, v := range m.Probs {
			if m.Probs[bias] < v {
				bias = k
			}
		}
		emoji := map[string]string{"cut": "🟢", "hold": "⚪", "hike": "🔴"}[bias]
		b.WriteString(fmt.Sprintf("  %s %s %5.2f%%", emoji, m.Date[len(m.Date)-5:], m.Implied))
	}
	return b.String()
}

type vixTermData struct {
	VIX    float64            `json:"vix"`
	VIX3M  float64            `json:"vix3m"`
	Ratio  float64            `json:"ratio"`
	Regime string             `json:"regime"`
	Prices map[string]float64 `json:"prices"`
}

func formatVIXTerm(raw string) string {
	var d vixTermData
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s  ", d.Regime))
	// term structure in one line: 9D → VIX → 3M → 6M
	order := []string{"_VIX9D", "_VIX", "_VIX3M", "_VIX6M"}
	labels := map[string]string{"_VIX9D": "9D", "_VIX": "VIX", "_VIX3M": "3M", "_VIX6M": "6M"}
	for _, k := range order {
		if v, ok := d.Prices[k]; ok {
			b.WriteString(fmt.Sprintf("%s:%.1f  ", labels[k], v))
		}
	}
	b.WriteString(fmt.Sprintf("R:%.2f", d.Ratio))
	return b.String()
}

// fetchGoldSilverRatio returns a formatted gold/silver ratio line.
func (s *MarketBriefingSource) fetchGoldSilverRatio(ctx context.Context) string {
	doFetch := func(sym string) float64 {
		q, err := s.marketService.GetMarketQuote(ctx, sym)
		if err != nil || q == nil || q.Price == 0 {
			return 0
		}
		return q.Price
	}
	gold := doFetch("GC=F")
	silver := doFetch("SI=F")
	if gold <= 0 || silver <= 0 {
		return ""
	}
	ratio := gold / silver
	note := " (正常 70-85)"
	if ratio > 85 {
		note = " ⚠️ 白银相对低估"
	} else if ratio < 70 {
		note = " ⚠️ 黄金相对低估"
	}
	return fmt.Sprintf("📊 金银比: %.1f%s", ratio, note)
}

// fetchForexLines returns formatted forex rate lines.
func (s *MarketBriefingSource) fetchForexLines(ctx context.Context) []string {
	pairs := []string{"USDCNY=X", "EURCNY=X", "HKDCNY=X"}
	var lines []string
	for _, sym := range pairs {
		q, err := s.marketService.GetMarketQuote(ctx, sym)
		if err != nil || q == nil || q.Price == 0 {
			continue
		}
		label := strings.TrimSuffix(sym, "=X")
		lines = append(lines, fmt.Sprintf("  %s: %.4f", label, q.Price))
	}
	return lines
}

type marketSentimentReport struct {
	Indices []struct {
		Name      string  `json:"name"`
		Close     float64 `json:"close"`
		ChangePct float64 `json:"change_pct"`
	} `json:"indices"`
	Breadth struct {
		UpCount   int `json:"up_count"`
		DownCount int `json:"down_count"`
	} `json:"breadth"`
	Limits struct {
		ZtCount int     `json:"limit_up"`
		DtCount int     `json:"limit_down"`
		ZbRate  float64 `json:"broken_ratio"`
	} `json:"limits"`
	Volume struct {
		Total     float64 `json:"total"`
		ChangePct float64 `json:"change_pct"`
	} `json:"volume"`
	Sentiment struct {
		Score float64 `json:"score"`
		Level string  `json:"level"`
	} `json:"sentiment"`
}

type hotSectorEveningReport struct {
	Sectors []struct {
		Name      string  `json:"name"`
		Rank      int     `json:"rank"`
		ChangePct float64 `json:"change_pct"`
		Leaders   []struct {
			Code      string  `json:"code"`
			Name      string  `json:"name"`
			ChangePct float64 `json:"change_pct"`
		} `json:"leaders"`
	} `json:"sectors"`
}

type lhbEveningReport struct {
	Stocks []struct {
		Code      string  `json:"code"`
		Name      string  `json:"name"`
		ChangePct float64 `json:"change_pct"`
		NetAmount float64 `json:"net_amount"`
		Reason    string  `json:"reason"`
	} `json:"stocks"`
	Institution struct {
		BuyStocks []struct {
			Code string `json:"code"`
			Name string `json:"name"`
		} `json:"buy_stocks"`
		SellStocks []struct {
			Code string `json:"code"`
			Name string `json:"name"`
		} `json:"sell_stocks"`
	} `json:"institution"`
}

type capitalFlowEveningReport struct {
	Margin *struct {
		MarginBalance  float64 `json:"margin_balance"`
		TotalChangePct float64 `json:"total_change_pct"`
	} `json:"margin"`
	MainCapitalInflow []struct {
		Code      string  `json:"code"`
		Name      string  `json:"name"`
		NetInflow float64 `json:"net_inflow"`
	} `json:"main_capital_inflow"`
}

func netSign(v float64) string {
	if v >= 0 {
		return "+"
	}
	return "-"
}
