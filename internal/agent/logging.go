package agent

import (
	"context"
	"time"

	"mumu-bot/internal/agentic"

	"github.com/bytedance/sonic"
)

// LogState 在连续消费和自检间共用已记录输入，避免同一轮重复打印
type LogState struct {
	seen    map[string]int
	started bool
}

type logStateKey struct{}

// WithLogState 绑定本轮日志正文的去重状态
func WithLogState(ctx context.Context, state *LogState) context.Context {
	return context.WithValue(ctx, logStateKey{}, state)
}

func logStateFrom(ctx context.Context) *LogState {
	state, _ := ctx.Value(logStateKey{}).(*LogState)
	return state
}

type modelTask struct {
	task      string
	modelName string
}

type modelTaskKey struct{}

// withModelTask 给模型请求日志和统计标注任务与模型名
func withModelTask(ctx context.Context, task, modelName string) context.Context {
	return context.WithValue(ctx, modelTaskKey{}, modelTask{task: task, modelName: modelName})
}

func modelTaskFrom(ctx context.Context) modelTask {
	task, _ := ctx.Value(modelTaskKey{}).(modelTask)
	return task
}

type modelStartKey struct{}

func modelStarted(ctx context.Context) time.Time {
	started, _ := ctx.Value(modelStartKey{}).(time.Time)
	return started
}

type contextLogEntry struct {
	Role string `json:"role"`
	Kind string `json:"kind,omitempty"`
	Text string `json:"text"`
}

// dynamicLogContext 首次记录动态文本，后续只记新增输入；静态人格、reasoning 和工具历史不重复输出
func dynamicLogContext(messages []*agentic.Message, seen map[string]int, first bool) []contextLogEntry {
	counts := map[string]int{}
	entries := make([]contextLogEntry, 0)
	for _, message := range messages {
		if message == nil || message.Role == agentic.RoleSystem {
			continue
		}
		kind, _ := message.Extra["kind"].(string)
		text := ""
		for _, block := range message.ContentBlocks {
			if block == nil {
				continue
			}
			if block.UserInputText != nil {
				text += block.UserInputText.Text
			}
			if first && block.AssistantGenText != nil {
				text += block.AssistantGenText.Text
			}
		}
		if text == "" {
			continue
		}
		entry := contextLogEntry{Role: string(message.Role), Kind: kind, Text: text}
		key, _ := sonic.MarshalString(entry)
		counts[key]++
		if counts[key] > seen[key] {
			entries = append(entries, entry)
		}
	}
	for key, count := range counts {
		seen[key] = max(seen[key], count)
	}
	return entries
}

func modelOutputText(message *agentic.Message) string {
	if message == nil {
		return ""
	}
	text := ""
	for _, block := range message.ContentBlocks {
		if block != nil && block.AssistantGenText != nil {
			text += block.AssistantGenText.Text
		}
	}
	return text
}
