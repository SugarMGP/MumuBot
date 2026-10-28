package agent

import (
	"context"

	"mumu-bot/internal/memory"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/schema"
)

// AppendAssistant 保存一条 assistant 工作集记录
func (s *Session) AppendAssistant(ctx context.Context, runID string, msg *schema.AgenticMessage) (*memory.TranscriptEntry, error) {
	if msg == nil || s.memory == nil {
		return nil, nil
	}
	payload, err := sonic.Marshal(msg)
	if err != nil {
		return nil, err
	}
	entry := &memory.TranscriptEntry{
		ConversationKind: s.kind, TargetID: s.targetID, EntryKind: "assistant",
		RunID: runID, Payload: payload,
	}
	if err := s.memory.AppendTranscript(ctx, entry); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.seen = append(s.seen, sessionEntry{msg: msg, transcriptID: entry.ID})
	s.mu.Unlock()
	return entry, nil
}

// SetUserTranscriptEntry 把持久化工作集 id 关联到内存用户消息
func (s *Session) SetUserTranscriptEntry(user *sessionUser, entry *memory.TranscriptEntry) {
	if user == nil || entry == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user.TranscriptID = entry.ID
}
