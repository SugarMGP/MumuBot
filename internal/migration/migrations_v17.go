package migration

import (
	"fmt"

	"gorm.io/gorm"
)

const v17BaseName = "clean_semantic_session_schema"

func initializeV17Schema(db *gorm.DB) error {
	statements := []string{
		`CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public`,
		`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE message_logs (
			id BIGSERIAL PRIMARY KEY,
			one_bot_message_id BIGINT NOT NULL,
			conversation_kind TEXT NOT NULL CHECK(conversation_kind IN ('group','private')),
			target_id BIGINT NOT NULL CHECK(target_id>0),
			user_id BIGINT NOT NULL,
			nickname TEXT NOT NULL,
			text_content TEXT NOT NULL,
			display_content TEXT NOT NULL,
			reply_to_message_id BIGINT,
			is_mentioned BOOLEAN NOT NULL DEFAULT false,
			message_time TIMESTAMPTZ NOT NULL,
			recalled_at TIMESTAMPTZ,
			media_refs JSONB,
			file_refs JSONB NOT NULL DEFAULT '[]'
		)`,
		`CREATE UNIQUE INDEX uq_message_onebot_id ON message_logs(one_bot_message_id)`,
		`CREATE INDEX idx_message_logs_scope_id ON message_logs(conversation_kind,target_id,id)`,
		`CREATE INDEX idx_message_logs_time ON message_logs(message_time)`,
		`CREATE INDEX idx_message_logs_time_id ON message_logs(message_time,id)`,
		`CREATE INDEX idx_message_logs_media_refs ON message_logs USING gin(media_refs jsonb_path_ops)`,
		`CREATE INDEX idx_message_logs_text_lower_trgm ON message_logs USING gin((lower(text_content)) gin_trgm_ops)`,
		`CREATE INDEX idx_message_logs_display_lower_trgm ON message_logs USING gin((lower(display_content)) gin_trgm_ops)`,
		`CREATE TABLE message_forwards (
			id BIGSERIAL PRIMARY KEY,
			handle TEXT NOT NULL UNIQUE,
			message_log_id BIGINT NOT NULL REFERENCES message_logs(id) ON DELETE CASCADE,
			parent_handle TEXT REFERENCES message_forwards(handle) ON DELETE CASCADE,
			nodes JSONB NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE conversation_targets (
			conversation_kind TEXT NOT NULL CHECK(conversation_kind IN ('group','private','worker','life')),
			target_id BIGINT NOT NULL CHECK(target_id>0),
			name TEXT NOT NULL DEFAULT '',
			remote_remark TEXT NOT NULL DEFAULT '',
			blocked BOOLEAN NOT NULL DEFAULT false,
			extra_prompt TEXT NOT NULL DEFAULT '',
			active BOOLEAN NOT NULL DEFAULT true,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			PRIMARY KEY(conversation_kind,target_id)
		)`,
		`CREATE INDEX idx_conversation_targets_list ON conversation_targets(conversation_kind,active DESC,target_id)`,
		`CREATE TABLE conversation_sessions (
			conversation_kind TEXT NOT NULL CHECK(conversation_kind IN ('group','private','worker','life')),
			target_id BIGINT NOT NULL CHECK(target_id>0),
			next_input_seq BIGINT NOT NULL DEFAULT 0,
			next_visible_seq BIGINT NOT NULL DEFAULT 0,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			PRIMARY KEY(conversation_kind,target_id)
		)`,
		`CREATE TABLE worker_sessions (
			id BIGSERIAL PRIMARY KEY,
			title TEXT NOT NULL CHECK(btrim(title)<>'' AND char_length(title) <= 30),
			status TEXT NOT NULL CHECK(status IN ('idle','running','failed','cancelled')),
			model_name TEXT NOT NULL DEFAULT '',
			last_error TEXT NOT NULL DEFAULT '',
			context_summary JSONB NOT NULL DEFAULT '[]',
			context_start_id BIGINT NOT NULL DEFAULT 0,
			context_through_id BIGINT NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE INDEX idx_worker_sessions_updated ON worker_sessions(updated_at DESC, id DESC)`,
		`CREATE TABLE conversation_transcript (
			id BIGSERIAL PRIMARY KEY,
			conversation_kind TEXT NOT NULL CHECK(conversation_kind IN ('group','private','worker','life')),
			target_id BIGINT NOT NULL CHECK(target_id>0),
			input_seq BIGINT,
			visible_seq BIGINT,
			entry_kind TEXT NOT NULL CHECK(entry_kind IN ('user','assistant','tool','observation','agent_message','system_prompt','context_digest','group_note','system_note','pinned_memory')),
			message_log_id BIGINT REFERENCES message_logs(id) ON DELETE RESTRICT,
			run_id TEXT NOT NULL DEFAULT '',
			tool_call_id TEXT NOT NULL DEFAULT '',
			tool_name TEXT NOT NULL DEFAULT '',
			tool_status TEXT NOT NULL DEFAULT '' CHECK(tool_status IN ('','planned','running','succeeded','failed','unknown','not_executed')),
			payload JSONB NOT NULL,
			result_ref TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE UNIQUE INDEX uq_transcript_input_seq ON conversation_transcript(conversation_kind,target_id,input_seq) WHERE input_seq IS NOT NULL`,
		`CREATE UNIQUE INDEX uq_transcript_visible_seq ON conversation_transcript(conversation_kind,target_id,visible_seq) WHERE visible_seq IS NOT NULL`,
		`CREATE UNIQUE INDEX uq_transcript_tool_call ON conversation_transcript(conversation_kind,target_id,run_id,tool_call_id) WHERE entry_kind='tool' AND tool_call_id<>''`,
		`CREATE INDEX idx_transcript_pending ON conversation_transcript(conversation_kind,target_id) WHERE visible_seq IS NULL AND entry_kind IN ('user','observation','agent_message')`,
		`CREATE INDEX idx_transcript_message_log ON conversation_transcript(message_log_id) WHERE message_log_id IS NOT NULL`,
		`CREATE TABLE topic_threads (
			id BIGSERIAL PRIMARY KEY,
			conversation_kind TEXT NOT NULL CHECK(conversation_kind IN ('group','private')),
			target_id BIGINT NOT NULL CHECK(target_id>0),
			status TEXT NOT NULL DEFAULT 'dormant' CHECK(status IN ('active','dormant')),
			stability_days DOUBLE PRECISION NOT NULL DEFAULT 30 CHECK(stability_days>0),
			recall_anchor_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			forget_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			forgotten_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE INDEX idx_topic_threads_scope ON topic_threads(conversation_kind,target_id,status,updated_at DESC,id DESC)`,
		`CREATE INDEX idx_topic_forget ON topic_threads(forget_at) WHERE forgotten_at IS NULL`,
		`CREATE TABLE topic_assignments (
			id BIGSERIAL PRIMARY KEY,
			message_log_id BIGINT NOT NULL UNIQUE REFERENCES message_logs(id) ON DELETE CASCADE,
			topic_id BIGINT REFERENCES topic_threads(id) ON DELETE RESTRICT
		)`,
		`CREATE INDEX idx_topic_assignments_topic ON topic_assignments(topic_id,message_log_id)`,
		`CREATE TABLE topic_summaries (
			id BIGSERIAL PRIMARY KEY,
			topic_id BIGINT NOT NULL REFERENCES topic_threads(id) ON DELETE RESTRICT,
			version BIGINT NOT NULL DEFAULT 1 CHECK(version>0),
			summary_json JSONB NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE(topic_id,version)
		)`,
		`CREATE TABLE topic_summary_sources (
			summary_id BIGINT NOT NULL REFERENCES topic_summaries(id) ON DELETE CASCADE,
			message_log_id BIGINT NOT NULL,
			one_bot_message_id BIGINT NOT NULL,
			message_time TIMESTAMPTZ NOT NULL,
			is_anchor BOOLEAN NOT NULL DEFAULT false,
			PRIMARY KEY(summary_id,message_log_id)
		)`,
		`CREATE INDEX idx_topic_sources_anchor ON topic_summary_sources(summary_id,is_anchor)`,
		`CREATE INDEX idx_topic_sources_time ON topic_summary_sources(message_time)`,
		`CREATE TABLE context_compaction_checkpoints (
			conversation_kind TEXT NOT NULL CHECK(conversation_kind IN ('group','private')),
			target_id BIGINT NOT NULL CHECK(target_id>0),
			version BIGINT NOT NULL DEFAULT 0,
			through_visible_seq BIGINT,
			through_message_log_id BIGINT,
			overview TEXT NOT NULL DEFAULT '',
			active_topic_versions JSONB NOT NULL DEFAULT '[]'::jsonb,
			batch_id TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			PRIMARY KEY(conversation_kind,target_id)
		)`,
		`CREATE TABLE knowledge_items (
			id BIGSERIAL PRIMARY KEY,
			subject_type TEXT NOT NULL CHECK(subject_type IN ('group','user','self','object')),
			subject_target TEXT NOT NULL CHECK(btrim(subject_target)<>''),
			memory_type TEXT NOT NULL CHECK(memory_type IN ('profile','experience')),
			label TEXT NOT NULL DEFAULT '',
			content TEXT NOT NULL CHECK(btrim(content)<>''),
			pinned BOOLEAN NOT NULL DEFAULT false,
			status TEXT NOT NULL DEFAULT 'active' CHECK(status IN ('active','archived')),
			stability_days DOUBLE PRECISION NOT NULL DEFAULT 30 CHECK(stability_days>0),
			recall_anchor_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			forget_at TIMESTAMPTZ,
			version BIGINT NOT NULL DEFAULT 1 CHECK(version>0),
			content_hash TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE INDEX idx_knowledge_subject ON knowledge_items(subject_type,subject_target,memory_type,status)`,
		`CREATE INDEX idx_knowledge_pinned ON knowledge_items(pinned,status)`,
		`CREATE INDEX idx_knowledge_status_updated ON knowledge_items(status,updated_at DESC,id DESC)`,
		`CREATE INDEX idx_knowledge_content_trgm ON knowledge_items USING gin(content gin_trgm_ops)`,
		`CREATE INDEX idx_knowledge_label_trgm ON knowledge_items USING gin(label gin_trgm_ops)`,
		`CREATE INDEX idx_knowledge_search_trgm ON knowledge_items USING gin((lower(content || ' ' || label || ' ' || subject_target)) gin_trgm_ops)`,
		`CREATE UNIQUE INDEX uq_knowledge_content_active ON knowledge_items(subject_type,subject_target,memory_type,content_hash) WHERE status='active'`,
		`CREATE INDEX idx_knowledge_forget ON knowledge_items(forget_at) WHERE status='active' AND forget_at IS NOT NULL`,
		`CREATE TABLE knowledge_revisions (
			id BIGSERIAL PRIMARY KEY,
			item_id BIGINT NOT NULL REFERENCES knowledge_items(id) ON DELETE CASCADE,
			version BIGINT NOT NULL,
			before_content TEXT NOT NULL DEFAULT '',
			after_content TEXT NOT NULL DEFAULT '',
			before_status TEXT NOT NULL DEFAULT '',
			after_status TEXT NOT NULL DEFAULT '',
			before_pinned BOOLEAN NOT NULL DEFAULT false,
			after_pinned BOOLEAN NOT NULL DEFAULT false,
			reason TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE(item_id,version)
		)`,
		`CREATE INDEX idx_knowledge_revisions_item ON knowledge_revisions(item_id,version DESC)`,
		`CREATE TABLE emotion_states (
			id BIGSERIAL PRIMARY KEY,
			kind TEXT NOT NULL,
			fact TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '',
			strength DOUBLE PRECISION NOT NULL DEFAULT 0,
			occurred_at TIMESTAMPTZ NOT NULL,
			anchor_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			ended_at TIMESTAMPTZ,
			version BIGINT NOT NULL DEFAULT 1
		)`,
		`CREATE TABLE emotion_revisions (
			id BIGSERIAL PRIMARY KEY,
			emotion_id BIGINT NOT NULL REFERENCES emotion_states(id) ON DELETE CASCADE,
			version BIGINT NOT NULL,
			snapshot JSONB NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE(emotion_id,version)
		)`,
		`CREATE INDEX idx_emotion_current ON emotion_states(ended_at,anchor_at)`,
		`CREATE TABLE mood_state (
			id INTEGER PRIMARY KEY,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			energy DOUBLE PRECISION NOT NULL DEFAULT 0.5,
			sociability DOUBLE PRECISION NOT NULL DEFAULT 0.5,
			last_reason TEXT NOT NULL DEFAULT ''
		)`,
		`INSERT INTO mood_state(id,energy,sociability,last_reason) VALUES(1,0.5,0.5,'')`,
		`CREATE TABLE life_state (
			id INTEGER PRIMARY KEY CHECK(id=1),
			awake BOOLEAN NOT NULL DEFAULT true,
			activity_text TEXT NOT NULL DEFAULT '',
			activity_since TIMESTAMPTZ,
			sleep_started_at TIMESTAMPTZ,
			woke_at TIMESTAMPTZ,
			context_summary JSONB NOT NULL DEFAULT '[]'::jsonb,
			context_through_id BIGINT NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			last_error_code TEXT NOT NULL DEFAULT '',
			retry_count INTEGER NOT NULL DEFAULT 0 CHECK(retry_count BETWEEN 0 AND 2),
			retry_at TIMESTAMPTZ,
			retry_exhausted BOOLEAN NOT NULL DEFAULT false,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`INSERT INTO life_state(id,awake,activity_text) VALUES(1,true,'')`,
		`CREATE TABLE schedules (
			id BIGSERIAL PRIMARY KEY,
			target_kind TEXT NOT NULL CHECK(target_kind IN ('group','private','worker','life')),
			target_id BIGINT NOT NULL CHECK(target_id>0),
			schedule_key TEXT NOT NULL DEFAULT '',
			run_at TIMESTAMPTZ,
			cron_expr TEXT NOT NULL DEFAULT '',
			next_run_at TIMESTAMPTZ NOT NULL,
			text TEXT NOT NULL CHECK(btrim(text)<>''),
			enabled BOOLEAN NOT NULL DEFAULT true,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			CHECK ((run_at IS NULL) <> (cron_expr=''))
		)`,
		`CREATE INDEX idx_schedules_due ON schedules(enabled,next_run_at)`,
		`CREATE UNIQUE INDEX uq_schedules_slot ON schedules(target_kind,target_id,schedule_key) WHERE schedule_key<>''`,
		`CREATE TABLE diaries (
			diary_date DATE PRIMARY KEY,
			content_markdown TEXT NOT NULL CHECK(btrim(content_markdown)<>''),
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE member_profiles (
			user_id BIGINT PRIMARY KEY,
			nickname TEXT NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE member_names (
			user_id BIGINT NOT NULL REFERENCES member_profiles(user_id) ON DELETE CASCADE,
			group_id BIGINT NOT NULL,
			value TEXT NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			PRIMARY KEY(user_id,group_id,value)
		)`,
		`CREATE INDEX idx_member_profiles_nickname_trgm ON member_profiles USING gin(nickname gin_trgm_ops)`,
		`CREATE INDEX idx_member_names_value_trgm ON member_names USING gin(value gin_trgm_ops)`,
		`CREATE TABLE stickers (
			id BIGSERIAL PRIMARY KEY,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			file_name TEXT NOT NULL,
			file_hash TEXT NOT NULL UNIQUE,
			description TEXT NOT NULL DEFAULT '',
			use_count BIGINT NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX idx_stickers_order ON stickers(use_count DESC,updated_at DESC,id DESC)`,
		`CREATE INDEX idx_stickers_description_trgm ON stickers USING gin(description gin_trgm_ops)`,
		`CREATE TABLE model_call_hourly (
			bucket_start TIMESTAMPTZ NOT NULL,
			task TEXT NOT NULL,
			model TEXT NOT NULL,
			request_count BIGINT NOT NULL DEFAULT 0,
			failure_count BIGINT NOT NULL DEFAULT 0,
			latency_ms_sum BIGINT NOT NULL DEFAULT 0,
			total_tokens BIGINT NOT NULL DEFAULT 0,
			prompt_tokens BIGINT NOT NULL DEFAULT 0,
			cached_tokens BIGINT NOT NULL DEFAULT 0,
			usage_reported_count BIGINT NOT NULL DEFAULT 0,
			PRIMARY KEY(bucket_start,task,model)
		)`,
		`CREATE TABLE app_config (
			config_key TEXT PRIMARY KEY CHECK(btrim(config_key)<>''),
			config_value JSONB NOT NULL,
			version BIGINT NOT NULL DEFAULT 1 CHECK(version>0),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE model_profiles (
			id BIGSERIAL PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			provider TEXT NOT NULL DEFAULT 'openai_responses',
			base_url TEXT NOT NULL,
			model_name TEXT NOT NULL,
			context_window INT NOT NULL CHECK(context_window>0),
			thinking_type TEXT NOT NULL DEFAULT '',
			reasoning_effort TEXT NOT NULL DEFAULT '',
			api_key_ciphertext BYTEA NOT NULL,
			api_key_nonce BYTEA NOT NULL,
			api_key_key_version INT NOT NULL DEFAULT 1,
			api_key_hint TEXT NOT NULL DEFAULT '',
			active BOOLEAN NOT NULL DEFAULT false,
			version BIGINT NOT NULL DEFAULT 1,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			CHECK(thinking_type IN ('','enabled','disabled')),
			CHECK(reasoning_effort IN ('','low','medium','high','xhigh','max'))
		)`,
		`CREATE UNIQUE INDEX uq_model_profiles_active ON model_profiles(active) WHERE active`,
		`CREATE TABLE mcp_servers (
			id BIGSERIAL PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			enabled BOOLEAN NOT NULL DEFAULT true,
			transport TEXT NOT NULL,
			config_ciphertext BYTEA NOT NULL,
			config_nonce BYTEA NOT NULL,
			key_version INT NOT NULL DEFAULT 1,
			version BIGINT NOT NULL DEFAULT 1,
			position INT NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE persona_presets (
			id BIGSERIAL PRIMARY KEY,
			name TEXT NOT NULL UNIQUE CHECK(btrim(name)<>''),
			description TEXT NOT NULL DEFAULT '',
			persona_card TEXT NOT NULL CHECK(btrim(persona_card)<>''),
			simulation_prompt TEXT NOT NULL CHECK(btrim(simulation_prompt)<>''),
			life_prompt TEXT NOT NULL DEFAULT '',
			builtin BOOLEAN NOT NULL DEFAULT false,
			active BOOLEAN NOT NULL DEFAULT false,
			version BIGINT NOT NULL DEFAULT 1 CHECK(version>0),
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE UNIQUE INDEX uq_persona_presets_active ON persona_presets(active) WHERE active`,
		`CREATE UNIQUE INDEX uq_persona_presets_builtin ON persona_presets(builtin) WHERE builtin`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("初始化 v17 schema 失败: %w", err)
		}
	}
	return nil
}
