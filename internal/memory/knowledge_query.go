package memory

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

func (m *Manager) SearchKnowledge(ctx context.Context, opts KnowledgeSearchOptions) ([]KnowledgeItem, error) {
	if opts.GroupID <= 0 {
		return nil, fmt.Errorf("知识查询需要有效群号，请使用当前群后重试")
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
			return nil, fmt.Errorf("知识主体无效，请使用群组、自身或有效成员 QQ 号")
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
	if !opts.IncludeInactive && !opts.ForMaintenance {
		base += " AND ki.status='active'"
	} else if opts.Status != "" {
		base += " AND ki.status=?"
		args = append(args, opts.Status)
	}
	if !opts.ForMaintenance {
		base += " AND " + knowledgeItemEvidenceSQL
	}
	if opts.ThroughID > 0 {
		base += " AND ki.reviewed_through_id<=?"
		args = append(args, opts.ThroughID)
		if opts.ForMaintenance {
			base += ` AND NOT EXISTS(SELECT 1 FROM knowledge_evidence_sets es JOIN knowledge_evidence_messages em ON em.evidence_set_id=es.id WHERE es.item_id=ki.id AND em.message_log_id>?)`
		} else {
			base += ` AND EXISTS(SELECT 1 FROM knowledge_evidence_sets es WHERE es.item_id=ki.id AND ` + KnowledgeEvidenceSetValiditySQL + ` AND NOT EXISTS(SELECT 1 FROM knowledge_evidence_messages em WHERE em.evidence_set_id=es.id AND em.message_log_id>?))`
		}
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
	if opts.IncludeInactive || opts.ForMaintenance {
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
	visibleItems := func() *gorm.DB {
		q := m.db.WithContext(ctx).Table("knowledge_items ki").Where("ki.group_id=?", groupID).Where(knowledgeItemEvidenceSQL)
		if len(opts.SubjectIDs) > 0 {
			q = q.Where("ki.subject_user_id=ANY(?)", int64Array(opts.SubjectIDs))
		}
		if upper > 0 {
			q = q.Where("ki.reviewed_through_id<=?", upper).Where(`EXISTS(SELECT 1 FROM knowledge_evidence_sets es WHERE es.item_id=ki.id AND `+KnowledgeEvidenceSetValiditySQL+` AND NOT EXISTS(SELECT 1 FROM knowledge_evidence_messages em WHERE em.evidence_set_id=es.id AND em.message_log_id>?))`, upper)
		}
		if !includeHistory {
			q = q.Where("ki.status='active'")
		}
		return q
	}
	load := func(ids []uint) ([]KnowledgeItem, error) {
		var rows []KnowledgeItem
		err := visibleItems().Where("ki.id IN ?", ids).Order("ki.id").Find(&rows).Error
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
		// 先过滤两端的状态、主体、证据和快照，再限制关系数，避免无效端点占满名额
		q := m.db.WithContext(ctx).Table("knowledge_relations kr").Select("kr.*").
			Where("kr.source_item_id IN ? OR kr.target_item_id IN ?", frontier, frontier).
			Where("kr.source_item_id IN (?) AND kr.target_item_id IN (?)", visibleItems().Select("ki.id"), visibleItems().Select("ki.id")).
			Where(`EXISTS(SELECT 1 FROM knowledge_evidence_sets es WHERE es.relation_id=kr.id AND ` + KnowledgeEvidenceSetValiditySQL + `)`)
		if !includeHistory {
			q = q.Where("kr.status='active'")
		}
		if upper > 0 {
			q = q.Where(`EXISTS(SELECT 1 FROM knowledge_evidence_sets es WHERE es.relation_id=kr.id AND `+KnowledgeEvidenceSetValiditySQL+` AND NOT EXISTS(SELECT 1 FROM knowledge_evidence_messages em WHERE em.evidence_set_id=es.id AND em.message_log_id>?))`, upper)
		}
		err = q.Order("kr.id").Limit(31).Scan(&relations).Error
		if err != nil {
			return nil, err
		}
		next := []uint{}
		endpointIDs := make([]uint, 0, len(relations)*2)
		for _, relation := range relations {
			endpointIDs = append(endpointIDs, relation.SourceItemID, relation.TargetItemID)
		}
		endpoints, err := load(endpointIDs)
		if err != nil {
			return nil, err
		}
		byID := make(map[uint]KnowledgeItem, len(endpoints))
		for _, endpoint := range endpoints {
			byID[endpoint.ID] = endpoint
		}
		for _, relation := range relations {
			if edges[relation.ID] {
				continue
			}
			if len(graph.Relations) >= 30 {
				graph.HasMore = true
				break
			}
			if _, sourceOK := byID[relation.SourceItemID]; !sourceOK {
				continue
			}
			if _, targetOK := byID[relation.TargetItemID]; !targetOK {
				continue
			}
			needed := 0
			if !seen[relation.SourceItemID] {
				needed++
			}
			if !seen[relation.TargetItemID] {
				needed++
			}
			if len(graph.Items)+needed > 20 {
				graph.HasMore = true
				continue
			}
			for _, id := range []uint{relation.SourceItemID, relation.TargetItemID} {
				if !seen[id] {
					seen[id] = true
					graph.Items = append(graph.Items, byID[id])
					next = append(next, id)
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
