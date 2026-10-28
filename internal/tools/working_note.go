package tools

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type SaveWorkingNoteInput struct {
	Note string `json:"note" jsonschema:"description=最多300字的当前会话工作便签；空字符串表示清除"`
}

func saveWorkingNote(ctx context.Context, input *SaveWorkingNoteInput) (any, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("便签能力未初始化"))
	}
	if input == nil {
		return nil, fmt.Errorf("缺少便签内容")
	}
	if err := tc.MemoryMgr.SaveWorkingNoteScope(ctx, tc.ConversationKind, tc.TargetID, input.Note); err != nil {
		return nil, err
	}
	return map[string]any{"success": true}, nil
}

func NewSaveWorkingNoteTool() (tool.InvokableTool, error) {
	return utils.InferTool("saveWorkingNote", "保存或清除当前会话的工作便签，只记录需要跨轮记住的临时安排。", saveWorkingNote)
}
