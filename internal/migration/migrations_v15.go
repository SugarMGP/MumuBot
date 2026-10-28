package migration

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const v15BaseName = "single_model_topic_context_memory_emotion"

// migrateV15 在事务内把 v14 收敛到单主模型运行结构
func migrateV15(db *gorm.DB) error {
	for _, table := range []string{"knowledge_evidence_messages", "knowledge_evidence_sets", "knowledge_relations", "learning_states"} {
		if err := renameLegacyTable(db, table); err != nil {
			return err
		}
	}
	if err := dropSingleColumnUniqueConstraints(db, "topic_summaries", "through_topic_assignment_id"); err != nil {
		return err
	}
	statements := []string{
		`ALTER TABLE message_logs ADD COLUMN IF NOT EXISTS message_ref TEXT NOT NULL DEFAULT ''`,
		`WITH numbered AS (
			SELECT id,row_number() OVER(PARTITION BY conversation_kind,target_id ORDER BY id) rn
			FROM message_logs
		) UPDATE message_logs ml SET message_ref='m'||numbered.rn FROM numbered
		WHERE ml.id=numbered.id AND btrim(ml.message_ref)=''`,
		`CREATE UNIQUE INDEX uq_message_conversation_ref ON message_logs(conversation_kind,target_id,message_ref) WHERE message_ref<>''`,

		`ALTER TABLE topic_threads ADD COLUMN IF NOT EXISTS conversation_kind TEXT NOT NULL DEFAULT 'group'`,
		`ALTER TABLE topic_threads ADD COLUMN IF NOT EXISTS target_id BIGINT`,
		`ALTER TABLE topic_threads ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'dormant'`,
		`ALTER TABLE topic_threads ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now()`,
		`UPDATE topic_threads SET target_id=group_id WHERE target_id IS NULL`,
		`ALTER TABLE topic_threads ALTER COLUMN group_id DROP NOT NULL`,
		`ALTER TABLE topic_threads ALTER COLUMN target_id SET NOT NULL`,
		`ALTER TABLE topic_threads DROP CONSTRAINT IF EXISTS topic_threads_status_check`,
		`ALTER TABLE topic_threads ADD CONSTRAINT topic_threads_status_check CHECK(status IN ('active','dormant'))`,
		`DO $$ BEGIN
		 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='topic_threads'::regclass AND conname='topic_threads_conversation_kind_check') THEN
		  ALTER TABLE topic_threads ADD CONSTRAINT topic_threads_conversation_kind_check CHECK(conversation_kind IN ('group','private'));
		 END IF;
		 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='topic_threads'::regclass AND conname='topic_threads_target_id_check') THEN
		  ALTER TABLE topic_threads ADD CONSTRAINT topic_threads_target_id_check CHECK(target_id>0);
		 END IF;
		END $$`,

		`ALTER TABLE topic_summaries ADD COLUMN IF NOT EXISTS topic_id BIGINT`,
		`ALTER TABLE topic_summaries ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 1`,
		`UPDATE topic_summaries ts SET topic_id=ta.topic_id FROM topic_assignments ta WHERE ta.id=ts.through_topic_assignment_id AND ts.topic_id IS NULL`,
		`ALTER TABLE topic_summaries ALTER COLUMN through_topic_assignment_id DROP NOT NULL`,
		`ALTER TABLE topic_summaries DROP COLUMN IF EXISTS embedding`,
		`ALTER TABLE topic_summaries DROP COLUMN IF EXISTS memory_processed`,
		`DO $$ DECLARE r record; tid BIGINT; BEGIN
		 FOR r IN SELECT ts.id,ml.conversation_kind,ml.target_id FROM topic_summaries ts
		 JOIN topic_assignments ta ON ta.id=ts.through_topic_assignment_id
		 JOIN message_logs ml ON ml.id=ta.message_log_id WHERE ts.topic_id IS NULL LOOP
		   INSERT INTO topic_threads(conversation_kind,target_id,status,created_at,updated_at)
		   VALUES(r.conversation_kind,r.target_id,'dormant',now(),now()) RETURNING id INTO tid;
		   UPDATE topic_summaries SET topic_id=tid WHERE id=r.id;
		 END LOOP;
		END $$`,
		`ALTER TABLE topic_summaries ALTER COLUMN topic_id SET NOT NULL`,
		`DO $$ BEGIN
		 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='topic_summaries'::regclass AND conname='topic_summaries_topic_id_fkey') THEN
		  ALTER TABLE topic_summaries ADD CONSTRAINT topic_summaries_topic_id_fkey FOREIGN KEY(topic_id) REFERENCES topic_threads(id) ON DELETE RESTRICT;
		 END IF;
		 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='topic_summaries'::regclass AND conname='topic_summaries_version_check') THEN
		  ALTER TABLE topic_summaries ADD CONSTRAINT topic_summaries_version_check CHECK(version>0);
		 END IF;
		END $$`,
		`WITH ranked AS (
			SELECT id,row_number() OVER(PARTITION BY topic_id ORDER BY created_at,id) rn FROM topic_summaries
		) UPDATE topic_summaries ts SET version=ranked.rn FROM ranked WHERE ts.id=ranked.id`,
		`CREATE UNIQUE INDEX uq_topic_summary_version ON topic_summaries(topic_id,version)`,

		`ALTER TABLE topic_summary_sources ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'source'`,
		`ALTER TABLE topic_summary_sources DROP CONSTRAINT IF EXISTS topic_summary_sources_pkey`,
		`ALTER TABLE topic_summary_sources ADD PRIMARY KEY(summary_id,message_log_id,role)`,
		`ALTER TABLE topic_summary_sources DROP CONSTRAINT IF EXISTS topic_summary_sources_role_check`,
		`ALTER TABLE topic_summary_sources ADD CONSTRAINT topic_summary_sources_role_check CHECK(role IN ('source','anchor'))`,

		`INSERT INTO topic_threads(conversation_kind,target_id,status,created_at,updated_at)
		 SELECT 'private',ps.target_id,'dormant',ps.updated_at,ps.updated_at FROM private_topic_states ps
		 WHERE NOT EXISTS(SELECT 1 FROM topic_threads tt WHERE tt.conversation_kind='private' AND tt.target_id=ps.target_id)`,
		`INSERT INTO topic_summaries(topic_id,through_topic_assignment_id,version,summary_json,created_at)
		 SELECT tt.id,NULL,1,ps.summary_json,ps.updated_at
		 FROM private_topic_states ps JOIN topic_threads tt ON tt.conversation_kind='private' AND tt.target_id=ps.target_id
		 WHERE COALESCE(ps.summary_json,'{}'::jsonb)<>'{}'::jsonb
		 AND NOT EXISTS(SELECT 1 FROM topic_summaries ts WHERE ts.topic_id=tt.id)`,
		`INSERT INTO topic_summary_sources(summary_id,message_log_id,role)
		 SELECT ts.id,psrc.message_log_id,'source'
		 FROM private_topic_states ps
		 JOIN topic_threads tt ON tt.conversation_kind='private' AND tt.target_id=ps.target_id
		 JOIN private_topic_sources psrc ON psrc.target_id=ps.target_id
		 JOIN LATERAL(SELECT id FROM topic_summaries WHERE topic_id=tt.id ORDER BY version DESC,id DESC LIMIT 1) ts ON true
		 ON CONFLICT DO NOTHING`,
		`INSERT INTO topic_summary_sources(summary_id,message_log_id,role)
		 SELECT src.summary_id,min(src.message_log_id),'anchor'
		 FROM topic_summary_sources src
		 WHERE src.role='source'
		   AND NOT EXISTS(SELECT 1 FROM topic_summary_sources anchor WHERE anchor.summary_id=src.summary_id AND anchor.role='anchor')
		 GROUP BY src.summary_id
		 ON CONFLICT DO NOTHING`,

		`ALTER TABLE topic_summaries DROP COLUMN IF EXISTS through_topic_assignment_id`,

		`ALTER TABLE knowledge_items ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 1`,
		`ALTER TABLE knowledge_items ADD COLUMN IF NOT EXISTS is_core BOOLEAN NOT NULL DEFAULT false`,
		`ALTER TABLE knowledge_items ADD COLUMN IF NOT EXISTS source_meta JSONB NOT NULL DEFAULT '{}'::jsonb`,
		`ALTER TABLE knowledge_items ADD COLUMN IF NOT EXISTS content_hash TEXT NOT NULL DEFAULT ''`,
		`DO $$ BEGIN
		 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='knowledge_items'::regclass AND conname='knowledge_items_version_check') THEN
		  ALTER TABLE knowledge_items ADD CONSTRAINT knowledge_items_version_check CHECK(version>0);
		 END IF;
		END $$`,
		`ALTER TABLE knowledge_items DROP COLUMN IF EXISTS embedding`,
		`ALTER TABLE knowledge_items DROP COLUMN IF EXISTS reviewed_through_id`,
		`UPDATE knowledge_items SET source_meta=jsonb_build_object('migrated',true,'conversation_kind',conversation_kind,'target_id',target_id) WHERE source_meta='{}'::jsonb`,
		`ALTER TABLE knowledge_items DROP CONSTRAINT IF EXISTS knowledge_items_check`,
		`ALTER TABLE knowledge_items DROP CONSTRAINT IF EXISTS knowledge_items_kind_check`,
		`ALTER TABLE knowledge_items ADD CONSTRAINT knowledge_items_kind_check CHECK(kind IN ('fact','preference','constraint','goal','term','expression','alias'))`,
		`ALTER TABLE knowledge_items DROP CONSTRAINT IF EXISTS knowledge_items_status_check`,
		`ALTER TABLE knowledge_items ADD CONSTRAINT knowledge_items_status_check CHECK(status IN ('active','archived'))`,
		`DROP INDEX IF EXISTS uq_active_knowledge_scope_content`,

		`ALTER TABLE group_agent_states DROP CONSTRAINT IF EXISTS group_agent_states_note_check`,
		`ALTER TABLE group_agent_states ADD CONSTRAINT group_agent_states_note_check CHECK(char_length(btrim(note))<=300)`,
		`CREATE TABLE IF NOT EXISTS legacy_mood_state AS SELECT * FROM mood_state`,
		`ALTER TABLE mood_state DROP COLUMN IF EXISTS valence`,

		`CREATE TABLE conversation_sessions (
		 conversation_kind TEXT NOT NULL CHECK(conversation_kind IN ('group','private')),
		 target_id BIGINT NOT NULL CHECK(target_id>0),
		 next_input_seq BIGINT NOT NULL DEFAULT 0,
		 next_visible_seq BIGINT NOT NULL DEFAULT 0,
		 next_message_ref BIGINT NOT NULL DEFAULT 0,
		 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		 PRIMARY KEY(conversation_kind,target_id))`,
		`INSERT INTO conversation_sessions(conversation_kind,target_id,next_input_seq,next_visible_seq,next_message_ref)
		 SELECT conversation_kind,target_id,count(*),0,
		 COALESCE(max(NULLIF(regexp_replace(message_ref,'\D','','g'),'')::BIGINT),0)
		 FROM message_logs GROUP BY conversation_kind,target_id`,
		`CREATE TABLE conversation_transcript (
		 id BIGSERIAL PRIMARY KEY,
		 conversation_kind TEXT NOT NULL CHECK(conversation_kind IN ('group','private')),
		 target_id BIGINT NOT NULL CHECK(target_id>0),
		 input_seq BIGINT,
		 visible_seq BIGINT,
		 entry_kind TEXT NOT NULL CHECK(entry_kind IN ('user','assistant','tool','internal_reference','observation')),
		 message_log_id BIGINT REFERENCES message_logs(id) ON DELETE RESTRICT,
		 run_id TEXT NOT NULL DEFAULT '',
		 tool_call_id TEXT NOT NULL DEFAULT '',
		 tool_name TEXT NOT NULL DEFAULT '',
		 tool_status TEXT NOT NULL DEFAULT '' CHECK(tool_status IN ('','planned','running','succeeded','failed','unknown','not_executed')),
		 payload JSONB NOT NULL,
		 result_ref TEXT NOT NULL DEFAULT '',
		 created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE UNIQUE INDEX uq_transcript_input_seq ON conversation_transcript(conversation_kind,target_id,input_seq) WHERE input_seq IS NOT NULL`,
		`CREATE UNIQUE INDEX uq_transcript_visible_seq ON conversation_transcript(conversation_kind,target_id,visible_seq) WHERE visible_seq IS NOT NULL`,
		`CREATE UNIQUE INDEX uq_transcript_tool_call ON conversation_transcript(conversation_kind,target_id,run_id,tool_call_id) WHERE entry_kind='tool' AND tool_call_id<>''`,
		`CREATE INDEX idx_transcript_scope_visible ON conversation_transcript(conversation_kind,target_id,visible_seq,id)`,
		`CREATE INDEX idx_transcript_pending ON conversation_transcript(conversation_kind,target_id,input_seq) WHERE visible_seq IS NULL`,
		`CREATE TABLE context_compaction_checkpoints (
		 conversation_kind TEXT NOT NULL CHECK(conversation_kind IN ('group','private')),
		 target_id BIGINT NOT NULL CHECK(target_id>0),
		 version BIGINT NOT NULL DEFAULT 0,
		 through_visible_seq BIGINT,
		 through_message_log_id BIGINT,
		 overview TEXT NOT NULL DEFAULT '',
		 active_topic_versions JSONB NOT NULL DEFAULT '[]'::jsonb,
		 reference_baseline TEXT NOT NULL DEFAULT '',
		 reference_versions JSONB NOT NULL DEFAULT '[]'::jsonb,
		 batch_id TEXT NOT NULL DEFAULT '',
		 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		 PRIMARY KEY(conversation_kind,target_id))`,
		`CREATE TABLE emotion_states (
		 id BIGSERIAL PRIMARY KEY,
		 about_user_id BIGINT NOT NULL DEFAULT 0,
		 kind TEXT NOT NULL,
		 strength DOUBLE PRECISION NOT NULL DEFAULT 0,
		 occurred_at TIMESTAMPTZ NOT NULL,
		 anchor_at TIMESTAMPTZ NOT NULL,
		 decay_class TEXT NOT NULL DEFAULT 'lingering' CHECK(decay_class IN ('brief','lingering')),
		 reason TEXT NOT NULL DEFAULT '',
		 source_meta JSONB NOT NULL DEFAULT '{}'::jsonb,
		 version BIGINT NOT NULL DEFAULT 1,
		 ended_at TIMESTAMPTZ,
		 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		 UNIQUE(about_user_id,kind))`,
		`CREATE TABLE emotion_revisions (
		 id BIGSERIAL PRIMARY KEY,
		 emotion_id BIGINT NOT NULL REFERENCES emotion_states(id) ON DELETE CASCADE,
		 before_strength DOUBLE PRECISION NOT NULL DEFAULT 0,
		 after_strength DOUBLE PRECISION NOT NULL DEFAULT 0,
		 reason TEXT NOT NULL DEFAULT '',
		 source_meta JSONB NOT NULL DEFAULT '{}'::jsonb,
		 version BIGINT NOT NULL,
		 created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE knowledge_revisions (
		 id BIGSERIAL PRIMARY KEY,
		 item_id BIGINT NOT NULL REFERENCES knowledge_items(id) ON DELETE CASCADE,
		 version BIGINT NOT NULL,
		 before_content TEXT NOT NULL DEFAULT '',
		 after_content TEXT NOT NULL DEFAULT '',
		 before_status TEXT NOT NULL DEFAULT '',
		 after_status TEXT NOT NULL DEFAULT '',
		 before_core BOOLEAN NOT NULL DEFAULT false,
		 after_core BOOLEAN NOT NULL DEFAULT false,
		 reason TEXT NOT NULL DEFAULT '',
		 source_meta JSONB NOT NULL DEFAULT '{}'::jsonb,
		 created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("v15 迁移失败: %w", err)
		}
	}
	for _, table := range []string{"private_topic_sources", "private_topic_states"} {
		if err := renameLegacyTable(db, table); err != nil {
			return err
		}
	}
	if err := backfillKnowledgeHashes(db); err != nil {
		return err
	}
	if err := seedKnowledgeRevisions(db); err != nil {
		return err
	}
	if err := dropLegacyColumns(db); err != nil {
		return err
	}
	if err := createV15Indexes(db); err != nil {
		return err
	}
	if err := dropVectorExtension(db); err != nil {
		return err
	}
	return validateV15Schema(db)
}

func initializeV15Schema(db *gorm.DB) error {
	statements := []string{
		`CREATE TABLE schema_migrations (version BIGINT PRIMARY KEY, name TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE conversation_targets (conversation_kind TEXT NOT NULL CHECK(conversation_kind IN ('group','private')), target_id BIGINT NOT NULL CHECK(target_id>0), name TEXT NOT NULL DEFAULT '', remote_remark TEXT NOT NULL DEFAULT '', blocked BOOLEAN NOT NULL DEFAULT false, extra_prompt TEXT NOT NULL DEFAULT '', active BOOLEAN NOT NULL DEFAULT true, last_seen_at TIMESTAMPTZ, updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(conversation_kind,target_id))`,
		`CREATE TABLE message_logs (id BIGSERIAL PRIMARY KEY, one_bot_message_id BIGINT NOT NULL, conversation_kind TEXT NOT NULL CHECK(conversation_kind IN ('group','private')), target_id BIGINT NOT NULL, user_id BIGINT NOT NULL, nickname TEXT NOT NULL, text_content TEXT NOT NULL, display_content TEXT NOT NULL, reply_to_message_id BIGINT, is_mentioned BOOLEAN NOT NULL DEFAULT false, message_time TIMESTAMPTZ NOT NULL, message_ref TEXT NOT NULL, recalled_at TIMESTAMPTZ, media_refs JSONB)`,
		`CREATE TABLE message_forwards (id BIGSERIAL PRIMARY KEY, handle TEXT NOT NULL UNIQUE, message_log_id BIGINT NOT NULL REFERENCES message_logs(id) ON DELETE CASCADE, parent_handle TEXT REFERENCES message_forwards(handle) ON DELETE CASCADE, nodes JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE topic_threads (id BIGSERIAL PRIMARY KEY, conversation_kind TEXT NOT NULL CHECK(conversation_kind IN ('group','private')), target_id BIGINT NOT NULL CHECK(target_id>0), status TEXT NOT NULL DEFAULT 'dormant' CHECK(status IN ('active','dormant')), created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE topic_assignments (id BIGSERIAL PRIMARY KEY, message_log_id BIGINT NOT NULL UNIQUE REFERENCES message_logs(id) ON DELETE CASCADE, topic_id BIGINT REFERENCES topic_threads(id) ON DELETE RESTRICT)`,
		`CREATE TABLE topic_summaries (id BIGSERIAL PRIMARY KEY, topic_id BIGINT NOT NULL REFERENCES topic_threads(id) ON DELETE RESTRICT, version BIGINT NOT NULL DEFAULT 1, summary_json JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE topic_summary_sources (summary_id BIGINT NOT NULL REFERENCES topic_summaries(id) ON DELETE CASCADE, message_log_id BIGINT NOT NULL REFERENCES message_logs(id) ON DELETE RESTRICT, role TEXT NOT NULL DEFAULT 'source' CHECK(role IN ('source','anchor')), PRIMARY KEY(summary_id,message_log_id,role))`,
		`CREATE TABLE conversation_sessions (conversation_kind TEXT NOT NULL CHECK(conversation_kind IN ('group','private')), target_id BIGINT NOT NULL CHECK(target_id>0), next_input_seq BIGINT NOT NULL DEFAULT 0, next_visible_seq BIGINT NOT NULL DEFAULT 0, next_message_ref BIGINT NOT NULL DEFAULT 0, updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(conversation_kind,target_id))`,
		`CREATE TABLE conversation_transcript (id BIGSERIAL PRIMARY KEY, conversation_kind TEXT NOT NULL CHECK(conversation_kind IN ('group','private')), target_id BIGINT NOT NULL CHECK(target_id>0), input_seq BIGINT, visible_seq BIGINT, entry_kind TEXT NOT NULL CHECK(entry_kind IN ('user','assistant','tool','internal_reference','observation')), message_log_id BIGINT REFERENCES message_logs(id) ON DELETE RESTRICT, run_id TEXT NOT NULL DEFAULT '', tool_call_id TEXT NOT NULL DEFAULT '', tool_name TEXT NOT NULL DEFAULT '', tool_status TEXT NOT NULL DEFAULT '' CHECK(tool_status IN ('','planned','running','succeeded','failed','unknown','not_executed')), payload JSONB NOT NULL, result_ref TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE context_compaction_checkpoints (conversation_kind TEXT NOT NULL CHECK(conversation_kind IN ('group','private')), target_id BIGINT NOT NULL CHECK(target_id>0), version BIGINT NOT NULL DEFAULT 0, through_visible_seq BIGINT, through_message_log_id BIGINT, overview TEXT NOT NULL DEFAULT '', active_topic_versions JSONB NOT NULL DEFAULT '[]'::jsonb, reference_baseline TEXT NOT NULL DEFAULT '', reference_versions JSONB NOT NULL DEFAULT '[]'::jsonb, batch_id TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(conversation_kind,target_id))`,
		`CREATE TABLE knowledge_items (id BIGSERIAL PRIMARY KEY, conversation_kind TEXT NOT NULL CHECK(conversation_kind IN ('group','private')), target_id BIGINT NOT NULL CHECK(target_id>0), subject_user_id BIGINT NOT NULL CHECK(subject_user_id>=0), kind TEXT NOT NULL CHECK(kind IN ('fact','preference','constraint','goal','term','expression','alias')), label TEXT NOT NULL DEFAULT '', content TEXT NOT NULL CHECK(btrim(content)<>''), status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','archived')), version BIGINT NOT NULL DEFAULT 1, is_core BOOLEAN NOT NULL DEFAULT false, source_meta JSONB NOT NULL DEFAULT '{}'::jsonb, content_hash TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE knowledge_revisions (id BIGSERIAL PRIMARY KEY, item_id BIGINT NOT NULL REFERENCES knowledge_items(id) ON DELETE CASCADE, version BIGINT NOT NULL, before_content TEXT NOT NULL DEFAULT '', after_content TEXT NOT NULL DEFAULT '', before_status TEXT NOT NULL DEFAULT '', after_status TEXT NOT NULL DEFAULT '', before_core BOOLEAN NOT NULL DEFAULT false, after_core BOOLEAN NOT NULL DEFAULT false, reason TEXT NOT NULL DEFAULT '', source_meta JSONB NOT NULL DEFAULT '{}'::jsonb, created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE emotion_states (id BIGSERIAL PRIMARY KEY, about_user_id BIGINT NOT NULL DEFAULT 0, kind TEXT NOT NULL, strength DOUBLE PRECISION NOT NULL DEFAULT 0, occurred_at TIMESTAMPTZ NOT NULL, anchor_at TIMESTAMPTZ NOT NULL, decay_class TEXT NOT NULL DEFAULT 'lingering' CHECK(decay_class IN ('brief','lingering')), reason TEXT NOT NULL DEFAULT '', source_meta JSONB NOT NULL DEFAULT '{}'::jsonb, version BIGINT NOT NULL DEFAULT 1, ended_at TIMESTAMPTZ, updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(about_user_id,kind))`,
		`CREATE TABLE emotion_revisions (id BIGSERIAL PRIMARY KEY, emotion_id BIGINT NOT NULL REFERENCES emotion_states(id) ON DELETE CASCADE, before_strength DOUBLE PRECISION NOT NULL DEFAULT 0, after_strength DOUBLE PRECISION NOT NULL DEFAULT 0, reason TEXT NOT NULL DEFAULT '', source_meta JSONB NOT NULL DEFAULT '{}'::jsonb, version BIGINT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE group_agent_states (conversation_kind TEXT NOT NULL, target_id BIGINT NOT NULL, note TEXT NOT NULL DEFAULT '', updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(conversation_kind,target_id))`,
		`CREATE TABLE member_profiles (user_id BIGINT PRIMARY KEY, nickname TEXT NOT NULL, last_seen_at TIMESTAMPTZ NOT NULL, message_count BIGINT NOT NULL DEFAULT 0, intimacy DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK(intimacy>=-1 AND intimacy<=1))`,
		`CREATE TABLE member_names (user_id BIGINT NOT NULL REFERENCES member_profiles(user_id) ON DELETE CASCADE, group_id BIGINT NOT NULL, value TEXT NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(user_id,group_id,value))`,
		`CREATE TABLE member_intimacy_logs (id BIGSERIAL PRIMARY KEY, user_id BIGINT NOT NULL, delta DOUBLE PRECISION NOT NULL, before_value DOUBLE PRECISION NOT NULL, after_value DOUBLE PRECISION NOT NULL, reason TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE stickers (id BIGSERIAL PRIMARY KEY, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), file_name TEXT NOT NULL, file_hash TEXT NOT NULL UNIQUE, description TEXT NOT NULL DEFAULT '', use_count BIGINT NOT NULL DEFAULT 0)`,
		`CREATE TABLE mood_state (id INTEGER PRIMARY KEY, updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), energy DOUBLE PRECISION NOT NULL DEFAULT 0.5, sociability DOUBLE PRECISION NOT NULL DEFAULT 0.5, last_reason TEXT NOT NULL DEFAULT '')`,
		`INSERT INTO mood_state(id,energy,sociability,last_reason) VALUES(1,0.5,0.5,'')`,
		`CREATE TABLE model_call_hourly (bucket_start TIMESTAMPTZ NOT NULL, task TEXT NOT NULL, model TEXT NOT NULL, request_count BIGINT NOT NULL DEFAULT 0, failure_count BIGINT NOT NULL DEFAULT 0, latency_ms_sum BIGINT NOT NULL DEFAULT 0, total_tokens BIGINT NOT NULL DEFAULT 0, usage_reported_count BIGINT NOT NULL DEFAULT 0, PRIMARY KEY(bucket_start,task,model))`,
		`INSERT INTO schema_migrations(version,name) VALUES(15,?)`,
	}
	for i := range statements {
		if statements[i] == `INSERT INTO schema_migrations(version,name) VALUES(15,?)` {
			if err := db.Exec(`INSERT INTO schema_migrations(version,name) VALUES(15,?)`, v15BaseName).Error; err != nil {
				return fmt.Errorf("初始化 v15 schema 失败: %w", err)
			}
			continue
		}
		if err := db.Exec(statements[i]).Error; err != nil {
			return fmt.Errorf("初始化 v15 schema 失败: %w", err)
		}
	}
	if err := createV15Indexes(db); err != nil {
		return err
	}
	return validateV15Schema(db)
}

func createV15Indexes(db *gorm.DB) error {
	statements := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_message_conversation_onebot ON message_logs(conversation_kind,target_id,one_bot_message_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_message_conversation_ref ON message_logs(conversation_kind,target_id,message_ref) WHERE message_ref<>''`,
		`CREATE INDEX IF NOT EXISTS idx_message_logs_scope_id ON message_logs(conversation_kind,target_id,id)`,
		`CREATE INDEX IF NOT EXISTS idx_message_logs_time ON message_logs(message_time)`,
		`CREATE INDEX IF NOT EXISTS idx_message_logs_content_trgm ON message_logs USING gin(text_content gin_trgm_ops)`,
		`CREATE INDEX IF NOT EXISTS idx_message_forwards_message ON message_forwards(message_log_id)`,
		`CREATE INDEX IF NOT EXISTS idx_topic_threads_scope ON topic_threads(conversation_kind,target_id,status)`,
		`CREATE INDEX IF NOT EXISTS idx_topic_assignments_topic ON topic_assignments(topic_id,message_log_id)`,
		`CREATE INDEX IF NOT EXISTS idx_topic_summaries_topic_version ON topic_summaries(topic_id,version)`,
		`CREATE INDEX IF NOT EXISTS idx_topic_summary_title_trgm ON topic_summaries USING gin((summary_json->>'title') gin_trgm_ops)`,
		`CREATE INDEX IF NOT EXISTS idx_topic_summary_gist_trgm ON topic_summaries USING gin((summary_json->>'gist') gin_trgm_ops)`,
		`CREATE INDEX IF NOT EXISTS idx_topic_summary_keywords_trgm ON topic_summaries USING gin(((summary_json->'keywords')::text) gin_trgm_ops)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_transcript_input_seq ON conversation_transcript(conversation_kind,target_id,input_seq) WHERE input_seq IS NOT NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_transcript_visible_seq ON conversation_transcript(conversation_kind,target_id,visible_seq) WHERE visible_seq IS NOT NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_transcript_tool_call ON conversation_transcript(conversation_kind,target_id,run_id,tool_call_id) WHERE entry_kind='tool' AND tool_call_id<>''`,
		`CREATE INDEX IF NOT EXISTS idx_transcript_scope_visible ON conversation_transcript(conversation_kind,target_id,visible_seq,id)`,
		`CREATE INDEX IF NOT EXISTS idx_transcript_pending ON conversation_transcript(conversation_kind,target_id,input_seq) WHERE visible_seq IS NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_active_knowledge_content ON knowledge_items(subject_user_id,kind,content_hash) WHERE status='active'`,
		`CREATE INDEX IF NOT EXISTS idx_knowledge_core_subject ON knowledge_items(subject_user_id,is_core,status)`,
		`CREATE INDEX IF NOT EXISTS idx_knowledge_content_trgm ON knowledge_items USING gin(content gin_trgm_ops)`,
		`CREATE INDEX IF NOT EXISTS idx_knowledge_label_trgm ON knowledge_items USING gin(label gin_trgm_ops)`,
		`CREATE INDEX IF NOT EXISTS idx_knowledge_source ON knowledge_items(conversation_kind,target_id)`,
		`CREATE INDEX IF NOT EXISTS idx_emotion_current ON emotion_states(about_user_id,ended_at,anchor_at)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_emotion_revision_version ON emotion_revisions(emotion_id,version)`,
		`CREATE INDEX IF NOT EXISTS idx_emotion_revisions_state ON emotion_revisions(emotion_id,version DESC)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_knowledge_revision_version ON knowledge_revisions(item_id,version)`,
		`CREATE INDEX IF NOT EXISTS idx_knowledge_revisions_item ON knowledge_revisions(item_id,version DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_group_agent_states_updated ON group_agent_states(updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_member_intimacy_logs_user ON member_intimacy_logs(user_id,created_at DESC)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("创建 v15 索引失败: %w", err)
		}
	}
	return nil
}

func backfillKnowledgeHashes(db *gorm.DB) error {
	var rows []struct {
		ID            uint
		SubjectUserID int64
		Kind          string
		Content       string
	}
	if err := db.Table("knowledge_items").Select("id,subject_user_id,kind,content").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%d\n%s\n%s", row.SubjectUserID, row.Kind, strings.ToLower(strings.TrimSpace(row.Content))))))
		if err := db.Exec(`UPDATE knowledge_items SET content_hash=? WHERE id=?`, hash, row.ID).Error; err != nil {
			return err
		}
	}
	return db.Exec(`WITH dup AS (
		SELECT id,row_number() OVER(PARTITION BY subject_user_id,kind,content_hash ORDER BY updated_at DESC,id DESC) rn
		FROM knowledge_items WHERE status='active'
	) UPDATE knowledge_items ki SET status='archived',version=version+1,updated_at=now() FROM dup WHERE ki.id=dup.id AND dup.rn>1`).Error
}

func seedKnowledgeRevisions(db *gorm.DB) error {
	return db.Exec(`INSERT INTO knowledge_revisions(item_id,version,before_content,after_content,before_status,after_status,before_core,after_core,reason,source_meta,created_at)
		SELECT ki.id,ki.version,'',ki.content,'',ki.status,false,ki.is_core,'迁移保留原始状态',ki.source_meta,ki.updated_at
		FROM knowledge_items ki WHERE NOT EXISTS(SELECT 1 FROM knowledge_revisions kr WHERE kr.item_id=ki.id)`).Error
}

func dropLegacyColumns(db *gorm.DB) error {
	for _, statement := range []string{
		`ALTER TABLE topic_threads DROP COLUMN IF EXISTS group_id`,
		`DROP INDEX IF EXISTS idx_topic_threads_group_id`,
		`DROP INDEX IF EXISTS idx_topic_summary_coverage`,
		`DROP INDEX IF EXISTS idx_knowledge_candidates`,
		`DROP INDEX IF EXISTS idx_knowledge_label`,
		`DROP INDEX IF EXISTS idx_knowledge_content_trgm`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("清理 v15 旧结构失败: %w", err)
		}
	}
	return nil
}

func dropVectorExtension(db *gorm.DB) error {
	var remaining int64
	if err := db.Raw(`SELECT count(*) FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() AND a.attnum>0 AND NOT a.attisdropped AND format_type(a.atttypid,a.atttypmod) LIKE '%vector%'`).Scan(&remaining).Error; err != nil {
		return err
	}
	if remaining > 0 {
		return fmt.Errorf("仍有 %d 个 vector 字段，拒绝删除 vector 扩展", remaining)
	}
	return db.Exec(`DROP EXTENSION IF EXISTS vector`).Error
}

func validateV15Schema(db *gorm.DB) error {
	for _, table := range []string{"message_logs", "message_forwards", "conversation_sessions", "conversation_transcript", "context_compaction_checkpoints", "topic_threads", "topic_assignments", "topic_summaries", "topic_summary_sources", "knowledge_items", "knowledge_revisions", "emotion_states", "emotion_revisions", "group_agent_states", "member_profiles", "member_names", "member_intimacy_logs", "stickers", "mood_state", "model_call_hourly", "conversation_targets"} {
		if !db.Migrator().HasTable(table) {
			return fmt.Errorf("v15 缺少表 %s", table)
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
			return fmt.Errorf("v15 缺少字段 %s.%s", column.table, column.name)
		}
	}
	return nil
}

func renameLegacyTable(db *gorm.DB, table string) error {
	legacy := "legacy_" + table
	hasTable := db.Migrator().HasTable(table)
	hasLegacy := db.Migrator().HasTable(legacy)
	if !hasTable {
		return nil
	}
	if hasLegacy {
		return fmt.Errorf("v15 迁移冲突：%s 与 %s 同时存在", table, legacy)
	}
	if err := db.Migrator().RenameTable(table, legacy); err != nil {
		return fmt.Errorf("保留历史表 %s 失败: %w", table, err)
	}
	return nil
}

func dropSingleColumnUniqueConstraints(db *gorm.DB, table, column string) error {
	var names []string
	if err := db.Raw(`SELECT c.conname FROM pg_constraint c
		JOIN pg_class t ON t.oid=c.conrelid
		WHERE t.relname=? AND c.contype='u' AND c.conkey=ARRAY[(SELECT attnum FROM pg_attribute WHERE attrelid=t.oid AND attname=?)]::smallint[]`, table, column).Scan(&names).Error; err != nil {
		return err
	}
	for _, name := range names {
		if err := db.Exec(fmt.Sprintf(`ALTER TABLE %s DROP CONSTRAINT %q`, table, name)).Error; err != nil {
			return err
		}
	}
	return nil
}
