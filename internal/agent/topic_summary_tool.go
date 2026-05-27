package agent

import (
	"context"
	"fmt"
	"strings"

	"mumu-bot/internal/memory"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/compose"
	flowagent "github.com/cloudwego/eino/flow/agent"
	agentreact "github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

const topicSummaryToolName = "submitTopicSummary"

type topicSummaryCaptureKey struct{}

type topicSummarySubmission struct {
	Title        string                           `json:"title" jsonschema:"description=当前话题标题"`
	Gist         string                           `json:"gist" jsonschema:"description=当前话题一句话摘要"`
	Facts        []string                         `json:"facts,omitempty" jsonschema:"description=当前已确认的稳定事实列表"`
	Participants []memory.TopicSummaryParticipant `json:"participants,omitempty" jsonschema:"description=当前话题中已定位的参与者与其位置"`
	OpenLoops    []string                         `json:"open_loops,omitempty" jsonschema:"description=当前话题里仍未闭合的事项"`
	RecentTurns  []string                         `json:"recent_turns,omitempty" jsonschema:"description=最近几轮关键推进"`
	Keywords     []string                         `json:"keywords,omitempty" jsonschema:"description=用于检索该话题的关键词"`
}

type topicSummaryToolOutput struct {
	Success bool `json:"success"`
}

func withTopicSummaryTarget(ctx context.Context, target *topicSummarySubmission) context.Context {
	return context.WithValue(ctx, topicSummaryCaptureKey{}, target)
}

func getTopicSummaryTarget(ctx context.Context) *topicSummarySubmission {
	target, _ := ctx.Value(topicSummaryCaptureKey{}).(*topicSummarySubmission)
	return target
}

func newTopicSummaryTool() (tool.InvokableTool, error) {
	return toolutils.InferTool(
		topicSummaryToolName,
		"提交当前群聊话题的结构化摘要结果。必须调用一次，不要输出普通文本。",
		func(ctx context.Context, input *topicSummarySubmission) (*topicSummaryToolOutput, error) {
			target := getTopicSummaryTarget(ctx)
			if target == nil {
				return nil, fmt.Errorf("话题摘要接收器未初始化")
			}

			*target = *input
			if err := agentreact.SetReturnDirectly(ctx); err != nil {
				return nil, err
			}
			return &topicSummaryToolOutput{Success: true}, nil
		},
	)
}

func newTopicSummaryExtractor(chatModel model.ToolCallingChatModel) (*agentreact.Agent, error) {
	if chatModel == nil {
		return nil, nil
	}

	summaryTool, err := newTopicSummaryTool()
	if err != nil {
		return nil, err
	}

	agent, err := agentreact.NewAgent(context.Background(), &agentreact.AgentConfig{
		ToolCallingModel: chatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools:               []tool.BaseTool{summaryTool},
			ExecuteSequentially: true,
		},
		MaxStep:            4,
		ToolReturnDirectly: map[string]struct{}{topicSummaryToolName: {}},
	})
	if err != nil {
		return nil, err
	}
	return agent, nil
}

func normalizeTopicSummarySubmission(raw *topicSummarySubmission) memory.TopicSummary {
	summary := memory.EmptyTopicSummary()
	if raw == nil {
		return summary
	}

	summary.Title = strings.TrimSpace(raw.Title)
	summary.Gist = strings.TrimSpace(raw.Gist)
	summary.Facts = compactTopicStrings(raw.Facts, 8)
	summary.OpenLoops = compactTopicStrings(raw.OpenLoops, 8)
	summary.RecentTurns = compactTopicStrings(raw.RecentTurns, 8)
	summary.Keywords = compactTopicStrings(raw.Keywords, 12)

	if len(raw.Participants) > 0 {
		participants := make([]memory.TopicSummaryParticipant, 0, len(raw.Participants))
		for _, participant := range raw.Participants {
			nickname := strings.TrimSpace(participant.Nickname)
			position := strings.TrimSpace(participant.Position)
			if nickname == "" && position == "" {
				continue
			}
			participants = append(participants, memory.TopicSummaryParticipant{
				Nickname: nickname,
				Position: position,
			})
			if len(participants) >= memory.TopicTailKeepMessages {
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

func buildTopicSummaryOptions() []flowagent.AgentOption {
	return []flowagent.AgentOption{
		flowagent.WithComposeOptions(
			compose.WithChatModelOption(model.WithToolChoice(schema.ToolChoiceForced, topicSummaryToolName)),
		),
	}
}
