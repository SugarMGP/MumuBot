package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"mumu-bot/internal/config"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/tools"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

func (a *Agent) newWorkerSummarizer(generation *Generation) (adk.TypedChatModelAgentMiddleware[*schema.AgenticMessage], error) {
	if generation == nil || generation.contextWindow <= 0 {
		return nil, nil
	}
	ratio := config.Snapshot().Compactor.TriggerRatio
	if ratio <= 0 {
		return nil, nil
	}
	triggerTokens := int(float64(generation.contextWindow) * ratio)
	if triggerTokens <= 0 {
		return nil, nil
	}
	return summarization.NewTyped(a.ctx, &summarization.TypedConfig[*schema.AgenticMessage]{
		Model: generation.model,
		Trigger: &summarization.TriggerCondition{
			ContextTokens: triggerTokens,
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
			return a.saveWorkerContext(ctx, uint(tc.TargetID), session, before, after)
		},
	})
}

func (a *Agent) saveWorkerContext(ctx context.Context, workerID uint, session *Session, before, after adk.TypedChatModelAgentState[*schema.AgenticMessage]) error {
	ids := make([]uint, 0, len(before.Messages))
	maxID := uint(0)
	for _, message := range before.Messages {
		id := workerTranscriptID(message)
		if id == 0 {
			continue
		}
		ids = append(ids, id)
		if id > maxID {
			maxID = id
		}
	}
	if tc := tools.GetToolContext(ctx); tc != nil {
		for _, id := range tc.VisibleTranscriptIDs {
			if id == 0 {
				continue
			}
			ids = append(ids, id)
			if id > maxID {
				maxID = id
			}
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
	payload, err := sonic.Marshal(after.Messages)
	if err != nil {
		return err
	}
	if err := a.memory.SaveWorkerContext(ctx, workerID, json.RawMessage(payload), through); err != nil {
		return err
	}
	if len(ids) > 0 {
		seqs, err := a.memory.MarkVisible(ctx, memory.ConversationKindWorker, int64(workerID), ids)
		if err != nil {
			return err
		}
		session.ApplyVisibleSeqs(seqs)
	}
	zap.L().Info("worker 上下文已压缩", zap.Uint("worker_id", workerID), zap.Uint("through_id", through))
	return nil
}

func workerTranscriptID(message *schema.AgenticMessage) uint {
	if message == nil || message.Extra == nil {
		return 0
	}
	switch value := message.Extra["worker_transcript_id"].(type) {
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

func tagWorkerTranscriptID(message *schema.AgenticMessage, id uint) {
	if message == nil || id == 0 {
		return
	}
	if message.Extra == nil {
		message.Extra = map[string]any{}
	}
	message.Extra["worker_transcript_id"] = id
}

func (a *Agent) assembleWorkerMessages(ctx context.Context, session *Session) ([]*schema.AgenticMessage, error) {
	row, err := a.memory.GetWorkerSessionByID(ctx, uint(session.targetID))
	if err != nil {
		return nil, err
	}
	checkpoint := make([]*schema.AgenticMessage, 0)
	through := row.ContextThroughID
	hasCheckpoint := len(row.ContextSummary) > 0 && string(row.ContextSummary) != "[]"
	if hasCheckpoint {
		if err := sonic.Unmarshal(row.ContextSummary, &checkpoint); err != nil {
			zap.L().Warn("解析 worker 摘要检查点失败，回退完整 transcript", zap.Uint("worker_id", row.ID), zap.Error(err))
			checkpoint = checkpoint[:0]
			hasCheckpoint = false
			through = 0
		}
	}
	if !hasCheckpoint {
		if err := session.ensureGenerationHead(ctx, a.memory, workerInstruction, ""); err != nil {
			return nil, err
		}
	}
	session.mu.Lock()
	entries := append([]sessionEntry(nil), session.seen...)
	pending := append([]*sessionUser(nil), session.pending...)
	session.mu.Unlock()

	messages := make([]*schema.AgenticMessage, 0, len(checkpoint)+len(entries)+len(pending))
	includedIDs := make([]uint, 0, len(entries)+len(pending))
	if hasCheckpoint {
		messages = append(messages, checkpoint...)
	}
	for _, entry := range orderEntries(entries) {
		if entry.msg == nil {
			continue
		}
		if hasCheckpoint && entry.transcriptID <= through {
			continue
		}
		tagWorkerTranscriptID(entry.msg, entry.transcriptID)
		if entry.transcriptID > 0 {
			includedIDs = append(includedIDs, entry.transcriptID)
		}
		messages = append(messages, entry.msg)
	}
	for _, user := range pending {
		if user == nil || user.Msg == nil {
			continue
		}
		if hasCheckpoint && user.TranscriptID > 0 && user.TranscriptID <= through {
			continue
		}
		tagWorkerTranscriptID(user.Msg, user.TranscriptID)
		if user.TranscriptID > 0 {
			includedIDs = append(includedIDs, user.TranscriptID)
		}
		messages = append(messages, user.Msg)
	}
	if tc := tools.GetToolContext(ctx); tc != nil {
		tc.VisibleTranscriptIDs = append([]uint(nil), includedIDs...)
		if seq := maxPendingSeq(pending); seq > tc.InjectedArrivalSeq {
			tc.InjectedArrivalSeq = seq
		}
	}
	return messages, nil
}
