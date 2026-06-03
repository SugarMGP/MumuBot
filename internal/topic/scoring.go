package topic

import (
	"sort"
	"strings"
	"unicode"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"
)

type topicCandidate struct {
	TopicID            uint
	Status             memory.TopicThreadStatus
	ReplyMatched       bool
	SemanticScore      float64
	ParticipantOverlap float64
	KeywordContinuity  float64
	LastMessageLogID   uint
}

func sortTopicCandidates(candidates []topicCandidate) []topicCandidate {
	sortedCandidates := make([]topicCandidate, len(candidates))
	copy(sortedCandidates, candidates)
	sort.SliceStable(sortedCandidates, func(i, j int) bool {
		left := sortedCandidates[i]
		right := sortedCandidates[j]
		if left.ReplyMatched != right.ReplyMatched {
			return left.ReplyMatched
		}
		leftScore := scoreTopicCandidate(left)
		rightScore := scoreTopicCandidate(right)
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		if left.LastMessageLogID != right.LastMessageLogID {
			return left.LastMessageLogID > right.LastMessageLogID
		}
		return left.TopicID < right.TopicID
	})
	return sortedCandidates
}

func scoreTopicCandidate(candidate topicCandidate) float64 {
	score := candidate.SemanticScore
	score += candidate.ParticipantOverlap * 0.08
	score += candidate.KeywordContinuity * 0.07
	if candidate.ReplyMatched {
		score += 1.0
	}
	return score
}

func topicSemanticSimilarity(query string, topic memory.TopicThread, state *topicRuntimeState) float64 {
	summary := ParseSummary(topic.SummaryJSON)
	summaryText := strings.TrimSpace(summary.Title + "\n" + summary.Gist + "\n" + strings.Join(summary.Facts, "\n"))
	tailText := ""
	if state != nil {
		tailText = renderTopicTailLines(state.tail, TailKeepMessages)
	}
	return max(textSimilarity(query, summaryText), textSimilarity(query, tailText))
}

func topicParticipantOverlap(msg *onebot.GroupMessage, state *topicRuntimeState) float64 {
	if msg == nil {
		return 0
	}
	if state != nil {
		for _, participant := range state.participants {
			if participant.UserID != 0 && participant.UserID == msg.UserID {
				return 1
			}
			if participant.UserID == 0 && participant.Nickname != "" && participant.Nickname == msg.Nickname {
				return 1
			}
		}
	}
	return 0
}

func topicKeywordContinuity(query string, topic memory.TopicThread) float64 {
	summary := ParseSummary(topic.SummaryJSON)
	if len(summary.Keywords) == 0 || strings.TrimSpace(query) == "" {
		return 0
	}
	matched := 0
	for _, keyword := range summary.Keywords {
		keyword = strings.TrimSpace(keyword)
		if keyword == "" {
			continue
		}
		if strings.Contains(query, keyword) {
			matched++
		}
	}
	if matched == 0 {
		return 0
	}
	return float64(matched) / float64(len(summary.Keywords))
}

func textSimilarity(left, right string) float64 {
	left = normalizeTopicText(left)
	right = normalizeTopicText(right)
	if left == "" || right == "" {
		return 0
	}
	if strings.Contains(right, left) || strings.Contains(left, right) {
		return 0.95
	}

	leftSet := runeBigramSet(left)
	rightSet := runeBigramSet(right)
	if len(leftSet) == 0 || len(rightSet) == 0 {
		return 0
	}

	intersection := 0
	for gram := range leftSet {
		if _, ok := rightSet[gram]; ok {
			intersection++
		}
	}
	if intersection == 0 {
		return 0
	}
	union := len(leftSet) + len(rightSet) - intersection
	return float64(intersection) / float64(union)
}

func normalizeTopicText(text string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(text)) {
		if unicode.IsSpace(r) {
			continue
		}
		if unicode.IsPunct(r) {
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

func runeBigramSet(text string) map[string]struct{} {
	runes := []rune(text)
	if len(runes) < 2 {
		return map[string]struct{}{text: {}}
	}
	result := make(map[string]struct{}, len(runes)-1)
	for i := 0; i < len(runes)-1; i++ {
		result[string(runes[i:i+2])] = struct{}{}
	}
	return result
}
