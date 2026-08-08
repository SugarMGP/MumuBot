package topic

import (
	"strings"

	"mumu-bot/internal/memory"
)

type topicSummarySubmission struct {
	Title        string                    `json:"title" jsonschema:"description=当前话题标题"`
	Gist         string                    `json:"gist" jsonschema:"description=当前话题一句话摘要"`
	Claims       []memory.RawMemoryClaim   `json:"claims,omitempty" jsonschema:"description=带主体、类型、正文和原始消息证据的长期记忆命题"`
	Participants []memory.TopicParticipant `json:"participants,omitempty" jsonschema:"description=当前话题中已定位的参与者与其位置"`
	OpenLoops    []string                  `json:"open_loops,omitempty" jsonschema:"description=当前话题里仍未闭合的事项"`
	RecentTurns  []string                  `json:"recent_turns,omitempty" jsonschema:"description=最近几轮关键推进"`
	Keywords     []string                  `json:"keywords,omitempty" jsonschema:"description=用于检索该话题的关键词"`
}

func normalizeTopicSummarySubmission(raw *topicSummarySubmission) memory.TopicSummary {
	summary := EmptySummary()
	if raw == nil {
		return summary
	}

	summary.Title = strings.TrimSpace(raw.Title)
	summary.Gist = strings.TrimSpace(raw.Gist)
	summary.OpenLoops = compactTopicStrings(raw.OpenLoops, 8)
	summary.RecentTurns = compactTopicStrings(raw.RecentTurns, 8)
	summary.Keywords = compactTopicStrings(raw.Keywords, 12)

	if len(raw.Participants) > 0 {
		participants := make([]memory.TopicParticipant, 0, len(raw.Participants))
		for _, participant := range raw.Participants {
			nickname := strings.TrimSpace(participant.Nickname)
			position := strings.TrimSpace(participant.Position)
			if nickname == "" && position == "" {
				continue
			}
			participants = append(participants, memory.TopicParticipant{
				UserID:   participant.UserID,
				Nickname: nickname,
				Position: position,
			})
			if len(participants) >= TailKeepMessages {
				break
			}
		}
		summary.Participants = participants
	}

	return summary
}

func compactTopicStrings(items []string, limit int) []string {
	if len(items) == 0 {
		return []string{}
	}

	result := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result
}
