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

var defaultReferenceSeen = []byte(`{"auto":{},"tool":{}}`)

func ensureConversationSession(tx *gorm.DB, kind string, targetID int64) error {
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ConversationSession{
		ConversationKind: kind, TargetID: targetID, ReferenceSeen: defaultReferenceSeen,
	}).Error
}

// AllocateInputSeq 在事务内分配会话输入序号
func (m *Manager) AllocateInputSeq(tx *gorm.DB, kind string, targetID int64) (uint64, error) {
	_, end, err := allocateSequenceRange(tx, kind, targetID, "next_input_seq", 1)
	return end, err
}

// AllocateVisibleSeq 为一个即将进入模型的条目分配可见序号
func (m *Manager) AllocateVisibleSeq(tx *gorm.DB, kind string, targetID int64) (uint64, error) {
	_, end, err := allocateSequenceRange(tx, kind, targetID, "next_visible_seq", 1)
	return end, err
}

// allocateSequenceRange 一次推进会话序号并返回本次分配的连续区间
func allocateSequenceRange(tx *gorm.DB, kind string, targetID int64, column string, count uint64) (uint64, uint64, error) {
	if count == 0 {
		return 0, 0, fmt.Errorf("序号分配数量无效")
	}
	if err := ensureConversationSession(tx, kind, targetID); err != nil {
		return 0, 0, err
	}
	var end uint64
	sql := "UPDATE conversation_sessions SET " + column + "=" + column + "+?,updated_at=now() WHERE conversation_kind=? AND target_id=? RETURNING " + column
	if err := tx.Raw(sql, count, kind, targetID).Scan(&end).Error; err != nil {
		return 0, 0, err
	}
	if end < count {
		return 0, 0, fmt.Errorf("会话序号分配失败")
	}
	return end - count + 1, end, nil
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
	entryKind := "user"
	var messageLogID *uint
	if logID == 0 {
		entryKind = "observation"
	} else {
		messageLogID = &logID
	}
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	var entry *TranscriptEntry
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		inputSeq, err := m.AllocateInputSeq(tx, kind, targetID)
		if err != nil {
			return err
		}
		entry = &TranscriptEntry{
			ConversationKind: kind, TargetID: targetID, InputSeq: &inputSeq,
			EntryKind: entryKind, MessageLogID: messageLogID, RunID: runID, Payload: payload,
		}
		return tx.Create(entry).Error
	})
	if err != nil {
		return nil, err
	}
	return entry, nil
}

// PendingInputs 返回尚未进入模型的输入，包括真人消息、观察和会话间消息
func (m *Manager) PendingInputs(ctx context.Context, kind string, targetID int64) ([]TranscriptEntry, error) {
	var rows []TranscriptEntry
	err := m.db.WithContext(ctx).Where("conversation_kind=? AND target_id=? AND entry_kind IN ('user','observation','agent_message') AND visible_seq IS NULL", kind, targetID).Order("input_seq,id").Find(&rows).Error
	return rows, err
}

// PendingReferences 返回已经写入但尚未成功送进模型的参考条目。
func (m *Manager) PendingReferences(ctx context.Context, kind string, targetID int64) ([]TranscriptEntry, error) {
	var rows []TranscriptEntry
	err := m.db.WithContext(ctx).Where("conversation_kind=? AND target_id=? AND entry_kind='internal_reference' AND visible_seq IS NULL", kind, targetID).Order("id").Find(&rows).Error
	return rows, err
}

// MarkVisibleAndPersistReceipts 在模型成功后同时推进 visible_seq 和会话级 reference_seen。
func (m *Manager) MarkVisibleAndPersistReceipts(ctx context.Context, kind string, targetID int64, ids []uint, receipt *ReferenceReceipt) (map[uint]uint64, error) {
	return m.markVisibleAndPersistReceipts(ctx, kind, targetID, ids, receipt)
}

func (m *Manager) markVisibleAndPersistReceipts(ctx context.Context, kind string, targetID int64, ids []uint, receipt *ReferenceReceipt) (map[uint]uint64, error) {
	result := map[uint]uint64{}
	if len(ids) == 0 && (receipt == nil || len(receipt.Auto) == 0 && len(receipt.Tool) == 0 && len(receipt.AutoDelete) == 0) {
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
		rows := make([]TranscriptEntry, 0, len(unique))
		if len(unique) > 0 {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id IN ? AND conversation_kind=? AND target_id=?", unique, kind, targetID).
				Find(&rows).Error; err != nil {
				return err
			}
			if len(rows) != len(unique) {
				return fmt.Errorf("transcript 记录不存在或不属于当前会话")
			}
		}
		byID := make(map[uint]TranscriptEntry, len(rows))
		for _, row := range rows {
			byID[row.ID] = row
			if row.VisibleSeq != nil {
				result[row.ID] = *row.VisibleSeq
			}
		}
		unvisible := make([]uint, 0, len(unique))
		for _, id := range unique {
			if byID[id].VisibleSeq == nil {
				unvisible = append(unvisible, id)
			}
		}
		if len(unvisible) > 0 {
			start, _, err := allocateSequenceRange(tx, kind, targetID, "next_visible_seq", uint64(len(unvisible)))
			if err != nil {
				return err
			}
			values := make([]string, 0, len(unvisible))
			args := make([]any, 0, len(unvisible)*2)
			for i, id := range unvisible {
				seq := start + uint64(i)
				values = append(values, "(?::bigint,?::bigint)")
				args = append(args, int64(id), int64(seq))
				result[id] = seq
			}
			sql := "UPDATE conversation_transcript SET visible_seq=v.seq::bigint FROM (VALUES " +
				strings.Join(values, ",") + ") AS v(id,seq) WHERE conversation_transcript.id=v.id::bigint"
			if err := tx.Exec(sql, args...).Error; err != nil {
				return err
			}
		}
		if kind != "" && targetID > 0 && receipt != nil && (len(receipt.Auto) > 0 || len(receipt.Tool) > 0 || len(receipt.AutoDelete) > 0) {
			if err := m.mergeReferenceSeen(tx, kind, targetID, receipt); err != nil {
				return err
			}
		}
		return nil
	})
	return result, err
}

func (m *Manager) mergeReferenceSeen(tx *gorm.DB, kind string, targetID int64, receipt *ReferenceReceipt) error {
	if err := ensureConversationSession(tx, kind, targetID); err != nil {
		return err
	}
	var row ConversationSession
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("conversation_kind=? AND target_id=?", kind, targetID).Take(&row).Error; err != nil {
		return err
	}
	seen := ReferenceSeen{Auto: map[string]string{}, Tool: map[string]string{}}
	if len(row.ReferenceSeen) > 0 {
		_ = sonic.Unmarshal(row.ReferenceSeen, &seen)
	}
	if seen.Auto == nil {
		seen.Auto = map[string]string{}
	}
	if seen.Tool == nil {
		seen.Tool = map[string]string{}
	}
	for key, version := range receipt.Auto {
		seen.Auto[key] = version
	}
	for key, version := range receipt.Tool {
		seen.Tool[key] = version
	}
	for _, key := range receipt.AutoDelete {
		delete(seen.Auto, key)
	}
	raw, err := sonic.Marshal(seen)
	if err != nil {
		return err
	}
	return tx.Model(&ConversationSession{}).Where("conversation_kind=? AND target_id=?", kind, targetID).Updates(map[string]any{"reference_seen": raw, "updated_at": time.Now()}).Error
}

// ReferenceSeenForSession 读取会话级参考账本。
func (m *Manager) ReferenceSeenForSession(ctx context.Context, kind string, targetID int64) (ReferenceSeen, error) {
	var row ConversationSession
	if err := m.db.WithContext(ctx).Where("conversation_kind=? AND target_id=?", kind, targetID).Take(&row).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return ReferenceSeen{Auto: map[string]string{}, Tool: map[string]string{}}, nil
		}
		return ReferenceSeen{}, err
	}
	seen := ReferenceSeen{Auto: map[string]string{}, Tool: map[string]string{}}
	if len(row.ReferenceSeen) > 0 {
		_ = sonic.Unmarshal(row.ReferenceSeen, &seen)
	}
	if seen.Auto == nil {
		seen.Auto = map[string]string{}
	}
	if seen.Tool == nil {
		seen.Tool = map[string]string{}
	}
	return seen, nil
}

// ResetReferenceSeen 用压缩 baseline 的 key/version 重置会话自动参考账本。
func (m *Manager) ResetReferenceSeen(tx *gorm.DB, kind string, targetID int64, auto map[string]string) error {
	if err := ensureConversationSession(tx, kind, targetID); err != nil {
		return err
	}
	if auto == nil {
		auto = map[string]string{}
	}
	seen := ReferenceSeen{Auto: auto, Tool: map[string]string{}}
	raw, err := sonic.Marshal(seen)
	if err != nil {
		return err
	}
	return tx.Model(&ConversationSession{}).Where("conversation_kind=? AND target_id=?", kind, targetID).Updates(map[string]any{"reference_seen": raw, "updated_at": time.Now()}).Error
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
// VisibleTranscript 返回检查点之后需要恢复进上下文的工作集记录
// pending 的 user/observation/agent_message/internal_reference 由各自的 pending 查询恢复
func (m *Manager) VisibleTranscript(ctx context.Context, kind string, targetID int64, afterVisibleSeq uint64) ([]TranscriptEntry, error) {
	var rows []TranscriptEntry
	q := m.db.WithContext(ctx).Where("conversation_kind=? AND target_id=?", kind, targetID)
	if afterVisibleSeq > 0 {
		q = q.Where("(visible_seq>? OR (visible_seq IS NULL AND entry_kind IN ('assistant','tool')))", afterVisibleSeq)
	} else {
		q = q.Where("visible_seq IS NOT NULL OR entry_kind IN ('assistant','tool')")
	}
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

type TranscriptSeed struct {
	EntryKind    string
	MessageLogID uint
	Payload      json.RawMessage
	CreatedAt    time.Time
}

// SeedTranscriptIfEmpty 把近期原文变成可见工作集，已有工作集时不写入
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
