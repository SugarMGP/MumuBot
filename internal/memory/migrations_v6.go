package memory

import "gorm.io/gorm"

func migrateV6(db *gorm.DB) error {
	if err := db.Exec("ALTER TABLE topic_summaries ALTER COLUMN embedding DROP NOT NULL").Error; err != nil {
		return err
	}
	// Revisit old pending assignments/summaries without deleting their existing topic identity.
	return db.Exec(`INSERT INTO learning_states(group_id,last_message_log_id)
		SELECT group_id,0 FROM message_logs GROUP BY group_id ON CONFLICT DO NOTHING;
		UPDATE learning_states ls SET last_message_log_id=LEAST(ls.last_message_log_id,COALESCE((
		 SELECT min(ml.id)-1 FROM message_logs ml LEFT JOIN topic_assignments ta ON ta.message_log_id=ml.id
		 WHERE ml.group_id=ls.group_id AND ml.recalled_at IS NULL AND btrim(ml.text_content)<>''
		 AND (ta.id IS NULL OR (ta.topic_id IS NOT NULL AND ta.id>COALESCE((SELECT max(ts.through_topic_assignment_id) FROM topic_summaries ts JOIN topic_assignments old ON old.id=ts.through_topic_assignment_id WHERE old.topic_id=ta.topic_id),0)))
		),ls.last_message_log_id))`).Error
}
