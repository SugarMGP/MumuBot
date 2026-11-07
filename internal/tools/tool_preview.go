package tools

import (
	"context"
	"fmt"
	"unicode/utf8"

	"mumu-bot/internal/memory"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

const ToolResultPreviewRunes = 2000

// IsWindowReadTool 判断显式窗口读取工具，这些结果不再被 2000 字符预览二次截断
func IsWindowReadTool(name string) bool {
	return previewWindowTools[name]
}

var previewWindowTools = map[string]bool{
	"readToolResult":   true,
	"readSessionEntry": true,
	"readDiary":        true,
	"readFile":         true,
}

// ExternalizeText 把超长工具文本外置，只返回预览和临时 result_ref
func ExternalizeText(ctx context.Context, mgr *memory.Manager, text string) (string, string, bool, error) {
	total := utf8.RuneCountInString(text)
	if mgr == nil || total <= ToolResultPreviewRunes {
		return text, "", false, nil
	}
	ref, err := mgr.SaveToolResultText(ctx, text)
	if err != nil {
		return "", "", false, err
	}
	preview := truncatePreviewRunes(text, ToolResultPreviewRunes) + fmt.Sprintf("\n\n[原文共 %d 字符，已截断至 %d 字符；使用 readToolResult 续读或用 query 搜索，result_ref=%s]", total, ToolResultPreviewRunes, ref)
	return preview, ref, true, nil
}

// ToolResultPreviewMiddleware 给压缩器等无 transcript 的工具调用提供同样的长文本外置
func ToolResultPreviewMiddleware(mgr *memory.Manager) compose.ToolMiddleware {
	preview := func(ctx context.Context, name, text string) (string, error) {
		if mgr == nil || previewWindowTools[name] {
			return text, nil
		}
		display, _, _, err := ExternalizeText(ctx, mgr, text)
		return display, err
	}
	return compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				output, err := next(ctx, input)
				if err != nil || output == nil {
					return output, err
				}
				display, previewErr := preview(ctx, input.Name, output.Result)
				if previewErr != nil {
					return nil, previewErr
				}
				output.Result = display
				return output, nil
			}
		},
		EnhancedInvokable: func(next compose.EnhancedInvokableToolEndpoint) compose.EnhancedInvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.EnhancedInvokableToolOutput, error) {
				output, err := next(ctx, input)
				if err != nil || output == nil || output.Result == nil {
					return output, err
				}
				text := toolResultText(output.Result)
				display, previewErr := preview(ctx, input.Name, text)
				if previewErr != nil {
					return nil, previewErr
				}
				output.Result = &schema.ToolResult{Parts: []schema.ToolOutputPart{{Type: schema.ToolPartTypeText, Text: display}}}
				return output, nil
			}
		},
	}
}

func truncatePreviewRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "…"
}
