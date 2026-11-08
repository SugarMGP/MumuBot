package llm

import (
	"context"
	"time"

	"mumu-bot/internal/logger"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	callbacktpl "github.com/cloudwego/eino/utils/callbacks"
	"go.uber.org/zap"
)

type modelLogStartKey struct{}

type contextLogEntry struct {
	Role string `json:"role"`
	Kind string `json:"kind,omitempty"`
	Text string `json:"text"`
}

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

func modelLogHandler(ctx context.Context, task, modelName string) callbacks.Handler {
	fields := []zap.Field{zap.String("task", task), zap.String("model", modelName)}
	state, _ := ctx.Value(logStateKey{}).(*LogState)
	if state == nil {
		state = &LogState{}
	}
	if state.seen == nil {
		state.seen = map[string]int{}
	}
	return callbacktpl.NewHandlerHelper().AgenticModel(&callbacktpl.AgenticModelCallbackHandler{
		OnStart: func(ctx context.Context, _ *callbacks.RunInfo, input *model.AgenticCallbackInput) context.Context {
			log := logger.FromContext(ctx).With(fields...)
			if input != nil {
				log = log.With(zap.Int("message_count", len(input.Messages)))
				if logger.ContentEnabled(ctx) {
					entries := dynamicLogContext(input.Messages, state.seen, !state.started)
					if len(entries) > 0 {
						log = log.With(zap.Any("context", entries))
					}
					state.started = true
				}
			}
			log.Info("模型请求开始")
			return context.WithValue(ctx, modelLogStartKey{}, time.Now())
		},
		OnEnd: func(ctx context.Context, _ *callbacks.RunInfo, output *model.AgenticCallbackOutput) context.Context {
			log := logger.FromContext(ctx).With(fields...).With(zap.Duration("duration", modelLogDuration(ctx)))
			if output != nil {
				log = log.With(zap.Any("usage", output.TokenUsage))
				if logger.ContentEnabled(ctx) {
					if text := modelOutputText(output.Message); text != "" {
						log = log.With(zap.String("output", text))
					}
				}
			}
			log.Info("模型请求完成")
			return ctx
		},
		OnError: func(ctx context.Context, _ *callbacks.RunInfo, err error) context.Context {
			logger.FromContext(ctx).With(fields...).Error("模型请求失败", zap.Duration("duration", modelLogDuration(ctx)), zap.Error(err))
			return ctx
		},
	}).Handler()
}

// dynamicLogContext 首次记录动态文本，后续只记新增输入；静态人格、reasoning 和工具历史不重复输出
func dynamicLogContext(messages []*schema.AgenticMessage, seen map[string]int, first bool) []contextLogEntry {
	counts := map[string]int{}
	entries := make([]contextLogEntry, 0)
	for _, message := range messages {
		if message == nil || message.Role == schema.AgenticRoleTypeSystem {
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

func modelOutputText(message *schema.AgenticMessage) string {
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

func modelLogDuration(ctx context.Context) time.Duration {
	started, _ := ctx.Value(modelLogStartKey{}).(time.Time)
	if started.IsZero() {
		return 0
	}
	return time.Since(started)
}
