package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mumu-bot/internal/agentic"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/tools"

	"github.com/bytedance/sonic"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	toolNotExecutedText = agentic.MissingToolResultText
	toolInterruptedText = "工具执行因进程退出而中断，没有可复用结果，请勿自动重放。"
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
	payload, err := sonic.Marshal(&agentic.Message{Role: agentic.RoleUser, Extra: map[string]any{"tool_arguments": arguments}})
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
func (s *Session) FinishToolTranscript(ctx context.Context, id uint, status, resultText string, enhanced *tools.Result, suppliedRef string) (string, *tools.Result, error) {
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
	windowRead := tools.IsWindowReadTool(row.ToolName)
	if resultRef == "" && !windowRead && !hasMediaBlock(blocks) {
		fullText := blocksText(blocks)
		preview, ref, truncated, saveErr := tools.ExternalizeText(ctx, s.memory, fullText)
		if saveErr != nil {
			return "", nil, saveErr
		}
		if truncated {
			resultRef = ref
			blocks = textResultBlocks(preview)
			resultText = preview
			enhanced = nil
		}
	}
	toolArguments := ""
	var prior struct {
		Extra map[string]any `json:"extra"`
	}
	if sonic.Unmarshal(row.Payload, &prior) == nil {
		toolArguments, _ = prior.Extra["tool_arguments"].(string)
	}
	payload, err := sonic.Marshal(&agentic.Message{
		Role: agentic.RoleUser,
		Extra: map[string]any{
			"source": "tool_result", "tool_status": status,
			"completed_at":   time.Now().UTC().Format(time.RFC3339Nano),
			"tool_arguments": strings.TrimSpace(toolArguments),
		},
		ContentBlocks: []*agentic.ContentBlock{{
			Type: agentic.BlockTypeFunctionToolResult,
			FunctionToolResult: &agentic.FunctionToolResult{
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
	s.appendSeenToolResult(id, payload)
	return resultText, enhanced, nil
}

// appendSeenToolResult 把已写库的工具回执按原始 payload 放进工作集，保证装配和数据库一致
func (s *Session) appendSeenToolResult(id uint, payload []byte) {
	var resultMsg agentic.Message
	if sonic.Unmarshal(payload, &resultMsg) != nil {
		return
	}
	tagTranscriptEntryID(&resultMsg, id)
	s.mu.Lock()
	s.seen = append(s.seen, sessionEntry{msg: &resultMsg, transcriptID: id})
	s.mu.Unlock()
}

func textResultBlocks(text string) []*agentic.FunctionToolResultContentBlock {
	return []*agentic.FunctionToolResultContentBlock{{
		Type: agentic.ToolResultPartText,
		Text: &agentic.UserInputText{Text: text},
	}}
}

func toolResultBlocks(result *tools.Result, fallbackText string) []*agentic.FunctionToolResultContentBlock {
	if result == nil {
		return textResultBlocks(fallbackText)
	}
	blocks := make([]*agentic.FunctionToolResultContentBlock, 0, len(result.Parts))
	for _, part := range result.Parts {
		switch part.Type {
		case tools.PartTypeText:
			blocks = append(blocks, &agentic.FunctionToolResultContentBlock{Type: agentic.ToolResultPartText, Text: &agentic.UserInputText{Text: part.Text}})
		case tools.PartTypeImage:
			if part.Image != nil {
				blocks = append(blocks, &agentic.FunctionToolResultContentBlock{Type: agentic.ToolResultPartImage, Image: convertImage(part.Image)})
			}
		case tools.PartTypeAudio:
			if part.Audio != nil {
				blocks = append(blocks, &agentic.FunctionToolResultContentBlock{Type: agentic.ToolResultPartAudio, Audio: convertAudio(part.Audio)})
			}
		case tools.PartTypeVideo:
			if part.Video != nil {
				blocks = append(blocks, &agentic.FunctionToolResultContentBlock{Type: agentic.ToolResultPartVideo, Video: convertVideo(part.Video)})
			}
		case tools.PartTypeFile:
			if part.File != nil {
				blocks = append(blocks, &agentic.FunctionToolResultContentBlock{Type: agentic.ToolResultPartFile, File: convertFile(part.File)})
			}
		}
	}
	if len(blocks) == 0 {
		return textResultBlocks(fallbackText)
	}
	return blocks
}

// toolResultManifest 把非 viewImage 的多媒体结果收敛成文本清单，避免媒体本体进入上下文
func toolResultManifest(blocks []*agentic.FunctionToolResultContentBlock) string {
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

func hasMediaBlock(blocks []*agentic.FunctionToolResultContentBlock) bool {
	for _, block := range blocks {
		if block != nil && block.Type != agentic.ToolResultPartText {
			return true
		}
	}
	return false
}

func blocksText(blocks []*agentic.FunctionToolResultContentBlock) string {
	var parts []string
	for _, block := range blocks {
		if block != nil && block.Text != nil && block.Text.Text != "" {
			parts = append(parts, block.Text.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// toolResultPayload 生成只有文本的工具回执，供缺失和中断修复共用
func toolResultPayload(callID, name, text string) ([]byte, error) {
	return sonic.Marshal(&agentic.Message{
		Role: agentic.RoleUser,
		ContentBlocks: []*agentic.ContentBlock{{
			Type: agentic.BlockTypeFunctionToolResult,
			FunctionToolResult: &agentic.FunctionToolResult{
				CallID: callID, Name: name, Content: textResultBlocks(text),
			},
		}},
	})
}

// RepairToolTranscript 在重启恢复时把中断工具改成不可重放的终态
func (s *Session) RepairToolTranscript(ctx context.Context, entry *memory.TranscriptEntry, status, text string) error {
	payload, err := toolResultPayload(entry.ToolCallID, entry.ToolName, text)
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
func (s *Session) AppendNotExecutedTool(ctx context.Context, runID, callID, toolName, text string) (*memory.TranscriptEntry, bool, error) {
	payload, err := toolResultPayload(callID, toolName, text)
	if err != nil {
		return nil, false, err
	}
	entry, created, err := s.memory.EnsureToolTranscript(ctx, &memory.TranscriptEntry{
		ConversationKind: s.kind, TargetID: s.targetID, EntryKind: "tool",
		RunID: runID, ToolCallID: callID, ToolName: toolName, ToolStatus: "not_executed", Payload: payload,
	})
	if err != nil {
		return nil, false, err
	}
	if !created && entry != nil && (entry.ToolStatus == "running" || entry.ToolStatus == "planned") {
		// 进程中断留下的 running/planned 行没有可复用结果，统一转成不可重放的终态
		if err := s.RepairToolTranscript(ctx, entry, "unknown", toolInterruptedText); err != nil {
			return nil, false, err
		}
	}
	return entry, created, nil
}

func convertImage(value *tools.Image) *agentic.UserInputImage {
	if value == nil {
		return nil
	}
	return &agentic.UserInputImage{URL: value.URL, Base64Data: value.Base64Data, FileID: value.FileID, MIMEType: value.MIMEType}
}

func convertAudio(value *tools.Audio) *agentic.UserInputAudio {
	if value == nil {
		return nil
	}
	return &agentic.UserInputAudio{URL: value.URL, Base64Data: value.Base64Data, FileID: value.FileID, MIMEType: value.MIMEType}
}

func convertVideo(value *tools.Video) *agentic.UserInputVideo {
	if value == nil {
		return nil
	}
	return &agentic.UserInputVideo{URL: value.URL, Base64Data: value.Base64Data, FileID: value.FileID, MIMEType: value.MIMEType}
}

func convertFile(value *tools.File) *agentic.UserInputFile {
	if value == nil {
		return nil
	}
	return &agentic.UserInputFile{URL: value.URL, Name: value.Name, Base64Data: value.Base64Data, FileID: value.FileID, MIMEType: value.MIMEType}
}

// syncToolChains 在模型请求前把运行时回执并入工作集，并保证每个工具调用都有回执
// 调用方只在返回变化时重新装配，装配后的下一次请求不会再看到悬空调用
func (a *Agent) syncToolChains(ctx context.Context, session *Session, flow []model.Message, messages []*agentic.Message) ([]*agentic.Message, error) {
	tc := tools.GetToolContext(ctx)
	if tc == nil {
		return messages, nil
	}
	captured, err := session.captureRuntimeToolResults(ctx, tc.RunID, flow, messages, tc.ToolDeclarations)
	if err != nil {
		return nil, err
	}
	if captured {
		messages, err = a.assembleForRun(ctx, session)
		if err != nil {
			return nil, err
		}
	}
	repaired, err := session.repairMissingToolCalls(ctx, tc.RunID, messages, tc.ToolDeclarations)
	if err != nil {
		return nil, err
	}
	if repaired {
		messages, err = a.assembleForRun(ctx, session)
		if err != nil {
			return nil, err
		}
	}
	return messages, nil
}

// captureRuntimeToolResults 把运行时已产生但未落 transcript 的工具回执补入库
// 未知工具不会触发 AfterTool，只有从运行时请求里才能补登记这条回执
func (s *Session) captureRuntimeToolResults(ctx context.Context, runID string, flowMessages []model.Message, assembled []*agentic.Message, declarations []*tool.Declaration) (bool, error) {
	if s.memory == nil || runID == "" || len(flowMessages) == 0 {
		return false, nil
	}
	known := map[string]bool{}
	for _, declaration := range declarations {
		if declaration != nil {
			known[declaration.Name] = true
		}
	}
	existing := map[string]bool{}
	inContext := map[string]bool{}
	for _, message := range assembled {
		if message == nil {
			continue
		}
		for _, block := range message.ContentBlocks {
			if block == nil {
				continue
			}
			if block.FunctionToolResult != nil && block.FunctionToolResult.CallID != "" {
				existing[block.FunctionToolResult.CallID] = true
			}
			if block.FunctionToolCall != nil && block.FunctionToolCall.CallID != "" {
				inContext[block.FunctionToolCall.CallID] = true
			}
		}
	}
	// 只补最近一次模型输出产生的工具回执，避免把压缩掉的历史结果重新写回工作集
	calls := map[string]model.ToolCall{}
	for index := len(flowMessages) - 1; index >= 0; index-- {
		message := flowMessages[index]
		if message.Role != model.RoleAssistant || len(message.ToolCalls) == 0 {
			continue
		}
		for _, call := range message.ToolCalls {
			if call.ID != "" {
				calls[call.ID] = call
			}
		}
		break
	}
	if len(calls) == 0 {
		return false, nil
	}
	changed := false
	for _, message := range flowMessages {
		if message.Role != model.RoleTool || message.ToolID == "" || existing[message.ToolID] {
			continue
		}
		call, hasCall := calls[message.ToolID]
		if !hasCall || !inContext[message.ToolID] {
			continue
		}
		name := message.ToolName
		if name == "" && hasCall {
			name = call.Function.Name
		}
		arguments := "{}"
		if hasCall && len(call.Function.Arguments) > 0 {
			arguments = string(call.Function.Arguments)
		}
		callRunID := s.toolCallRunID(ctx, runID, assembled, message.ToolID)
		id, _, resumed, err := s.BeginToolTranscript(ctx, callRunID, name, message.ToolID, arguments)
		if err != nil {
			return changed, err
		}
		if resumed {
			// 回执已在库里但不在本次装配中，必须重新装配，不能当成本次已完成
			existing[message.ToolID] = true
			changed = true
			continue
		}
		status := "succeeded"
		content := message.Content
		if !known[name] {
			// 未知工具按旧契约返回可纠正 JSON，而不是运行时生成的裸错误文本
			status = "failed"
			content = tools.ToolErrorResult(fmt.Errorf("没有工具 %s，请使用已提供的工具名称", name))
		} else {
			var outcome struct {
				Success *bool `json:"success"`
			}
			if sonic.UnmarshalString(message.Content, &outcome) == nil && outcome.Success != nil && !*outcome.Success {
				status = "failed"
			}
		}
		if _, _, err := s.FinishToolTranscript(ctx, id, status, content, nil, ""); err != nil {
			return changed, err
		}
		existing[message.ToolID] = true
		changed = true
	}
	return changed, nil
}

// repairMissingToolCalls 给没有回执的助手调用补一条 not_executed 行，避免工具链永远悬空
func (s *Session) repairMissingToolCalls(ctx context.Context, runID string, messages []*agentic.Message, declarations []*tool.Declaration) (bool, error) {
	if s.memory == nil || runID == "" {
		return false, nil
	}
	known := map[string]bool{}
	for _, declaration := range declarations {
		if declaration != nil {
			known[declaration.Name] = true
		}
	}
	results := map[string]bool{}
	repaired := false
	for _, message := range messages {
		if message == nil {
			continue
		}
		for _, block := range message.ContentBlocks {
			if block != nil && block.FunctionToolResult != nil && block.FunctionToolResult.CallID != "" {
				results[block.FunctionToolResult.CallID] = true
			}
		}
	}
	for _, message := range messages {
		if message == nil || message.Role != agentic.RoleAssistant {
			continue
		}
		for _, block := range message.ContentBlocks {
			if block == nil || block.FunctionToolCall == nil {
				continue
			}
			call := block.FunctionToolCall
			if call.CallID == "" || results[call.CallID] {
				continue
			}
			callRunID := s.toolCallRunID(ctx, runID, messages, call.CallID)
			text := toolNotExecutedText
			if !known[call.Name] {
				text = tools.ToolErrorResult(fmt.Errorf("没有工具 %s，请使用已提供的工具名称", call.Name))
			}
			entry, _, err := s.AppendNotExecutedTool(ctx, callRunID, call.CallID, call.Name, text)
			if err != nil {
				return false, err
			}
			if entry != nil {
				s.appendSeenToolResult(entry.ID, entry.Payload)
			}
			results[call.CallID] = true
			repaired = true
		}
	}
	return repaired, nil
}

// toolCallRunID 取工具调用所属 assistant 行的 run_id；回执与 assistant 必须同一次运行
func (s *Session) toolCallRunID(ctx context.Context, fallback string, messages []*agentic.Message, callID string) string {
	for _, message := range messages {
		if message == nil || message.Role != agentic.RoleAssistant {
			continue
		}
		for _, block := range message.ContentBlocks {
			if block == nil || block.FunctionToolCall == nil || block.FunctionToolCall.CallID != callID {
				continue
			}
			if id := transcriptEntryID(message); id != 0 {
				if row, err := s.memory.GetTranscript(ctx, id); err == nil && row.RunID != "" {
					return row.RunID
				}
			}
			return fallback
		}
	}
	return fallback
}
