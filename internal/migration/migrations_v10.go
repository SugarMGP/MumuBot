package migration

import (
	"fmt"

	"mumu-bot/internal/memory"

	"gorm.io/gorm"
)

// migrateV10 合并未发布的 v10-v12：会话作用域、成员好感度日志和记忆模型一次完成
func migrateV10(db *gorm.DB) error {
	if err := migrateConversationScopes(db); err != nil {
		return err
	}
	if err := migrateMemberIntimacyLogs(db); err != nil {
		return err
	}
	return migrateKnowledgeModel(db)
}

func validateV10Schema(db *gorm.DB) error {
	if err := validateMemberIntimacyLogs(db); err != nil {
		return err
	}
	if err := validateKnowledgeModel(db); err != nil {
		return err
	}
	return validateConversationScopeSchema(db)
}

// validateConversationScopeSchema 校验会话作用域结构的列、主键和消息唯一索引，防止结构漂移后静默重复入库
func validateConversationScopeSchema(db *gorm.DB) error {
	var columns int64
	if err := db.Raw(`SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND (
	 (table_name='message_logs' AND column_name IN ('conversation_kind','target_id'))
	 OR (table_name='knowledge_items' AND column_name IN ('conversation_kind','target_id'))
	 OR (table_name='learning_states' AND column_name IN ('conversation_kind','target_id'))
	 OR (table_name='group_agent_states' AND column_name IN ('conversation_kind','target_id')))`).Scan(&columns).Error; err != nil {
		return err
	}
	if columns != 8 {
		return fmt.Errorf("会话作用域字段不完整")
	}
	var tables int64
	if err := db.Raw(`SELECT count(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name IN ('conversation_targets','friend_requests')`).Scan(&tables).Error; err != nil {
		return err
	}
	if tables != 2 {
		return fmt.Errorf("会话管理表不完整")
	}
	var messageIndex bool
	if err := db.Raw(`SELECT EXISTS(SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid
	 WHERE i.indrelid='message_logs'::regclass AND c.relname='uq_message_conversation_onebot'
	 AND i.indisvalid AND i.indisunique AND i.indpred IS NULL AND i.indnkeyatts=3
	 AND pg_get_indexdef(i.indexrelid,1,true)='conversation_kind'
	 AND pg_get_indexdef(i.indexrelid,2,true)='target_id'
	 AND pg_get_indexdef(i.indexrelid,3,true)='one_bot_message_id')`).Scan(&messageIndex).Error; err != nil {
		return err
	}
	if !messageIndex {
		return fmt.Errorf("消息会话唯一索引不完整")
	}
	var primaryKeys int64
	if err := db.Raw(`SELECT count(*) FROM (
	 SELECT 1 FROM pg_index i WHERE i.indrelid='learning_states'::regclass AND i.indisprimary AND i.indnkeyatts=2
	  AND pg_get_indexdef(i.indexrelid,1,true)='conversation_kind' AND pg_get_indexdef(i.indexrelid,2,true)='target_id'
	 UNION ALL
	 SELECT 1 FROM pg_index i WHERE i.indrelid='group_agent_states'::regclass AND i.indisprimary AND i.indnkeyatts=2
	  AND pg_get_indexdef(i.indexrelid,1,true)='conversation_kind' AND pg_get_indexdef(i.indexrelid,2,true)='target_id'
	 UNION ALL
	 SELECT 1 FROM pg_index i WHERE i.indrelid='conversation_targets'::regclass AND i.indisprimary AND i.indnkeyatts=2
	  AND pg_get_indexdef(i.indexrelid,1,true)='conversation_kind' AND pg_get_indexdef(i.indexrelid,2,true)='target_id'
	) keys`).Scan(&primaryKeys).Error; err != nil {
		return err
	}
	if primaryKeys != 3 {
		return fmt.Errorf("会话作用域主键不完整")
	}
	return nil
}

func migrateConversationScopes(db *gorm.DB) error {
	statements := []string{
		// 在已发布 v4-v9 结构上执行，旧索引先显式清理
		`ALTER TABLE message_logs RENAME COLUMN group_id TO target_id`,
		`ALTER TABLE message_logs ADD COLUMN conversation_kind TEXT NOT NULL DEFAULT 'group' CHECK(conversation_kind IN ('group','private'))`,
		`DROP INDEX IF EXISTS uq_message_group_onebot`,
		`DROP INDEX IF EXISTS idx_message_logs_group_id`,
		`CREATE UNIQUE INDEX uq_message_conversation_onebot ON message_logs(conversation_kind,target_id,one_bot_message_id)`,
		`CREATE INDEX idx_message_logs_scope ON message_logs(conversation_kind,target_id)`,

		`ALTER TABLE knowledge_items RENAME COLUMN group_id TO target_id`,
		`ALTER TABLE knowledge_items ADD COLUMN conversation_kind TEXT NOT NULL DEFAULT 'group' CHECK(conversation_kind IN ('group','private'))`,
		`DROP INDEX IF EXISTS idx_knowledge_group_subject`,
		`DROP INDEX IF EXISTS idx_knowledge_label`,
		`DROP INDEX IF EXISTS idx_knowledge_content_trgm`,
		`CREATE INDEX idx_knowledge_scope_subject ON knowledge_items(conversation_kind,target_id,subject_user_id,status)`,
		`CREATE INDEX idx_knowledge_scope_label ON knowledge_items(conversation_kind,target_id,lower(btrim(label)))`,
		// 启用知识的去重与引用合并由紧随其后的记忆模型收敛步骤按 label 和 content 一起处理
		`CREATE INDEX idx_knowledge_content_trgm ON knowledge_items USING gin(content public.gin_trgm_ops)`,

		`ALTER TABLE learning_states RENAME COLUMN group_id TO target_id`,
		`ALTER TABLE learning_states ADD COLUMN conversation_kind TEXT NOT NULL DEFAULT 'group' CHECK(conversation_kind IN ('group','private'))`,
		`ALTER TABLE learning_states DROP CONSTRAINT IF EXISTS learning_states_pkey`,
		`ALTER TABLE learning_states ADD PRIMARY KEY(conversation_kind,target_id)`,

		`ALTER TABLE group_agent_states RENAME COLUMN group_id TO target_id`,
		`ALTER TABLE group_agent_states ADD COLUMN conversation_kind TEXT NOT NULL DEFAULT 'group' CHECK(conversation_kind IN ('group','private'))`,
		`ALTER TABLE group_agent_states DROP CONSTRAINT IF EXISTS group_agent_states_pkey`,
		`ALTER TABLE group_agent_states ADD PRIMARY KEY(conversation_kind,target_id)`,

		`CREATE TABLE conversation_targets (conversation_kind TEXT NOT NULL CHECK(conversation_kind IN ('group','private')), target_id BIGINT NOT NULL CHECK(target_id>0), name TEXT NOT NULL DEFAULT '', remote_remark TEXT NOT NULL DEFAULT '', blocked BOOLEAN NOT NULL DEFAULT false, extra_prompt TEXT NOT NULL DEFAULT '', active BOOLEAN NOT NULL DEFAULT true, last_seen_at TIMESTAMPTZ, updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(conversation_kind,target_id))`,
		`CREATE INDEX idx_conversation_targets_active ON conversation_targets(conversation_kind,active,blocked)`,
		`CREATE TABLE friend_requests (id BIGSERIAL PRIMARY KEY, flag TEXT NOT NULL, user_id BIGINT NOT NULL, nickname TEXT NOT NULL DEFAULT '', comment TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'pending', received_at TIMESTAMPTZ NOT NULL)`,
		`CREATE INDEX idx_friend_requests_status ON friend_requests(status,received_at DESC)`,
		`CREATE TABLE private_topic_states (target_id BIGINT PRIMARY KEY CHECK(target_id>0), summary_json JSONB NOT NULL DEFAULT '{}'::jsonb, through_message_log_id BIGINT NOT NULL DEFAULT 0, updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("v10 迁移失败: %w", err)
		}
	}
	var invalid int64
	if err := db.Raw(`SELECT count(*) FROM message_logs WHERE target_id<=0 OR conversation_kind NOT IN ('group','private')`).Scan(&invalid).Error; err != nil {
		return err
	}
	if invalid > 0 {
		return fmt.Errorf("v10 message_logs 作用域数据无效: %d", invalid)
	}
	if err := db.Raw(`SELECT count(*) FROM knowledge_items WHERE target_id<=0 OR conversation_kind NOT IN ('group','private')`).Scan(&invalid).Error; err != nil {
		return err
	}
	if invalid > 0 {
		return fmt.Errorf("v10 knowledge_items 作用域数据无效: %d", invalid)
	}
	if err := db.Raw(`SELECT count(*) FROM learning_states WHERE target_id<=0 OR conversation_kind NOT IN ('group','private')`).Scan(&invalid).Error; err != nil {
		return err
	}
	if invalid > 0 {
		return fmt.Errorf("v10 learning_states 作用域数据无效: %d", invalid)
	}
	if err := db.Raw(`SELECT count(*) FROM group_agent_states WHERE target_id<=0 OR conversation_kind NOT IN ('group','private')`).Scan(&invalid).Error; err != nil {
		return err
	}
	if invalid > 0 {
		return fmt.Errorf("v10 group_agent_states 作用域数据无效: %d", invalid)
	}
	return nil
}

func migrateMemberIntimacyLogs(db *gorm.DB) error {
	if err := db.Exec(`
		CREATE TABLE member_intimacy_logs (
			id BIGSERIAL PRIMARY KEY,
			user_id BIGINT NOT NULL,
			delta DOUBLE PRECISION NOT NULL,
			before_value DOUBLE PRECISION NOT NULL,
			after_value DOUBLE PRECISION NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE INDEX idx_member_intimacy_logs_user ON member_intimacy_logs(user_id, created_at DESC);
	`).Error; err != nil {
		return fmt.Errorf("创建成员好感度日志失败: %w", err)
	}
	return nil
}

func validateMemberIntimacyLogs(db *gorm.DB) error {
	var exists bool
	if err := db.Raw(`SELECT EXISTS (
		SELECT 1 FROM information_schema.tables
		WHERE table_schema=current_schema() AND table_name='member_intimacy_logs'
	)`).Scan(&exists).Error; err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("member_intimacy_logs 不存在")
	}
	return nil
}

func migrateKnowledgeModel(db *gorm.DB) error {
	statements := []string{
		// part_of 关系只指向 episode，先随 episode 一起清理
		`DELETE FROM knowledge_relations WHERE kind='part_of' OR source_item_id IN (SELECT id FROM knowledge_items WHERE kind='episode') OR target_item_id IN (SELECT id FROM knowledge_items WHERE kind='episode')`,
		// v4 的证据组与组内消息外键均为 ON DELETE CASCADE
		`DELETE FROM knowledge_items WHERE kind='episode'`,
		`ALTER TABLE knowledge_items DROP CONSTRAINT IF EXISTS knowledge_items_kind_check`,
		`ALTER TABLE knowledge_items ADD CONSTRAINT knowledge_items_kind_check CHECK (kind IN ('fact','preference','constraint','goal','term','expression','alias'))`,
		`ALTER TABLE knowledge_items DROP CONSTRAINT IF EXISTS knowledge_items_status_check`,
		`ALTER TABLE knowledge_items ADD CONSTRAINT knowledge_items_status_check CHECK (status IN ('active','archived'))`,
		`ALTER TABLE knowledge_relations DROP CONSTRAINT IF EXISTS knowledge_relations_kind_check`,
		`ALTER TABLE knowledge_relations ADD CONSTRAINT knowledge_relations_kind_check CHECK (kind IN ('variant_of','supersedes','contradicts'))`,
		`ALTER TABLE knowledge_relations DROP CONSTRAINT IF EXISTS knowledge_relations_status_check`,
		`ALTER TABLE knowledge_relations ADD CONSTRAINT knowledge_relations_status_check CHECK (status IN ('active','archived'))`,
		`UPDATE topic_summaries SET summary_json = summary_json - 'related_topics' WHERE jsonb_exists(summary_json, 'related_topics')`,
		`CREATE TABLE private_topic_sources (
		 target_id BIGINT NOT NULL CONSTRAINT fk_private_topic_source_state REFERENCES private_topic_states(target_id) ON DELETE CASCADE,
		 message_log_id BIGINT NOT NULL CONSTRAINT fk_private_topic_source_message REFERENCES message_logs(id) ON DELETE RESTRICT,
		 PRIMARY KEY(target_id,message_log_id))`,
		`CREATE INDEX idx_private_topic_source_message ON private_topic_sources(message_log_id)`,
		// 无来源的旧私聊摘要保持不可用，从原文重新整理，不伪造历史依据
		`UPDATE learning_states ls SET last_message_log_id=0 WHERE ls.conversation_kind='private' AND EXISTS(SELECT 1 FROM private_topic_states ps WHERE ps.target_id=ls.target_id)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("记忆模型收敛失败: %w", err)
		}
	}
	return mergeKnowledgeIdentity(db)
}

func validateKnowledgeModel(db *gorm.DB) error {
	var episodes int64
	if err := db.Raw(`SELECT count(*) FROM knowledge_items WHERE kind='episode'`).Scan(&episodes).Error; err != nil {
		return err
	}
	if episodes > 0 {
		return fmt.Errorf("记忆模型收敛后仍存在 %d 条经历知识", episodes)
	}
	var parts int64
	if err := db.Raw(`SELECT count(*) FROM knowledge_relations WHERE kind='part_of'`).Scan(&parts).Error; err != nil {
		return err
	}
	if parts > 0 {
		return fmt.Errorf("记忆模型收敛后仍存在 %d 条 part_of 关系", parts)
	}
	if err := db.Exec("SELECT target_id,message_log_id FROM private_topic_sources LIMIT 0").Error; err != nil {
		return fmt.Errorf("私聊摘要来源不完整: %w", err)
	}
	var constraints int64
	if err := db.Raw(`SELECT count(*) FROM pg_constraint WHERE convalidated AND (
	 (conrelid='knowledge_items'::regclass AND conname='knowledge_items_kind_check'
	  AND pg_get_constraintdef(oid)='CHECK ((kind = ANY (ARRAY[''fact''::text, ''preference''::text, ''constraint''::text, ''goal''::text, ''term''::text, ''expression''::text, ''alias''::text])))')
	 OR (conrelid='knowledge_relations'::regclass AND conname='knowledge_relations_kind_check'
	  AND pg_get_constraintdef(oid)='CHECK ((kind = ANY (ARRAY[''variant_of''::text, ''supersedes''::text, ''contradicts''::text])))')
	 OR (conrelid='private_topic_sources'::regclass AND conname='fk_private_topic_source_state' AND confrelid='private_topic_states'::regclass AND confdeltype='c')
	 OR (conrelid='private_topic_sources'::regclass AND conname='fk_private_topic_source_message' AND confrelid='message_logs'::regclass AND confdeltype='r'))`).Scan(&constraints).Error; err != nil {
		return err
	}
	if constraints != 4 {
		return fmt.Errorf("知识类型或摘要来源外键约束不完整")
	}
	var identity bool
	if err := db.Raw(`SELECT EXISTS(SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid
	 WHERE i.indrelid='knowledge_items'::regclass AND c.relname='uq_active_knowledge_scope_content'
	 AND i.indisvalid AND i.indisunique AND i.indnkeyatts=6
	 AND pg_get_indexdef(i.indexrelid,1,true)='conversation_kind' AND pg_get_indexdef(i.indexrelid,2,true)='target_id'
	 AND pg_get_indexdef(i.indexrelid,3,true)='subject_user_id' AND pg_get_indexdef(i.indexrelid,4,true)='kind'
	 AND pg_get_expr(i.indexprs,i.indrelid)='lower(btrim(label)), lower(btrim(content))'
	 AND pg_get_expr(i.indpred,i.indrelid)='(status = ''active''::text)')`).Scan(&identity).Error; err != nil {
		return err
	}
	if !identity {
		return fmt.Errorf("启用知识正文与术语唯一索引不完整")
	}
	return nil
}

func mergeKnowledgeIdentity(db *gorm.DB) error {
	statements := []string{
		`DROP INDEX IF EXISTS uq_active_knowledge_scope_content`,
		`CREATE TEMP TABLE knowledge_identity_merge ON COMMIT DROP AS
		 SELECT id,first_value(id) OVER (PARTITION BY conversation_kind,target_id,subject_user_id,kind,lower(btrim(label)),lower(btrim(content)) ORDER BY updated_at DESC,id DESC) keep_id
		 FROM knowledge_items WHERE status='active'`,
		`UPDATE knowledge_evidence_sets es SET item_id=d.keep_id FROM knowledge_identity_merge d WHERE es.item_id=d.id AND d.id<>d.keep_id`,
		`ALTER TABLE knowledge_relations DROP CONSTRAINT knowledge_relations_source_item_id_target_item_id_kind_key`,
		`DELETE FROM knowledge_relations kr WHERE
		 COALESCE((SELECT keep_id FROM knowledge_identity_merge WHERE id=kr.source_item_id),kr.source_item_id)
		 =COALESCE((SELECT keep_id FROM knowledge_identity_merge WHERE id=kr.target_item_id),kr.target_item_id)`,
		`WITH mapped AS (SELECT kr.id,kr.kind,
		 COALESCE(s.keep_id,kr.source_item_id) source_id,COALESCE(t.keep_id,kr.target_item_id) target_id
		 FROM knowledge_relations kr LEFT JOIN knowledge_identity_merge s ON s.id=kr.source_item_id LEFT JOIN knowledge_identity_merge t ON t.id=kr.target_item_id)
		 UPDATE knowledge_relations kr SET
		 source_item_id=CASE WHEN m.kind='contradicts' THEN LEAST(m.source_id,m.target_id) ELSE m.source_id END,
		 target_item_id=CASE WHEN m.kind='contradicts' THEN GREATEST(m.source_id,m.target_id) ELSE m.target_id END
		 FROM mapped m WHERE kr.id=m.id`,
		`CREATE TEMP TABLE knowledge_relation_merge ON COMMIT DROP AS
		 SELECT id,first_value(id) OVER (PARTITION BY source_item_id,target_item_id,kind ORDER BY (status='active') DESC,id) keep_id FROM knowledge_relations`,
		`UPDATE knowledge_evidence_sets es SET relation_id=d.keep_id FROM knowledge_relation_merge d WHERE es.relation_id=d.id AND d.id<>d.keep_id`,
		`DELETE FROM knowledge_relations kr USING knowledge_relation_merge d WHERE kr.id=d.id AND d.id<>d.keep_id`,
		`ALTER TABLE knowledge_relations ADD CONSTRAINT knowledge_relations_source_item_id_target_item_id_kind_key UNIQUE(source_item_id,target_item_id,kind)`,
		`UPDATE knowledge_items ki SET status='archived',updated_at=now() FROM knowledge_identity_merge d WHERE ki.id=d.id AND d.id<>d.keep_id`,
		`UPDATE knowledge_items ki SET reviewed_through_id=d.upper_id,updated_at=d.latest FROM (
		 SELECT d.keep_id,max(ki.reviewed_through_id) upper_id,max(ki.updated_at) latest FROM knowledge_identity_merge d JOIN knowledge_items ki ON ki.id=d.id GROUP BY d.keep_id
		 ) d WHERE ki.id=d.keep_id`,
		`CREATE UNIQUE INDEX uq_active_knowledge_scope_content ON knowledge_items(conversation_kind,target_id,subject_user_id,kind,lower(btrim(label)),lower(btrim(content))) WHERE status='active'`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("知识身份收敛失败: %w", err)
		}
	}
	var scopes []struct {
		ConversationKind string
		TargetID         int64
	}
	if err := db.Model(&memory.KnowledgeItem{}).Distinct("conversation_kind,target_id").Find(&scopes).Error; err != nil {
		return err
	}
	for _, scope := range scopes {
		if err := memory.ArchiveInvalidKnowledgeRelations(db, scope.ConversationKind, scope.TargetID); err != nil {
			return err
		}
	}
	return nil
}
