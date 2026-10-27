package agent

import (
	"context"
	"strings"

	"mumu-bot/internal/memory"

	"github.com/cloudwego/eino/schema"
)

// topicCoverage 计算会话内已被有效话题摘要覆盖的消息
func (s *Session) topicCoverage(ctx context.Context, mem *memory.Manager, entries []sessionEntry) (memory.TopicCoverage, error) {
	ids := make([]uint, 0, len(entries))
	for _, entry := range entries {
		if entry.user != nil && entry.user.LogID != 0 {
			ids = append(ids, entry.user.LogID)
		}
	}
	if mem == nil || len(ids) == 0 {
		return memory.TopicCoverage{Covered: map[uint]bool{}}, nil
	}
	return mem.TopicCoverage(ctx, s.kind, s.targetID, ids)
}

// splitSegments 按真人消息把连续记录切成交互段
func splitSegments(entries []sessionEntry) []*sessionSegment {
	segments := make([]*sessionSegment, 0, len(entries))
	for _, entry := range entries {
		if len(segments) == 0 || entry.user != nil {
			segments = append(segments, &sessionSegment{})
		}
		segment := segments[len(segments)-1]
		segment.items = append(segment.items, entry)
		if entry.user != nil {
			segment.users = append(segment.users, entry.user)
		}
	}
	return segments
}

// segmentCovered 判断一个交互段是否已被话题摘要覆盖
func segmentCovered(segment *sessionSegment, coverage memory.TopicCoverage) bool {
	if len(segment.users) == 0 {
		return false
	}
	for _, user := range segment.users {
		if user.LogID == 0 || !coverage.Covered[user.LogID] {
			return false
		}
	}
	return true
}

// segmentTokens 估算一个交互段的 token 数
func segmentTokens(segment *sessionSegment) int {
	total := 0
	for _, entry := range segment.items {
		total += messageTokens(entry.msg)
	}
	return total
}

// renderTopicDigest 渲染话题摘要块，预算不够时丢掉最早的摘要
func renderTopicDigest(summaries []memory.TopicDigestSummary, budgetTokens int) string {
	if len(summaries) == 0 {
		return ""
	}
	text := buildTopicDigest(summaries)
	for estimateTokens(text) > budgetTokens && len(summaries) > 1 {
		summaries = summaries[1:]
		text = buildTopicDigest(summaries)
	}
	return text
}

// buildTopicDigest 把话题摘要拼成一段文本
func buildTopicDigest(summaries []memory.TopicDigestSummary) string {
	var b strings.Builder
	b.WriteString("【本会话早期摘要】以下是本会话较早内容的要点，只在没有原文可查时参考：\n")
	for _, summary := range summaries {
		title := strings.TrimSpace(summary.Title)
		if title == "" {
			title = "话题"
		}
		b.WriteString("### " + title + "\n")
		if gist := strings.TrimSpace(summary.Gist); gist != "" {
			b.WriteString("概况：" + gist + "\n")
		}
		if len(summary.OpenLoops) > 0 {
			b.WriteString("未完事项：" + strings.Join(summary.OpenLoops, "；") + "\n")
		}
		if len(summary.Participants) > 0 {
			b.WriteString("在场：" + strings.Join(summary.Participants, "、") + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// estimateTokens 粗略估算文本 token 数，中文按每字 2/3、其余按每 4 字符 1 token
func estimateTokens(text string) int {
	if text == "" {
		return 0
	}
	cjk, other := 0, 0
	for _, r := range text {
		if r >= 0x4E00 && r <= 0x9FFF {
			cjk++
			continue
		}
		other++
	}
	return cjk*2/3 + other/4
}

// messageTokens 估算一条消息占用的 token，图片按 base64 长度折算
func messageTokens(message *schema.AgenticMessage) int {
	if message == nil {
		return 0
	}
	total := 0
	for _, block := range message.ContentBlocks {
		if block == nil {
			continue
		}
		switch {
		case block.AssistantGenText != nil:
			total += estimateTokens(block.AssistantGenText.Text)
		case block.UserInputText != nil:
			total += estimateTokens(block.UserInputText.Text)
		case block.Reasoning != nil:
			total += estimateTokens(block.Reasoning.Text)
		case block.FunctionToolCall != nil:
			total += estimateTokens(block.FunctionToolCall.Name + block.FunctionToolCall.Arguments)
		case block.FunctionToolResult != nil:
			for _, part := range block.FunctionToolResult.Content {
				if part == nil {
					continue
				}
				if part.Text != nil {
					total += estimateTokens(part.Text.Text)
				}
				if part.Image != nil {
					total += len(part.Image.Base64Data) / 4
				}
			}
		}
	}
	return total
}
