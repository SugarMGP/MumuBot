package memory

import (
	"strings"
	"unicode"
)

// topicSimilarityThreshold 是话题摘要模糊匹配的下限。阈值偏低会引入弱相关话题，
// 偏高会让改述话题漏召；用真实日志评测后再调。
const topicSimilarityThreshold = 0.30

// textSimilarity 衡量查询词与话题摘要的字符二元组相似度。
// 中文按连续汉字的二元组切分，英文数字按整词切分；精确子串直接给 1。
func textSimilarity(text, query string) float64 {
	text = strings.ToLower(strings.TrimSpace(text))
	query = strings.ToLower(strings.TrimSpace(query))
	if text == "" || query == "" {
		return 0
	}
	if strings.Contains(text, query) {
		return 1
	}
	queryGrams := textGrams(query)
	if len(queryGrams) == 0 {
		return 0
	}
	textGramsSet := textGrams(text)
	if len(textGramsSet) == 0 {
		return 0
	}
	inter := 0
	for gram := range queryGrams {
		if _, ok := textGramsSet[gram]; ok {
			inter++
		}
	}
	if inter == 0 {
		return 0
	}
	coverage := float64(inter) / float64(len(queryGrams))
	dice := float64(2*inter) / float64(len(queryGrams)+len(textGramsSet))
	return 0.7*coverage + 0.3*dice
}

// textGrams 把文本拆成可比较的检索单元：汉字二元组、英文数字整词。
func textGrams(text string) map[string]struct{} {
	grams := map[string]struct{}{}
	han := make([]rune, 0, 8)
	word := make([]rune, 0, 8)
	flushHan := func() {
		switch {
		case len(han) == 1:
			grams[string(han)] = struct{}{}
		case len(han) > 1:
			for i := 0; i+1 < len(han); i++ {
				grams[string(han[i:i+2])] = struct{}{}
			}
		}
		han = han[:0]
	}
	flushWord := func() {
		if len(word) > 0 {
			grams[string(word)] = struct{}{}
		}
		word = word[:0]
	}
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r):
			flushWord()
			han = append(han, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			flushHan()
			word = append(word, r)
		default:
			flushHan()
			flushWord()
		}
	}
	flushHan()
	flushWord()
	return grams
}

// LiteralContainsPattern 把用户输入转成只含字面量的 LIKE/ILIKE contains 模式
func LiteralContainsPattern(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + replacer.Replace(value) + "%"
}
