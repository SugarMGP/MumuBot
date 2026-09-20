package memory

import "gorm.io/gorm"

func checkKnowledgeActivation(tx *gorm.DB, id uint) error {
	var count int64
	if err := tx.Table("knowledge_relations kr").Joins("JOIN knowledge_items source ON source.id=kr.source_item_id").Where("kr.target_item_id=? AND kr.kind='supersedes' AND kr.status='active' AND source.status='active'", id).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return invalidKnowledge("该知识已有启用的替代解释，请先处理替代关系")
	}
	return nil
}

func checkRelationActivation(tx *gorm.DB, relation KnowledgeRelation) error {
	if relation.Kind != "supersedes" {
		return nil
	}
	var cyclic bool
	if err := tx.Raw(`WITH RECURSIVE reach(id) AS (
	 SELECT ?::bigint UNION SELECT kr.target_item_id FROM knowledge_relations kr JOIN reach r ON kr.source_item_id=r.id
	 WHERE kr.kind='supersedes' AND kr.status='active' AND kr.id<>?
	) SELECT EXISTS(SELECT 1 FROM reach WHERE id=?)`, relation.TargetItemID, relation.ID, relation.SourceItemID).Scan(&cyclic).Error; err != nil {
		return err
	}
	if cyclic {
		return invalidKnowledge("替代关系会形成循环，请核对新旧解释")
	}
	return nil
}
