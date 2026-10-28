package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"mumu-bot/internal/memory"

	"github.com/bytedance/sonic"
	cb "github.com/cloudwego/eino/callbacks"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
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

type toolErrorOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// TerminalToolError 标记必须结束当前 Agent 轮次的错误
type TerminalToolError struct{ error }

// NewTerminalToolError 标记必须结束当前 Agent 轮次的错误
func NewTerminalToolError(err error) error { return &TerminalToolError{error: err} }

// UnknownToolHandler 将模型调用的未知工具转成可纠正结果
func UnknownToolHandler(ctx context.Context, name, _ string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return sonic.MarshalString(&toolErrorOutput{Message: fmt.Sprintf("没有工具 %s，请使用已提供的工具名称", name)})
}

var sortedJSONAPI = sonic.Config{SortMapKeys: true, UseNumber: true}.Froze()

// NewToolArgumentsHandler 创建按工具 schema 矫正并标准化参数的处理器
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
		canonical, err := canonicalizeToolArguments(arguments, schemas[name])
		if err != nil {
			return arguments, nil
		}
		return canonical, nil
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

// ToolTranscriptMiddleware 把每次工具执行作为 tool 工作集条目保存，大结果只保留预览和 result_ref
func ToolTranscriptMiddleware() compose.ToolMiddleware {
	return compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				tc := GetToolContext(ctx)
				if tc == nil || tc.Session == nil {
					return next(ctx, input)
				}
				id, existing, resumed, beginErr := tc.Session.BeginToolTranscript(ctx, tc.RunID, input.Name, input.CallID, input.Arguments)
				if beginErr != nil {
					return nil, beginErr
				}
				if resumed {
					if id != 0 {
						tc.VisibleTranscriptIDs = append(tc.VisibleTranscriptIDs, id)
					}
					return &compose.ToolOutput{Result: existing}, nil
				}
				output, err := next(ctx, input)
				status := "succeeded"
				result := ""
				if output != nil {
					result = output.Result
				}
				if err != nil {
					status = "failed"
					result = err.Error()
				} else if output != nil && !toolOutputSucceeded(output) {
					status = "failed"
				}
				display, _, finishErr := tc.Session.FinishToolTranscript(ctx, id, status, result, nil, "")
				if finishErr != nil {
					return nil, finishErr
				}
				if id != 0 {
					tc.VisibleTranscriptIDs = append(tc.VisibleTranscriptIDs, id)
				}
				if output != nil {
					output.Result = display
				}
				return output, err
			}
		},
		EnhancedInvokable: func(next compose.EnhancedInvokableToolEndpoint) compose.EnhancedInvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.EnhancedInvokableToolOutput, error) {
				tc := GetToolContext(ctx)
				if tc == nil || tc.Session == nil {
					return next(ctx, input)
				}
				id, existing, resumed, beginErr := tc.Session.BeginToolTranscript(ctx, tc.RunID, input.Name, input.CallID, input.Arguments)
				if beginErr != nil {
					return nil, beginErr
				}
				if resumed {
					if id != 0 {
						tc.VisibleTranscriptIDs = append(tc.VisibleTranscriptIDs, id)
					}
					return &compose.EnhancedInvokableToolOutput{Result: &schema.ToolResult{Parts: []schema.ToolOutputPart{{Type: schema.ToolPartTypeText, Text: existing}}}}, nil
				}
				output, err := next(ctx, input)
				status := "succeeded"
				result := ""
				if output != nil {
					result = toolResultText(output.Result)
				}
				if err != nil {
					status = "failed"
					result = err.Error()
				}
				var enhanced *schema.ToolResult
				if output != nil {
					enhanced = output.Result
				}
				displayText, displayEnhanced, finishErr := tc.Session.FinishToolTranscript(ctx, id, status, result, enhanced, "")
				if finishErr != nil {
					return nil, finishErr
				}
				if id != 0 {
					tc.VisibleTranscriptIDs = append(tc.VisibleTranscriptIDs, id)
				}
				if output != nil {
					if displayEnhanced != nil {
						output.Result = displayEnhanced
					} else {
						output.Result = &schema.ToolResult{Parts: []schema.ToolOutputPart{{Type: schema.ToolPartTypeText, Text: displayText}}}
					}
				}
				return output, err
			}
		},
	}
}

func toolResultText(result *schema.ToolResult) string {
	if result == nil {
		return ""
	}
	var parts []string
	for _, part := range result.Parts {
		switch part.Type {
		case schema.ToolPartTypeText:
			parts = append(parts, part.Text)
		case schema.ToolPartTypeImage:
			parts = append(parts, "[图片工具结果]")
		case schema.ToolPartTypeAudio:
			parts = append(parts, "[音频工具结果]")
		case schema.ToolPartTypeVideo:
			parts = append(parts, "[视频工具结果]")
		case schema.ToolPartTypeFile:
			parts = append(parts, "[文件工具结果]")
		}
	}
	return strings.Join(parts, "\n")
}

// ToolErrorMiddleware 将可恢复工具错误转成 JSON 结果，让 Eino 继续循环
func ToolErrorMiddleware() compose.InvokableToolMiddleware {
	return func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
		return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
			output, err := next(ctx, input)
			if err == nil {
				return output, nil
			}
			var terminal *TerminalToolError
			if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, memory.ErrSnapshotChanged) || errors.Is(err, memory.ErrConversationUnavailable) || errors.As(err, &terminal) {
				return nil, err
			}
			result, encodeErr := sonic.MarshalString(&toolErrorOutput{Message: err.Error()})
			if encodeErr != nil {
				return nil, encodeErr
			}
			return &compose.ToolOutput{Result: result}, nil
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

// NewToolLogHandler 创建统一的工具调用日志回调
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
