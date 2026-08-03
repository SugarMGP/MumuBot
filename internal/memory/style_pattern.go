package memory

import (
	"context"
	"fmt"
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

type ExpressionMatch struct {
	Situation  string
	Expression string
	Examples   []string
}

func (m *Manager) SearchExpressions(ctx context.Context, groupID int64, query string, throughOneBotMessageID int64, limit int) ([]ExpressionMatch, error) {
	query = strings.TrimSpace(query)
	if groupID == 0 || query == "" || throughOneBotMessageID <= 0 || limit <= 0 {
		return nil, nil
	}
	var upperBound uint
	if err := m.db.WithContext(ctx).Model(&MessageLog{}).Select("id").
		Where("group_id = ? AND one_bot_message_id = ?", groupID, throughOneBotMessageID).
		Scan(&upperBound).Error; err != nil {
		return nil, err
	}
	if upperBound == 0 {
		return nil, fmt.Errorf("表达方式查询缺少有效消息快照")
	}

	prepared, err := m.PrepareHybridQuery(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	var vectors []rankedIDRow
	if err := m.db.WithContext(ctx).Raw(`SELECT sp.id FROM style_patterns sp
		WHERE sp.group_id = ? AND sp.status = 'active' AND 1 - (sp.embedding <=> ?) >= 0.3
		AND EXISTS (
			SELECT 1 FROM style_pattern_evidence e JOIN message_logs ml ON ml.id = e.message_log_id
			WHERE e.style_pattern_id = sp.id AND ml.recalled_at IS NULL
				AND btrim(ml.text_content) <> '' AND ml.id <= ?
		)
		ORDER BY sp.embedding <=> ? LIMIT 20`, groupID, prepared.embedding, upperBound, prepared.embedding).Scan(&vectors).Error; err != nil {
		return nil, err
	}
	var texts []rankedIDRow
	if err := m.db.WithContext(ctx).Raw(`SELECT id FROM (
		SELECT sp.id, greatest(
			max(greatest(word_similarity(?, sp.situation), word_similarity(sp.situation, ?))),
			max(greatest(word_similarity(?, sp.expression), word_similarity(sp.expression, ?))),
			max(greatest(word_similarity(?, ml.text_content), word_similarity(ml.text_content, ?)))
		) score
		FROM style_patterns sp
		JOIN style_pattern_evidence e ON e.style_pattern_id = sp.id
		JOIN message_logs ml ON ml.id = e.message_log_id
		WHERE sp.group_id = ? AND sp.status = 'active' AND ml.recalled_at IS NULL
			AND btrim(ml.text_content) <> '' AND ml.id <= ?
		GROUP BY sp.id
	) ranked WHERE score >= 0.1 ORDER BY score DESC LIMIT 20`, query, query, query, query, query, query, groupID, upperBound).Scan(&texts).Error; err != nil {
		return nil, err
	}
	ids := fuseRRF(rankRows(vectors), rankRows(texts))
	if len(ids) > limit {
		ids = ids[:limit]
	}
	if len(ids) == 0 {
		return nil, nil
	}
	type expressionRow struct {
		ID          uint
		Situation   string
		Expression  string
		TextContent string
	}
	var rows []expressionRow
	if err := m.db.WithContext(ctx).Raw(`SELECT id, situation, expression, text_content FROM (
		SELECT sp.id, sp.situation, sp.expression, ml.text_content,
			row_number() OVER (PARTITION BY sp.id ORDER BY ml.id DESC) example_rank
		FROM style_patterns sp
		JOIN style_pattern_evidence e ON e.style_pattern_id = sp.id
		JOIN message_logs ml ON ml.id = e.message_log_id
		WHERE sp.id IN ? AND sp.group_id = ? AND sp.status = 'active'
			AND ml.recalled_at IS NULL AND btrim(ml.text_content) <> '' AND ml.id <= ?
	) examples WHERE example_rank <= 3 ORDER BY id, example_rank`, ids, groupID, upperBound).Scan(&rows).Error; err != nil {
		return nil, err
	}
	byID := make(map[uint]*ExpressionMatch, len(ids))
	for _, row := range rows {
		item := byID[row.ID]
		if item == nil {
			item = &ExpressionMatch{Situation: row.Situation, Expression: row.Expression}
			byID[row.ID] = item
		}
		item.Examples = append(item.Examples, row.TextContent)
	}
	result := make([]ExpressionMatch, 0, len(ids))
	for _, id := range ids {
		if item := byID[id]; item != nil && len(item.Examples) > 0 {
			result = append(result, *item)
		}
	}
	return result, nil
}
