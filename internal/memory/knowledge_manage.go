package memory

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *Manager) GetWorkingNote(ctx context.Context, groupID int64) (*ConversationAgentState, error) {
	return m.GetWorkingNoteScope(ctx, ConversationKindGroup, groupID)
}

func (m *Manager) GetWorkingNoteScope(ctx context.Context, kind string, targetID int64) (*ConversationAgentState, error) {
	var row ConversationAgentState
	err := m.db.WithContext(ctx).First(&row, "conversation_kind=? AND target_id=?", kind, targetID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &ConversationAgentState{ConversationKind: kind, TargetID: targetID}, nil
	}
	return &row, err
}

func (m *Manager) SaveWorkingNote(ctx context.Context, groupID int64, note string) error {
	return m.SaveWorkingNoteScope(ctx, ConversationKindGroup, groupID, note)
}

func (m *Manager) SaveWorkingNoteScope(ctx context.Context, kind string, targetID int64, note string) error {
	note = strings.TrimSpace(note)
	if targetID <= 0 || utf8.RuneCountInString(note) > 300 {
		return errors.New("便签目标无效或超过 300 个字符")
	}
	if note == "" {
		return m.db.WithContext(ctx).Where("conversation_kind=? AND target_id=?", kind, targetID).Delete(&ConversationAgentState{}).Error
	}
	row := ConversationAgentState{ConversationKind: kind, TargetID: targetID, Note: note, UpdatedAt: time.Now()}
	return m.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "conversation_kind"}, {Name: "target_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"note", "updated_at"}),
	}).Create(&row).Error
}
