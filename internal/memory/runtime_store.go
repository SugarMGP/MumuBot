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

func ensureConversationSession(tx *gorm.DB, kind string, targetID int64) error {
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ConversationSession{
		ConversationKind: kind, TargetID: targetID,
	}).Error
}

// AllocateInputSeq 在事务内分配会话输入序号
func (m *Manager) AllocateInputSeq(tx *gorm.DB, kind string, targetID int64) (uint64, error) {
	_, end, err := allocateSequenceRange(tx, kind, targetID, "next_input_seq", 1)
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

// AppendTranscripts 在一个事务内写入多条工作集记录，保证代头不会只写一半
func (m *Manager) AppendTranscripts(ctx context.Context, entries []*TranscriptEntry) error {
	if len(entries) == 0 {
		return nil
	}
	for _, entry := range entries {
		if entry == nil || entry.ConversationKind == "" || entry.TargetID <= 0 || entry.EntryKind == "" {
			return fmt.Errorf("transcript 参数无效")
		}
		if len(entry.Payload) == 0 {
			entry.Payload = json.RawMessage(`{}`)
		}
	}
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, entry := range entries {
			if err := tx.Create(entry).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// AppendUserTranscript 与原文写入共用事务，避免留下没有输入工作集的半条消息
func (m *Manager) AppendUserTranscript(tx *gorm.DB, kind string, targetID int64, logID uint, payload json.RawMessage, runID string) (*TranscriptEntry, error) {
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
	inputSeq, err := m.AllocateInputSeq(tx, kind, targetID)
	if err != nil {
		return nil, err
	}
	entry := &TranscriptEntry{
		ConversationKind: kind, TargetID: targetID, InputSeq: &inputSeq,
		EntryKind: entryKind, MessageLogID: messageLogID, RunID: runID, Payload: payload,
	}
	return entry, tx.Create(entry).Error
}

// PendingInputs 返回尚未进入模型的输入，包括真人消息、观察和会话间消息
func (m *Manager) PendingInputs(ctx context.Context, kind string, targetID int64) ([]TranscriptEntry, error) {
	return m.PendingInputsAfter(ctx, kind, targetID, 0)
}

// PendingInputsAfter 只恢复显式工作集边界之后的待处理输入
func (m *Manager) PendingInputsAfter(ctx context.Context, kind string, targetID int64, afterID uint) ([]TranscriptEntry, error) {
	var rows []TranscriptEntry
	q := m.db.WithContext(ctx).Where("conversation_kind=? AND target_id=? AND entry_kind IN ('user','observation','agent_message') AND visible_seq IS NULL", kind, targetID)
	if afterID > 0 {
		q = q.Where("id>?", afterID)
	}
	err := q.Order("input_seq,id").Find(&rows).Error
	return rows, err
}

// MarkVisible 在模型成功后推进 visible_seq。
func (m *Manager) MarkVisible(ctx context.Context, kind string, targetID int64, ids []uint) (map[uint]uint64, error) {
	result := map[uint]uint64{}
	if len(ids) == 0 {
		return result, nil
	}
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = markVisibleTx(tx, kind, targetID, ids)
		return err
	})
	return result, err
}

// markVisibleTx 在调用方事务里按给定顺序给尚未可见的行分配 visible_seq
func markVisibleTx(tx *gorm.DB, kind string, targetID int64, ids []uint) (map[uint]uint64, error) {
	result := map[uint]uint64{}
	unique := make([]uint, 0, len(ids))
	seen := map[uint]bool{}
	for _, id := range ids {
		if id != 0 && !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	if len(unique) == 0 {
		return result, nil
	}
	rows := make([]TranscriptEntry, 0, len(unique))
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id IN ? AND conversation_kind=? AND target_id=?", unique, kind, targetID).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) != len(unique) {
		return nil, fmt.Errorf("transcript 记录不存在或不属于当前会话")
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
	if len(unvisible) == 0 {
		return result, nil
	}
	start, _, err := allocateSequenceRange(tx, kind, targetID, "next_visible_seq", uint64(len(unvisible)))
	if err != nil {
		return nil, err
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
		return nil, err
	}
	return result, nil
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

// VisibleTranscript 返回检查点之后需要恢复进上下文的工作集记录
// pending 的 user/observation/agent_message 由各自的 pending 查询恢复
func (m *Manager) VisibleTranscript(ctx context.Context, kind string, targetID int64, afterVisibleSeq uint64, afterID uint) ([]TranscriptEntry, error) {
	var rows []TranscriptEntry
	q := m.db.WithContext(ctx).Where("conversation_kind=? AND target_id=?", kind, targetID)
	// worker 和生活主线用检查点内部编号裁剪恢复范围，不必每次把完整历史装回内存
	if afterID > 0 {
		q = q.Where("id>?", afterID)
	}
	// 代头行在首次成功请求前没有 visible_seq，必须一并恢复，否则重启会重复写入
	openKinds := "('assistant','tool','system_prompt','context_digest','group_note','system_note','pinned_memory')"
	if afterVisibleSeq > 0 {
		q = q.Where("(visible_seq>? OR (visible_seq IS NULL AND entry_kind IN "+openKinds+"))", afterVisibleSeq)
	} else {
		q = q.Where("visible_seq IS NOT NULL OR entry_kind IN " + openKinds)
	}
	// 代头永远排在最前，其余按交付顺序
	err := q.Order("CASE WHEN entry_kind IN ('system_prompt','group_note','context_digest','pinned_memory') THEN 0 ELSE 1 END, visible_seq NULLS LAST, id").Find(&rows).Error
	return rows, err
}

// DeleteTranscriptHeads 删除压缩换代的头部行，返回本批引用候选
func (m *Manager) DeleteTranscriptHeads(tx *gorm.DB, kind string, targetID int64) (TranscriptCandidates, error) {
	condition := "conversation_kind=? AND target_id=? AND entry_kind IN ('system_prompt','group_note','context_digest','pinned_memory')"
	args := []any{kind, targetID}
	candidates, err := transcriptCandidates(tx, condition, args...)
	if err != nil {
		return TranscriptCandidates{}, err
	}
	if err := tx.Where(condition, args...).Delete(&TranscriptEntry{}).Error; err != nil {
		return TranscriptCandidates{}, err
	}
	return candidates, nil
}

// DeleteTranscriptThrough 删除检查点覆盖的工作集记录，并返回本批引用候选
func (m *Manager) DeleteTranscriptThrough(tx *gorm.DB, kind string, targetID int64, visibleSeq uint64) (TranscriptCandidates, error) {
	if visibleSeq == 0 {
		return TranscriptCandidates{}, nil
	}
	condition := "conversation_kind=? AND target_id=? AND visible_seq IS NOT NULL AND visible_seq<=?"
	args := []any{kind, targetID, visibleSeq}
	candidates, err := transcriptCandidates(tx, condition, args...)
	if err != nil {
		return TranscriptCandidates{}, err
	}
	if err := tx.Where(condition, args...).Delete(&TranscriptEntry{}).Error; err != nil {
		return TranscriptCandidates{}, err
	}
	return candidates, nil
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
		DoUpdates: clause.AssignmentColumns([]string{"version", "through_visible_seq", "through_message_log_id", "overview", "active_topic_versions", "batch_id", "updated_at"}),
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
