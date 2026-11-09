package source

import (
	"regexp"
	"strings"
	"unicode"
)

// 金十信噪比策略。
//
// 旧逻辑是“命中 include 关键词或 important=1 就放行”，宽词会把埃及央行、
// 伊朗外交部表态、LME 库存、商品产量预测全部放进来。这里改成打分：
// 实体（谁）+ 事件（发生了什么）+ 数据发布 + important，再对边缘市场、
// 人事、预告、机构观点和官方表态降权。达到阈值的才进入后续路由。
type jin10ScoreResult struct {
	Score   int
	Reasons []string
	Drop    bool
}

var (
	// 只保留中美欧日（+英国）央行；其他央行出现时按边缘市场降权。
	jin10CoreCentralBanks = []string{
		"美联储", "联邦储备", "欧洲央行", "日本央行", "中国人民银行",
		"人民银行", "中国央行", "英国央行", "英格兰银行",
	}
	jin10ChinaPolicyOrgs = []string{
		"国务院", "政治局", "财政部", "发改委", "商务部", "证监会",
		"海关总署", "人民银行", "中国央行", "国家统计局",
	}
	jin10GlobalOrgs = []string{
		"国际货币基金组织", "IMF", "世界银行", "欧佩克", "OPEC",
	}
	jin10MegaCaps = []string{
		"英伟达", "特斯拉", "苹果", "微软", "谷歌", "亚马逊", "Meta",
		"台积电", "博通", "OpenAI", "软银",
	}
	jin10CoreRegions = []string{
		"美国", "中国", "欧元区", "欧洲", "日本", "英国", "德国", "法国",
	}
	jin10CoreIndicators = []string{
		"非农", "CPI", "PPI", "GDP", "PMI", "失业率", "零售销售",
		"消费者信心", "ISM", "PCE", "核心通胀", "通胀率", "投资者信心指数",
		"工业产出", "M2", "社融", "新增人民币贷款", "贸易帐",
	}
	jin10ReleaseMarkers = []string{
		"公布", "前值", "录得", "同比", "环比", "新增", "降至",
		"升至", "增长", "下降", "上涨", "回落",
	}
	// 一档数据：中美欧日都值得单独推的硬指标，发布时额外加权。
	jin10Tier1Indicators = []string{
		"非农", "CPI", "PPI", "GDP", "PMI", "失业率", "PCE", "社融",
		"新增人民币贷款",
	}
	jin10USDataOrgs = []string{"美国劳工部", "劳工统计局", "美国商务部", "美国经济分析局"}
	jin10EUDataOrgs = []string{"欧盟统计局", "欧元区"}
	jin10JPDataOrgs = []string{"日本总务省", "日本内阁府"}
	jin10CNDataOrgs = []string{"国家统计局", "统计局", "人民银行"}

	jin10RateActions = []string{
		"利率决议", "FOMC", "加息", "降息", "降准", "LPR", "MLF",
		"逆回购", "存款准备金率", "关键利率", "货币政策", "公开市场操作",
		"维持利率", "利率不变",
	}
	jin10PolicyTopics = []string{
		"关税", "制裁", "出口管制", "进口管制", "反制", "实体清单",
		"不可靠实体清单", "禁令", "补贴", "救市", "反倾销",
	}
	jin10PolicyVerbs = []string{
		"宣布", "决定", "生效", "实施", "暂停", "调整", "加征", "延长",
		"取消", "发布", "出台", "落地", "签署",
	}
	jin10GeoEscalations = []string{
		"开战", "宣战", "袭击", "空袭", "导弹", "封锁", "军事行动",
		"地面进攻", "停火协议", "击落", "爆炸", "政变",
	}
	jin10GeoMajor = []string{
		"开战", "宣战", "全面战争", "封锁霍尔木兹", "核打击", "政变",
	}
	jin10MarketMoves = []string{
		"熔断", "暴跌", "暴涨", "崩盘", "违约", "破产", "历史新高",
		"暂停交易", "涨超", "跌逾", "盘前跌", "盘前涨",
	}
	jin10CompanyEvents = []string{
		"财报", "业绩", "收购", "并购", "回购", "IPO", "上市",
	}

	jin10Personnel  = []string{"继任", "人选", "职位", "接替", "候选人", "任命"}
	jin10Preview    = []string{"将于", "十分钟后", "前瞻", "预告", "即将公布"}
	jin10Forecast   = []string{"机构：", "分析师", "研报", "预计", "预期", "展望", "策略"}
	jin10Statements = []string{
		"外交部", "发言人", "克里姆林宫", "据伊朗媒体", "据俄罗斯媒体",
		"据以色列媒体", "据乌克兰媒体", "回应", "否认", "谴责", "调侃",
		"呼吁", "警告", "表态",
	}
	// 否定/降温语境：命中地缘升级词但被明确否定时，不能按“升级”计分。
	jin10Negations = []string{
		"极不可能", "不太可能", "不可能", "不会", "无意", "否认",
		"没有计划", "并非", "尚无", "未计划",
	}
	// 边缘市场：央行/数据默认不出现在中美欧日口径里。
	jin10EmergingMarkets = []string{
		"埃及", "墨西哥", "土耳其", "巴西", "俄罗斯央行", "印度央行",
		"菲律宾", "印尼", "南非", "阿根廷", "智利", "哥伦比亚", "秘鲁",
		"尼日利亚", "巴基斯坦", "匈牙利", "波兰", "捷克", "泰国央行",
		"马来西亚央行", "韩国央行",
	}

	// 促销/导流内容直接丢弃，不参与评分。
	jin10DropPatterns = []string{
		"点击查看", "点击解锁", "一文看懂", "VIP交易提醒", "金十期货图示",
		"直播", "会员", "金十整理", "期货盯盘神器", "专属文章", "APP独家",
	}

	// 这些关键词只在个股语境下算公告噪音；商务部公告、交易所通知不算。
	jin10AmbiguousNoise = map[string]bool{
		"公告": true, "调研": true, "互动平台": true,
		"拟投资": true, "签订合同": true, "项目投资": true,
	}
)

var (
	jin10RoundupSpan     = regexp.MustCompile(`(?is)<span[^>]*class="[^"]*section-news[^"]*"[^>]*>(.*?)</span>`)
	jin10RoundupIndex    = regexp.MustCompile(`^\s*\d+\s*[.、]\s*`)
	jin10PolicyNoiseCtx  = append(append([]string{}, jin10ChinaPolicyOrgs...), "交易所", "上期所", "大商所", "郑商所", "上海国际能源交易中心")
	jin10PolicyNoiseVerb = []string{"决定", "批准", "调整", "暂停", "实施", "发布", "通知", "答记者问", "公告"}
)

// scoreJin10 给单条快讯（或合集里的单条）打分。Drop=true 表示直接丢弃。
func scoreJin10(text string, important int) jin10ScoreResult {
	lower := strings.ToLower(text)
	result := jin10ScoreResult{}
	add := func(score int, reason string) {
		result.Score += score
		if reason != "" {
			result.Reasons = append(result.Reasons, reason)
		}
	}
	hit := func(values []string) string { return firstMatchedKeyword(lower, values) }

	if matched := hit(jin10DropPatterns); matched != "" {
		result.Drop = true
		result.Reasons = append(result.Reasons, "促销:"+matched)
		return result
	}

	coreBank := hit(jin10CoreCentralBanks)
	emerging := hit(jin10EmergingMarkets)
	// “央行开展逆回购/MLF/降准”这类不写“中国人民银行”的操作，按核心央行处理；
	// 但“墨西哥央行/俄罗斯央行”不能套用这条规则。
	if coreBank == "" && emerging == "" && strings.Contains(lower, "央行") && hit(jin10RateActions) != "" {
		coreBank = "央行操作"
	}
	chinaOrg := hit(jin10ChinaPolicyOrgs)
	globalOrg := hit(jin10GlobalOrgs)
	mega := hit(jin10MegaCaps)
	switch {
	case coreBank != "":
		add(30, "核心央行:"+coreBank)
	case chinaOrg != "":
		add(30, "中国政策:"+chinaOrg)
	case globalOrg != "":
		add(15, "国际组织:"+globalOrg)
	}
	if mega != "" {
		add(15, "巨头:"+mega)
	}

	indicator := hit(jin10CoreIndicators)
	// 数据发布要求发布措辞和数值都出现在指标附近；“油价回落…等待CPI”
	// 只有远距离的“回落”和月份，不是数据发布。
	release := indicator != "" && jin10KeywordNear(lower, indicator, jin10ReleaseMarkers, 80) != "" &&
		jin10DigitSoonAfter(lower, indicator, 40)
	region := hit(jin10CoreRegions)
	if region == "" {
		switch {
		case indicator != "" && hit(jin10CNDataOrgs) != "":
			region = "中国"
		case indicator != "" && hit(jin10USDataOrgs) != "":
			region = "美国"
		case indicator != "" && hit(jin10EUDataOrgs) != "":
			region = "欧元区"
		case indicator != "" && hit(jin10JPDataOrgs) != "":
			region = "日本"
		case indicator == "非农" || indicator == "ISM" || indicator == "PCE" || indicator == "零售销售":
			region = "美国"
		}
	}
	if release {
		if region != "" {
			add(10, "核心地区:"+region)
		}
		add(25, "数据发布:"+indicator)
		if jin10IsTier1Indicator(indicator) {
			add(10, "一档数据:"+indicator)
		}
	}

	rateAction := hit(jin10RateActions)
	if rateAction != "" {
		add(30, "利率/货币操作:"+rateAction)
	}
	policyAction := false
	if topic := hit(jin10PolicyTopics); topic != "" && hit(jin10PolicyVerbs) != "" {
		policyAction = true
		add(25, "政策动作:"+topic)
	}
	geoAction := hit(jin10GeoEscalations)
	if geoAction != "" && jin10KeywordNear(lower, geoAction, jin10Negations, 80) != "" {
		// “极不可能开战”这类否定表态按普通表态处理，不计地缘升级。
		geoAction = ""
	}
	if geoAction != "" {
		add(25, "地缘:"+geoAction)
		if major := hit(jin10GeoMajor); major != "" {
			add(15, "重大升级:"+major)
		}
	}
	marketMove := hit(jin10MarketMoves)
	if marketMove != "" {
		add(25, "市场异动:"+marketMove)
	}
	if mega != "" && hit(jin10CompanyEvents) != "" {
		add(15, "公司事件")
	}
	if hit([]string{"OPEC", "欧佩克"}) != "" && hit([]string{"减产", "增产", "决定", "会议"}) != "" {
		add(20, "OPEC")
	}
	if important >= 1 {
		add(10, "important")
	}

	if matched := hit(jin10Personnel); matched != "" {
		add(-25, "人事降权:"+matched)
	}
	if matched := hit(jin10Preview); matched != "" {
		add(-25, "预告降权:"+matched)
	}
	if !release {
		if matched := hit(jin10Forecast); matched != "" {
			add(-25, "观点降权:"+matched)
		}
	}
	if !policyAction && geoAction == "" && marketMove == "" {
		if matched := hit(jin10Statements); matched != "" {
			add(-10, "表态降权:"+matched)
		}
	}
	if emerging != "" && coreBank == "" && chinaOrg == "" && globalOrg == "" {
		add(-40, "边缘市场:"+emerging)
	}
	return result
}

// splitJin10Roundup 把金十的 section-news 多条目合集拆成单条，避免一条合集里
// 某个词（例如“开战”）让整段通过 critical 或评分。没有合集标记时返回原文。
func splitJin10Roundup(rawHTML, cleaned string) []string {
	matches := jin10RoundupSpan.FindAllStringSubmatch(rawHTML, -1)
	if len(matches) == 0 {
		return []string{cleaned}
	}
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		text := strings.TrimSpace(jin10RoundupIndex.ReplaceAllString(htmlToPlainText(match[1]), ""))
		if text != "" {
			out = append(out, text)
		}
	}
	if len(out) == 0 {
		return []string{cleaned}
	}
	return out
}

// jin10NoiseKeyword 判断是否个股公告噪音；政策机构发布的公告/通知不算。
func jin10NoiseKeyword(text string, excludeKeywords []string) string {
	if firstMatchedKeyword(strings.ToLower(text), jin10PolicyNoiseCtx) != "" &&
		matchesAnyKeyword(strings.ToLower(text), jin10PolicyNoiseVerb) {
		return ""
	}
	keyword := firstMatchedKeyword(strings.ToLower(text), excludeKeywords)
	if keyword == "" {
		return ""
	}
	if jin10AmbiguousNoise[keyword] {
		// 个股公告通常同时出现公司/股份/股东等词；没有这些词时先交给评分。
		if !matchesAnyKeyword(strings.ToLower(text), []string{"公司", "股份", "股东", "证券", "董事会", "控股"}) {
			return ""
		}
	}
	return keyword
}

func jin10IsTier1Indicator(indicator string) bool {
	for _, value := range jin10Tier1Indicators {
		if strings.EqualFold(value, indicator) {
			return true
		}
	}
	return false
}

// jin10KeywordNear 判断 others 中是否有词出现在 anchor 附近（字节距离 window 内）。
// 用于把“数据发布措辞贴着指标”和“否定词贴着升级词”区分开，避免远距离误命中。
func jin10KeywordNear(text, anchor string, others []string, window int) string {
	anchor = strings.ToLower(anchor)
	index := strings.Index(text, anchor)
	if index < 0 {
		return ""
	}
	for _, other := range others {
		other = strings.ToLower(other)
		if other == "" {
			continue
		}
		start := 0
		for {
			offset := strings.Index(text[start:], other)
			if offset < 0 {
				break
			}
			pos := start + offset
			distance := pos - index
			if distance < 0 {
				distance = -distance
			}
			if distance <= window {
				return other
			}
			start = pos + 1
		}
	}
	return ""
}

// jin10DigitSoonAfter 判断指标后面紧跟范围（window 字节）内是否出现数值。
func jin10DigitSoonAfter(text, anchor string, window int) bool {
	index := strings.Index(text, strings.ToLower(anchor))
	if index < 0 {
		return false
	}
	tail := text[index+len(strings.ToLower(anchor)):]
	if len(tail) > window {
		tail = tail[:window]
	}
	return strings.IndexFunc(tail, unicode.IsDigit) >= 0
}

func normalizeJin10Content(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
