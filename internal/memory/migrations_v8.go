package memory

import (
	"fmt"

	"gorm.io/gorm"
)

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
		WHERE status='active' AND NOT (` + knowledgeItemEvidenceSQL + `);
	`).Error; err != nil {
		return err
	}
	var groups []int64
	if err := db.Model(&KnowledgeItem{}).Distinct("group_id").Pluck("group_id", &groups).Error; err != nil {
		return err
	}
	for _, groupID := range groups {
		if err := archiveInvalidKnowledgeRelations(db, groupID); err != nil {
			return err
		}
	}
	return nil
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
