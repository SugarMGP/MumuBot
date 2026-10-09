package learning

import (
	"context"
	"fmt"
	"strings"

	"mumu-bot/internal/memory"
)

// filterUsableRows 筛选未撤回且原文非空的消息，同时返回其内部 ID 列表
func filterUsableRows(rows []memory.MessageLog) ([]memory.MessageLog, []uint) {
	valid := make([]memory.MessageLog, 0, len(rows))
	ids := make([]uint, 0, len(rows))
	for _, row := range rows {
		if row.RecalledAt == nil && strings.TrimSpace(row.TextContent) != "" {
			valid = append(valid, row)
			ids = append(ids, row.ID)
		}
	}
	return valid, ids
}

type knowledgeInvestigation struct {
	manager *memory.Manager
	batch   memory.KnowledgeBatch
	seen    map[uint]bool
	partial map[uint]int
}
type knowledgeSearchInput struct {
	Query         string `json:"query"`
	SubjectUserID *int64 `json:"subject_user_id,omitempty"`
	Offset        int    `json:"offset,omitempty"`
	ItemID        uint   `json:"item_id,omitempty" jsonschema:"description=读取已有知识的完整正文、证据和关系；offset 分页"`
	RelationID    uint   `json:"relation_id,omitempty" jsonschema:"description=读取关系自己的独立证据；不能同时指定 item_id"`
}

func (r *knowledgeInvestigation) search(ctx context.Context, input *knowledgeSearchInput) (any, error) {
	if input == nil || (input.ItemID != 0 && input.RelationID != 0) {
		return nil, fmt.Errorf("请提供查询参数，item_id 与 relation_id 不能同时使用")
	}
	if input.Offset < 0 {
		return nil, fmt.Errorf("offset 不得为负数")
	}
	if input.RelationID != 0 {
		var relation memory.KnowledgeRelation
		q := r.manager.GetDB().WithContext(ctx).Table("knowledge_relations kr").Select("kr.*").Joins("JOIN knowledge_items s ON s.id=kr.source_item_id JOIN knowledge_items t ON t.id=kr.target_item_id").Where("kr.id=? AND s.conversation_kind=? AND s.target_id=? AND t.conversation_kind=? AND t.target_id=? AND s.reviewed_through_id<=? AND t.reviewed_through_id<=?", input.RelationID, r.batch.ConversationKind, r.batch.TargetID, r.batch.ConversationKind, r.batch.TargetID, r.batch.ThroughID, r.batch.ThroughID)
		if err := q.Take(&relation).Error; err != nil {
			return nil, err
		}
		sets, err := r.manager.ListKnowledgeEvidenceScope(ctx, r.batch.ConversationKind, r.batch.TargetID, 0, input.RelationID)
		if err != nil {
			return nil, err
		}
		groups := r.visibleEvidence(sets)
		start := min(len(groups), input.Offset)
		end := min(len(groups), input.Offset+10)
		return map[string]any{"relation": relation, "evidence_sets": groups[start:end], "has_more": end < len(groups), "next_offset": end}, nil
	}
	if input.ItemID != 0 {
		items, err := r.manager.SearchKnowledge(ctx, memory.KnowledgeSearchOptions{ConversationKind: r.batch.ConversationKind, TargetID: r.batch.TargetID, ItemID: input.ItemID, ForMaintenance: true, ThroughID: r.batch.ThroughID, Limit: 1})
		if err != nil {
			return nil, err
		}
		if len(items) != 1 {
			return nil, fmt.Errorf("知识不存在或不在本轮范围内")
		}
		sets, err := r.manager.ListKnowledgeEvidenceScope(ctx, r.batch.ConversationKind, r.batch.TargetID, input.ItemID, 0)
		if err != nil {
			return nil, err
		}
		groups := r.visibleEvidence(sets)
		start := min(len(groups), input.Offset)
		end := min(len(groups), input.Offset+10)
		var relations []memory.KnowledgeRelation
		if err := r.manager.GetDB().WithContext(ctx).Table("knowledge_relations kr").Select("kr.*").Joins("JOIN knowledge_items s ON s.id=kr.source_item_id JOIN knowledge_items t ON t.id=kr.target_item_id").Where("s.conversation_kind=? AND s.target_id=? AND t.conversation_kind=? AND t.target_id=? AND s.reviewed_through_id<=? AND t.reviewed_through_id<=? AND (kr.source_item_id=? OR kr.target_item_id=?)", r.batch.ConversationKind, r.batch.TargetID, r.batch.ConversationKind, r.batch.TargetID, r.batch.ThroughID, r.batch.ThroughID, input.ItemID, input.ItemID).Order("kr.id").Offset(input.Offset).Limit(11).Scan(&relations).Error; err != nil {
			return nil, err
		}
		more := end < len(groups) || len(relations) > 10
		if len(relations) > 10 {
			relations = relations[:10]
		}
		r.batch.ExpectedItems[input.ItemID] = items[0].UpdatedAt
		return map[string]any{"item": items[0], "evidence_sets": groups[start:end], "has_valid_evidence": len(groups) > 0, "relations": relations, "has_more": more, "next_offset": input.Offset + 10, "instruction": "使用原文读取工具完整读取后才能提交依据；用 item_id 读取关系另一端，relation_id 读取关系独立依据"}, nil
	}
	if input.SubjectUserID != nil && *input.SubjectUserID == memory.SubjectSelfInputID {
		self := r.batch.SelfID
		input.SubjectUserID = &self
	}
	items, err := r.manager.SearchKnowledge(ctx, memory.KnowledgeSearchOptions{ConversationKind: r.batch.ConversationKind, TargetID: r.batch.TargetID, Query: input.Query, SubjectUserID: input.SubjectUserID, ForMaintenance: true, ThroughID: r.batch.ThroughID, Limit: 11, Offset: input.Offset})
	if err != nil {
		return nil, err
	}
	more := len(items) > 10
	if more {
		items = items[:10]
	}
	for _, item := range items {
		r.batch.ExpectedItems[item.ID] = item.UpdatedAt
	}
	return map[string]any{"items": items, "has_more": more, "next_offset": input.Offset + len(items)}, nil
}

func (r *knowledgeInvestigation) visibleEvidence(sets []memory.KnowledgeEvidence) [][]uint {
	var groups [][]uint
	for _, set := range sets {
		var ids []uint
		visible := set.Valid
		for _, row := range set.Messages {
			if row.ID > r.batch.ThroughID {
				visible = false
			}
			if row.RecalledAt == nil {
				ids = append(ids, row.ID)
			}
		}
		if visible && len(ids) > 0 {
			groups = append(groups, ids)
		}
	}
	return groups
}

func (r *knowledgeInvestigation) renderMessages(page memory.KnowledgeMessagePage, offset int) (any, error) {
	records := make([]map[string]any, 0, len(page.Messages))
	remaining := 6500
	next := uint(0)
	for _, row := range page.Messages {
		text := []rune(row.TextContent)
		if offset > len(text) {
			return nil, fmt.Errorf("读取位置超出原文长度")
		}
		if remaining < 500 {
			page.HasMore = true
			break
		}
		end := min(len(text), offset+remaining-300)
		complete := end == len(text)
		records = append(records, map[string]any{"id": row.ID, "user_id": row.UserID, "nickname": row.Nickname, "time": row.MessageTime, "onebot_message_id": row.OneBotMessageID, "reply_to_message_id": row.ReplyToMessageID, "text": string(text[offset:end]), "offset": offset, "next_offset": end, "complete": complete})
		remaining -= end - offset + 300
		next = row.ID
		if offset <= r.partial[row.ID] {
			r.partial[row.ID] = max(r.partial[row.ID], end)
			if complete {
				r.seen[row.ID] = true
			}
		}
	}
	return map[string]any{"messages": records, "has_more": page.HasMore, "next_id": next}, nil
}
