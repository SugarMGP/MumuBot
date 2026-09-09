package memory

import (
	"context"
	"fmt"
	"strings"
)

func (m *Manager) SearchKnowledge(ctx context.Context, opts KnowledgeSearchOptions) ([]KnowledgeItem, error) {
	if opts.GroupID <= 0 {
		return nil, fmt.Errorf("knowledge queries require a group")
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 6
	}
	if limit > 100 {
		limit = 100
	}
	base := "ki.group_id=?"
	args := []any{opts.GroupID}
	if opts.SelfID > 0 && opts.SubjectUserID != nil && *opts.SubjectUserID == opts.SelfID {
		base = "TRUE"
		args = nil
	}
	if opts.ItemID > 0 {
		base += " AND ki.id=?"
		args = append(args, opts.ItemID)
	}
	if opts.SubjectUserID != nil {
		if *opts.SubjectUserID < 0 {
			return nil, fmt.Errorf("invalid subject")
		}
		base += " AND ki.subject_user_id=?"
		args = append(args, *opts.SubjectUserID)
	} else if len(opts.SubjectIDs) > 0 {
		base += " AND ki.subject_user_id=ANY(?)"
		args = append(args, int64Array(opts.SubjectIDs))
	}
	if opts.Kind != "" {
		base += " AND ki.kind=?"
		args = append(args, opts.Kind)
	}
	if !opts.IncludeInactive {
		base += " AND ki.status='active' AND " + knowledgeEvidenceSQL("item_id")
	} else if opts.Status != "" {
		base += " AND ki.status=?"
		args = append(args, opts.Status)
	}
	if opts.ThroughID > 0 {
		base += " AND ki.reviewed_through_id<=?"
		args = append(args, opts.ThroughID)
		base += ` AND ((ki.status='candidate' AND NOT EXISTS(SELECT 1 FROM knowledge_evidence_sets es WHERE es.item_id=ki.id)) OR EXISTS(SELECT 1 FROM knowledge_evidence_sets es WHERE es.item_id=ki.id AND (ki.status='candidate' OR (` + validKnowledgeSetSQL + `)) AND NOT EXISTS(SELECT 1 FROM knowledge_evidence_messages em WHERE em.evidence_set_id=es.id AND em.message_log_id>?)))`
		args = append(args, opts.ThroughID)
	}
	query := strings.TrimSpace(opts.Query)
	if query == "" && opts.Prepared == nil {
		var rows []KnowledgeItem
		err := m.db.WithContext(ctx).Table("knowledge_items ki").Where(base, args...).Order("ki.updated_at DESC,ki.id DESC").Limit(limit).Offset(max(0, opts.Offset)).Find(&rows).Error
		return rows, err
	}
	var prepared HybridQuery
	if opts.Prepared != nil {
		prepared = *opts.Prepared
	} else {
		var err error
		prepared, err = m.PrepareHybridQuery(ctx, []string{query})
		if err != nil {
			return nil, err
		}
	}
	if prepared.Empty() {
		return nil, nil
	}
	var vectors, texts, labels []rankedIDRow
	poolLimit := " LIMIT 30"
	if opts.IncludeInactive {
		poolLimit = ""
	}
	if len(prepared.embedding.Slice()) > 0 {
		sql := `SELECT ki.id FROM knowledge_items ki WHERE ` + base + ` AND ki.embedding IS NOT NULL AND 1-(ki.embedding <=> ?)>=? ORDER BY ki.embedding <=> ?,ki.id` + poolLimit
		values := append(append([]any{}, args...), prepared.embedding, 0.3, prepared.embedding)
		if err := m.db.WithContext(ctx).Raw(sql, values...).Scan(&vectors).Error; err != nil {
			return nil, err
		}
	}
	sql := `SELECT id FROM (SELECT ki.id,(SELECT max(greatest(public.word_similarity(ki.content,fragment),public.word_similarity(fragment,ki.content))) FROM unnest(?::text[]) fragments(fragment)) score FROM knowledge_items ki WHERE ` + base + `) ranked WHERE score>=? ORDER BY score DESC,id` + poolLimit
	values := append([]any{prepared.FragmentArray()}, args...)
	values = append(values, contextTextThreshold)
	if err := m.db.WithContext(ctx).Raw(sql, values...).Scan(&texts).Error; err != nil {
		return nil, err
	}
	sql = `SELECT ki.id FROM knowledge_items ki WHERE ` + base + ` AND ki.label<>'' AND EXISTS(SELECT 1 FROM unnest(?::text[]) fragments(fragment) WHERE position(lower(btrim(ki.label)) in lower(fragment))>0) ORDER BY ki.id` + poolLimit
	values = append(append([]any{}, args...), prepared.FragmentArray())
	if err := m.db.WithContext(ctx).Raw(sql, values...).Scan(&labels).Error; err != nil {
		return nil, err
	}
	ids := fuseRRF(rankRows(labels), rankRows(vectors), rankRows(texts))
	if opts.Offset > 0 {
		if opts.Offset >= len(ids) {
			return nil, nil
		}
		ids = ids[opts.Offset:]
	}
	if len(ids) > limit {
		ids = ids[:limit]
	}
	return m.loadKnowledgeInOrder(ctx, ids, base, args)
}

func (m *Manager) loadKnowledgeInOrder(ctx context.Context, ids []uint, base string, args []any) ([]KnowledgeItem, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []KnowledgeItem
	if err := m.db.WithContext(ctx).Table("knowledge_items ki").Where(base, args...).Where("ki.id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	byID := map[uint]KnowledgeItem{}
	for _, row := range rows {
		byID[row.ID] = row
	}
	result := make([]KnowledgeItem, 0, len(ids))
	for _, id := range ids {
		if row, ok := byID[id]; ok {
			result = append(result, row)
		}
	}
	return result, nil
}

func (m *Manager) GetKnowledgeNeighborhood(ctx context.Context, groupID int64, seedIDs []uint, depth int, includeHistory bool, opts KnowledgeGraphOptions) (*KnowledgeGraph, error) {
	if groupID <= 0 || len(seedIDs) == 0 {
		return &KnowledgeGraph{}, nil
	}
	depth = max(1, min(depth, 3))
	graph := &KnowledgeGraph{}
	upper := opts.ThroughID
	seen := map[uint]bool{}
	edges := map[uint]bool{}
	load := func(ids []uint) ([]KnowledgeItem, error) {
		var rows []KnowledgeItem
		q := m.db.WithContext(ctx).Table("knowledge_items ki").Where("ki.group_id=? AND ki.id IN ?", groupID, ids).Where(knowledgeEvidenceSQL("item_id"))
		if len(opts.SubjectIDs) > 0 {
			q = q.Where("ki.subject_user_id=ANY(?)", int64Array(opts.SubjectIDs))
		}
		if upper > 0 {
			q = q.Where("ki.reviewed_through_id<=?", upper).Where(`EXISTS(SELECT 1 FROM knowledge_evidence_sets es WHERE es.item_id=ki.id AND `+validKnowledgeSetSQL+` AND NOT EXISTS(SELECT 1 FROM knowledge_evidence_messages em WHERE em.evidence_set_id=es.id AND em.message_log_id>?))`, upper)
		}
		if includeHistory {
			q = q.Where("ki.status IN ('active','archived')")
		} else {
			q = q.Where("ki.status='active'")
		}
		err := q.Order("ki.id").Find(&rows).Error
		return rows, err
	}
	seeds, err := load(seedIDs)
	if err != nil {
		return nil, err
	}
	frontier := []uint{}
	for _, row := range seeds {
		if len(graph.Items) == 20 {
			graph.HasMore = true
			break
		}
		if !seen[row.ID] {
			seen[row.ID] = true
			graph.Items = append(graph.Items, row)
			frontier = append(frontier, row.ID)
		}
	}
	for step := 0; step < depth && len(frontier) > 0; step++ {
		var relations []KnowledgeRelation
		q := m.db.WithContext(ctx).Table("knowledge_relations kr").Select("kr.*").Joins("JOIN knowledge_items s ON s.id=kr.source_item_id JOIN knowledge_items t ON t.id=kr.target_item_id").Where("s.group_id=? AND t.group_id=? AND (kr.source_item_id IN ? OR kr.target_item_id IN ?)", groupID, groupID, frontier, frontier).Where("kr.status='active'").Where(`EXISTS(SELECT 1 FROM knowledge_evidence_sets es WHERE es.relation_id=kr.id AND ` + validKnowledgeSetSQL + `)`)
		if len(opts.SubjectIDs) > 0 {
			subjects := int64Array(opts.SubjectIDs)
			q = q.Where("s.subject_user_id=ANY(?) AND t.subject_user_id=ANY(?)", subjects, subjects)
		}
		err = q.Order("kr.id").Limit(31).Scan(&relations).Error
		if err != nil {
			return nil, err
		}
		next := []uint{}
		for _, relation := range relations {
			if upper > 0 {
				var usable int64
				if err := m.db.WithContext(ctx).Table("knowledge_evidence_sets es").Where("es.relation_id=?", relation.ID).Where(validKnowledgeSetSQL).Where("NOT EXISTS(SELECT 1 FROM knowledge_evidence_messages em WHERE em.evidence_set_id=es.id AND em.message_log_id>?)", upper).Count(&usable).Error; err != nil {
					return nil, err
				}
				if usable == 0 {
					continue
				}
			}
			if edges[relation.ID] {
				continue
			}
			if len(graph.Relations) >= 30 {
				graph.HasMore = true
				break
			}
			rows, e := load([]uint{relation.SourceItemID, relation.TargetItemID})
			if e != nil {
				return nil, e
			}
			if len(rows) != 2 {
				continue
			}
			needed := 0
			for _, row := range rows {
				if !seen[row.ID] {
					needed++
				}
			}
			if len(graph.Items)+needed > 20 {
				graph.HasMore = true
				continue
			}
			for _, row := range rows {
				if !seen[row.ID] {
					seen[row.ID] = true
					graph.Items = append(graph.Items, row)
					next = append(next, row.ID)
				}
			}
			edges[relation.ID] = true
			graph.Relations = append(graph.Relations, relation)
		}
		frontier = next
	}
	return graph, nil
}

func (m *Manager) GetKnowledgeItem(ctx context.Context, groupID int64, id uint) (*KnowledgeItem, error) {
	var item KnowledgeItem
	err := m.db.WithContext(ctx).Where("group_id=? AND id=?", groupID, id).First(&item).Error
	return &item, err
}
