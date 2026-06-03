package topic

import (
	"fmt"
	"strings"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"
)

const topicPromptTailLines = 4

func renderTopicPromptSection(topic *memory.TopicThread, state *topicRuntimeState) string {
	if topic == nil {
		return ""
	}
	summary := ParseSummary(topic.SummaryJSON)
	title := strings.TrimSpace(summary.Title)
	if title == "" {
		title = fmt.Sprintf("话题 %d", topic.ID)
	}

	var lines []string
	lines = append(lines, "### "+title)
	if gist := strings.TrimSpace(summary.Gist); gist != "" {
		lines = append(lines, "概况："+gist)
	}
	if len(summary.Facts) > 0 {
		lines = append(lines, "已确认："+strings.Join(summary.Facts, "；"))
	}
	if len(summary.Participants) > 0 {
		parts := make([]string, 0, len(summary.Participants))
		for _, participant := range summary.Participants {
			if participant.Nickname == "" || participant.Position == "" {
				continue
			}
			parts = append(parts, participant.Nickname+"："+participant.Position)
		}
		if len(parts) > 0 {
			lines = append(lines, "各方立场："+strings.Join(parts, "；"))
		}
	}
	if len(summary.OpenLoops) > 0 {
		lines = append(lines, "未完事项："+strings.Join(summary.OpenLoops, "；"))
	}
	if len(summary.RecentTurns) > 0 {
		lines = append(lines, "最近摘要："+strings.Join(summary.RecentTurns, "；"))
	}
	if topic.Status == memory.TopicThreadStatusActive && state != nil {
		if tail := renderTopicTailLines(state.tail, topicPromptTailLines); tail != "" {
			lines = append(lines, "关键原文：\n"+tail)
		}
	}
	return strings.Join(lines, "\n")
}

func renderTopicTailLines(tail []*onebot.GroupMessage, limit int) string {
	if limit <= 0 || len(tail) == 0 {
		return ""
	}
	if len(tail) > limit {
		tail = tail[len(tail)-limit:]
	}
	lines := make([]string, 0, len(tail))
	for _, msg := range tail {
		text := messageTopicText(msg)
		if text == "" {
			continue
		}
		lines = append(lines, text)
	}
	return strings.Join(lines, "\n")
}

func renderTopicSummaryForAssignment(topic memory.TopicThread) string {
	summary := ParseSummary(topic.SummaryJSON)
	parts := []string{strings.TrimSpace(summary.Title), strings.TrimSpace(summary.Gist)}
	if len(summary.Facts) > 0 {
		parts = append(parts, strings.Join(summary.Facts, "；"))
	}
	if len(summary.OpenLoops) > 0 {
		parts = append(parts, "未完事项："+strings.Join(summary.OpenLoops, "；"))
	}
	if len(summary.Keywords) > 0 {
		parts = append(parts, "关键词："+strings.Join(summary.Keywords, "，"))
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func participantNames(participants []memory.TopicParticipantRef) []string {
	names := make([]string, 0, len(participants))
	seen := make(map[string]struct{}, len(participants))
	for _, participant := range participants {
		name := strings.TrimSpace(participant.Nickname)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}
