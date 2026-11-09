package agent

import (
	"context"
	"strings"

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
	// 同一指针也留在模型 state 里，摘要边界据此拿到本轮的 assistant 行号
	tagTranscriptEntryID(msg, entry.ID)
	s.mu.Lock()
	s.seen = append(s.seen, sessionEntry{msg: msg, transcriptID: entry.ID})
	s.mu.Unlock()
	return entry, nil
}

func agenticMessageText(message *schema.AgenticMessage) string {
	if message == nil {
		return ""
	}
	var parts []string
	for _, block := range message.ContentBlocks {
		if block == nil {
			continue
		}
		switch {
		case block.UserInputText != nil:
			parts = append(parts, block.UserInputText.Text)
		case block.AssistantGenText != nil:
			parts = append(parts, block.AssistantGenText.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func (s *Session) AppendSeenMessage(msg *schema.AgenticMessage, transcriptID uint, visibleSeq uint64) {
	if msg == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, sessionEntry{msg: msg, transcriptID: transcriptID, visibleSeq: visibleSeq})
}

// AppendSeenObservation 恢复已进入模型的观察事件，保留 user 标记供分段和窗口统计
func (s *Session) AppendSeenObservation(msg *schema.AgenticMessage, transcriptID uint, visibleSeq uint64) {
	if msg == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user := &sessionUser{Msg: msg, TranscriptID: transcriptID, IsObservation: true}
	s.seen = append(s.seen, sessionEntry{user: user, msg: msg, transcriptID: transcriptID, visibleSeq: visibleSeq})
}

func (s *Session) ApplyVisibleSeqs(seqs map[uint]uint64) {
	if len(seqs) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.seen {
		if seq, ok := seqs[s.seen[i].transcriptID]; ok {
			s.seen[i].visibleSeq = seq
		}
	}
	kept := s.pending[:0]
	for _, user := range s.pending {
		if seq, ok := seqs[user.TranscriptID]; ok {
			entry := sessionEntry{msg: user.Msg, transcriptID: user.TranscriptID, visibleSeq: seq}
			if !user.IsMaintenanceHint {
				entry.user = user
			}
			s.seen = append(s.seen, entry)
			continue
		}
		kept = append(kept, user)
	}
	s.pending = kept
	s.seen = orderEntries(s.seen)
}

// hasTranscriptLocked 判断同一个 transcript 行是否已经在工作集里
func (s *Session) hasTranscriptLocked(transcriptID uint) bool {
	if transcriptID == 0 {
		return false
	}
	for _, entry := range s.seen {
		if entry.transcriptID == transcriptID {
			return true
		}
	}
	for _, user := range s.pending {
		if user != nil && user.TranscriptID == transcriptID {
			return true
		}
	}
	return false
}

// RestorePendingMaintenanceHint 恢复尚未进入模型输入的维护提醒
func (s *Session) RestorePendingMaintenanceHint(payload *schema.AgenticMessage, transcriptID uint) {
	if payload == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, &sessionUser{Msg: payload, Text: agenticMessageText(payload), IsMaintenanceHint: true, TranscriptID: transcriptID, Order: s.nextOrderLocked()})
}
