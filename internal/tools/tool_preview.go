package tools

import (
	"context"
	"fmt"
	"unicode/utf8"

	"mumu-bot/internal/memory"
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

func truncatePreviewRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "…"
}
