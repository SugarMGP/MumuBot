package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

type SessionMessage struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	FromSessionKind string    `gorm:"type:text;not null" json:"from_session_kind"`
	FromTargetID    int64     `gorm:"column:from_target_id;not null" json:"from_target_id"`
	ToSessionKind   string    `gorm:"type:text;not null" json:"to_session_kind"`
	ToTargetID      int64     `gorm:"column:to_target_id;not null" json:"to_target_id"`
	Text            string    `gorm:"type:text;not null" json:"text"`
	CreatedAt       time.Time `json:"created_at"`
}

func (SessionMessage) TableName() string { return "session_messages" }

func normalizeSessionMessage(fromKind string, fromTarget int64, toKind string, toTarget int64, text string) (string, error) {
	text = strings.TrimSpace(text)
	if fromKind != ConversationKindGroup && fromKind != ConversationKindPrivate {
		return "", fmt.Errorf("来源会话类型无效")
	}
	if toKind != ConversationKindGroup && toKind != ConversationKindPrivate {
		return "", fmt.Errorf("目标会话类型无效")
	}
	if fromTarget <= 0 || toTarget <= 0 {
		return "", fmt.Errorf("会话目标无效")
	}
	if text == "" {
		return "", fmt.Errorf("消息正文不能为空")
	}
	if len([]rune(text)) > 2000 {
		return "", fmt.Errorf("消息正文不能超过 2000 字符")
	}
	return text, nil
}

// EnqueueSessionAgentMessage 在同一事务中写入会话消息和 agent_message transcript，避免只写一半。
func (m *Manager) EnqueueSessionAgentMessage(ctx context.Context, fromKind string, fromTarget int64, toKind string, toTarget int64, text string, payload json.RawMessage) (*SessionMessage, *TranscriptEntry, error) {
	text, err := normalizeSessionMessage(fromKind, fromTarget, toKind, toTarget, text)
	if err != nil {
		return nil, nil, err
	}
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	row := &SessionMessage{FromSessionKind: fromKind, FromTargetID: fromTarget, ToSessionKind: toKind, ToTargetID: toTarget, Text: text, CreatedAt: time.Now()}
	var entry *TranscriptEntry
	err = m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := LockConversationAllowed(tx, toKind, toTarget); err != nil {
			return err
		}
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		inputSeq, err := m.AllocateInputSeq(tx, toKind, toTarget)
		if err != nil {
			return err
		}
		entry = &TranscriptEntry{ConversationKind: toKind, TargetID: toTarget, InputSeq: &inputSeq, EntryKind: "agent_message", Payload: payload}
		return tx.Create(entry).Error
	})
	if err != nil {
		return nil, nil, err
	}
	return row, entry, nil
}
