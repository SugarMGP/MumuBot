package migration

import (
	"fmt"

	"gorm.io/gorm"
)

// 旧阶段校验只引用 v8 已存在的列，不能依赖当前会话模型
const legacyKnowledgeEvidenceValiditySQL = `(SELECT count(*) FROM knowledge_evidence_messages em WHERE em.evidence_set_id=es.id) BETWEEN 1 AND 16
 AND NOT EXISTS(SELECT 1 FROM knowledge_evidence_messages em JOIN message_logs ml ON ml.id=em.message_log_id WHERE em.evidence_set_id=es.id AND (ml.recalled_at IS NOT NULL OR btrim(ml.text_content)=''))`
const legacyKnowledgeItemEvidenceSQL = `EXISTS(SELECT 1 FROM knowledge_evidence_sets es WHERE es.item_id=ki.id AND ` + legacyKnowledgeEvidenceValiditySQL + `)`

func migrateV8(db *gorm.DB) error {
	// 旧候选保留正文与依据，存量语义核对后再明确启用，不能批量当作可信事实
	if err := db.Exec(`
		UPDATE knowledge_items SET status='archived',updated_at=now() WHERE status='candidate';
		UPDATE knowledge_relations SET status='archived' WHERE status='candidate';
		ALTER TABLE knowledge_items DROP CONSTRAINT knowledge_items_status_check;
		ALTER TABLE knowledge_items ADD CONSTRAINT knowledge_items_status_check CHECK(status IN ('active','archived'));
		ALTER TABLE knowledge_items ALTER COLUMN status SET DEFAULT 'active';
		ALTER TABLE knowledge_relations DROP CONSTRAINT knowledge_relations_status_check;
		ALTER TABLE knowledge_relations ADD CONSTRAINT knowledge_relations_status_check CHECK(status IN ('active','archived'));
		ALTER TABLE knowledge_relations ALTER COLUMN status SET DEFAULT 'active';
		DROP INDEX idx_knowledge_candidates;
		UPDATE knowledge_items ki SET status='archived',updated_at=now()
		WHERE status='active' AND NOT (` + legacyKnowledgeItemEvidenceSQL + `);
	`).Error; err != nil {
		return err
	}
	return db.Exec(`WITH changed AS (
 UPDATE knowledge_relations kr SET status='archived' FROM knowledge_items s,knowledge_items t
 WHERE kr.source_item_id=s.id AND kr.target_item_id=t.id AND kr.status='active'
 AND (s.status<>'active' OR (t.status<>'active' AND kr.kind<>'supersedes')
 OR NOT EXISTS(SELECT 1 FROM knowledge_evidence_sets es WHERE es.relation_id=kr.id AND ` + legacyKnowledgeEvidenceValiditySQL + `)
 OR EXISTS(SELECT 1 FROM knowledge_items ki WHERE ki.id IN(s.id,t.id) AND NOT (` + legacyKnowledgeItemEvidenceSQL + `)))
 RETURNING kr.source_item_id,kr.target_item_id
 ) UPDATE knowledge_items SET updated_at=now() WHERE id IN(SELECT source_item_id FROM changed UNION SELECT target_item_id FROM changed)`).Error
}

func validateV8Schema(db *gorm.DB) error {
	var count int64
	if err := db.Raw(`SELECT count(*) FROM pg_constraint
	 WHERE conrelid IN ('knowledge_items'::regclass,'knowledge_relations'::regclass)
	 AND conname IN ('knowledge_items_status_check','knowledge_relations_status_check') AND convalidated
	 AND pg_get_constraintdef(oid) = 'CHECK ((status = ANY (ARRAY[''active''::text, ''archived''::text])))'`).Scan(&count).Error; err != nil {
		return err
	}
	if count != 2 {
		return fmt.Errorf("v8 知识状态约束不完整")
	}
	return nil
}
