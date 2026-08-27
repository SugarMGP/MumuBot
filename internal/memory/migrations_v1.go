package memory

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const latestSchemaVersion = 2

func LatestSchemaVersion() int { return latestSchemaVersion }

var v1BusinessTables = []string{
	"message_logs", "topic_threads", "topic_assignments", "topic_summaries",
	"memories", "memory_evidence", "style_patterns", "style_pattern_evidence",
	"jargons", "jargon_evidence", "member_profiles", "member_names",
	"member_traits", "member_trait_evidence", "learning_states", "stickers", "mood_state",
}

func RunMigrations(db *gorm.DB, selfID int64, dimensions int) error {
	if db == nil {
		return fmt.Errorf("PostgreSQL 未初始化")
	}
	if selfID <= 0 {
		return fmt.Errorf("OneBot self_id 无效")
	}
	if dimensions <= 0 {
		return fmt.Errorf("embedding 维度无效")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext('mumubot_schema_migrations'))`).Error; err != nil {
			return fmt.Errorf("获取数据库迁移锁失败: %w", err)
		}
		if err := ensureExtensions(tx); err != nil {
			return err
		}
		tables, err := currentTables(tx)
		if err != nil {
			return err
		}
		if tables["schema_migrations"] {
			return applyVersionedMigrations(tx, selfID, dimensions)
		}
		present := 0
		for _, table := range v1BusinessTables {
			if tables[table] {
				present++
			}
		}
		switch {
		case len(tables) == 0:
			if err := initializeV1Schema(tx, dimensions); err != nil {
				return err
			}
		case present == len(v1BusinessTables) && len(tables) == len(v1BusinessTables):
			if err := migrateV1(tx, selfID, dimensions); err != nil {
				return err
			}
		default:
			return fmt.Errorf("数据库结构不完整：发现 %d/%d 张已知业务表，拒绝猜测修复", present, len(v1BusinessTables))
		}
		if err := recordSchemaVersion(tx, 1, "v1_schema"); err != nil {
			return err
		}
		return applyVersionedMigrations(tx, selfID, dimensions)
	})
}

func ensureExtensions(db *gorm.DB) error {
	for _, extension := range []string{"vector", "pg_trgm"} {
		if err := db.Exec("CREATE EXTENSION IF NOT EXISTS " + extension + " WITH SCHEMA public").Error; err != nil {
			return fmt.Errorf("启用 PostgreSQL 扩展 %s 失败: %w", extension, err)
		}
	}
	return nil
}

func currentTables(db *gorm.DB) (map[string]bool, error) {
	var names []string
	if err := db.Raw(`SELECT tablename FROM pg_tables WHERE schemaname = current_schema()`).Scan(&names).Error; err != nil {
		return nil, err
	}
	result := make(map[string]bool, len(names))
	for _, name := range names {
		result[name] = true
	}
	return result, nil
}

func applyVersionedMigrations(db *gorm.DB, selfID int64, dimensions int) error {
	tables, err := currentTables(db)
	if err != nil {
		return err
	}
	for _, table := range v1BusinessTables {
		if !tables[table] {
			return fmt.Errorf("schema 版本表存在，但缺少业务表 %s", table)
		}
	}
	if !tables["model_call_hourly"] {
		return fmt.Errorf("schema 版本表存在，但缺少模型调用统计表 model_call_hourly")
	}
	var versions []SchemaMigration
	if err := db.Order("version ASC").Find(&versions).Error; err != nil {
		return err
	}
	current := 0
	for i, item := range versions {
		expected := i + 1
		if item.Version != expected {
			return fmt.Errorf("schema 版本记录不连续：期望 v%d，实际 v%d", expected, item.Version)
		}
		if item.Version > latestSchemaVersion {
			return fmt.Errorf("数据库 schema v%d 高于程序支持的 v%d", item.Version, latestSchemaVersion)
		}
		expectedName := "v1_schema"
		if item.Version == 2 {
			expectedName = "drop_forward_payload"
		}
		if item.Name != expectedName {
			return fmt.Errorf("schema v%d 名称无效：%s", item.Version, item.Name)
		}
		current = item.Version
	}
	if current < 1 {
		return fmt.Errorf("schema 版本表存在但没有有效版本记录，拒绝猜测迁移")
	}
	if current < 2 {
		if err := migrateV2(db); err != nil {
			return err
		}
		if err := recordSchemaVersion(db, 2, "drop_forward_payload"); err != nil {
			return err
		}
	}
	if err := validateV1Schema(db, dimensions); err != nil {
		return err
	}
	return validateV2Schema(db)
}

func recordSchemaVersion(db *gorm.DB, version int, name string) error {
	return db.Exec(`INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, now())`, version, name).Error
}

func initializeV1Schema(db *gorm.DB, dimensions int) error {
	for _, statement := range v1SchemaStatements(dimensions) {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("初始化 v1 schema 失败: %w", err)
		}
	}
	return nil
}

func migrateV1(db *gorm.DB, selfID int64, dimensions int) error {
	requiredColumns := map[string][]string{
		"memories":        {"scope", "subject_user_id", "kind", "status", "content", "embedding"},
		"memory_evidence": {"memory_id", "message_log_id", "topic_summary_id"},
	}
	for table, columns := range requiredColumns {
		for _, column := range columns {
			var exists bool
			if err := db.Raw(`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=? AND column_name=?)`, table, column).Scan(&exists).Error; err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("旧数据库缺少 v1 迁移所需字段 %s.%s", table, column)
			}
		}
	}
	var invalidScopes int64
	if err := db.Raw(`SELECT count(*) FROM memories WHERE scope IS NULL OR scope NOT IN ('group','self','member')`).Scan(&invalidScopes).Error; err != nil {
		return err
	}
	if invalidScopes > 0 {
		return fmt.Errorf("旧数据库存在 %d 条未知记忆主体范围，拒绝猜测迁移", invalidScopes)
	}

	statements := []string{
		`DROP INDEX IF EXISTS uq_active_memory_content`,
		`ALTER TABLE memories DROP CONSTRAINT IF EXISTS memories_scope_subject_check`,
		fmt.Sprintf(`UPDATE memories SET subject_user_id = CASE scope WHEN 'group' THEN 0 WHEN 'self' THEN %d ELSE subject_user_id END`, selfID),
		`UPDATE memories SET status = 'archived' WHERE status = 'candidate'`,
		`ALTER TABLE memories ALTER COLUMN subject_user_id SET DEFAULT 0`,
		`ALTER TABLE memories ALTER COLUMN subject_user_id SET NOT NULL`,
		`ALTER TABLE memories DROP COLUMN scope`,
		`ALTER TABLE memories ADD CONSTRAINT memories_group_check CHECK (group_id > 0)`,
		`ALTER TABLE memories ADD CONSTRAINT memories_subject_check CHECK (subject_user_id >= 0)`,
		`ALTER TABLE memories ADD CONSTRAINT memories_kind_check CHECK (kind IN ('fact','episode','preference','constraint','goal'))`,
		`ALTER TABLE memories ADD CONSTRAINT memories_status_check CHECK (status IN ('active','archived'))`,
		`ALTER TABLE memories ADD CONSTRAINT memories_content_check CHECK (btrim(content) <> '')`,
		`CREATE TABLE memory_evidence_v1 (memory_id BIGINT NOT NULL, message_log_id BIGINT NOT NULL, PRIMARY KEY(memory_id, message_log_id))`,
		`INSERT INTO memory_evidence_v1(memory_id, message_log_id) SELECT DISTINCT memory_id, message_log_id FROM memory_evidence WHERE message_log_id IS NOT NULL`,
		`INSERT INTO memory_evidence_v1(memory_id, message_log_id)
		 SELECT DISTINCT me.memory_id, ta.message_log_id FROM memory_evidence me
		 JOIN topic_summaries ts ON ts.id=me.topic_summary_id
		 JOIN topic_assignments through_ta ON through_ta.id=ts.through_topic_assignment_id
		 JOIN topic_assignments ta ON ta.topic_id=through_ta.topic_id AND ta.id<=through_ta.id
		 WHERE me.topic_summary_id IS NOT NULL ON CONFLICT DO NOTHING`,
		`DROP TABLE memory_evidence`,
		`ALTER TABLE memory_evidence_v1 RENAME TO memory_evidence`,
		`ALTER TABLE memory_evidence ADD CONSTRAINT fk_memory_evidence_memory FOREIGN KEY(memory_id) REFERENCES memories(id) ON DELETE CASCADE`,
		`ALTER TABLE memory_evidence ADD CONSTRAINT fk_memory_evidence_message FOREIGN KEY(message_log_id) REFERENCES message_logs(id) ON DELETE RESTRICT`,
		`CREATE INDEX idx_memory_evidence_message_log_id ON memory_evidence(message_log_id)`,
		`WITH ranked AS (
		 SELECT id,
		  first_value(id) OVER (PARTITION BY group_id, subject_user_id, kind, lower(btrim(content)) ORDER BY updated_at DESC, id DESC) AS keeper_id,
		  row_number() OVER (PARTITION BY group_id, subject_user_id, kind, lower(btrim(content)) ORDER BY updated_at DESC, id DESC) AS position
		 FROM memories WHERE status='active'
		)
		INSERT INTO memory_evidence(memory_id, message_log_id)
		SELECT ranked.keeper_id, memory_evidence.message_log_id
		FROM ranked JOIN memory_evidence ON memory_evidence.memory_id=ranked.id
		WHERE ranked.position>1 ON CONFLICT DO NOTHING`,
		`WITH ranked AS (
		 SELECT id,
		  row_number() OVER (PARTITION BY group_id, subject_user_id, kind, lower(btrim(content)) ORDER BY updated_at DESC, id DESC) AS position
		 FROM memories WHERE status='active'
		)
		UPDATE memories SET status='archived', updated_at=now()
		FROM ranked WHERE memories.id=ranked.id AND ranked.position>1`,
		`CREATE UNIQUE INDEX uq_active_memory_content ON memories(group_id, subject_user_id, kind, lower(btrim(content))) WHERE status='active'`,
		`DROP INDEX IF EXISTS idx_message_logs_one_bot_message_id`,
		`DROP INDEX IF EXISTS uni_message_logs_one_bot_message_id`,
		`CREATE UNIQUE INDEX uq_message_group_onebot ON message_logs(group_id, one_bot_message_id)`,
		`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE model_call_hourly (
		 bucket_start TIMESTAMPTZ NOT NULL, task TEXT NOT NULL, model TEXT NOT NULL,
		 request_count BIGINT NOT NULL DEFAULT 0, failure_count BIGINT NOT NULL DEFAULT 0,
		 latency_ms_sum BIGINT NOT NULL DEFAULT 0,
		 total_tokens BIGINT NOT NULL DEFAULT 0, usage_reported_count BIGINT NOT NULL DEFAULT 0,
		 PRIMARY KEY(bucket_start, task, model))`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("执行 migrateV1 失败: %w", err)
		}
	}
	return validateV1Schema(db, dimensions)
}

func validateV1Schema(db *gorm.DB, dimensions int) error {
	for _, table := range []string{"memories", "topic_summaries", "style_patterns"} {
		var formatted string
		if err := db.Raw(`SELECT format_type(a.atttypid, a.atttypmod) FROM pg_attribute a
			JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace
			WHERE n.nspname=current_schema() AND c.relname=? AND a.attname='embedding' AND a.attnum>0`, table).Scan(&formatted).Error; err != nil {
			return err
		}
		expected := fmt.Sprintf("vector(%d)", dimensions)
		actual := strings.TrimSpace(formatted)
		if actual != expected && actual != "public."+expected {
			return fmt.Errorf("%s.embedding 维度不匹配：当前 %s，期望 %s", table, formatted, expected)
		}
	}
	return nil
}

func v1SchemaStatements(dimensions int) []string {
	vectorType := fmt.Sprintf("public.vector(%d)", dimensions)
	return []string{
		`CREATE TABLE message_logs (id BIGSERIAL PRIMARY KEY, one_bot_message_id BIGINT NOT NULL, group_id BIGINT NOT NULL, user_id BIGINT NOT NULL, nickname TEXT NOT NULL, text_content TEXT NOT NULL, display_content TEXT NOT NULL, forward_payload JSONB, reply_to_message_id BIGINT, is_mentioned BOOLEAN NOT NULL, message_time TIMESTAMPTZ NOT NULL, recalled_at TIMESTAMPTZ)`,
		`CREATE UNIQUE INDEX uq_message_group_onebot ON message_logs(group_id, one_bot_message_id)`,
		`CREATE INDEX idx_message_logs_group_id ON message_logs(group_id)`,
		`CREATE INDEX idx_message_logs_user_id ON message_logs(user_id)`,
		`CREATE INDEX idx_message_logs_message_time ON message_logs(message_time)`,
		`CREATE TABLE topic_threads (id BIGSERIAL PRIMARY KEY, group_id BIGINT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE INDEX idx_topic_threads_group_id ON topic_threads(group_id)`,
		`CREATE TABLE topic_assignments (id BIGSERIAL PRIMARY KEY, message_log_id BIGINT NOT NULL UNIQUE REFERENCES message_logs(id) ON DELETE CASCADE, topic_id BIGINT REFERENCES topic_threads(id) ON DELETE RESTRICT)`,
		`CREATE INDEX idx_topic_assignments_topic_id ON topic_assignments(topic_id)`,
		`CREATE TABLE topic_summaries (id BIGSERIAL PRIMARY KEY, through_topic_assignment_id BIGINT NOT NULL UNIQUE REFERENCES topic_assignments(id) ON DELETE RESTRICT, summary_json JSONB NOT NULL, embedding ` + vectorType + ` NOT NULL, memory_processed BOOLEAN NOT NULL DEFAULT false, created_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE memories (id BIGSERIAL PRIMARY KEY, group_id BIGINT NOT NULL CHECK(group_id>0), subject_user_id BIGINT NOT NULL DEFAULT 0 CHECK(subject_user_id>=0), kind TEXT NOT NULL CHECK(kind IN ('fact','episode','preference','constraint','goal')), status TEXT NOT NULL CHECK(status IN ('active','archived')), content TEXT NOT NULL CHECK(btrim(content)<>''), embedding ` + vectorType + ` NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE INDEX idx_memories_group_id ON memories(group_id)`,
		`CREATE INDEX idx_memories_subject_user_id ON memories(subject_user_id)`,
		`CREATE INDEX idx_memories_kind ON memories(kind)`,
		`CREATE INDEX idx_memories_status ON memories(status)`,
		`CREATE UNIQUE INDEX uq_active_memory_content ON memories(group_id, subject_user_id, kind, lower(btrim(content))) WHERE status='active'`,
		`CREATE TABLE memory_evidence (memory_id BIGINT NOT NULL REFERENCES memories(id) ON DELETE CASCADE, message_log_id BIGINT NOT NULL REFERENCES message_logs(id) ON DELETE RESTRICT, PRIMARY KEY(memory_id,message_log_id))`,
		`CREATE INDEX idx_memory_evidence_message_log_id ON memory_evidence(message_log_id)`,
		`CREATE TABLE style_patterns (id BIGSERIAL PRIMARY KEY, group_id BIGINT NOT NULL, situation TEXT NOT NULL, expression TEXT NOT NULL, status TEXT NOT NULL, embedding ` + vectorType + ` NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE INDEX idx_style_patterns_group_id ON style_patterns(group_id)`,
		`CREATE INDEX idx_style_patterns_status ON style_patterns(status)`,
		`CREATE UNIQUE INDEX uq_style_pattern_text ON style_patterns(group_id,lower(btrim(situation)),lower(btrim(expression)))`,
		`CREATE TABLE style_pattern_evidence (style_pattern_id BIGINT NOT NULL REFERENCES style_patterns(id) ON DELETE CASCADE, message_log_id BIGINT NOT NULL REFERENCES message_logs(id) ON DELETE RESTRICT, PRIMARY KEY(style_pattern_id,message_log_id))`,
		`CREATE TABLE jargons (id BIGSERIAL PRIMARY KEY, group_id BIGINT NOT NULL, term TEXT NOT NULL, meaning TEXT NOT NULL, status TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE INDEX idx_jargons_group_id ON jargons(group_id)`, `CREATE INDEX idx_jargons_status ON jargons(status)`,
		`CREATE UNIQUE INDEX uq_jargon_term ON jargons(group_id,lower(btrim(term)))`,
		`CREATE TABLE jargon_evidence (jargon_id BIGINT NOT NULL REFERENCES jargons(id) ON DELETE CASCADE, message_log_id BIGINT NOT NULL REFERENCES message_logs(id) ON DELETE RESTRICT, PRIMARY KEY(jargon_id,message_log_id))`,
		`CREATE TABLE member_profiles (user_id BIGINT PRIMARY KEY, nickname TEXT NOT NULL, last_seen_at TIMESTAMPTZ NOT NULL, message_count BIGINT NOT NULL)`,
		`CREATE TABLE member_names (user_id BIGINT NOT NULL REFERENCES member_profiles(user_id) ON DELETE CASCADE, group_id BIGINT NOT NULL, value TEXT NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(user_id,group_id,value))`,
		`CREATE TABLE member_traits (id BIGSERIAL PRIMARY KEY, user_id BIGINT NOT NULL REFERENCES member_profiles(user_id) ON DELETE CASCADE, kind TEXT NOT NULL, value TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE INDEX idx_member_traits_user_id ON member_traits(user_id)`,
		`CREATE UNIQUE INDEX uq_member_trait ON member_traits(user_id,kind,lower(btrim(value)))`,
		`CREATE TABLE member_trait_evidence (member_trait_id BIGINT NOT NULL REFERENCES member_traits(id) ON DELETE CASCADE, message_log_id BIGINT NOT NULL REFERENCES message_logs(id) ON DELETE RESTRICT, PRIMARY KEY(member_trait_id,message_log_id))`,
		`CREATE TABLE learning_states (group_id BIGINT NOT NULL, kind TEXT NOT NULL, last_message_log_id BIGINT NOT NULL, PRIMARY KEY(group_id,kind))`,
		`CREATE TABLE stickers (id BIGSERIAL PRIMARY KEY, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), file_name TEXT NOT NULL, file_hash TEXT NOT NULL UNIQUE, description TEXT, use_count INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE mood_state (id BIGINT PRIMARY KEY, updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), valence DOUBLE PRECISION NOT NULL DEFAULT 0, energy DOUBLE PRECISION NOT NULL DEFAULT 0.5, sociability DOUBLE PRECISION NOT NULL DEFAULT 0.5, last_reason TEXT)`,
		`INSERT INTO mood_state(id,energy,sociability) VALUES(1,0.5,0.5)`,
		`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
		`CREATE TABLE model_call_hourly (bucket_start TIMESTAMPTZ NOT NULL, task TEXT NOT NULL, model TEXT NOT NULL, request_count BIGINT NOT NULL DEFAULT 0, failure_count BIGINT NOT NULL DEFAULT 0, latency_ms_sum BIGINT NOT NULL DEFAULT 0, total_tokens BIGINT NOT NULL DEFAULT 0, usage_reported_count BIGINT NOT NULL DEFAULT 0, PRIMARY KEY(bucket_start,task,model))`,
	}
}
