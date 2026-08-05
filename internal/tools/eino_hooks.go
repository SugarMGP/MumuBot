package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/bytedance/sonic"
	cb "github.com/cloudwego/eino/callbacks"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	callbacktpl "github.com/cloudwego/eino/utils/callbacks"
	"github.com/eino-contrib/jsonschema"
)

type toolLogStateKey struct{}

type toolLogState struct {
	Input string
}

type duplicateToolOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

var sortedJSONAPI = sonic.Config{SortMapKeys: true, UseNumber: true}.Froze()

// NewToolArgumentsHandler 创建按工具 schema 矫正并标准化参数的处理器。
func NewToolArgumentsHandler(ctx context.Context, toolList []einotool.BaseTool) (func(context.Context, string, string) (string, error), error) {
	schemas := make(map[string]*jsonschema.Schema, len(toolList))
	for _, t := range toolList {
		info, err := t.Info(ctx)
		if err != nil {
			return nil, fmt.Errorf("读取工具参数 schema 失败: %w", err)
		}
		if info == nil || info.Name == "" || info.ParamsOneOf == nil {
			continue
		}
		parameterSchema, err := info.ParamsOneOf.ToJSONSchema()
		if err != nil {
			return nil, fmt.Errorf("转换工具 %s 参数 schema 失败: %w", info.Name, err)
		}
		schemas[info.Name] = parameterSchema
	}

	return func(_ context.Context, name, arguments string) (string, error) {
		return canonicalizeToolArguments(arguments, schemas[name])
	}, nil
}

func canonicalizeToolArguments(arguments string, parameterSchema *jsonschema.Schema) (string, error) {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return "", nil
	}

	var payload any
	if err := sortedJSONAPI.UnmarshalFromString(trimmed, &payload); err != nil {
		return trimmed, nil
	}
	payload = coerceToolArgument(payload, parameterSchema)

	canonical, err := sortedJSONAPI.MarshalToString(payload)
	if err != nil {
		return "", err
	}
	return canonical, nil
}

func coerceToolArgument(value any, parameterSchema *jsonschema.Schema) any {
	if parameterSchema == nil {
		return value
	}

	switch parameterSchema.Type {
	case "object":
		object, ok := value.(map[string]any)
		if !ok || parameterSchema.Properties == nil {
			return value
		}
		for name, fieldValue := range object {
			if fieldSchema, exists := parameterSchema.Properties.Get(name); exists {
				object[name] = coerceToolArgument(fieldValue, fieldSchema)
			}
		}
		return object
	case "array":
		items, ok := value.([]any)
		if !ok || parameterSchema.Items == nil {
			return value
		}
		for i := range items {
			items[i] = coerceToolArgument(items[i], parameterSchema.Items)
		}
		return items
	}

	text, ok := value.(string)
	if !ok {
		return value
	}
	text = strings.TrimSpace(text)
	if !sonic.ValidString(text) {
		return value
	}
	switch parameterSchema.Type {
	case "integer", "number":
		return json.Number(text)
	case "boolean":
		if strings.EqualFold(text, "true") {
			return true
		}
		if strings.EqualFold(text, "false") {
			return false
		}
	}
	return value
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
