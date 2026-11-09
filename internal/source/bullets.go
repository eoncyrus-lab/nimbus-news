package source

import (
	"strings"
	"unicode"
)

// formatBulletContent 把长文按句/段切成分点。
//
// ForexLive 的“每日要闻汇总”会把几十条快讯拼成一段上万字符的正文，直接推送
// 很难读。这里按换行、ForexLive 的 "---" 分隔符和句子边界切分，每条一行。
// 只在正文达到 minRunes 时启用，普通 1–3 句快讯保持原文。
func formatBulletContent(text string, minRunes int) string {
	text = strings.TrimSpace(text)
	if minRunes <= 0 {
		minRunes = 400
	}
	if len([]rune(text)) < minRunes {
		return text
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	// ForexLive 用 " --- " 分隔事件块；同时兼容无空格的写法。
	text = strings.ReplaceAll(text, " --- ", "\n")
	text = strings.ReplaceAll(text, "---", "\n")

	var bullets []string
	for _, paragraph := range strings.Split(text, "\n") {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" || strings.HasPrefix(paragraph, "This article was written by") {
			continue
		}
		for _, sentence := range splitSentences(paragraph) {
			sentence = strings.TrimSpace(sentence)
			if sentence == "" {
				continue
			}
			// "Summary:" / "EUROPEAN SESSION" 这类短标签单独成行，作为分节提示。
			bullets = append(bullets, sentence)
		}
	}
	if len(bullets) < 3 {
		return text
	}

	var b strings.Builder
	for i, bullet := range bullets {
		if i > 0 && i%6 == 0 {
			b.WriteString("\n")
		}
		b.WriteString("- ")
		b.WriteString(bullet)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// splitSentences 按中英文句末标点切分。英文句号要求后面是空白，避免切断
// 小数、版本号和 URL；常见缩写（U.S./Inc./Jan.）不切。
func splitSentences(text string) []string {
	runes := []rune(text)
	var out []string
	start := 0
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		boundary := false
		switch r {
		case '。', '！', '？', '!', '?':
			boundary = true
		case '.':
			if i+1 < len(runes) && unicode.IsDigit(runes[i+1]) {
				continue
			}
			if isSentenceAbbreviation(runes[:i]) {
				continue
			}
			if i+1 < len(runes) && !unicode.IsSpace(runes[i+1]) {
				continue
			}
			boundary = true
		}
		if !boundary {
			continue
		}
		sentence := strings.TrimSpace(string(runes[start : i+1]))
		if sentence != "" {
			out = append(out, sentence)
		}
		start = i + 1
	}
	if start < len(runes) {
		if tail := strings.TrimSpace(string(runes[start:])); tail != "" {
			out = append(out, tail)
		}
	}
	return out
}

var sentenceAbbreviations = map[string]struct{}{
	"u.s": {}, "u.k": {}, "mr": {}, "mrs": {}, "ms": {}, "dr": {}, "prof": {},
	"vs": {}, "inc": {}, "ltd": {}, "co": {}, "corp": {}, "llc": {},
	"jan": {}, "feb": {}, "mar": {}, "apr": {}, "jun": {}, "jul": {}, "aug": {},
	"sep": {}, "sept": {}, "oct": {}, "nov": {}, "dec": {},
	"no": {}, "vol": {}, "approx": {}, "est": {}, "fig": {},
	"e.g": {}, "i.e": {}, "a.m": {}, "p.m": {},
}

func isSentenceAbbreviation(prefix []rune) bool {
	word := lastLatinWord(prefix)
	if word == "" {
		return false
	}
	_, ok := sentenceAbbreviations[word]
	return ok
}

// lastLatinWord 取句号前的最后一个拉丁词（允许内部点号，如 U.S）。
func lastLatinWord(runes []rune) string {
	end := len(runes)
	start := end
	for start > 0 {
		r := runes[start-1]
		if unicode.IsLetter(r) || r == '.' || r == '&' {
			start--
			continue
		}
		break
	}
	word := strings.TrimSuffix(string(runes[start:end]), ".")
	return strings.ToLower(word)
}
