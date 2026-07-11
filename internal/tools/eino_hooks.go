package tools

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/bytedance/sonic"
	cb "github.com/cloudwego/eino/callbacks"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	callbacktpl "github.com/cloudwego/eino/utils/callbacks"
)

type toolLogStateKey struct{}

type toolLogState struct {
	Input string
}

type duplicateToolOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

var sortedJSONAPI = sonic.Config{SortMapKeys: true}.Froze()

// CanonicalizeToolArguments 标准化工具调用参数，消除空白和字段顺序差异。
func CanonicalizeToolArguments(arguments string) (string, error) {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return "", nil
	}

	var payload any
	if err := sonic.UnmarshalString(trimmed, &payload); err != nil {
		return trimmed, nil
	}

	canonical, err := sortedJSONAPI.MarshalToString(payload)
	if err != nil {
		return "", err
	}
	return canonical, nil
}

// ToolDedupMiddleware 拦截同一轮 think 中完全相同的工具调用。
func ToolDedupMiddleware() compose.InvokableToolMiddleware {
	return func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
		return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
			tc := GetToolContext(ctx)
			if tc == nil {
				return next(ctx, input)
			}

			if tc.IsToolCallSeen(input.Name, input.Arguments) {
				zap.L().Debug("工具调用被中间件去重",
					zap.String("tool", input.Name),
					zap.String("arguments", input.Arguments))
				output, err := sonic.MarshalString(&duplicateToolOutput{
					Success: true,
					Message: fmt.Sprintf("检测到与本轮完全相同的 %s 调用，已忽略", input.Name),
				})
				if err != nil {
					return nil, err
				}
				return &compose.ToolOutput{Result: output}, nil
			}

			output, err := next(ctx, input)
			if err == nil && toolOutputSucceeded(output) {
				tc.MarkToolCallSucceeded(input.Name, input.Arguments)
			}
			return output, err
		}
	}
}

func toolOutputSucceeded(output *compose.ToolOutput) bool {
	if output == nil {
		return false
	}
	var result struct {
		Success *bool `json:"success"`
	}
	if err := sonic.UnmarshalString(output.Result, &result); err != nil || result.Success == nil {
		return true
	}
	return *result.Success
}

// NewToolLogHandler 创建统一的工具调用日志回调。
func NewToolLogHandler() cb.Handler {
	return callbacktpl.NewHandlerHelper().
		Tool(&callbacktpl.ToolCallbackHandler{
			OnStart: func(ctx context.Context, info *cb.RunInfo, input *einotool.CallbackInput) context.Context {
				if input == nil {
					return ctx
				}
				return context.WithValue(ctx, toolLogStateKey{}, &toolLogState{
					Input: input.ArgumentsInJSON,
				})
			},
			OnEnd: func(ctx context.Context, info *cb.RunInfo, output *einotool.CallbackOutput) context.Context {
				if info == nil || output == nil {
					return ctx
				}
				state, _ := ctx.Value(toolLogStateKey{}).(*toolLogState)
				if state != nil {
					LogToolCall(info.Name, state.Input, output.Response, nil)
				} else {
					LogToolCall(info.Name, "", output.Response, nil)
				}
				return ctx
			},
			OnError: func(ctx context.Context, info *cb.RunInfo, err error) context.Context {
				if info == nil {
					return ctx
				}
				state, _ := ctx.Value(toolLogStateKey{}).(*toolLogState)
				if state != nil {
					LogToolCall(info.Name, state.Input, "", err)
				} else {
					LogToolCall(info.Name, "", "", err)
				}
				return ctx
			},
		}).
		Handler()
}
