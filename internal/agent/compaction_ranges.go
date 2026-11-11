package agent

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"mumu-bot/internal/memory"
)

// ordinalSpan 是候选编号的闭区间
type ordinalSpan struct {
	Start int
	End   int
}

// resolvedCompaction 是候选编号区间还原后的提交结果
type resolvedCompaction struct {
	Topics     []memory.CompactionTopicInput
	Coverage   float64
	Unassigned []int
}

// resolveCompactionTopics 把候选编号区间还原成真实消息引用，并结算覆盖率
func resolveCompactionTopics(candidate *compactionCandidate, topics []CompactionTopic) (*resolvedCompaction, []string, error) {
	total := len(candidate.Refs)
	resolved := &resolvedCompaction{Topics: make([]memory.CompactionTopicInput, 0, len(topics)), Coverage: 1}
	if total == 0 {
		return resolved, nil, nil
	}
	owners := make([]int, total+1)
	seenRefs := make(map[string]bool, len(topics))
	warnings := make([]string, 0)
	for index, topic := range topics {
		ref := strings.TrimSpace(topic.Ref)
		if ref == "" {
			return nil, nil, fmt.Errorf("话题引用为空")
		}
		if seenRefs[ref] {
			return nil, nil, fmt.Errorf("话题引用 %s 重复", ref)
		}
		seenRefs[ref] = true
		if err := memory.ValidateTopicSummary(topic.Summary); err != nil {
			return nil, nil, err
		}
		spans, spanWarnings := parseOrdinalSpans(topic.Ranges, total)
		warnings = append(warnings, spanWarnings...)
		ordinals := make([]int, 0)
		for _, span := range spans {
			for ordinal := span.Start; ordinal <= span.End; ordinal++ {
				switch owners[ordinal] {
				case 0:
					owners[ordinal] = index + 1
					ordinals = append(ordinals, ordinal)
				case index + 1:
					// 同一话题内的重叠区间，直接忽略
				default:
					warnings = append(warnings, fmt.Sprintf("候选 #%d 重复归属，保留先提交的话题", ordinal))
				}
			}
		}
		anchors := make(map[int]bool, len(topic.Anchors))
		for _, anchor := range topic.Anchors {
			if anchor < 1 || anchor > total || owners[anchor] != index+1 {
				warnings = append(warnings, fmt.Sprintf("候选 #%d 不在话题 %s 的区间内，锚点已忽略", anchor, ref))
				continue
			}
			anchors[anchor] = true
		}
		if len(ordinals) == 0 && !isExistingTopicRef(ref) {
			return nil, nil, fmt.Errorf("新话题 %s 没有覆盖任何候选消息", ref)
		}
		messages := make([]memory.TopicMessageRef, 0, len(ordinals))
		for _, ordinal := range ordinals {
			entry := candidate.Refs[ordinal-1]
			messages = append(messages, memory.TopicMessageRef{MessageID: entry.OneBotID, IsAnchor: anchors[ordinal]})
		}
		resolved.Topics = append(resolved.Topics, memory.CompactionTopicInput{Ref: ref, Summary: topic.Summary, Messages: messages})
	}
	if len(topics) == 0 {
		return nil, nil, fmt.Errorf("压缩结果至少需要一个有效话题")
	}
	unassigned := make([]int, 0)
	for ordinal := 1; ordinal <= total; ordinal++ {
		if owners[ordinal] == 0 {
			unassigned = append(unassigned, ordinal)
		}
	}
	resolved.Unassigned = unassigned
	resolved.Coverage = float64(total-len(unassigned)) / float64(total)
	if coverageNeedsRepair(total, len(unassigned)) {
		return nil, warnings, fmt.Errorf("话题覆盖率 %.0f%%，未覆盖 %s；请补充分配后重试",
			resolved.Coverage*100, formatOrdinalRanges(unassigned))
	}
	return resolved, warnings, nil
}

// parseOrdinalSpans 解析模型提交的编号区间，只对两位以内的越界做笔误修正
func parseOrdinalSpans(raw []string, total int) ([]ordinalSpan, []string) {
	spans := make([]ordinalSpan, 0, len(raw))
	warnings := make([]string, 0)
	for _, item := range raw {
		for _, token := range splitOrdinalTokens(item) {
			span, warning, ok := parseOrdinalSpan(token, total)
			if warning != "" {
				warnings = append(warnings, warning)
			}
			if ok {
				spans = append(spans, span)
			}
		}
	}
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].Start < spans[j].Start })
	return spans, warnings
}

func splitOrdinalTokens(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case ',', '，', '、', ';', '；', ' ', '\t', '\n', '\r':
			return true
		}
		return false
	})
}

func parseOrdinalSpan(token string, total int) (ordinalSpan, string, bool) {
	normalized := strings.NewReplacer("~", "-", "～", "-", "—", "-", "－", "-").Replace(strings.TrimSpace(token))
	if normalized == "" {
		return ordinalSpan{}, "", false
	}
	parts := strings.SplitN(normalized, "-", 2)
	start, err := strconv.Atoi(parseOrdinalNumber(parts[0]))
	if err != nil {
		return ordinalSpan{}, fmt.Sprintf("无法识别的编号区间 %q，已忽略", token), false
	}
	end := start
	if len(parts) == 2 {
		value, err := strconv.Atoi(parseOrdinalNumber(parts[1]))
		if err != nil {
			return ordinalSpan{}, fmt.Sprintf("无法识别的编号区间 %q，已忽略", token), false
		}
		end = value
	}
	if start > end {
		return ordinalSpan{}, fmt.Sprintf("编号区间 %q 起点大于终点，已忽略", token), false
	}
	clamped := false
	if start < 1 && 1-start <= 2 {
		start = 1
		clamped = true
	}
	if end > total && end-total <= 2 {
		end = total
		clamped = true
	}
	if start < 1 || end > total || start > end {
		return ordinalSpan{}, fmt.Sprintf("编号区间 %q 超出候选范围 1-%d，已忽略", token, total), false
	}
	if clamped {
		return ordinalSpan{Start: start, End: end}, fmt.Sprintf("编号区间 %q 已按候选范围 1-%d 修正", token, total), true
	}
	return ordinalSpan{Start: start, End: end}, "", true
}

// parseOrdinalNumber 去掉模型可能带上的 # 前缀和“条”后缀
func parseOrdinalNumber(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimLeft(value, "#")
	value = strings.TrimSuffix(value, "条")
	return strings.TrimSpace(value)
}

// formatOrdinalRanges 把编号列表压缩成便于模型修正的区间文本
func formatOrdinalRanges(ordinals []int) string {
	if len(ordinals) == 0 {
		return "无"
	}
	sorted := append([]int(nil), ordinals...)
	sort.Ints(sorted)
	parts := make([]string, 0)
	for index := 0; index < len(sorted); {
		start := sorted[index]
		end := start
		for index+1 < len(sorted) && sorted[index+1] == end+1 {
			index++
			end = sorted[index]
		}
		if start == end {
			parts = append(parts, fmt.Sprintf("#%d", start))
		} else {
			parts = append(parts, fmt.Sprintf("#%d-#%d", start, end))
		}
		index++
		if len(parts) == 20 {
			parts = append(parts, "…")
			break
		}
	}
	return strings.Join(parts, "、")
}

func isExistingTopicRef(ref string) bool {
	if len(ref) < 2 || ref[0] != 't' {
		return false
	}
	value, err := strconv.Atoi(ref[1:])
	return err == nil && value > 0
}
