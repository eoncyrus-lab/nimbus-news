package event

import (
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// CanonicalURL 去掉跟踪参数与 fragment，用于高精度身份匹配。
func CanonicalURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return strings.ToLower(raw)
	}
	u.Fragment = ""
	u.Host = strings.ToLower(u.Host)
	u.Scheme = strings.ToLower(u.Scheme)
	q := u.Query()
	for key := range q {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "utm_") || lower == "ref" || lower == "source" ||
			lower == "spm" || lower == "from" || lower == "share" || lower == "fbclid" ||
			lower == "gclid" {
			q.Del(key)
		}
	}
	u.RawQuery = q.Encode()
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String()
}

var (
	urlRE      = regexp.MustCompile(`https?://\S+`)
	bweTailRE  = regexp.MustCompile(`🕒.*$`)
	breakingRE = regexp.MustCompile(`(?i)^\s*(?:breaking|update|urgent)\s*[:：]\s*`)
	cnPrefixRE = regexp.MustCompile(`^\s*(?:快讯|独家|突发|更新)\s*[:：]\s*`)
	spaceRE    = regexp.MustCompile(`\s+`)
	numberRE   = regexp.MustCompile(`[-+]?\d+(?:\.\d+)?\s*(?:%|bp|基点|万亿|亿|万)?`)
)

// TitleCore 生成跨源合并用的标题核心：
// 去掉 BWE 的时间/短链尾巴、行首星号、快讯前缀、URL、标点，并统一小写。
func TitleCore(title string) string {
	s := strings.TrimSpace(title)
	s = bweTailRE.ReplaceAllString(s, "")
	s = urlRE.ReplaceAllString(s, " ")
	s = strings.TrimLeft(s, "* \t")
	s = breakingRE.ReplaceAllString(s, "")
	s = cnPrefixRE.ReplaceAllString(s, "")
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '%':
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteRune(' ')
		default:
			// 其余标点/符号直接丢弃
		}
	}
	return strings.TrimSpace(spaceRE.ReplaceAllString(b.String(), " "))
}

// TitleTokens 把标题切成可比较的 token：拉丁词 + 中文二元组。
func TitleTokens(core string) map[string]struct{} {
	tokens := make(map[string]struct{})
	var latin strings.Builder
	var han []rune
	flushLatin := func() {
		word := strings.ToLower(latin.String())
		latin.Reset()
		if _, stop := stopWords[word]; len(word) >= 2 && !stop {
			tokens[word] = struct{}{}
		}
	}
	flushHan := func() {
		if len(han) == 0 {
			return
		}
		if len(han) == 1 {
			tokens[string(han)] = struct{}{}
		} else {
			for i := 0; i+1 < len(han); i++ {
				tokens[string(han[i:i+2])] = struct{}{}
			}
		}
		han = han[:0]
	}
	for _, r := range core {
		switch {
		case unicode.Is(unicode.Han, r):
			flushLatin()
			han = append(han, r)
		case unicode.IsLetter(r), unicode.IsDigit(r):
			flushHan()
			latin.WriteRune(r)
		default:
			flushLatin()
			flushHan()
		}
	}
	flushLatin()
	flushHan()
	return tokens
}

// Jaccard 返回两个 token 集合的相似度。
func Jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for token := range a {
		if _, ok := b[token]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// NumberSignature 提取带百分号/量级的数字，用于“同一数据事件”判定。
func NumberSignature(text string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, match := range numberRE.FindAllString(strings.ToLower(text), -1) {
		match = strings.ReplaceAll(match, " ", "")
		if len(match) < 2 {
			continue
		}
		// 纯小整数（1、2、3）噪声太大，必须带单位/小数才计入。
		hasUnit := strings.ContainsAny(match, "%亿万个点万亿")
		if !hasUnit && !strings.Contains(match, ".") {
			continue
		}
		out[match] = struct{}{}
	}
	return out
}

// IntersectionSize 返回两个集合的交集大小。
func IntersectionSize(a, b map[string]struct{}) int {
	n := 0
	for item := range a {
		if _, ok := b[item]; ok {
			n++
		}
	}
	return n
}

// entityAliases 把常见写法归一到稳定实体名。只放高价值、低歧义的主体。
var entityAliases = map[string]string{
	"federal reserve": "fed", "the fed": "fed", "美联储": "fed", "fomc": "fed", "powell": "fed", "鲍威尔": "fed",
	"ecb": "ecb", "欧洲央行": "ecb", "lagarde": "ecb", "拉加德": "ecb",
	"boj": "boj", "日本央行": "boj", "ueda": "boj", "植田": "boj",
	"pboc": "pboc", "中国央行": "pboc", "人民银行": "pboc",
	"bank of england": "boe", "boe": "boe", "英国央行": "boe", "bailey": "boe",
	"trump": "trump", "特朗普": "trump",
	"biden": "biden", "拜登": "biden",
	"nvidia": "nvidia", "英伟达": "nvidia", "黄仁勋": "nvidia",
	"openai": "openai", "anthropic": "anthropic", "tesla": "tesla", "特斯拉": "tesla",
	"apple": "apple", "苹果": "apple", "microsoft": "microsoft", "微软": "microsoft",
	"google": "google", "alphabet": "google", "meta": "meta",
	"binance": "binance", "tether": "tether", "bitcoin": "btc", "比特币": "btc",
	"china": "china", "中国": "china", "beijing": "china",
	"united states": "us", "u.s.": "us", "美国": "us", "washington": "us",
	"iran": "iran", "伊朗": "iran", "israel": "israel", "以色列": "israel",
	"russia": "russia", "俄罗斯": "russia", "ukraine": "ukraine", "乌克兰": "ukraine",
	"taiwan": "taiwan", "台湾": "taiwan", "台海": "taiwan",
	"hormuz": "hormuz", "霍尔木兹": "hormuz",
	"opec": "opec", "欧佩克": "opec",
	"japan": "japan", "日本": "japan", "europe": "eu", "欧盟": "eu",
	"uk": "uk", "英国": "uk", "germany": "germany", "德国": "germany",
	"goldman": "goldman", "高盛": "goldman", "jpmorgan": "jpmorgan", "摩根大通": "jpmorgan",
	"blackrock": "blackrock", "贝莱德": "blackrock",
	"sec": "sec", "美国证监会": "sec",
	"treasury": "treasury", "美国财政部": "treasury",
	"congress": "congress", "国会": "congress",
	"vix": "vix", "sp500": "sp500", "s&p 500": "sp500", "标普": "sp500",
	"nasdaq": "nasdaq", "纳斯达克": "nasdaq", "dow": "dow", "道指": "dow",
}

var tickerRE = regexp.MustCompile(`\$([A-Z]{2,6})\b`)

// ExtractEntities 返回规范化后的实体列表（最多 8 个，稳定排序）。
func ExtractEntities(text string) []string {
	lower := strings.ToLower(text)
	seen := make(map[string]struct{})
	for alias, canonical := range entityAliases {
		if containsAlias(lower, alias) {
			seen[canonical] = struct{}{}
		}
	}
	for _, match := range tickerRE.FindAllStringSubmatch(text, -1) {
		seen[strings.ToLower(match[1])] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for entity := range seen {
		out = append(out, entity)
	}
	sort.Strings(out)
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

// shareEntity 返回两个实体集合的交集数量。
func shareEntity(a, b []string) int {
	set := make(map[string]struct{}, len(a))
	for _, item := range a {
		set[item] = struct{}{}
	}
	n := 0
	for _, item := range b {
		if _, ok := set[item]; ok {
			n++
		}
	}
	return n
}

func hashID(parts ...string) string {
	h := fnv64a(strings.Join(parts, "\x1f"))
	return strconv.FormatUint(h, 16)
}

// fnv64a 是 FNV-1a 的轻量实现，避免为事件 ID 引入额外依赖。
func fnv64a(s string) uint64 {
	const (
		offset = 1469598103934665603
		prime  = 1099511628211
	)
	var h uint64 = offset
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}

var stopWords = map[string]struct{}{
	"the": {}, "and": {}, "for": {}, "with": {}, "from": {}, "that": {}, "this": {},
	"has": {}, "have": {}, "will": {}, "are": {}, "was": {}, "were": {}, "its": {},
	"after": {}, "before": {}, "into": {}, "over": {}, "says": {}, "said": {},
	"new": {}, "now": {}, "just": {}, "more": {}, "than": {}, "about": {},
}

// containsAlias 对 ASCII 别名做词边界匹配，避免 "sec" 命中 "second"；
// 中文别名直接做子串匹配。
func containsAlias(text, alias string) bool {
	if alias == "" {
		return false
	}
	if !isASCII(alias) {
		return strings.Contains(text, alias)
	}
	for start := 0; start+len(alias) <= len(text); {
		idx := strings.Index(text[start:], alias)
		if idx < 0 {
			return false
		}
		pos := start + idx
		beforeOK := pos == 0 || !isWordByte(text[pos-1])
		after := pos + len(alias)
		afterOK := after == len(text) || !isWordByte(text[after])
		if beforeOK && afterOK {
			return true
		}
		start = pos + 1
	}
	return false
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
