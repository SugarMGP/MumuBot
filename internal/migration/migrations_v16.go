package migration

import (
	"fmt"

	"gorm.io/gorm"
)

const v16BaseName = "remove_working_notes"

// migrateV16 删除已经被连续 transcript 和话题概览替代的工作便签
func migrateV16(db *gorm.DB) error {
	for _, statement := range []string{
		`DELETE FROM conversation_transcript WHERE entry_kind='internal_reference'`,
		`UPDATE context_compaction_checkpoints SET reference_baseline='', reference_versions='[]'::jsonb, updated_at=now()`,
		`DROP TABLE IF EXISTS group_agent_states`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("删除工作便签及其旧参考失败: %w", err)
		}
	}
	return nil
}

func validateV16Schema(db *gorm.DB) error {
	if db.Migrator().HasTable("group_agent_states") {
		return fmt.Errorf("v16 仍存在工作便签表 group_agent_states")
	}
	for _, table := range []string{"message_logs", "message_forwards", "conversation_sessions", "conversation_transcript", "context_compaction_checkpoints", "topic_threads", "topic_assignments", "topic_summaries", "topic_summary_sources", "knowledge_items", "knowledge_revisions", "emotion_states", "emotion_revisions", "member_profiles", "member_names", "member_intimacy_logs", "stickers", "mood_state", "model_call_hourly", "conversation_targets"} {
		if !db.Migrator().HasTable(table) {
			return fmt.Errorf("v16 缺少表 %s", table)
		}
	}
	for _, column := range []struct{ table, name string }{
		{"message_logs", "message_ref"},
		{"conversation_transcript", "visible_seq"},
		{"context_compaction_checkpoints", "through_visible_seq"},
		{"knowledge_items", "content_hash"},
		{"topic_summaries", "topic_id"},
	} {
		if !db.Migrator().HasColumn(column.table, column.name) {
			return fmt.Errorf("v16 缺少字段 %s.%s", column.table, column.name)
		}
	}
	return nil
}
