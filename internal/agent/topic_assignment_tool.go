package agent

import (
	"context"
	"fmt"
	"mumu-bot/internal/memory"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	flowagent "github.com/cloudwego/eino/flow/agent"
	agentreact "github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

const topicAssignmentToolName = "submitTopicAssignments"

type topicAssignmentCaptureKey struct{}

type topicAssignmentSubmission struct {
	Assignments []topicAssignmentDecision `json:"assignments" jsonschema:"description=逐条消息的话题分配结果"`
}

type topicAssignmentDecision struct {
	MessageKey  string  `json:"message_key" jsonschema:"description=输入消息的编号，例如 m123"`
	Action      string  `json:"action" jsonschema:"enum=no_topic,enum=new,enum=reuse,description=分配动作"`
	TopicID     uint    `json:"topic_id,omitempty" jsonschema:"description=reuse 时填写已有话题 ID"`
	NewTopicKey string  `json:"new_topic_key,omitempty" jsonschema:"description=创建新话题时填写批内新话题临时编号"`
	Reason      string  `json:"reason,omitempty" jsonschema:"description=简短判断理由"`
	Confidence  float64 `json:"confidence,omitempty" jsonschema:"description=0 到 1 的置信度"`
}

type topicAssignmentToolOutput struct {
	Success bool `json:"success"`
}

type topicAssignmentCandidate struct {
	ID            uint
	Status        memory.TopicThreadStatus
	Summary       string
	Tail          string
	Participants  []string
	LastMessageID uint
	Score         float64
}

func withTopicAssignmentTarget(ctx context.Context, target *topicAssignmentSubmission) context.Context {
	return context.WithValue(ctx, topicAssignmentCaptureKey{}, target)
}

func getTopicAssignmentTarget(ctx context.Context) *topicAssignmentSubmission {
	target, _ := ctx.Value(topicAssignmentCaptureKey{}).(*topicAssignmentSubmission)
	return target
}

func newTopicAssignmentTool() (tool.InvokableTool, error) {
	return toolutils.InferTool(
		topicAssignmentToolName,
		"提交批量话题分配结果。必须调用一次，不要输出普通文本。",
		func(ctx context.Context, input *topicAssignmentSubmission) (*topicAssignmentToolOutput, error) {
			target := getTopicAssignmentTarget(ctx)
			if target == nil {
				return nil, fmt.Errorf("话题分配接收器未初始化")
			}
			*target = *input
			if err := agentreact.SetReturnDirectly(ctx); err != nil {
				return nil, err
			}
			return &topicAssignmentToolOutput{Success: true}, nil
		},
	)
}

func newTopicAssignmentExtractor(chatModel model.ToolCallingChatModel) (*agentreact.Agent, error) {
	if chatModel == nil {
		return nil, nil
	}
	assignmentTool, err := newTopicAssignmentTool()
	if err != nil {
		return nil, err
	}
	return agentreact.NewAgent(context.Background(), &agentreact.AgentConfig{
		ToolCallingModel: chatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools:               []tool.BaseTool{assignmentTool},
			ExecuteSequentially: true,
		},
		MaxStep:            4,
		ToolReturnDirectly: map[string]struct{}{topicAssignmentToolName: {}},
	})
}

func buildTopicAssignmentOptions() []flowagent.AgentOption {
	return []flowagent.AgentOption{
		flowagent.WithComposeOptions(
			compose.WithChatModelOption(model.WithToolChoice(schema.ToolChoiceForced, topicAssignmentToolName)),
		),
	}
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
		item.Reason = strings.TrimSpace(item.Reason)
		if item.MessageKey == "" {
			continue
		}
		result = append(result, item)
	}
	return result
}

func buildTopicAssignmentPrompt(groupID int64, messages []topicAssignJob, candidates []topicAssignmentCandidate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "群 %d 有一批新消息需要分配话题。请按时间顺序判断每条消息：\n", groupID)
	b.WriteString("- no_topic: 灌水、纯表情、单字附和、无可持续上下文的消息。\n")
	b.WriteString("- reuse: 归入已有候选话题，topic_id 必须来自候选；候选可能是 active，也可能是 archived。\n")
	b.WriteString("- new: 新话题，使用 new_topic_key；同一新话题多条消息必须复用同一个 key。\n")
	b.WriteString("\n候选话题：\n")
	if len(candidates) == 0 {
		b.WriteString("无\n")
	}
	for _, candidate := range candidates {
		fmt.Fprintf(&b, "topic_id=%d status=%v last_message_log_id=%d", candidate.ID, candidate.Status, candidate.LastMessageID)
		if candidate.Score > 0 {
			fmt.Fprintf(&b, " score=%.3f", candidate.Score)
		}
		b.WriteString("\n")
		if candidate.Summary != "" {
			b.WriteString(candidate.Summary + "\n")
		}
		if candidate.Tail != "" {
			b.WriteString("最近原文：\n" + candidate.Tail + "\n")
		}
		if len(candidate.Participants) > 0 {
			b.WriteString("参与者：" + strings.Join(candidate.Participants, "、") + "\n")
		}
	}
	b.WriteString("\n待分配消息：\n")
	for _, msg := range messages {
		text := ""
		nickname := ""
		if msg.message != nil {
			text = messageTopicText(msg.message)
			nickname = strings.TrimSpace(msg.message.Nickname)
		}
		fmt.Fprintf(&b, "%s %s: %s\n", assignmentMessageKey(msg), nickname, text)
	}
	b.WriteString("\n请调用工具提交 assignments，覆盖每条输入消息。不要输出普通文本。")
	return b.String()
}
