package memory

import "gorm.io/gorm"

func migrateV5(db *gorm.DB) error {
	// Legacy profile values remain available, but no longer own live message retention.
	if err := db.Exec(`DO $$ DECLARE c record; BEGIN
		FOR c IN SELECT conname,conrelid::regclass AS owner FROM pg_constraint
		WHERE contype='f' AND conrelid=to_regclass('legacy_member_trait_evidence') AND confrelid='message_logs'::regclass
		LOOP EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I',c.owner,c.conname); END LOOP;
	END $$`).Error; err != nil {
		return err
	}
	return db.Exec(`UPDATE knowledge_items ki SET reviewed_through_id=0,updated_at=now()
		WHERE status='candidate' OR EXISTS(SELECT 1 FROM knowledge_relations kr WHERE kr.status='candidate' AND (kr.source_item_id=ki.id OR kr.target_item_id=ki.id))`).Error
}
