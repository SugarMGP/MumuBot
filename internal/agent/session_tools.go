package agent

import (
	"context"
	"strings"

	"mumu-bot/internal/memory"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/schema"
)

// BeginToolTranscript 在执行工具前幂等登记工具回执
func (s *Session) BeginToolTranscript(ctx context.Context, runID, toolName, callID, arguments string) (id uint, existingResult string, resumed bool, err error) {
	if s.memory == nil {
		return 0, "", false, nil
	}
	if id, result, ok, findErr := s.memory.ResumeToolTranscript(ctx, s.kind, s.targetID, runID, callID); findErr != nil {
		return 0, "", false, findErr
	} else if ok {
		return id, result, true, nil
	}
	payload, err := sonic.Marshal(&schema.AgenticMessage{Role: schema.AgenticRoleTypeUser, Extra: map[string]any{"tool_arguments": arguments}})
	if err != nil {
		return 0, "", false, err
	}
	entry, _, err := s.memory.EnsureToolTranscript(ctx, &memory.TranscriptEntry{
		ConversationKind: s.kind, TargetID: s.targetID, EntryKind: "tool",
		RunID: runID, ToolCallID: callID, ToolName: toolName, ToolStatus: "running", Payload: payload,
	})
	if err != nil {
		return 0, "", false, err
	}
	return entry.ID, "", false, nil
}

// FinishToolTranscript 更新工具回执；大文本只保留预览和内容寻址句柄
func (s *Session) FinishToolTranscript(ctx context.Context, id uint, status, resultText string, enhanced *schema.ToolResult, suppliedRef string) (string, *schema.ToolResult, error) {
	if s.memory == nil || id == 0 {
		return resultText, enhanced, nil
	}
	row, err := s.memory.GetTranscript(ctx, id)
	if err != nil {
		return "", nil, err
	}
	resultRef := strings.TrimSpace(suppliedRef)
	blocks := toolResultBlocks(enhanced, resultText)
	if hasMediaBlock(blocks) && row.ToolName != "viewImage" {
		blocks = textResultBlocks(toolResultManifest(blocks))
		enhanced = nil
	}
	if resultRef == "" && row.ToolName != "readToolResult" && !hasMediaBlock(blocks) {
		if fullText := blocksText(blocks); estimateTokens(fullText) > 4000 {
			ref, saveErr := s.memory.SaveToolResultText(ctx, fullText)
			if saveErr != nil {
				return "", nil, saveErr
			}
			resultRef = ref
			preview := truncateRunes(fullText, 1600) + "\n\n[工具结果已保存，使用 readToolResult 读取全文]"
			blocks = textResultBlocks(preview)
			resultText = preview
			enhanced = nil
		}
	}
	payload, err := sonic.Marshal(&schema.AgenticMessage{
		Role:  schema.AgenticRoleTypeUser,
		Extra: map[string]any{"source": "tool_result", "tool_status": status},
		ContentBlocks: []*schema.ContentBlock{{
			Type: schema.ContentBlockTypeFunctionToolResult,
			FunctionToolResult: &schema.FunctionToolResult{
				CallID: row.ToolCallID, Name: row.ToolName, Content: blocks,
			},
		}},
	})
	if err != nil {
		return "", nil, err
	}
	if err := s.memory.UpdateToolTranscript(ctx, id, status, payload, resultRef); err != nil {
		return "", nil, err
	}
	var resultMsg schema.AgenticMessage
	if err := sonic.Unmarshal(payload, &resultMsg); err == nil {
		s.mu.Lock()
		s.seen = append(s.seen, sessionEntry{msg: &resultMsg, transcriptID: id})
		s.mu.Unlock()
	}
	return resultText, enhanced, nil
}

func textResultBlocks(text string) []*schema.FunctionToolResultContentBlock {
	return []*schema.FunctionToolResultContentBlock{{
		Type: schema.FunctionToolResultContentBlockTypeText,
		Text: &schema.UserInputText{Text: text},
	}}
}

func toolResultBlocks(result *schema.ToolResult, fallbackText string) []*schema.FunctionToolResultContentBlock {
	if result == nil {
		return textResultBlocks(fallbackText)
	}
	blocks := make([]*schema.FunctionToolResultContentBlock, 0, len(result.Parts))
	for _, part := range result.Parts {
		switch part.Type {
		case schema.ToolPartTypeText:
			blocks = append(blocks, &schema.FunctionToolResultContentBlock{Type: schema.FunctionToolResultContentBlockTypeText, Text: &schema.UserInputText{Text: part.Text}})
		case schema.ToolPartTypeImage:
			if part.Image != nil {
				blocks = append(blocks, &schema.FunctionToolResultContentBlock{Type: schema.FunctionToolResultContentBlockTypeImage, Image: convertImage(part.Image)})
			}
		case schema.ToolPartTypeAudio:
			if part.Audio != nil {
				blocks = append(blocks, &schema.FunctionToolResultContentBlock{Type: schema.FunctionToolResultContentBlockTypeAudio, Audio: convertAudio(part.Audio)})
			}
		case schema.ToolPartTypeVideo:
			if part.Video != nil {
				blocks = append(blocks, &schema.FunctionToolResultContentBlock{Type: schema.FunctionToolResultContentBlockTypeVideo, Video: convertVideo(part.Video)})
			}
		case schema.ToolPartTypeFile:
			if part.File != nil {
				blocks = append(blocks, &schema.FunctionToolResultContentBlock{Type: schema.FunctionToolResultContentBlockTypeFile, File: convertFile(part.File)})
			}
		}
	}
	if len(blocks) == 0 {
		return textResultBlocks(fallbackText)
	}
	return blocks
}

// toolResultManifest 把非 viewImage 的多媒体结果收敛成文本清单，避免媒体本体进入上下文
func toolResultManifest(blocks []*schema.FunctionToolResultContentBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block == nil {
			continue
		}
		switch {
		case block.Text != nil:
			if text := strings.TrimSpace(block.Text.Text); text != "" {
				parts = append(parts, text)
			}
		case block.Image != nil:
			parts = append(parts, "[图片工具结果]")
		case block.Audio != nil:
			parts = append(parts, "[音频工具结果]")
		case block.Video != nil:
			parts = append(parts, "[视频工具结果]")
		case block.File != nil:
			parts = append(parts, "[文件工具结果]")
		}
	}
	if len(parts) == 0 {
		return "工具返回了多媒体结果，未将媒体本体放入上下文"
	}
	return strings.Join(parts, "\n")
}

func hasMediaBlock(blocks []*schema.FunctionToolResultContentBlock) bool {
	for _, block := range blocks {
		if block != nil && block.Type != schema.FunctionToolResultContentBlockTypeText {
			return true
		}
	}
	return false
}

func blocksText(blocks []*schema.FunctionToolResultContentBlock) string {
	var parts []string
	for _, block := range blocks {
		if block != nil && block.Text != nil && block.Text.Text != "" {
			parts = append(parts, block.Text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// RepairToolTranscript 在重启恢复时把中断工具改成不可重放的终态
func (s *Session) RepairToolTranscript(ctx context.Context, entry *memory.TranscriptEntry, status, text string) error {
	payload, err := sonic.Marshal(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{{
			Type: schema.ContentBlockTypeFunctionToolResult,
			FunctionToolResult: &schema.FunctionToolResult{
				CallID: entry.ToolCallID, Name: entry.ToolName, Content: textResultBlocks(text),
			},
		}},
	})
	if err != nil {
		return err
	}
	if err := s.memory.UpdateToolTranscript(ctx, entry.ID, status, payload, ""); err != nil {
		return err
	}
	entry.Payload = payload
	entry.ToolStatus = status
	return nil
}

// AppendNotExecutedTool 为缺少回执的工具调用补一条关闭协议的结果
func (s *Session) AppendNotExecutedTool(ctx context.Context, runID, callID, toolName string) (*memory.TranscriptEntry, error) {
	payload, err := sonic.Marshal(&schema.AgenticMessage{
		Role: schema.AgenticRoleTypeUser,
		ContentBlocks: []*schema.ContentBlock{{
			Type: schema.ContentBlockTypeFunctionToolResult,
			FunctionToolResult: &schema.FunctionToolResult{
				CallID: callID, Name: toolName,
				Content: textResultBlocks("工具调用没有执行：进程在工具开始前中断，请勿自动重放。"),
			},
		}},
	})
	if err != nil {
		return nil, err
	}
	entry, created, err := s.memory.EnsureToolTranscript(ctx, &memory.TranscriptEntry{
		ConversationKind: s.kind, TargetID: s.targetID, EntryKind: "tool",
		RunID: runID, ToolCallID: callID, ToolName: toolName, ToolStatus: "not_executed", Payload: payload,
	})
	if err != nil {
		return nil, err
	}
	if created {
		return entry, nil
	}
	return entry, nil
}

func truncateRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "…"
}

func commonValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func convertImage(value *schema.ToolOutputImage) *schema.UserInputImage {
	if value == nil {
		return nil
	}
	return &schema.UserInputImage{URL: commonValue(value.URL), Base64Data: commonValue(value.Base64Data), FileID: commonValue(value.FileID), MIMEType: value.MIMEType}
}

func convertAudio(value *schema.ToolOutputAudio) *schema.UserInputAudio {
	if value == nil {
		return nil
	}
	return &schema.UserInputAudio{URL: commonValue(value.URL), Base64Data: commonValue(value.Base64Data), FileID: commonValue(value.FileID), MIMEType: value.MIMEType}
}

func convertVideo(value *schema.ToolOutputVideo) *schema.UserInputVideo {
	if value == nil {
		return nil
	}
	return &schema.UserInputVideo{URL: commonValue(value.URL), Base64Data: commonValue(value.Base64Data), FileID: commonValue(value.FileID), MIMEType: value.MIMEType}
}

func convertFile(value *schema.ToolOutputFile) *schema.UserInputFile {
	if value == nil {
		return nil
	}
	return &schema.UserInputFile{URL: commonValue(value.URL), Base64Data: commonValue(value.Base64Data), FileID: commonValue(value.FileID), MIMEType: value.MIMEType}
}

// CaptureStateToolResults 补记 Eino 提供给模型但未被工具 wrapper 捕获的结果，例如未知工具
func (s *Session) CaptureStateToolResults(ctx context.Context, runID string, messages []*schema.AgenticMessage) ([]uint, error) {
	if s.memory == nil || strings.TrimSpace(runID) == "" {
		return nil, nil
	}
	var captured []uint
	for _, message := range messages {
		if message == nil {
			continue
		}
		for _, block := range message.ContentBlocks {
			if block == nil || block.FunctionToolResult == nil || strings.TrimSpace(block.FunctionToolResult.CallID) == "" {
				continue
			}
			result := block.FunctionToolResult
			if _, _, ok, err := s.memory.ResumeToolTranscript(ctx, s.kind, s.targetID, runID, result.CallID); err != nil {
				return nil, err
			} else if ok {
				continue
			}
			payload, err := sonic.Marshal(message)
			if err != nil {
				return nil, err
			}
			status := "succeeded"
			var outcome struct {
				Success *bool `json:"success"`
			}
			if sonic.UnmarshalString(toolResultBlocksText(result.Content), &outcome) == nil && outcome.Success != nil && !*outcome.Success {
				status = "failed"
			}
			entry, created, err := s.memory.EnsureToolTranscript(ctx, &memory.TranscriptEntry{
				ConversationKind: s.kind, TargetID: s.targetID, EntryKind: "tool",
				RunID: runID, ToolCallID: result.CallID, ToolName: result.Name,
				ToolStatus: status, Payload: payload,
			})
			if err != nil {
				return nil, err
			}
			if !created {
				continue
			}
			s.mu.Lock()
			s.seen = append(s.seen, sessionEntry{msg: message, transcriptID: entry.ID})
			s.mu.Unlock()
			captured = append(captured, entry.ID)
		}
	}
	return captured, nil
}

func toolResultBlocksText(blocks []*schema.FunctionToolResultContentBlock) string {
	var parts []string
	for _, block := range blocks {
		if block != nil && block.Text != nil {
			parts = append(parts, block.Text.Text)
		}
	}
	return strings.Join(parts, "\n")
}
