package event

import (
	"strings"
)

// ClassRule 类别的基础分与关键词。命中 critical 的词会把分数抬到静默时段可穿透。
type ClassRule struct {
	Class    string   `json:"class" yaml:"class"`
	Base     float64  `json:"base" yaml:"base"`
	Keywords []string `json:"keywords" yaml:"keywords"`
	Critical []string `json:"critical" yaml:"critical"`
}

// Rules 全部路由规则。默认规则在 DefaultRules，可由配置覆盖。
type Rules struct {
	Classes          []ClassRule       `json:"classes" yaml:"classes"`
	SourceTiers      map[string]string `json:"source_tiers" yaml:"source_tiers"`
	CriticalKeywords []string          `json:"critical_keywords" yaml:"critical_keywords"`
}

// DefaultRules 按架构文档 §4 的类别与分值。
func DefaultRules() Rules {
	return Rules{
		Classes: []ClassRule{
			{Class: ClassCentralBank, Base: 90, Keywords: []string{
				"美联储", "央行", "利率决议", "加息", "降息", "降准", "qe", "qt", "流动性",
				"fed", "fomc", "ecb", "boj", "pboc", "boe", "rate decision", "rate cut", "rate hike",
				"quantitative easing", "balance sheet",
			}, Critical: []string{
				"紧急降息", "紧急加息", "紧急流动性", "紧急会议", "emergency rate", "emergency meeting",
				"intermeeting", "surprise cut", "surprise hike",
			}},
			{Class: ClassDataSurprise, Base: 75, Keywords: []string{
				"cpi", "ppi", "pce", "非农", "nonfarm", "nfp", "gdp", "pmi", "通胀", "通缩",
				"经济数据", "就业数据", "零售销售", "consumer price", "producer price", "payroll",
				"unemployment", "jobless claims", "retail sales",
			}, Critical: []string{
				"创纪录", "record high", "record low", "远超预期", "远不及预期",
			}},
			{Class: ClassGeopolitics, Base: 70, Keywords: []string{
				"战争", "制裁", "关税", "停火", "休战", "伊朗", "以色列", "俄罗斯", "乌克兰",
				"台海", "霍尔木兹", "地缘", "军事", "空袭", "导弹",
				"war", "sanction", "tariff", "ceasefire", "truce", "iran", "israel", "russia",
				"ukraine", "taiwan", "hormuz", "military strike", "air strike", "missile",
			}, Critical: []string{
				"战争升级", "全面战争", "核", "封锁海峡", "开战", "宣战",
				"war escalation", "nuclear", "strait closed", "declaration of war",
			}},
			{Class: ClassSystemicCo, Base: 65, Keywords: []string{
				"并购", "收购", "ipo", "破产", "调查", "起诉", "会计", "退市", "重组",
				"merger", "acquisition", "bankruptcy", "probe", "investigation", "sec charges",
				"accounting", "delisting", "restructuring",
			}, Critical: []string{
				"系统性风险", "systemic risk", "too big to fail", "挤兑", "bank run",
			}},
			{Class: ClassMarketRegime, Base: 70, Keywords: []string{
				"熔断", "暴跌", "暴涨", "恐慌", "信用利差", "干预", "救市", "流动性危机",
				"circuit breaker", "crash", "selloff", "panic", "credit spread", "intervention",
				"bailout", "liquidity crisis", "vix",
			}, Critical: []string{
				"熔断", "circuit breaker", "历史新高", "历史新低", "all-time high", "all-time low",
			}},
			{Class: ClassAnalysis, Base: 35, Keywords: []string{
				"分析", "观点", "展望", "策略", "研报", "解读", "点评",
				"analysis", "outlook", "strategist", "research note", "commentary", "kobeissi",
			}},
			{Class: ClassDomesticPolit, Base: 20, Keywords: []string{
				"选举", "弹劾", "人事", "党派", "国会", "白宫", "内阁",
				"election", "impeachment", "congress", "white house", "cabinet", "partisan",
			}},
		},
		SourceTiers: map[string]string{
			"bwe-tradfi":          "wire",
			"bloomberg-markets":   "wire",
			"bloomberg-economics": "wire",
			"forexlive-breaking":  "wire",
			"wallstcn-hk":         "wire",
			"fed-speeches":        "official",
			"fed-press":           "official",
			"kobeissi-letter":     "analysis",
			"guanfu-score":        "analysis",
		},
		CriticalKeywords: []string{
			"fomc", "紧急降息", "紧急加息", "紧急会议", "战争升级", "全面战争", "宣战",
			"熔断", "核打击", "封锁海峡", "emergency rate", "emergency meeting",
			"war escalation", "circuit breaker", "strait closed",
		},
	}
}

// Classify 返回命中最多的类别；同分时按 Classes 顺序（央行优先）。
func (r Rules) Classify(title, content string) (string, []string, bool) {
	text := strings.ToLower(title + "\n" + content)
	best := ""
	bestHits := 0
	var bestMatched []string
	critical := false
	for _, rule := range r.Classes {
		hits := 0
		var matched []string
		for _, keyword := range rule.Keywords {
			if containsAlias(text, strings.ToLower(keyword)) {
				hits++
				matched = append(matched, rule.Class+":"+keyword)
			}
		}
		for _, keyword := range rule.Critical {
			if containsAlias(text, strings.ToLower(keyword)) {
				critical = true
				matched = append(matched, "critical:"+keyword)
			}
		}
		if hits > bestHits {
			best, bestHits, bestMatched = rule.Class, hits, matched
		}
	}
	if best == "" {
		best = ClassUnclassified
	}
	for _, keyword := range r.CriticalKeywords {
		if containsAlias(text, strings.ToLower(keyword)) {
			critical = true
			bestMatched = append(bestMatched, "critical:"+keyword)
		}
	}
	return best, bestMatched, critical
}

// BaseScore 返回类别基础分。
func (r Rules) BaseScore(class string) float64 {
	for _, rule := range r.Classes {
		if rule.Class == class {
			return rule.Base
		}
	}
	return 40 // 未分类默认 defer
}

// SourceBonus 返回源等级加分：wire/official +10，分析 +0。
func (r Rules) SourceBonus(source string) (float64, string) {
	switch r.SourceTiers[source] {
	case "wire", "official":
		return 10, "source:tier"
	case "analysis":
		return 0, "source:analysis"
	default:
		return 0, ""
	}
}

// HasSurprise 判断是否出现“超预期/不及预期”类措辞。
func HasSurprise(text string) bool {
	lower := strings.ToLower(text)
	for _, word := range []string{
		"超预期", "超出预期", "不及预期", "低于预期", "高于预期", "意外", "创纪录",
		"stronger than expected", "weaker than expected", "better than expected",
		"worse than expected", "unexpected", "surprise", "record high", "record low",
	} {
		if containsAlias(lower, word) {
			return true
		}
	}
	return false
}
