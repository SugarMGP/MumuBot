package memory

import (
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *Manager) RecordMemberName(userID, groupID int64, value string, updatedAt time.Time) error {
	value = strings.TrimSpace(value)
	if userID == 0 || groupID == 0 || value == "" {
		return nil
	}
	name := MemberName{UserID: userID, GroupID: groupID, Value: value, UpdatedAt: updatedAt}
	return m.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "group_id"}, {Name: "value"}},
		DoUpdates: clause.Assignments(map[string]any{"updated_at": gorm.Expr("GREATEST(member_names.updated_at, EXCLUDED.updated_at)")}),
	}).Create(&name).Error
}

func (m *Manager) LatestMemberGroupCard(userID, groupID int64) (string, error) {
	var row MemberName
	err := m.db.Where("user_id = ? AND group_id = ?", userID, groupID).Order("updated_at DESC").First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return "", nil
	}
	return row.Value, err
}

func (m *Manager) ListMemberTraits(userID int64) ([]MemberTrait, error) {
	var rows []MemberTrait
	err := m.db.Where("user_id = ?", userID).Order("kind, updated_at DESC").Find(&rows).Error
	return rows, err
}

func saveMemberTrait(tx *gorm.DB, trait *MemberTrait, messageIDs []uint) error {
	if trait == nil {
		return nil
	}
	trait.Kind = strings.TrimSpace(trait.Kind)
	trait.Value = strings.TrimSpace(trait.Value)
	if trait.UserID == 0 || trait.Kind == "" || trait.Value == "" || len(messageIDs) == 0 {
		return nil
	}
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(trait)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		if err := tx.Where("user_id = ? AND kind = ? AND lower(btrim(value)) = lower(btrim(?))", trait.UserID, trait.Kind, trait.Value).First(trait).Error; err != nil {
			return err
		}
	}
	for _, messageID := range messageIDs {
		evidence := MemberTraitEvidence{MemberTraitID: trait.ID, MessageLogID: messageID}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&evidence).Error; err != nil {
			return err
		}
	}
	return nil
}
