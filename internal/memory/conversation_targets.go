package memory

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ContactSnapshot struct {
	Kind         string
	TargetID     int64
	Name         string
	RemoteRemark string
	Active       bool
	LastSeenAt   time.Time
}

func (m *Manager) UpsertConversationTarget(ctx context.Context, target ConversationTarget) error {
	if target.ConversationKind == "" || target.TargetID <= 0 {
		return gorm.ErrInvalidData
	}
	if target.UpdatedAt.IsZero() {
		target.UpdatedAt = time.Now()
	}
	return m.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "conversation_kind"}, {Name: "target_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"name":          target.Name,
			"remote_remark": target.RemoteRemark,
			"active":        target.Active,
			"last_seen_at":  target.LastSeenAt,
			"updated_at":    target.UpdatedAt,
		}),
	}).Create(&target).Error
}

func (m *Manager) SyncConversationContacts(ctx context.Context, contacts []ContactSnapshot) error {
	seen := make(map[string]struct{}, len(contacts))
	for _, contact := range contacts {
		key := contact.Kind + ":" + fmt.Sprint(contact.TargetID)
		seen[key] = struct{}{}
		if err := m.UpsertConversationTarget(ctx, ConversationTarget{
			ConversationKind: contact.Kind,
			TargetID:         contact.TargetID,
			Name:             contact.Name,
			RemoteRemark:     contact.RemoteRemark,
			Active:           contact.Active,
			LastSeenAt:       &contact.LastSeenAt,
			UpdatedAt:        time.Now(),
		}); err != nil {
			return err
		}
	}
	for _, kind := range []string{ConversationKindGroup, ConversationKindPrivate} {
		var rows []ConversationTarget
		if err := m.db.WithContext(ctx).Where("conversation_kind=?", kind).Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			if _, ok := seen[kind+":"+fmt.Sprint(row.TargetID)]; ok {
				continue
			}
			if err := m.db.WithContext(ctx).Model(&ConversationTarget{}).Where("conversation_kind=? AND target_id=?", kind, row.TargetID).Updates(map[string]any{"active": false, "updated_at": time.Now()}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// MarkConversationActive 只更新会话活跃状态：active=false 只改已有记录；
// active=true 时缺少记录会补一条最小记录，保留已有的备注、拉黑和额外提示
func (m *Manager) MarkConversationActive(ctx context.Context, kind string, targetID int64, active bool) error {
	if targetID <= 0 || (kind != ConversationKindGroup && kind != ConversationKindPrivate) {
		return gorm.ErrInvalidData
	}
	if !active {
		return m.db.WithContext(ctx).Model(&ConversationTarget{}).
			Where("conversation_kind=? AND target_id=?", kind, targetID).
			Updates(map[string]any{"active": false, "updated_at": time.Now()}).Error
	}
	return m.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "conversation_kind"}, {Name: "target_id"}},
		DoUpdates: clause.Assignments(map[string]any{"active": true, "updated_at": time.Now()}),
	}).Create(&ConversationTarget{ConversationKind: kind, TargetID: targetID, Active: true, UpdatedAt: time.Now()}).Error
}

func (m *Manager) ListConversationTargets(ctx context.Context, kind string, activeOnly bool) ([]ConversationTarget, error) {
	var rows []ConversationTarget
	q := m.db.WithContext(ctx).Where("conversation_kind=?", kind).Order("active DESC, target_id")
	if activeOnly {
		q = q.Where("active=true")
	}
	return rows, q.Find(&rows).Error
}

func (m *Manager) GetConversationTarget(ctx context.Context, kind string, targetID int64) (*ConversationTarget, error) {
	var row ConversationTarget
	if err := m.db.WithContext(ctx).Where("conversation_kind=? AND target_id=?", kind, targetID).First(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func (m *Manager) ConversationAllowed(ctx context.Context, kind string, targetID int64) (bool, error) {
	row, err := m.GetConversationTarget(ctx, kind, targetID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return false, nil
		}
		return false, err
	}
	return row.Active && !row.Blocked, nil
}

func (m *Manager) ListActiveGroupIDs(ctx context.Context) ([]int64, error) {
	rows, err := m.ListConversationTargets(ctx, ConversationKindGroup, true)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		if !row.Blocked {
			ids = append(ids, row.TargetID)
		}
	}
	return ids, nil
}

func (m *Manager) GetGroupExtraPrompt(ctx context.Context, groupID int64) (string, error) {
	row, err := m.GetConversationTarget(ctx, ConversationKindGroup, groupID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", nil
		}
		return "", err
	}
	return row.ExtraPrompt, nil
}

func (m *Manager) SetConversationBlocked(ctx context.Context, kind string, targetID int64, blocked bool) error {
	result := m.db.WithContext(ctx).Model(&ConversationTarget{}).
		Where("conversation_kind=? AND target_id=?", kind, targetID).
		Updates(map[string]any{"blocked": blocked, "updated_at": time.Now()}).Error
	return result
}

func (m *Manager) SetConversationExtraPrompt(ctx context.Context, targetID int64, prompt string) error {
	result := m.db.WithContext(ctx).Model(&ConversationTarget{}).
		Where("conversation_kind=? AND target_id=?", ConversationKindGroup, targetID).
		Updates(map[string]any{"extra_prompt": prompt, "updated_at": time.Now()}).Error
	return result
}
