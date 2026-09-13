package memory

import "gorm.io/gorm"

func migrateV7(db *gorm.DB) error {
	return db.Exec(`
		DO $$ DECLARE c record; BEGIN
		FOR c IN SELECT conname FROM pg_constraint WHERE conrelid='topic_summaries'::regclass
		AND contype='u' AND conkey=ARRAY[(SELECT attnum FROM pg_attribute WHERE attrelid='topic_summaries'::regclass AND attname='through_topic_assignment_id')]::smallint[]
		LOOP EXECUTE format('ALTER TABLE topic_summaries DROP CONSTRAINT %I',c.conname); END LOOP;
		END $$;
		CREATE INDEX idx_topic_summary_coverage ON topic_summaries(through_topic_assignment_id,id DESC);
		CREATE TABLE topic_summary_sources (
		 summary_id BIGINT NOT NULL REFERENCES topic_summaries(id) ON DELETE CASCADE,
		 message_log_id BIGINT NOT NULL REFERENCES message_logs(id) ON DELETE RESTRICT,
		 PRIMARY KEY(summary_id,message_log_id));
		CREATE INDEX idx_topic_summary_source_message ON topic_summary_sources(message_log_id);
	`).Error
}
