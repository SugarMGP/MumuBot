package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AllocateInputSeq 在事务内分配会话输入序号
func (m *Manager) AllocateInputSeq(tx *gorm.DB, kind string, targetID int64) (uint64, error) {
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ConversationSession{ConversationKind: kind, TargetID: targetID}).Error; err != nil {
		return 0, err
	}
	var session ConversationSession
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("conversation_kind=? AND target_id=?", kind, targetID).Take(&session).Error; err != nil {
		return 0, err
	}
	session.NextInputSeq++
	if err := tx.Model(&ConversationSession{}).Where("conversation_kind=? AND target_id=?", kind, targetID).Updates(map[string]any{"next_input_seq": session.NextInputSeq, "updated_at": time.Now()}).Error; err != nil {
		return 0, err
	}
	return session.NextInputSeq, nil
}

// AllocateMessageRef 在消息落库事务内分配稳定的 m 编号
func (m *Manager) AllocateMessageRef(tx *gorm.DB, kind string, targetID int64) (string, error) {
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ConversationSession{ConversationKind: kind, TargetID: targetID}).Error; err != nil {
		return "", err
	}
	var session ConversationSession
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("conversation_kind=? AND target_id=?", kind, targetID).Take(&session).Error; err != nil {
		return "", err
	}
	session.NextMessageRef++
	if err := tx.Model(&ConversationSession{}).Where("conversation_kind=? AND target_id=?", kind, targetID).Updates(map[string]any{"next_message_ref": session.NextMessageRef, "updated_at": time.Now()}).Error; err != nil {
		return "", err
	}
	return fmt.Sprintf("m%d", session.NextMessageRef), nil
}

// AllocateVisibleSeq 为一个即将进入模型的条目分配可见序号
func (m *Manager) AllocateVisibleSeq(tx *gorm.DB, kind string, targetID int64) (uint64, error) {
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ConversationSession{ConversationKind: kind, TargetID: targetID}).Error; err != nil {
		return 0, err
	}
	var session ConversationSession
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("conversation_kind=? AND target_id=?", kind, targetID).Take(&session).Error; err != nil {
		return 0, err
	}
	session.NextVisibleSeq++
	if err := tx.Model(&ConversationSession{}).Where("conversation_kind=? AND target_id=?", kind, targetID).Updates(map[string]any{"next_visible_seq": session.NextVisibleSeq, "updated_at": time.Now()}).Error; err != nil {
		return 0, err
	}
	return session.NextVisibleSeq, nil
}

// AppendTranscript 保存一条工作集记录
func (m *Manager) AppendTranscript(ctx context.Context, entry *TranscriptEntry) error {
	if entry == nil || entry.ConversationKind == "" || entry.TargetID <= 0 || entry.EntryKind == "" {
		return fmt.Errorf("transcript 参数无效")
	}
	if len(entry.Payload) == 0 {
		entry.Payload = json.RawMessage(`{}`)
	}
	return m.db.WithContext(ctx).Create(entry).Error
}

// AppendUserTranscript 将已落库的真人消息加入工作集
func (m *Manager) AppendUserTranscript(ctx context.Context, kind string, targetID int64, logID uint, payload json.RawMessage, runID string) (*TranscriptEntry, error) {
	inputSeq, err := m.AllocateInputSeq(m.db.WithContext(ctx), kind, targetID)
	if err != nil {
		return nil, err
	}
	entryKind := "user"
	var messageLogID *uint
	if logID == 0 {
		entryKind = "observation"
	} else {
		messageLogID = &logID
	}
	entry := &TranscriptEntry{
		ConversationKind: kind, TargetID: targetID, InputSeq: &inputSeq,
		EntryKind: entryKind, MessageLogID: messageLogID, RunID: runID, Payload: payload,
	}
	if err := m.AppendTranscript(ctx, entry); err != nil {
		return nil, err
	}
	return entry, nil
}

// PendingInputs 返回尚未进入模型的真人输入
func (m *Manager) PendingInputs(ctx context.Context, kind string, targetID int64) ([]TranscriptEntry, error) {
	var rows []TranscriptEntry
	err := m.db.WithContext(ctx).Where("conversation_kind=? AND target_id=? AND entry_kind IN ('user','observation') AND visible_seq IS NULL", kind, targetID).Order("input_seq,id").Find(&rows).Error
	return rows, err
}

// MarkVisible 在模型请求成功后为一批工作集条目分配可见序号
func (m *Manager) MarkVisible(ctx context.Context, ids []uint) (map[uint]uint64, error) {
	result := map[uint]uint64{}
	if len(ids) == 0 {
		return result, nil
	}
	unique := make([]uint, 0, len(ids))
	seen := map[uint]bool{}
	for _, id := range ids {
		if id != 0 && !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, id := range unique {
			var row TranscriptEntry
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", id).Take(&row).Error; err != nil {
				return err
			}
			if row.VisibleSeq != nil {
				result[id] = *row.VisibleSeq
				continue
			}
			seq, err := m.AllocateVisibleSeq(tx, row.ConversationKind, row.TargetID)
			if err != nil {
				return err
			}
			if err := tx.Model(&TranscriptEntry{}).Where("id=?", id).Update("visible_seq", seq).Error; err != nil {
				return err
			}
			result[id] = seq
		}
		return nil
	})
	return result, err
}

// ResumeToolTranscript 读取同一运行中相同 CallID 的已有回执，避免重复执行工具
func (m *Manager) ResumeToolTranscript(ctx context.Context, kind string, targetID int64, runID, callID string) (id uint, result string, ok bool, err error) {
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(callID) == "" {
		return 0, "", false, nil
	}
	var row TranscriptEntry
	err = m.db.WithContext(ctx).Where("conversation_kind=? AND target_id=? AND run_id=? AND tool_call_id=? AND entry_kind='tool'", kind, targetID, runID, callID).Take(&row).Error
	if err == gorm.ErrRecordNotFound {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, err
	}
	if row.ToolStatus == "running" || row.ToolStatus == "planned" {
		return 0, "", false, fmt.Errorf("工具调用 %s 尚未完成，不能重复执行", callID)
	}
	return row.ID, payloadToolResultText(row.Payload), true, nil
}

func payloadToolResultText(payload []byte) string {
	var message struct {
		ContentBlocks []struct {
			FunctionToolResult *struct {
				Content []struct {
					Text *struct {
						Text string `json:"text"`
					} `json:"text"`
				} `json:"content"`
			} `json:"function_tool_result"`
		} `json:"content_blocks"`
	}
	if sonic.Unmarshal(payload, &message) != nil {
		return ""
	}
	var parts []string
	for _, block := range message.ContentBlocks {
		if block.FunctionToolResult == nil {
			continue
		}
		for _, part := range block.FunctionToolResult.Content {
			if part.Text != nil && part.Text.Text != "" {
				parts = append(parts, part.Text.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

// UpdateToolTranscript 更新工具条目状态和结果
func (m *Manager) UpdateToolTranscript(ctx context.Context, id uint, status string, payload json.RawMessage, resultRef string) error {
	updates := map[string]any{"tool_status": status}
	if len(payload) > 0 {
		updates["payload"] = payload
	}
	if strings.TrimSpace(resultRef) != "" {
		updates["result_ref"] = resultRef
	}
	return m.db.WithContext(ctx).Model(&TranscriptEntry{}).Where("id=?", id).Updates(updates).Error
}

// GetTranscript 读取一条工作集记录
func (m *Manager) GetTranscript(ctx context.Context, id uint) (*TranscriptEntry, error) {
	var row TranscriptEntry
	if err := m.db.WithContext(ctx).Where("id=?", id).Take(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// VisibleTranscript 返回检查点之后的可见条目和尚未闭合的非用户条目
func (m *Manager) VisibleTranscript(ctx context.Context, kind string, targetID int64, afterVisibleSeq uint64) ([]TranscriptEntry, error) {
	var rows []TranscriptEntry
	q := m.db.WithContext(ctx).Where("conversation_kind=? AND target_id=?", kind, targetID)
	if afterVisibleSeq > 0 {
		q = q.Where("(visible_seq>? OR visible_seq IS NULL)", afterVisibleSeq)
	} else {
		q = q.Where("visible_seq IS NOT NULL OR entry_kind<>'user'")
	}
	q = q.Where("entry_kind<>'user' OR visible_seq IS NOT NULL")
	err := q.Order("visible_seq NULLS LAST,id").Find(&rows).Error
	return rows, err
}

// DeleteTranscriptThrough 删除检查点覆盖的工作集记录
func (m *Manager) DeleteTranscriptThrough(tx *gorm.DB, kind string, targetID int64, visibleSeq uint64) error {
	if visibleSeq == 0 {
		return nil
	}
	return tx.Where("conversation_kind=? AND target_id=? AND visible_seq IS NOT NULL AND visible_seq<=?", kind, targetID, visibleSeq).Delete(&TranscriptEntry{}).Error
}

// GetCheckpoint 读取会话当前检查点
func (m *Manager) GetCheckpoint(ctx context.Context, kind string, targetID int64) (*CompactionCheckpoint, error) {
	var row CompactionCheckpoint
	err := m.db.WithContext(ctx).Where("conversation_kind=? AND target_id=?", kind, targetID).Take(&row).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// SaveCheckpoint 保存压缩检查点，版本号由服务端递增
func (m *Manager) SaveCheckpoint(tx *gorm.DB, row CompactionCheckpoint) error {
	if row.Version == 0 {
		row.Version = 1
	}
	row.CreatedAt = time.Now()
	row.UpdatedAt = row.CreatedAt
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "conversation_kind"}, {Name: "target_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"version", "through_visible_seq", "through_message_log_id", "overview", "active_topic_versions", "reference_baseline", "reference_versions", "batch_id", "updated_at"}),
	}).Create(&row).Error
}

// TopicSnapshotJSON 把有序话题快照转成 JSON
func TopicSnapshotJSON(items []TopicSnapshot) ([]byte, error) {
	if items == nil {
		items = []TopicSnapshot{}
	}
	return sonic.Marshal(items)
}

// EnsureToolTranscript 按 run_id 和 call_id 幂等创建工具回执
func (m *Manager) EnsureToolTranscript(ctx context.Context, entry *TranscriptEntry) (*TranscriptEntry, bool, error) {
	if entry == nil || entry.ToolCallID == "" {
		return nil, false, fmt.Errorf("工具回执参数无效")
	}
	if len(entry.Payload) == 0 {
		entry.Payload = json.RawMessage(`{}`)
	}
	result := m.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(entry)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected > 0 {
		return entry, true, nil
	}
	var existing TranscriptEntry
	if err := m.db.WithContext(ctx).Where("conversation_kind=? AND target_id=? AND run_id=? AND tool_call_id=? AND entry_kind='tool'", entry.ConversationKind, entry.TargetID, entry.RunID, entry.ToolCallID).Take(&existing).Error; err != nil {
		return nil, false, err
	}
	return &existing, false, nil
}

// ParseVersionList 读取检查点中的引用版本列表
func ParseVersionList(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var result []string
	if err := sonic.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}

type TranscriptSeed struct {
	EntryKind    string
	MessageLogID uint
	Payload      json.RawMessage
	CreatedAt    time.Time
}

// SeedTranscriptIfEmpty 把升级前近期原文变成可见工作集，已有工作集时不写入
func (m *Manager) SeedTranscriptIfEmpty(ctx context.Context, kind string, targetID int64, seeds []TranscriptSeed) ([]TranscriptEntry, bool, error) {
	if kind == "" || targetID <= 0 || len(seeds) == 0 {
		return nil, false, nil
	}
	createdEntries := make([]TranscriptEntry, 0, len(seeds))
	created := false
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&TranscriptEntry{}).Where("conversation_kind=? AND target_id=?", kind, targetID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return nil
		}
		created = true
		for _, seed := range seeds {
			if seed.EntryKind != "user" && seed.EntryKind != "assistant" {
				return fmt.Errorf("历史工作集类型无效")
			}
			entry := TranscriptEntry{
				ConversationKind: kind, TargetID: targetID, EntryKind: seed.EntryKind,
				MessageLogID: &seed.MessageLogID, Payload: seed.Payload, CreatedAt: seed.CreatedAt,
			}
			if seed.EntryKind == "user" {
				inputSeq, err := m.AllocateInputSeq(tx, kind, targetID)
				if err != nil {
					return err
				}
				entry.InputSeq = &inputSeq
			}
			visibleSeq, err := m.AllocateVisibleSeq(tx, kind, targetID)
			if err != nil {
				return err
			}
			entry.VisibleSeq = &visibleSeq
			if len(entry.Payload) == 0 {
				entry.Payload = json.RawMessage(`{}`)
			}
			if err := tx.Create(&entry).Error; err != nil {
				return err
			}
			createdEntries = append(createdEntries, entry)
		}
		return nil
	})
	return createdEntries, created, err
}
