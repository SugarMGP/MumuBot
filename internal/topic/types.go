package topic

const (
	TailKeepMessages    = 8
	assignmentBatchSize = 20
)

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
}
