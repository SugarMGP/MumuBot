package topic

import (
	"errors"
	"strings"

	"mumu-bot/internal/memory"
)

const (
	MaxActiveThreadsPerGroup = 5
	SummaryHistoryLimit      = 5
	TailKeepMessages         = 8
)

var ErrStateChanged = errors.New("topic state changed")

type AssignmentAction string

const (
	AssignmentActionNoTopic AssignmentAction = "no_topic"
	AssignmentActionReuse   AssignmentAction = "reuse"
	AssignmentActionNew     AssignmentAction = "new"
)

type AssignmentBatchItem struct {
	MessageLogID uint
	Action       AssignmentAction
	TopicID      uint
	NewTopicKey  string
	MatchReason  string
	MatchScore   float64
}

type AssignmentBatchInput struct {
	GroupID int64
	Items   []AssignmentBatchItem
}

type AssignmentBatchResult struct {
	UpdatedTopicIDs   []uint
	ArchivedTopicIDs  []uint
	MessageLogIDs     []uint
	NoTopicMessageIDs []uint
}

type ThreadSearchHit struct {
	Topic memory.TopicThread
	Score float64
}

func IsAssignmentProcessed(msg memory.MessageLog) bool {
	return msg.TopicThreadID != 0 || strings.TrimSpace(msg.TopicMatchReason) != ""
}
