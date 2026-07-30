package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

type CultureReviewEvidence struct {
	MessageID uint
	Text      string
}

type CultureReviewItem struct {
	Kind     string
	ID       uint
	Label    string
	Value    string
	Evidence []CultureReviewEvidence
}

type LearningMessage struct {
	MessageLog
	AssignmentID *uint
	TopicID      *uint
}

type CultureStyleInput struct {
	Situation  string
	Expression string
	MessageIDs []uint
}

type CultureJargonInput struct {
	Term       string
	Meaning    string
	MessageIDs []uint
}

type MemberTraitInput struct {
	UserID     int64
	Kind       string
	Value      string
	MessageIDs []uint
}

func (m *Manager) GetProcessableLearningBatch(groupID int64, lastID uint, limit int) ([]LearningMessage, error) {
	var rows []LearningMessage
	err := m.db.Table("message_logs ml").Select("ml.*, ta.id AS assignment_id, ta.topic_id").
		Joins("LEFT JOIN topic_assignments ta ON ta.message_log_id = ml.id").
		Where("ml.group_id = ? AND ml.id > ?", groupID, lastID).
		Order("ml.id ASC").Limit(limit).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	return completedLearningPrefix(rows), nil
}

func completedLearningPrefix(rows []LearningMessage) []LearningMessage {
	ready := rows[:0]
	for _, row := range rows {
		if row.AssignmentID == nil {
			break
		}
		ready = append(ready, row)
	}
	return ready
}

func (m *Manager) CommitCultureBatch(ctx context.Context, groupID int64, watermark uint, styles []CultureStyleInput, jargons []CultureJargonInput) error {
	type preparedStyle struct {
		pattern StylePattern
		ids     []uint
	}
	prepared := make([]preparedStyle, 0, len(styles))
	preparedByKey := make(map[string]int, len(styles))
	for _, input := range styles {
		situation := strings.TrimSpace(input.Situation)
		expression := strings.TrimSpace(input.Expression)
		if situation == "" || expression == "" {
			continue
		}
		key := strings.ToLower(situation) + "\x00" + strings.ToLower(expression)
		if index, ok := preparedByKey[key]; ok {
			prepared[index].ids = append(prepared[index].ids, input.MessageIDs...)
			continue
		}
		pattern := StylePattern{}
		err := m.db.WithContext(ctx).
			Where("group_id = ? AND lower(btrim(situation)) = lower(btrim(?)) AND lower(btrim(expression)) = lower(btrim(?))", groupID, situation, expression).
			First(&pattern).Error
		if err == nil {
			preparedByKey[key] = len(prepared)
			prepared = append(prepared, preparedStyle{pattern: pattern, ids: input.MessageIDs})
			continue
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		embedding, err := m.embedding.Embed(ctx, situation+"\n"+expression)
		if err != nil {
			return err
		}
		vector, err := EmbeddingVector(embedding)
		if err != nil {
			return err
		}
		preparedByKey[key] = len(prepared)
		prepared = append(prepared, preparedStyle{pattern: StylePattern{GroupID: groupID, Situation: situation, Expression: expression, Status: StylePatternStatusCandidate, Embedding: vector}, ids: input.MessageIDs})
	}
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, item := range prepared {
			if err := saveStylePattern(tx, &item.pattern, item.ids); err != nil {
				return err
			}
		}
		for _, input := range jargons {
			term := strings.TrimSpace(input.Term)
			meaning := strings.TrimSpace(input.Meaning)
			item := Jargon{GroupID: groupID, Term: term, Meaning: meaning, Status: CultureStatusCandidate}
			if err := m.SaveJargonCandidate(tx, &item, input.MessageIDs); err != nil {
				return err
			}
		}
		return updateLearningState(tx, groupID, LearningKindCulture, watermark)
	})
}

func (m *Manager) CommitMemberProfileBatch(ctx context.Context, groupID int64, watermark uint, traits []MemberTraitInput) error {
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, input := range traits {
			trait := MemberTrait{UserID: input.UserID, Kind: input.Kind, Value: input.Value}
			if err := saveMemberTrait(tx, &trait, input.MessageIDs); err != nil {
				return err
			}
		}
		return updateLearningState(tx, groupID, LearningKindMemberProfile, watermark)
	})
}

func (m *Manager) ReviewCulture(idsStyle, idsJargon []uint, approveStyle, approveJargon map[uint]bool) error {
	return m.db.Transaction(func(tx *gorm.DB) error {
		for _, id := range idsStyle {
			status := StylePatternStatusRejected
			if approveStyle[id] {
				status = StylePatternStatusActive
			}
			if err := tx.Model(&StylePattern{}).Where("id = ? AND status = ?", id, StylePatternStatusCandidate).Update("status", status).Error; err != nil {
				return err
			}
		}
		for _, id := range idsJargon {
			status := CultureStatusRejected
			if approveJargon[id] {
				status = CultureStatusActive
			}
			if err := tx.Model(&Jargon{}).Where("id = ? AND status = ?", id, CultureStatusCandidate).Update("status", status).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (m *Manager) ListCultureReviewItems(groupID int64, limit int) ([]CultureReviewItem, error) {
	if groupID == 0 || limit <= 0 {
		return nil, nil
	}
	type row struct {
		Kind        string
		ID          uint
		Label       string
		Value       string
		MessageID   uint
		TextContent string
	}
	var rows []row
	err := m.db.Raw(`WITH candidates AS (
		SELECT 'style' kind, sp.id, sp.situation label, sp.expression value, sp.created_at
		FROM style_patterns sp WHERE sp.group_id = ? AND sp.status = 'candidate'
		AND (SELECT count(*) FROM style_pattern_evidence e WHERE e.style_pattern_id = sp.id) >= 2
		UNION ALL
		SELECT 'jargon' kind, j.id, j.term label, j.meaning value, j.created_at
		FROM jargons j WHERE j.group_id = ? AND j.status = 'candidate'
		AND (SELECT count(*) FROM jargon_evidence e WHERE e.jargon_id = j.id) >= 2
		ORDER BY created_at ASC LIMIT ?
	), evidence AS (
		SELECT 'style' kind, e.style_pattern_id id, e.message_log_id FROM style_pattern_evidence e
		UNION ALL
		SELECT 'jargon' kind, e.jargon_id id, e.message_log_id FROM jargon_evidence e
	)
	SELECT c.kind, c.id, c.label, c.value, ml.id message_id, ml.text_content
	FROM candidates c JOIN evidence e ON e.kind = c.kind AND e.id = c.id
	JOIN message_logs ml ON ml.id = e.message_log_id
	WHERE btrim(ml.text_content) <> '' ORDER BY c.created_at, c.kind, c.id, ml.id`, groupID, groupID, limit).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	items := make([]CultureReviewItem, 0, limit)
	index := make(map[string]int, limit)
	for _, row := range rows {
		key := row.Kind + ":" + fmt.Sprint(row.ID)
		i, ok := index[key]
		if !ok {
			index[key] = len(items)
			items = append(items, CultureReviewItem{Kind: row.Kind, ID: row.ID, Label: row.Label, Value: row.Value})
			i = len(items) - 1
		}
		items[i].Evidence = append(items[i].Evidence, CultureReviewEvidence{MessageID: row.MessageID, Text: row.TextContent})
	}
	return items, nil
}
