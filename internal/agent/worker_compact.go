package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/persona"
	"mumu-bot/internal/tools"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

func (a *Agent) newWorkerSummarizer(ctx context.Context, generation *Generation) (adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage], error) {
	if generation == nil || generation.workerModel.contextWindow <= 0 {
		return nil, nil
	}
	ratio := configFor(ctx).Compactor.TriggerRatio
	if ratio <= 0 {
		return nil, nil
	}
	triggerTokens := int(float64(generation.workerModel.contextWindow) * ratio)
	if triggerTokens <= 0 {
		return nil, nil
	}
	var pendingHead []*schema.AgenticMessage
	return summarization.NewTyped(a.ctx, &summarization.TypedConfig[*schema.AgenticMessage]{
		Model: generation.workerModel.model,
		Trigger: &summarization.TriggerCondition{
			ContextTokens: triggerTokens,
		},
		UserInstruction: persona.RenderFor(workerSummaryInstruction, configFor(ctx)),
		Finalize: func(ctx context.Context, _ []*schema.AgenticMessage, summary *schema.AgenticMessage) ([]*schema.AgenticMessage, error) {
			text := strings.TrimSpace(agenticMessageText(summary))
			if text == "" {
				return nil, fmt.Errorf("worker 摘要正文为空，保留原上下文")
			}
			tc := tools.GetToolContext(ctx)
			head := previewSystemMessage(a.personaFor(ctx), memory.ConversationKindWorker, tc.TargetID)
			pendingHead = []*schema.AgenticMessage{head}
			note := schema.UserAgenticMessage(systemNote("上下文摘要", text))
			note.Extra = map[string]any{"kind": kindWorkerSummary}
			return []*schema.AgenticMessage{head, note}, nil
		},
		Callback: func(ctx context.Context, before, after adk.TypedChatModelAgentState[*schema.AgenticMessage]) error {
			tc := tools.GetToolContext(ctx)
			if tc == nil || tc.Session == nil || tc.TargetID <= 0 {
				return fmt.Errorf("worker 摘要缺少工具上下文")
			}
			session, ok := tc.Session.(*Session)
			if !ok || session == nil {
				return fmt.Errorf("worker 摘要缺少会话")
			}
			head := pendingHead
			pendingHead = nil
			return a.saveWorkerContext(ctx, uint(tc.TargetID), session, before, after, head)
		},
	})
}

func (a *Agent) saveWorkerContext(ctx context.Context, workerID uint, session *Session, before, after adk.TypedChatModelAgentState[*schema.AgenticMessage], head []*schema.AgenticMessage) error {
	ids := transcriptIDs(before.Messages)
	maxID := uint(0)
	for _, id := range ids {
		if id > maxID {
			maxID = id
		}
	}
	row, err := a.memory.GetWorkerSessionByID(ctx, workerID)
	if err != nil {
		return err
	}
	through := row.ContextThroughID
	if maxID > through {
		through = maxID
	}
	payload, err := sonic.Marshal(stripHeadEntries(after.Messages))
	if err != nil {
		return err
	}
	heads, err := headEntries(head)
	if err != nil {
		return err
	}
	seqs, headIDs, candidates, err := a.memory.CommitWorkerCheckpoint(ctx, workerID, json.RawMessage(payload), through, ids, heads)
	if err != nil {
		return err
	}
	session.ApplyVisibleSeqs(seqs)
	session.ApplyCheckpoint(through)
	session.attachHeadMessages(head, headIDs)
	a.releaseTranscriptCandidates(ctx, candidates)
	zap.L().Info("worker 上下文已压缩", zap.Uint("worker_id", workerID), zap.Uint("through_id", through))
	return nil
}

// transcriptIDs 按消息在请求中的真实顺序收集已持久化行号，供成功后分配 visible_seq
func transcriptIDs(messages []*schema.AgenticMessage) []uint {
	ids := make([]uint, 0, len(messages))
	seen := map[uint]bool{}
	for _, message := range messages {
		id := transcriptEntryID(message)
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

func transcriptEntryID(message *schema.AgenticMessage) uint {
	if message == nil || message.Extra == nil {
		return 0
	}
	switch value := message.Extra["transcript_id"].(type) {
	case uint:
		return value
	case uint64:
		return uint(value)
	case int:
		return uint(value)
	case int64:
		return uint(value)
	case float64:
		return uint(value)
	case json.Number:
		v, _ := value.Int64()
		return uint(v)
	default:
		return 0
	}
}

func tagTranscriptEntryID(message *schema.AgenticMessage, id uint) {
	if message == nil || id == 0 {
		return
	}
	if message.Extra == nil {
		message.Extra = map[string]any{}
	}
	message.Extra["transcript_id"] = id
}

func (a *Agent) assembleWorkerMessages(ctx context.Context, session *Session) ([]*schema.AgenticMessage, error) {
	row, err := a.memory.GetWorkerSessionByID(ctx, uint(session.targetID))
	if err != nil {
		return nil, err
	}
	checkpoint := make([]*schema.AgenticMessage, 0)
	through := row.ContextThroughID
	hasCheckpoint := len(row.ContextSummary) > 0 && string(row.ContextSummary) != "[]"
	if row.ContextStartID > 0 && through < row.ContextStartID {
		hasCheckpoint = false
		through = 0
	}
	if hasCheckpoint {
		if err := sonic.Unmarshal(row.ContextSummary, &checkpoint); err != nil {
			zap.L().Warn("解析 worker 摘要检查点失败，回退完整 transcript", zap.Uint("worker_id", row.ID), zap.Error(err))
			checkpoint = checkpoint[:0]
			hasCheckpoint = false
			through = 0
		}
	}
	if err := session.ensureGenerationHead(ctx, a.memory, a.personaFor(ctx).SessionInstruction(persona.ModeWorker, session.targetID), ""); err != nil {
		return nil, err
	}
	session.mu.Lock()
	entries := append([]sessionEntry(nil), session.seen...)
	pending := append([]*sessionUser(nil), session.pending...)
	session.mu.Unlock()

	messages := make([]*schema.AgenticMessage, 0, len(checkpoint)+len(entries)+len(pending))
	ordered := orderEntries(entries)
	for _, entry := range ordered {
		if entry.msg != nil && isHeadEntry(entry.msg) {
			tagTranscriptEntryID(entry.msg, entry.transcriptID)
			messages = append(messages, entry.msg)
		}
	}
	if hasCheckpoint {
		messages = append(messages, checkpoint...)
	}
	for _, entry := range ordered {
		if entry.msg == nil || isHeadEntry(entry.msg) {
			continue
		}
		if row.ContextStartID > 0 && entry.transcriptID < row.ContextStartID {
			continue
		}
		if hasCheckpoint && entry.transcriptID <= through {
			continue
		}
		tagTranscriptEntryID(entry.msg, entry.transcriptID)
		messages = append(messages, entry.msg)
	}
	for _, user := range pending {
		if user == nil || user.Msg == nil {
			continue
		}
		if row.ContextStartID > 0 && user.TranscriptID > 0 && user.TranscriptID < row.ContextStartID {
			continue
		}
		if hasCheckpoint && user.TranscriptID > 0 && user.TranscriptID <= through {
			continue
		}
		tagTranscriptEntryID(user.Msg, user.TranscriptID)
		messages = append(messages, user.Msg)
	}
	if tc := tools.GetToolContext(ctx); tc != nil {
		if seq := maxPendingSeq(pending); seq > tc.InjectedArrivalSeq {
			tc.InjectedArrivalSeq = seq
		}
	}
	return messages, nil
}
