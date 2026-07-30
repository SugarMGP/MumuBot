package memory

import (
	"context"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func saveStylePattern(tx *gorm.DB, pattern *StylePattern, messageIDs []uint) error {
	created := false
	if pattern.ID == 0 {
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(pattern)
		if result.Error != nil {
			return result.Error
		}
		created = result.RowsAffected == 1
	}
	if !created && pattern.ID == 0 {
		if err := tx.Where("group_id = ? AND lower(btrim(situation)) = lower(btrim(?)) AND lower(btrim(expression)) = lower(btrim(?))", pattern.GroupID, pattern.Situation, pattern.Expression).First(pattern).Error; err != nil {
			return err
		}
	}
	newEvidence := false
	for _, messageID := range messageIDs {
		evidence := StylePatternEvidence{StylePatternID: pattern.ID, MessageLogID: messageID}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&evidence)
		if result.Error != nil {
			return result.Error
		}
		newEvidence = newEvidence || result.RowsAffected == 1
	}
	if !created && newEvidence && pattern.Status == StylePatternStatusRejected {
		return tx.Model(pattern).Update("status", StylePatternStatusCandidate).Error
	}
	return nil
}

func (m *Manager) SearchStylePatterns(ctx context.Context, groupID int64, situation string, limit int) ([]StylePattern, error) {
	situation = strings.TrimSpace(situation)
	if groupID == 0 || situation == "" || limit <= 0 {
		return nil, nil
	}
	embedding, err := m.embedding.Embed(ctx, situation)
	if err != nil {
		return nil, err
	}
	vector, err := EmbeddingVector(embedding)
	if err != nil {
		return nil, err
	}
	var vectors []rankedIDRow
	if err := m.db.WithContext(ctx).Raw(`SELECT id FROM style_patterns
		WHERE group_id = ? AND status = 'active' AND 1 - (embedding <=> ?) >= 0.3
		ORDER BY embedding <=> ? LIMIT 20`, groupID, vector, vector).Scan(&vectors).Error; err != nil {
		return nil, err
	}
	var texts []rankedIDRow
	if err := m.db.WithContext(ctx).Raw(`SELECT id FROM style_patterns
		WHERE group_id = ? AND status = 'active' AND similarity(situation, ?) >= 0.1
		ORDER BY similarity(situation, ?) DESC LIMIT 20`, groupID, situation, situation).Scan(&texts).Error; err != nil {
		return nil, err
	}
	ids := fuseRRF(rankRows(vectors), rankRows(texts))
	if len(ids) > limit {
		ids = ids[:limit]
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []StylePattern
	if err := m.db.WithContext(ctx).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	byID := make(map[uint]StylePattern, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	result := make([]StylePattern, 0, len(rows))
	for _, id := range ids {
		if row, ok := byID[id]; ok {
			result = append(result, row)
		}
	}
	return result, nil
}
