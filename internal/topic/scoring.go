package topic

import (
	"sort"

	"mumu-bot/internal/memory"
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
	// 回复命中是最强的话题归属信号；语义分是主分，参与者和关键词只做轻微修正。
	score := candidate.SemanticScore
	score += candidate.ParticipantOverlap * 0.08
	score += candidate.KeywordContinuity * 0.07
	if candidate.ReplyMatched {
		score += 1.0
	}
	return score
}
