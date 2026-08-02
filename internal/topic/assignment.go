package topic

import (
	"fmt"
	"strings"
)

type topicAssignmentSubmission struct {
	Assignments []topicAssignmentDecision `json:"assignments" jsonschema:"description=逐条消息的话题分配结果"`
}

type topicAssignmentDecision struct {
	MessageKey  string `json:"message_key" jsonschema:"description=输入消息的编号，例如 m123"`
	Action      string `json:"action" jsonschema:"enum=no_topic,enum=new,enum=reuse,description=分配动作"`
	TopicID     uint   `json:"topic_id,omitempty" jsonschema:"description=reuse 时填写已有话题 ID"`
	NewTopicKey string `json:"new_topic_key,omitempty" jsonschema:"description=创建新话题时填写批内新话题临时编号"`
}

type topicAssignmentCandidate struct {
	ID               uint
	Summary          string
	Tail             string
	LastMessageID    uint
	SourceMessageIDs []uint
}

func normalizeTopicAssignmentSubmission(raw *topicAssignmentSubmission) []topicAssignmentDecision {
	if raw == nil || len(raw.Assignments) == 0 {
		return nil
	}
	result := make([]topicAssignmentDecision, 0, len(raw.Assignments))
	for _, item := range raw.Assignments {
		item.MessageKey = strings.TrimSpace(item.MessageKey)
		item.Action = strings.ToLower(strings.TrimSpace(item.Action))
		item.NewTopicKey = strings.TrimSpace(item.NewTopicKey)
		result = append(result, item)
	}
	return result
}

func buildTopicAssignmentPrompt(groupID int64, messages []topicAssignJob, candidates []topicAssignmentCandidate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "群 %d 有一批新消息需要分配话题。请按时间顺序判断每条消息：\n", groupID)
	b.WriteString("- no_topic: 灌水、纯表情、单字附和、无可持续上下文的消息。\n")
	b.WriteString("- reuse: 归入已有候选话题，topic_id 必须来自候选。\n")
	b.WriteString("- new: 新话题，使用 new_topic_key；同一新话题多条消息必须复用同一个 key。\n")
	b.WriteString("- 每个 message_key 只能返回一次，不能重复。\n")
	b.WriteString("\n候选话题：\n")
	if len(candidates) == 0 {
		b.WriteString("无\n")
	}
	for _, candidate := range candidates {
		fmt.Fprintf(&b, "topic_id=%d last_message_log_id=%d\n", candidate.ID, candidate.LastMessageID)
		if candidate.Summary != "" {
			b.WriteString(candidate.Summary + "\n")
		}
		if candidate.Tail != "" {
			b.WriteString("最近原文：\n" + candidate.Tail + "\n")
		}
	}
	b.WriteString("\n待分配消息：\n")
	for _, msg := range messages {
		fmt.Fprintf(&b, "%s", assignmentMessageKey(msg))
		if msg.replyTopicID != 0 {
			fmt.Fprintf(&b, " reply_topic_id=%d", msg.replyTopicID)
		}
		fmt.Fprintf(&b, " %s: %s\n", msg.nickname, msg.text)
	}
	b.WriteString("\nassignments 必须覆盖每条输入消息，不要遗漏。")
	return b.String()
}
