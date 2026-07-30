package memory

import (
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *Manager) SaveJargonCandidate(tx *gorm.DB, jargon *Jargon, messageIDs []uint) error {
	if jargon == nil {
		return nil
	}
	jargon.Term = strings.TrimSpace(jargon.Term)
	jargon.Meaning = strings.TrimSpace(jargon.Meaning)
	if jargon.GroupID == 0 || jargon.Term == "" || jargon.Meaning == "" || len(messageIDs) == 0 {
		return nil
	}
	candidateMeaning := jargon.Meaning
	jargon.Status = CultureStatusCandidate
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(jargon)
	if result.Error != nil {
		return result.Error
	}
	created := result.RowsAffected == 1
	if !created {
		if err := tx.Where("group_id = ? AND lower(btrim(term)) = lower(btrim(?))", jargon.GroupID, jargon.Term).First(jargon).Error; err != nil {
			return err
		}
	}
	newEvidence := false
	for _, messageID := range messageIDs {
		evidence := JargonEvidence{JargonID: jargon.ID, MessageLogID: messageID}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&evidence)
		if result.Error != nil {
			return result.Error
		}
		newEvidence = newEvidence || result.RowsAffected == 1
	}
	if !created && newEvidence && jargon.Status != CultureStatusActive {
		return tx.Model(jargon).Updates(map[string]any{"meaning": candidateMeaning, "status": CultureStatusCandidate}).Error
	}
	return nil
}

func (m *Manager) SearchJargons(groupID int64, keyword string, limit int) ([]Jargon, error) {
	var rows []Jargon
	q := m.db.Where("group_id = ? AND status = ?", groupID, CultureStatusActive)
	if keyword = strings.TrimSpace(keyword); keyword != "" {
		q = q.Where("similarity(term, ?) >= 0.1 OR term ILIKE ? OR meaning ILIKE ?", keyword, "%"+keyword+"%", "%"+keyword+"%")
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.Order("updated_at DESC").Find(&rows).Error
	return rows, err
}

func (m *Manager) GetAllApprovedJargons() ([]Jargon, error) {
	var rows []Jargon
	err := m.db.Where("status = ?", CultureStatusActive).Order("group_id, term").Find(&rows).Error
	return rows, err
}
