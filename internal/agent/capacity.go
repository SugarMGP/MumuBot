package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"mumu-bot/internal/config"
	"mumu-bot/internal/mcp"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/persona"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// ContextCapacity 检查当前 generation 工具下全部持久工作集能否装入目标窗口
func (a *Agent) ContextCapacity(ctx context.Context, contextWindow int) ([]string, error) {
	if a == nil {
		return nil, fmt.Errorf("Agent 未初始化")
	}
	groupTools, privateTools, lifeTools, err := buildToolLists(nil)
	if err != nil {
		return nil, err
	}
	workerTools, err := buildTools(workerToolBuilders())
	if err != nil {
		return nil, err
	}
	if generation := a.currentGeneration(); generation != nil {
		groupTools, privateTools, lifeTools, workerTools = generation.groupTools, generation.privateTools, generation.lifeTools, generation.workerTools
	}
	return capacityForWindow(ctx, a.memory, contextWindow, groupTools, privateTools, lifeTools, workerTools)
}

// ContextCapacityFor 供 Agent 尚未创建时做只读容量检查
func ContextCapacityFor(ctx context.Context, mem *memory.Manager, contextWindow int, mcpManager *mcp.Manager) ([]string, error) {
	groupTools, privateTools, lifeTools, err := buildToolLists(mcpManager)
	if err != nil {
		return nil, err
	}
	workerTools, err := buildTools(workerToolBuilders())
	if err != nil {
		return nil, err
	}
	return capacityForWindow(ctx, mem, contextWindow, groupTools, privateTools, lifeTools, workerTools)
}

func capacityForWindow(ctx context.Context, mem *memory.Manager, contextWindow int, groupTools, privateTools, lifeTools, workerTools []tool.BaseTool) ([]string, error) {
	if mem == nil {
		return nil, fmt.Errorf("记忆系统未初始化")
	}
	if contextWindow <= 0 {
		return nil, fmt.Errorf("模型上下文窗口必须大于 0")
	}
	cfg := config.Snapshot()
	p, err := persona.NewPersona(&cfg.Persona, cfg.Identity)
	if err != nil {
		return nil, fmt.Errorf("加载人格失败: %w", err)
	}
	affected := make([]string, 0)
	previewCtx := withGeneration(ctx, &Generation{contextWindow: contextWindow})
	groupIDs, err := mem.ListActiveGroupIDs(ctx)
	if err != nil {
		return nil, err
	}
	for _, groupID := range groupIDs {
		messages, buildErr := previewPersistedMessages(previewCtx, mem, p, memory.ConversationKindGroup, groupID)
		if buildErr != nil {
			return nil, buildErr
		}
		if requestTokens(messages, modelToolInfos(ctx, groupTools)) > contextWindow {
			affected = append(affected, sessionKey(memory.ConversationKindGroup, groupID))
		}
	}
	privateTargets, err := mem.ListConversationTargets(ctx, memory.ConversationKindPrivate, true)
	if err != nil {
		return nil, err
	}
	for _, target := range privateTargets {
		messages, buildErr := previewPersistedMessages(previewCtx, mem, p, memory.ConversationKindPrivate, target.TargetID)
		if buildErr != nil {
			return nil, buildErr
		}
		if requestTokens(messages, modelToolInfos(ctx, privateTools)) > contextWindow {
			affected = append(affected, sessionKey(memory.ConversationKindPrivate, target.TargetID))
		}
	}
	workers, err := mem.ListWorkerSessions(ctx)
	if err != nil {
		return nil, err
	}
	for _, worker := range workers {
		messages, buildErr := previewPersistedMessages(previewCtx, mem, p, memory.ConversationKindWorker, int64(worker.ID))
		if buildErr != nil {
			return nil, buildErr
		}
		if requestTokens(messages, modelToolInfos(ctx, workerTools)) > contextWindow {
			affected = append(affected, sessionKey(memory.ConversationKindWorker, int64(worker.ID)))
		}
	}
	lifeMessages, err := previewPersistedMessages(previewCtx, mem, p, memory.ConversationKindLife, memory.LifeTargetID)
	if err != nil {
		return nil, err
	}
	if requestTokens(lifeMessages, modelToolInfos(ctx, lifeTools)) > contextWindow {
		affected = append(affected, sessionKey(memory.ConversationKindLife, memory.LifeTargetID))
	}
	sort.Strings(affected)
	return affected, nil
}

// previewPersistedMessages 只读拼装持久工作集，不写代头、不写群说明、不触发压缩
func previewPersistedMessages(ctx context.Context, mem *memory.Manager, p *persona.Persona, kind string, targetID int64) ([]*schema.AgenticMessage, error) {
	checkpoint, err := mem.GetCheckpoint(ctx, kind, targetID)
	if err != nil {
		return nil, err
	}
	afterVisible := uint64(0)
	afterID := uint(0)
	if checkpoint != nil && checkpoint.ThroughVisibleSeq != nil {
		afterVisible = *checkpoint.ThroughVisibleSeq
	}
	startID := uint(0)
	switch kind {
	case memory.ConversationKindWorker:
		row, rowErr := mem.GetWorkerSessionByID(ctx, uint(targetID))
		if rowErr != nil {
			return nil, rowErr
		}
		startID = row.ContextStartID
		if row.ContextThroughID > afterID {
			afterID = row.ContextThroughID
		}
		if startID > 0 && startID-1 > afterID {
			afterID = startID - 1
		}
	case memory.ConversationKindLife:
		row, rowErr := mem.GetLifeState(ctx)
		if rowErr != nil {
			return nil, rowErr
		}
		if row.ContextThroughID > afterID {
			afterID = row.ContextThroughID
		}
	}
	visible, err := mem.VisibleTranscript(ctx, kind, targetID, afterVisible, afterID)
	if err != nil {
		return nil, err
	}
	pending, err := mem.PendingInputsAfter(ctx, kind, targetID, afterID)
	if err != nil {
		return nil, err
	}
	messages := make([]*schema.AgenticMessage, 0, len(visible)+len(pending)+6)
	hasSystem := false
	hasDigest := false
	hasPinned := false
	for _, entry := range visible {
		var msg schema.AgenticMessage
		if err := sonic.Unmarshal(entry.Payload, &msg); err != nil {
			return nil, fmt.Errorf("解析 %s:%d 持久工作集失败: %w", kind, targetID, err)
		}
		switch messageKind(&msg) {
		case kindSystemPrompt:
			hasSystem = true
		case kindContextDigest:
			hasDigest = true
		case kindPinnedMemory:
			hasPinned = true
		}
		messages = append(messages, &msg)
	}
	head := make([]*schema.AgenticMessage, 0, 6)
	if !hasSystem {
		head = append(head, previewSystemMessage(p, kind))
	}
	if kind == memory.ConversationKindGroup || kind == memory.ConversationKindPrivate {
		pinnedText := ""
		if !hasDigest {
			digest, digestErr := mem.LoadContextDigest(ctx, kind, targetID)
			if digestErr != nil {
				return nil, digestErr
			}
			pinned, pinnedErr := mem.PinnedMemoriesForConversation(ctx, kind, targetID)
			if pinnedErr != nil {
				return nil, pinnedErr
			}
			pinnedText = renderPinnedMemories(pinned)
			session := &Session{kind: kind, targetID: targetID, memory: mem}
			digestText, _ := renderBoundedDigest(ctx, session, digest, pinnedText)
			if strings.TrimSpace(digestText) == "" && checkpoint != nil {
				digestText = checkpoint.Overview
			}
			if text := strings.TrimSpace(digestText); text != "" {
				note := schema.UserAgenticMessage(systemNote("上下文摘要", text))
				note.Extra = map[string]any{"kind": kindContextDigest}
				head = append(head, note)
			}
		}
		if !hasPinned {
			if pinnedText == "" {
				pinned, pinnedErr := mem.PinnedMemoriesForConversation(ctx, kind, targetID)
				if pinnedErr != nil {
					return nil, pinnedErr
				}
				pinnedText = renderPinnedMemories(pinned)
			}
			if strings.TrimSpace(pinnedText) != "" {
				note := schema.UserAgenticMessage(systemNote("长期记忆", pinnedText))
				note.Extra = map[string]any{"kind": kindPinnedMemory}
				head = append(head, note)
			}
		}
		if kind == memory.ConversationKindGroup {
			if extra, noteErr := mem.GetGroupExtraPrompt(ctx, targetID); noteErr == nil && strings.TrimSpace(extra) != "" {
				hasNote := false
				for _, msg := range messages {
					if messageKind(msg) == kindGroupNote {
						hasNote = true
						break
					}
				}
				if !hasNote {
					note := schema.UserAgenticMessage(systemNote("群说明", extra))
					note.Extra = map[string]any{"kind": kindGroupNote}
					head = append(head, note)
				}
			}
		}
	}
	if kind == memory.ConversationKindLife && !hasPinned {
		pinned, pinnedErr := mem.PinnedMemoriesForSelf(ctx)
		if pinnedErr != nil {
			return nil, pinnedErr
		}
		if text := strings.TrimSpace(renderPinnedMemories(pinned)); text != "" {
			note := schema.UserAgenticMessage(systemNote("长期记忆", text))
			note.Extra = map[string]any{"kind": kindPinnedMemory}
			head = append(head, note)
		}
	}
	if kind == memory.ConversationKindLife || kind == memory.ConversationKindWorker {
		summary, summaryStart := previewSummary(ctx, mem, kind, targetID)
		if len(summary) > 0 && summaryStart {
			summaryMessages := make([]*schema.AgenticMessage, 0)
			if err := sonic.Unmarshal(summary, &summaryMessages); err != nil {
				return nil, fmt.Errorf("解析 %s:%d 摘要检查点失败: %w", kind, targetID, err)
			}
			head = append(head, summaryMessages...)
		}
	}
	messages = append(head, messages...)
	for _, entry := range pending {
		var msg schema.AgenticMessage
		if err := sonic.Unmarshal(entry.Payload, &msg); err != nil {
			return nil, fmt.Errorf("解析 %s:%d 待处理输入失败: %w", kind, targetID, err)
		}
		messages = append(messages, &msg)
	}
	return messages, nil
}

func previewSystemMessage(p *persona.Persona, kind string) *schema.AgenticMessage {
	var text string
	switch kind {
	case memory.ConversationKindLife:
		text = p.Instruction(persona.ModeLife)
	case memory.ConversationKindWorker:
		text = persona.Render(workerInstruction, persona.IdentityFromConfig(config.Snapshot().Identity))
	default:
		text = p.Instruction(previewPersonaMode(kind))
	}
	system := schema.SystemAgenticMessage(text)
	system.Extra = map[string]any{"kind": kindSystemPrompt}
	return system
}

func previewPersonaMode(kind string) string {
	if kind == memory.ConversationKindPrivate {
		return persona.ModePrivate
	}
	return persona.ModeGroup
}

func capacitySummaryValid(raw []byte) bool {
	text := strings.TrimSpace(string(raw))
	return text != "" && text != "[]" && text != "null"
}

func previewSummary(ctx context.Context, mem *memory.Manager, kind string, targetID int64) ([]byte, bool) {
	switch kind {
	case memory.ConversationKindLife:
		row, err := mem.GetLifeState(ctx)
		if err != nil || row.ContextThroughID == 0 || !capacitySummaryValid(row.ContextSummary) {
			return nil, false
		}
		return row.ContextSummary, true
	case memory.ConversationKindWorker:
		row, err := mem.GetWorkerSessionByID(ctx, uint(targetID))
		if err != nil || !capacitySummaryValid(row.ContextSummary) || row.ContextThroughID == 0 {
			return nil, false
		}
		if row.ContextStartID > 0 && row.ContextThroughID < row.ContextStartID {
			return nil, false
		}
		return row.ContextSummary, true
	default:
		return nil, false
	}
}
