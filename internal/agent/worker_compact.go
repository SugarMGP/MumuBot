package agent

import (
	"context"
	"encoding/json"

	"mumu-bot/internal/agentic"
	"mumu-bot/internal/persona"
	"mumu-bot/internal/tools"

	"github.com/bytedance/sonic"
	"go.uber.org/zap"
)

func (a *Agent) saveWorkerContext(ctx context.Context, workerID uint, session *Session, before, after, head []*agentic.Message) error {
	ids := transcriptIDs(before)
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
	payload, err := sonic.Marshal(stripHeadEntries(after))
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
	if attempt := switchAttemptFrom(ctx); attempt != nil {
		attempt.report.Compacted = true
	}
	session.ApplyVisibleSeqs(seqs)
	session.ApplyCheckpoint(through)
	session.attachHeadMessages(head, headIDs)
	a.releaseTranscriptCandidates(ctx, candidates)
	zap.L().Info("worker 上下文已压缩", zap.Uint("worker_id", workerID), zap.Uint("through_id", through))
	return nil
}

// transcriptIDs 按消息在请求中的真实顺序收集已持久化行号，供成功后分配 visible_seq
func transcriptIDs(messages []*agentic.Message) []uint {
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

func transcriptEntryID(message *agentic.Message) uint {
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

func tagTranscriptEntryID(message *agentic.Message, id uint) {
	if message == nil || id == 0 {
		return
	}
	if message.Extra == nil {
		message.Extra = map[string]any{}
	}
	message.Extra["transcript_id"] = id
}

func (a *Agent) assembleWorkerMessages(ctx context.Context, session *Session) ([]*agentic.Message, error) {
	row, err := a.memory.GetWorkerSessionByID(ctx, uint(session.targetID))
	if err != nil {
		return nil, err
	}
	checkpoint := make([]*agentic.Message, 0)
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

	messages := make([]*agentic.Message, 0, len(checkpoint)+len(entries)+len(pending))
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
		if tc := tools.GetToolContext(ctx); tc != nil {
			tc.ObservedThroughOrder = max(tc.ObservedThroughOrder, user.Order)
		}
	}
	return messages, nil
}
