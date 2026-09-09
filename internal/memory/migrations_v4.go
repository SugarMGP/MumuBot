package memory

import (
	"fmt"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	pgvector "github.com/pgvector/pgvector-go"
	"gorm.io/gorm"
)

func migrateV4(db *gorm.DB, selfID int64, dimensions int) error {
	statements := []string{
		`CREATE TABLE knowledge_items (id BIGSERIAL PRIMARY KEY, group_id BIGINT NOT NULL CHECK(group_id>0), subject_user_id BIGINT NOT NULL CHECK(subject_user_id>=0), kind TEXT NOT NULL CHECK(kind IN ('fact','episode','preference','constraint','goal','term','expression','alias')), label TEXT NOT NULL DEFAULT '', content TEXT NOT NULL CHECK(btrim(content)<>''), status TEXT NOT NULL CHECK(status IN ('candidate','active','archived')), embedding public.vector(` + fmt.Sprint(dimensions) + `), reviewed_through_id BIGINT NOT NULL DEFAULT 0 CHECK(reviewed_through_id>=0), created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), CHECK(kind<>'term' OR btrim(label)<>''))`,
		`CREATE INDEX idx_knowledge_group_subject ON knowledge_items(group_id,subject_user_id,status)`,
		`CREATE INDEX idx_knowledge_candidates ON knowledge_items(status,reviewed_through_id,id)`,
		`CREATE INDEX idx_knowledge_label ON knowledge_items(group_id,lower(btrim(label)))`,
		`CREATE INDEX idx_knowledge_content_trgm ON knowledge_items USING gin(content public.gin_trgm_ops)`,
		`CREATE TABLE knowledge_relations (id BIGSERIAL PRIMARY KEY, source_item_id BIGINT NOT NULL REFERENCES knowledge_items(id) ON DELETE RESTRICT, target_item_id BIGINT NOT NULL REFERENCES knowledge_items(id) ON DELETE RESTRICT, kind TEXT NOT NULL CHECK(kind IN ('variant_of','part_of','supersedes','contradicts')), status TEXT NOT NULL CHECK(status IN ('candidate','active','archived')), CHECK(source_item_id<>target_item_id), CHECK(kind<>'contradicts' OR source_item_id<target_item_id), UNIQUE(source_item_id,target_item_id,kind))`,
		`CREATE INDEX idx_knowledge_relation_target ON knowledge_relations(target_item_id)`,
		`CREATE TABLE knowledge_evidence_sets (id BIGSERIAL PRIMARY KEY, item_id BIGINT REFERENCES knowledge_items(id) ON DELETE CASCADE, relation_id BIGINT REFERENCES knowledge_relations(id) ON DELETE CASCADE, CHECK((item_id IS NULL)<>(relation_id IS NULL)))`,
		`CREATE INDEX idx_knowledge_evidence_item ON knowledge_evidence_sets(item_id)`,
		`CREATE INDEX idx_knowledge_evidence_relation ON knowledge_evidence_sets(relation_id)`,
		`CREATE TABLE knowledge_evidence_messages (evidence_set_id BIGINT NOT NULL REFERENCES knowledge_evidence_sets(id) ON DELETE CASCADE, message_log_id BIGINT NOT NULL REFERENCES message_logs(id) ON DELETE RESTRICT, PRIMARY KEY(evidence_set_id,message_log_id))`,
		`CREATE INDEX idx_knowledge_evidence_message ON knowledge_evidence_messages(message_log_id)`,
		`CREATE TABLE group_agent_states (group_id BIGINT PRIMARY KEY CHECK(group_id>0), note TEXT NOT NULL CHECK(char_length(btrim(note)) BETWEEN 1 AND 300), updated_at TIMESTAMPTZ NOT NULL DEFAULT now())`,
	}
	for _, sql := range statements {
		if err := db.Exec(sql).Error; err != nil {
			return fmt.Errorf("v4 建表: %w", err)
		}
	}
	if err := migrateKnowledgeRows(db); err != nil {
		return err
	}
	if err := migratePendingKnowledge(db, selfID); err != nil {
		return err
	}
	// 暂存不再使用的画像数据，待部署备份与验收完成后再清理
	for _, sql := range []string{
		`ALTER TABLE member_traits RENAME TO legacy_member_traits`,
		`ALTER TABLE member_trait_evidence RENAME TO legacy_member_trait_evidence`,
		`DROP TABLE memory_evidence, jargon_evidence, style_pattern_evidence`,
		`DROP TABLE memories, jargons, style_patterns`,
		`DELETE FROM learning_states`,
		`ALTER TABLE learning_states DROP CONSTRAINT learning_states_pkey`,
		`ALTER TABLE learning_states DROP COLUMN kind`,
		`ALTER TABLE learning_states ADD PRIMARY KEY(group_id)`,
		`INSERT INTO learning_states(group_id,last_message_log_id) SELECT DISTINCT group_id,0 FROM message_logs`,
		`UPDATE topic_summaries SET summary_json=summary_json-'claims'`,
		`ALTER TABLE topic_summaries DROP COLUMN memory_processed`,
	} {
		if err := db.Exec(sql).Error; err != nil {
			return fmt.Errorf("v4 切换: %w", err)
		}
	}
	if err := db.Exec(`DO $$ DECLARE c record; BEGIN
		FOR c IN SELECT conname,conrelid::regclass AS owner FROM pg_constraint
		WHERE contype='f' AND conrelid=to_regclass('legacy_member_trait_evidence') AND confrelid='message_logs'::regclass
		LOOP EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I',c.owner,c.conname); END LOOP;
	END $$`).Error; err != nil {
		return fmt.Errorf("v4 清理遗留约束: %w", err)
	}
	if err := db.Exec(`UPDATE knowledge_items ki SET reviewed_through_id=0,updated_at=now()
		WHERE status='candidate' OR EXISTS(SELECT 1 FROM knowledge_relations kr WHERE kr.status='candidate' AND (kr.source_item_id=ki.id OR kr.target_item_id=ki.id))`).Error; err != nil {
		return fmt.Errorf("v4 重置候选进度: %w", err)
	}
	if err := db.Exec("ALTER TABLE topic_summaries ALTER COLUMN embedding DROP NOT NULL").Error; err != nil {
		return fmt.Errorf("v4 放宽摘要向量: %w", err)
	}
	if err := db.Exec(`INSERT INTO learning_states(group_id,last_message_log_id)
		SELECT group_id,0 FROM message_logs GROUP BY group_id ON CONFLICT DO NOTHING;
		UPDATE learning_states ls SET last_message_log_id=LEAST(ls.last_message_log_id,COALESCE((
		 SELECT min(ml.id)-1 FROM message_logs ml LEFT JOIN topic_assignments ta ON ta.message_log_id=ml.id
		 WHERE ml.group_id=ls.group_id AND ml.recalled_at IS NULL AND btrim(ml.text_content)<>''
		 AND (ta.id IS NULL OR (ta.topic_id IS NOT NULL AND ta.id>COALESCE((SELECT max(ts.through_topic_assignment_id) FROM topic_summaries ts JOIN topic_assignments old ON old.id=ts.through_topic_assignment_id WHERE old.topic_id=ta.topic_id),0)))
		),ls.last_message_log_id))`).Error; err != nil {
		return fmt.Errorf("v4 接续统一整理水位: %w", err)
	}
	return nil
}

type legacyKnowledgeRow struct {
	ID            uint
	GroupID       int64
	SubjectUserID int64
	Kind          string
	Label         string
	Content       string
	Status        string
	Embedding     *pgvector.Vector
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func migrateKnowledgeRows(db *gorm.DB) error {
	sources := []struct{ sql, evidence, key string }{
		{`SELECT id,group_id,subject_user_id,kind,'' label,content,status,embedding,created_at,updated_at FROM memories ORDER BY id`, "memory_evidence", "memory_id"},
		{`SELECT id,group_id,0 subject_user_id,'term' kind,term label,meaning content,status,NULL embedding,created_at,updated_at FROM jargons ORDER BY id`, "jargon_evidence", "jargon_id"},
		{`SELECT id,group_id,0 subject_user_id,'expression' kind,'' label,'在' || situation || '时：' || expression content,status,NULL embedding,created_at,updated_at FROM style_patterns ORDER BY id`, "style_pattern_evidence", "style_pattern_id"},
		{`SELECT mt.id,ml.group_id,mt.user_id subject_user_id,'alias' kind,mt.value label,mt.value content,'candidate' status,NULL embedding,mt.created_at,mt.updated_at FROM member_traits mt JOIN member_trait_evidence e ON e.member_trait_id=mt.id JOIN message_logs ml ON ml.id=e.message_log_id WHERE mt.kind='alias' GROUP BY mt.id,ml.group_id ORDER BY mt.id,ml.group_id`, "member_trait_evidence", "member_trait_id"},
	}
	var unknown int64
	if err := db.Raw(`SELECT count(*) FROM member_traits WHERE kind NOT IN ('alias','speaking','phrase')`).Scan(&unknown).Error; err != nil {
		return err
	}
	if unknown > 0 {
		return fmt.Errorf("%d 条未知成员特征，拒绝静默丢弃", unknown)
	}
	for _, source := range sources {
		var rows []legacyKnowledgeRow
		if err := db.Raw(source.sql).Scan(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			var messages []MessageLog
			if err := db.Table(source.evidence+" e").Select("ml.*").Joins("JOIN message_logs ml ON ml.id=e.message_log_id").Where("e."+source.key+"=? AND ml.group_id=?", row.ID, row.GroupID).Order("ml.id").Scan(&messages).Error; err != nil {
				return err
			}
			switch row.Status {
			case "rejected":
				row.Status = "archived"
			case "active", "candidate", "archived":
			default:
				return fmt.Errorf("未知旧知识状态 %s", row.Status)
			}
			if row.Status == "active" {
				if len(messages) == 0 || len(messages) > 16 {
					row.Status = "candidate"
				}
				for _, m := range messages {
					if m.RecalledAt != nil || strings.TrimSpace(m.TextContent) == "" {
						row.Status = "candidate"
					}
				}
			}
			item := KnowledgeItem{GroupID: row.GroupID, SubjectUserID: row.SubjectUserID, Kind: row.Kind, Label: strings.TrimSpace(row.Label), Content: strings.TrimSpace(row.Content), Status: row.Status, Embedding: row.Embedding, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
			if err := insertMigratedKnowledge(db, &item, messages); err != nil {
				return err
			}
		}
	}
	return nil
}

func insertMigratedKnowledge(db *gorm.DB, item *KnowledgeItem, messages []MessageLog) error {
	var existing KnowledgeItem
	err := db.Where("group_id=? AND subject_user_id=? AND kind=? AND lower(btrim(label))=lower(btrim(?)) AND lower(btrim(content))=lower(btrim(?))", item.GroupID, item.SubjectUserID, item.Kind, item.Label, item.Content).Order("id").Limit(1).Find(&existing).Error
	if err != nil {
		return err
	}
	if existing.ID > 0 {
		item.ID = existing.ID
		if item.Status == "active" || (item.Status == "candidate" && existing.Status == "archived") {
			if err := db.Model(&existing).Updates(map[string]any{"status": item.Status, "updated_at": gorm.Expr("GREATEST(updated_at,?)", item.UpdatedAt)}).Error; err != nil {
				return err
			}
		}
	} else if err := db.Create(item).Error; err != nil {
		return err
	}
	if len(messages) == 0 {
		return nil
	}
	set := KnowledgeEvidenceSet{ItemID: &item.ID}
	if err := db.Create(&set).Error; err != nil {
		return err
	}
	for _, msg := range messages {
		if msg.GroupID != item.GroupID {
			return fmt.Errorf("迁移证据跨群")
		}
		if err := db.Create(&KnowledgeEvidenceMessage{EvidenceSetID: set.ID, MessageLogID: msg.ID}).Error; err != nil {
			return err
		}
	}
	return nil
}

func migratePendingKnowledge(db *gorm.DB, selfID int64) error {
	var rows []struct {
		ID                       uint
		SummaryJSON              string
		ThroughTopicAssignmentID uint
		GroupID                  int64
		TopicID                  uint
	}
	if err := db.Raw(`SELECT ts.id,ts.summary_json,ts.through_topic_assignment_id,tt.group_id,tt.id topic_id FROM topic_summaries ts JOIN topic_assignments ta ON ta.id=ts.through_topic_assignment_id JOIN topic_threads tt ON tt.id=ta.topic_id WHERE NOT ts.memory_processed ORDER BY ts.id`).Scan(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		var summary struct {
			Claims []RawMemoryClaim `json:"claims"`
		}
		if err := sonic.UnmarshalString(row.SummaryJSON, &summary); err != nil {
			return fmt.Errorf("解析旧摘要 %d: %w", row.ID, err)
		}
		for _, raw := range summary.Claims {
			claim, err := NormalizeMemoryClaim(raw, selfID)
			if err != nil {
				return fmt.Errorf("迁移摘要 %d: %w", row.ID, err)
			}
			var messages []MessageLog
			if err := db.Table("message_logs ml").Select("ml.*").Joins("JOIN topic_assignments ta ON ta.message_log_id=ml.id").Where("ml.group_id=? AND ml.one_bot_message_id IN ? AND ta.topic_id=? AND ta.id<=?", row.GroupID, claim.EvidenceMessageIDs, row.TopicID, row.ThroughTopicAssignmentID).Order("ml.id").Scan(&messages).Error; err != nil {
				return err
			}
			if len(messages) != len(claim.EvidenceMessageIDs) {
				return fmt.Errorf("旧摘要 %d 证据缺失，迁移已回滚", row.ID)
			}
			item := KnowledgeItem{GroupID: row.GroupID, SubjectUserID: claim.SubjectUserID, Kind: string(claim.Kind), Content: claim.Content, Status: "candidate"}
			if err := insertMigratedKnowledge(db, &item, messages); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateV4Schema(db *gorm.DB, dimensions int) error {
	tables, err := currentTables(db)
	if err != nil {
		return err
	}
	for _, table := range []string{"message_logs", "topic_threads", "topic_assignments", "topic_summaries", "knowledge_items", "knowledge_relations", "knowledge_evidence_sets", "knowledge_evidence_messages", "group_agent_states", "learning_states", "member_profiles", "member_names", "stickers", "mood_state", "model_call_hourly"} {
		if !tables[table] {
			return fmt.Errorf("v4 缺少表 %s", table)
		}
	}
	for _, table := range []string{"memories", "memory_evidence", "jargons", "jargon_evidence", "style_patterns", "style_pattern_evidence", "member_traits", "member_trait_evidence"} {
		if tables[table] {
			return fmt.Errorf("v4 遗留运行时表 %s", table)
		}
	}
	for _, table := range []string{"knowledge_items", "topic_summaries"} {
		var typ string
		if err := db.Raw(`SELECT format_type(a.atttypid,a.atttypmod) FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() AND c.relname=? AND a.attname='embedding' AND NOT a.attisdropped`, table).Scan(&typ).Error; err != nil {
			return err
		}
		expected := fmt.Sprintf("vector(%d)", dimensions)
		if typ != expected && typ != "public."+expected {
			return fmt.Errorf("%s 向量维度不匹配: %s", table, typ)
		}
	}
	return nil
}
