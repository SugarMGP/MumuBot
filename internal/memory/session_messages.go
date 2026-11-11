package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const MaxSessionMessageRunes = 2000

// SessionMessage 是内部投信的投递结果；编号就是目标 transcript 行号
type SessionMessage struct {
	ID              uint      `json:"id"`
	FromSessionKind string    `json:"from_session_kind"`
	FromTargetID    int64     `json:"from_target_id"`
	ToSessionKind   string    `json:"to_session_kind"`
	ToTargetID      int64     `json:"to_target_id"`
	Text            string    `json:"text"`
	CreatedAt       time.Time `json:"created_at"`
}

func normalizeSessionMessage(fromKind string, fromTarget int64, toKind string, toTarget int64, text string) (string, error) {
	text = strings.TrimSpace(text)
	if !validSessionKind(fromKind) {
		return "", fmt.Errorf("来源会话类型无效")
	}
	if !validSessionKind(toKind) {
		return "", fmt.Errorf("目标会话类型无效")
	}
	if fromTarget <= 0 || toTarget <= 0 {
		return "", fmt.Errorf("会话目标无效")
	}
	if text == "" {
		return "", fmt.Errorf("消息正文不能为空")
	}
	return text, nil
}

// validSessionKind 判断会话类型是否属于当前支持的四类
func validSessionKind(kind string) bool {
	return kind == ConversationKindGroup || kind == ConversationKindPrivate ||
		kind == ConversationKindWorker || kind == ConversationKindLife
}

// EnqueueSessionAgentMessageWithPrepare 在同一事务内允许 worker 新上下文先落起点
func (m *Manager) EnqueueSessionAgentMessageWithPrepare(ctx context.Context, fromKind string, fromTarget int64, toKind string, toTarget int64, text string, build func(*SessionMessage) (json.RawMessage, error), prepare func(*gorm.DB, *SessionMessage) error) (*SessionMessage, *TranscriptEntry, error) {
	var row *SessionMessage
	var entry *TranscriptEntry
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 生活投信和时钟统一先锁生活状态，再锁目标，避免重置补试时形成反向锁序
		if toKind == ConversationKindLife {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id=?", LifeTargetID).Take(&LifeState{}).Error; err != nil {
				return err
			}
		}
		created, transcript, err := m.enqueueSessionMessageTx(tx, fromKind, fromTarget, toKind, toTarget, text, build, prepare)
		if err != nil {
			return err
		}
		row, entry = created, transcript
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return row, entry, nil
}

// enqueueSessionMessageTx 完成可用性校验、输入序号分配、目标 transcript 入队与最终 payload 更新
func (m *Manager) enqueueSessionMessageTx(tx *gorm.DB, fromKind string, fromTarget int64, toKind string, toTarget int64, text string, build func(*SessionMessage) (json.RawMessage, error), prepare func(*gorm.DB, *SessionMessage) error) (*SessionMessage, *TranscriptEntry, error) {
	text, err := normalizeSessionMessage(fromKind, fromTarget, toKind, toTarget, text)
	if err != nil {
		return nil, nil, err
	}
	if err := LockConversationAllowed(tx, toKind, toTarget); err != nil {
		return nil, nil, err
	}
	inputSeq, err := m.AllocateInputSeq(tx, toKind, toTarget)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	entry := &TranscriptEntry{
		ConversationKind: toKind, TargetID: toTarget, InputSeq: &inputSeq,
		EntryKind: "agent_message", Payload: json.RawMessage(`{}`),
	}
	if err := tx.Create(entry).Error; err != nil {
		return nil, nil, err
	}
	row := &SessionMessage{
		ID: entry.ID, FromSessionKind: fromKind, FromTargetID: fromTarget,
		ToSessionKind: toKind, ToTargetID: toTarget, Text: text, CreatedAt: now,
	}
	if prepare != nil {
		if err := prepare(tx, row); err != nil {
			return nil, nil, err
		}
	}
	payload := json.RawMessage(`{}`)
	if build != nil {
		payload, err = build(row)
		if err != nil {
			return nil, nil, err
		}
	}
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if err := tx.Model(&TranscriptEntry{}).Where("id=?", entry.ID).Updates(map[string]any{"payload": payload}).Error; err != nil {
		return nil, nil, err
	}
	entry.Payload = payload
	return row, entry, nil
}

// MarkSessionMessageWake 持久化内部信的 wake 标记，保证叫醒后重启仍能处理这次醒来
func (m *Manager) MarkSessionMessageWake(ctx context.Context, id uint) error {
	if id == 0 {
		return fmt.Errorf("内部消息编号无效")
	}
	var row TranscriptEntry
	if err := m.db.WithContext(ctx).Select("id", "payload").Where("id=?", id).First(&row).Error; err != nil {
		return err
	}
	var payload map[string]any
	if err := json.Unmarshal(row.Payload, &payload); err != nil {
		return err
	}
	extra, ok := payload["extra"].(map[string]any)
	if !ok {
		extra = map[string]any{}
	}
	extra["wake"] = true
	payload["extra"] = extra
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return m.db.WithContext(ctx).Model(&TranscriptEntry{}).Where("id=?", id).Update("payload", json.RawMessage(raw)).Error
}
