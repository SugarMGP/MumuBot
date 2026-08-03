package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"mumu-bot/internal/config"

	"github.com/cloudwego/eino/components/model"
	pgvector "github.com/pgvector/pgvector-go"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type EmbeddingProvider interface {
	Embed(ctx context.Context, text string) ([]float64, error)
}

type MemoryIngestInput struct {
	GroupID           int64
	RelatedUserID     int64
	SelfID            int64
	Content           string
	MessageLogID      *uint
	TopicSummaryID    *uint
	SubjectCandidates []TopicParticipantRef
	AllowedKinds      []MemoryKind
}

type TopicMemoryCandidateInput struct {
	GroupID        int64
	TopicSummaryID uint
	SelfID         int64
	Claims         []TopicMemoryClaimInput
	Participants   []TopicParticipantRef
}

type TopicMemoryClaimInput struct {
	Content      string
	AllowedKinds []MemoryKind
}

type memoryMergeInput struct {
	Incoming   Memory
	Candidates []Memory
}

type memoryMergeDecision struct {
	ShouldMerge   bool
	TargetID      uint
	MergeIDs      []uint
	MergedContent string
}

type Manager struct {
	db          *gorm.DB
	embedding   EmbeddingProvider
	claimModel  model.BaseChatModel
	cleanupStop chan struct{}
	stopOnce    sync.Once
	background  sync.WaitGroup
}

func NewManager(embedding EmbeddingProvider, claimModel model.ToolCallingChatModel) (*Manager, error) {
	if embedding == nil {
		return nil, fmt.Errorf("embedding 未初始化")
	}
	if claimModel == nil {
		return nil, fmt.Errorf("claimModel 未初始化")
	}
	cfg := config.Get()
	db, err := gorm.Open(postgres.Open(cfg.Database.DSN))
	if err != nil {
		return nil, fmt.Errorf("连接 PostgreSQL 数据库失败: %w", err)
	}
	if err := ensureExtensions(db); err != nil {
		return nil, err
	}
	if err := migrateSchema(db, cfg.Embedding.Dimensions); err != nil {
		return nil, fmt.Errorf("初始化 PostgreSQL 数据结构失败: %w", err)
	}
	m := &Manager{db: db, embedding: embedding, claimModel: claimModel, cleanupStop: make(chan struct{})}
	m.startMessageLogCleanup()
	m.startMoodDecay()
	return m, nil
}

func ensureExtensions(db *gorm.DB) error {
	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
		return fmt.Errorf("启用 PostgreSQL 扩展 vector 失败，请确认服务端已安装扩展且运行账户有创建权限: %w", err)
	}
	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS pg_trgm").Error; err != nil {
		return fmt.Errorf("启用 PostgreSQL 扩展 pg_trgm 失败，请确认运行账户有创建权限: %w", err)
	}
	return nil
}

func migrateSchema(db *gorm.DB, dimensions int) error {
	models := []any{
		&MessageLog{}, &TopicThread{}, &TopicAssignment{}, &TopicSummaryRecord{},
		&Memory{}, &MemoryEvidence{}, &StylePattern{}, &StylePatternEvidence{},
		&Jargon{}, &JargonEvidence{}, &MemberProfile{}, &MemberName{}, &MemberTrait{},
		&MemberTraitEvidence{}, &LearningState{}, &Sticker{}, &MoodState{},
	}
	if err := db.AutoMigrate(models...); err != nil {
		return err
	}
	for _, table := range []string{"memories", "topic_summaries", "style_patterns"} {
		stmt := fmt.Sprintf("ALTER TABLE %s ALTER COLUMN embedding TYPE vector(%d)", table, dimensions)
		if err := db.Exec(stmt).Error; err != nil {
			return err
		}
	}
	statements := []string{
		`ALTER TABLE topic_assignments DROP CONSTRAINT IF EXISTS fk_topic_assignments_message`,
		`ALTER TABLE topic_assignments ADD CONSTRAINT fk_topic_assignments_message FOREIGN KEY (message_log_id) REFERENCES message_logs(id) ON DELETE CASCADE`,
		`ALTER TABLE topic_assignments DROP CONSTRAINT IF EXISTS fk_topic_assignments_topic`,
		`ALTER TABLE topic_assignments ADD CONSTRAINT fk_topic_assignments_topic FOREIGN KEY (topic_id) REFERENCES topic_threads(id) ON DELETE RESTRICT`,
		`ALTER TABLE topic_summaries DROP CONSTRAINT IF EXISTS fk_topic_summaries_assignment`,
		`ALTER TABLE topic_summaries ADD CONSTRAINT fk_topic_summaries_assignment FOREIGN KEY (through_topic_assignment_id) REFERENCES topic_assignments(id) ON DELETE RESTRICT`,
		`ALTER TABLE memories DROP CONSTRAINT IF EXISTS memories_scope_subject_check`,
		`ALTER TABLE memories ADD CONSTRAINT memories_scope_subject_check CHECK ((scope = 'member' AND subject_user_id IS NOT NULL) OR (scope <> 'member' AND subject_user_id IS NULL))`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_active_memory_content ON memories (group_id, scope, COALESCE(subject_user_id, 0), kind, lower(btrim(content))) WHERE status = 'active'`,
		`ALTER TABLE memory_evidence DROP CONSTRAINT IF EXISTS memory_evidence_source_check`,
		`ALTER TABLE memory_evidence ADD CONSTRAINT memory_evidence_source_check CHECK ((message_log_id IS NOT NULL)::int + (topic_summary_id IS NOT NULL)::int = 1)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_memory_message_evidence ON memory_evidence(memory_id, message_log_id) WHERE message_log_id IS NOT NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_memory_summary_evidence ON memory_evidence(memory_id, topic_summary_id) WHERE topic_summary_id IS NOT NULL`,
		`ALTER TABLE memory_evidence DROP CONSTRAINT IF EXISTS fk_memory_evidence_memory`,
		`ALTER TABLE memory_evidence ADD CONSTRAINT fk_memory_evidence_memory FOREIGN KEY (memory_id) REFERENCES memories(id) ON DELETE CASCADE`,
		`ALTER TABLE memory_evidence DROP CONSTRAINT IF EXISTS fk_memory_evidence_message`,
		`ALTER TABLE memory_evidence ADD CONSTRAINT fk_memory_evidence_message FOREIGN KEY (message_log_id) REFERENCES message_logs(id) ON DELETE RESTRICT`,
		`ALTER TABLE memory_evidence DROP CONSTRAINT IF EXISTS fk_memory_evidence_summary`,
		`ALTER TABLE memory_evidence ADD CONSTRAINT fk_memory_evidence_summary FOREIGN KEY (topic_summary_id) REFERENCES topic_summaries(id) ON DELETE RESTRICT`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_style_pattern_text ON style_patterns(group_id, lower(btrim(situation)), lower(btrim(expression)))`,
		`ALTER TABLE style_pattern_evidence DROP CONSTRAINT IF EXISTS fk_style_evidence_pattern`,
		`ALTER TABLE style_pattern_evidence ADD CONSTRAINT fk_style_evidence_pattern FOREIGN KEY (style_pattern_id) REFERENCES style_patterns(id) ON DELETE CASCADE`,
		`ALTER TABLE style_pattern_evidence DROP CONSTRAINT IF EXISTS fk_style_evidence_message`,
		`ALTER TABLE style_pattern_evidence ADD CONSTRAINT fk_style_evidence_message FOREIGN KEY (message_log_id) REFERENCES message_logs(id) ON DELETE RESTRICT`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_jargon_term ON jargons(group_id, lower(btrim(term)))`,
		`ALTER TABLE jargon_evidence DROP CONSTRAINT IF EXISTS fk_jargon_evidence_jargon`,
		`ALTER TABLE jargon_evidence ADD CONSTRAINT fk_jargon_evidence_jargon FOREIGN KEY (jargon_id) REFERENCES jargons(id) ON DELETE CASCADE`,
		`ALTER TABLE jargon_evidence DROP CONSTRAINT IF EXISTS fk_jargon_evidence_message`,
		`ALTER TABLE jargon_evidence ADD CONSTRAINT fk_jargon_evidence_message FOREIGN KEY (message_log_id) REFERENCES message_logs(id) ON DELETE RESTRICT`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_member_trait ON member_traits(user_id, kind, lower(btrim(value)))`,
		`ALTER TABLE member_names DROP CONSTRAINT IF EXISTS fk_member_names_profile`,
		`ALTER TABLE member_names ADD CONSTRAINT fk_member_names_profile FOREIGN KEY (user_id) REFERENCES member_profiles(user_id) ON DELETE CASCADE`,
		`ALTER TABLE member_traits DROP CONSTRAINT IF EXISTS fk_member_traits_profile`,
		`ALTER TABLE member_traits ADD CONSTRAINT fk_member_traits_profile FOREIGN KEY (user_id) REFERENCES member_profiles(user_id) ON DELETE CASCADE`,
		`ALTER TABLE member_trait_evidence DROP CONSTRAINT IF EXISTS fk_member_trait_evidence_trait`,
		`ALTER TABLE member_trait_evidence ADD CONSTRAINT fk_member_trait_evidence_trait FOREIGN KEY (member_trait_id) REFERENCES member_traits(id) ON DELETE CASCADE`,
		`ALTER TABLE member_trait_evidence DROP CONSTRAINT IF EXISTS fk_member_trait_evidence_message`,
		`ALTER TABLE member_trait_evidence ADD CONSTRAINT fk_member_trait_evidence_message FOREIGN KEY (message_log_id) REFERENCES message_logs(id) ON DELETE RESTRICT`,
	}
	for _, stmt := range statements {
		if err := db.Exec(stmt).Error; err != nil {
			return err
		}
	}
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(&MoodState{ID: 1, Energy: 0.5, Sociability: 0.5}).Error
}

func EmbeddingVector(values []float64) (pgvector.Vector, error) {
	expected := config.Get().Embedding.Dimensions
	if len(values) != expected {
		return pgvector.Vector{}, fmt.Errorf("embedding 维度不匹配: got %d, want %d", len(values), expected)
	}
	result := make([]float32, len(values))
	for i, value := range values {
		result[i] = float32(value)
	}
	return pgvector.NewVector(result), nil
}

func (m *Manager) GetRecentMessages(groupID, throughOneBotMessageID int64, limit, offset int) []MessageLog {
	var items []MessageLog
	q := m.db.Where("group_id = ?", groupID).Order("message_time DESC, id DESC").Limit(limit)
	if throughOneBotMessageID > 0 {
		upperBound := m.db.Model(&MessageLog{}).Select("id").
			Where("group_id = ? AND one_bot_message_id = ?", groupID, throughOneBotMessageID)
		q = q.Where("message_logs.id <= (?)", upperBound)
	}
	if offset > 0 {
		q = q.Offset(offset)
	}
	if err := q.Find(&items).Error; err != nil {
		zap.L().Warn("读取最近消息失败", zap.Int64("group_id", groupID), zap.Error(err))
		return nil
	}
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return items
}

func (m *Manager) GetMessageCountByTime(groupID, userID int64, start time.Time) (int64, error) {
	var count int64
	err := m.db.Model(&MessageLog{}).Where("group_id = ? AND user_id = ? AND message_time >= ?", groupID, userID, start).Count(&count).Error
	return count, err
}

type rankedID struct {
	ID   uint
	Rank int
}

type rankedIDRow struct{ ID uint }

func rankRows(rows []rankedIDRow) []rankedID {
	result := make([]rankedID, len(rows))
	for i, row := range rows {
		result[i] = rankedID{ID: row.ID, Rank: i + 1}
	}
	return result
}

func fuseRRF(lists ...[]rankedID) []uint {
	scores := make(map[uint]float64)
	for _, list := range lists {
		for _, item := range list {
			scores[item.ID] += 1 / float64(60+item.Rank)
		}
	}
	ids := make([]uint, 0, len(scores))
	for id := range scores {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if scores[ids[i]] == scores[ids[j]] {
			return ids[i] < ids[j]
		}
		return scores[ids[i]] > scores[ids[j]]
	})
	return ids
}

func (m *Manager) SearchSimilarMemories(ctx context.Context, text string, groupID int64, scope MemoryScope, limit int, vectorThreshold float64) ([]Memory, error) {
	text = strings.TrimSpace(text)
	if text == "" || limit <= 0 {
		return nil, nil
	}
	embedding, err := m.embedding.Embed(ctx, text)
	if err != nil {
		return nil, err
	}
	base := "status = 'active'"
	args := []any{}
	if groupID != 0 {
		base += " AND group_id = ?"
		args = append(args, groupID)
	}
	if scope != "" {
		base += " AND scope = ?"
		args = append(args, scope)
	}
	var vectorRows []rankedIDRow
	vectorSQL := "SELECT id FROM memories WHERE " + base + " AND 1 - (embedding <=> ?) >= ? ORDER BY embedding <=> ? LIMIT 20"
	vector, err := EmbeddingVector(embedding)
	if err != nil {
		return nil, err
	}
	vectorArgs := append(append([]any{}, args...), vector, vectorThreshold, vector)
	if err := m.db.WithContext(ctx).Raw(vectorSQL, vectorArgs...).Scan(&vectorRows).Error; err != nil {
		return nil, err
	}
	var textRows []rankedIDRow
	textSQL := "SELECT id FROM memories WHERE " + base + " AND similarity(content, ?) >= 0.1 ORDER BY similarity(content, ?) DESC LIMIT 20"
	textArgs := append(append([]any{}, args...), text, text)
	if err := m.db.WithContext(ctx).Raw(textSQL, textArgs...).Scan(&textRows).Error; err != nil {
		return nil, err
	}
	ids := fuseRRF(rankRows(vectorRows), rankRows(textRows))
	if len(ids) > limit {
		ids = ids[:limit]
	}
	return m.loadMemoriesInOrder(ctx, ids)
}

func (m *Manager) loadMemoriesInOrder(ctx context.Context, ids []uint) ([]Memory, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []Memory
	if err := m.db.WithContext(ctx).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	byID := make(map[uint]Memory, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	result := make([]Memory, 0, len(rows))
	for _, id := range ids {
		if row, ok := byID[id]; ok {
			result = append(result, row)
		}
	}
	return result, nil
}

func (m *Manager) QueryMemory(ctx context.Context, query string, groupID int64, scope MemoryScope, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 10
	}
	return m.SearchSimilarMemories(ctx, query, groupID, scope, limit, 0.3)
}

func (m *Manager) DeleteMemory(ctx context.Context, id uint) error {
	return requireAffected(m.db.WithContext(ctx).Delete(&Memory{}, id))
}

func (m *Manager) ArchiveMemory(ctx context.Context, id uint) error {
	return requireAffected(m.db.WithContext(ctx).Model(&Memory{}).Where("id = ?", id).Update("status", MemoryStatusArchived))
}

func (m *Manager) RestoreMemoryToCandidate(ctx context.Context, id uint) error {
	return requireAffected(m.db.WithContext(ctx).Model(&Memory{}).Where("id = ?", id).Update("status", MemoryStatusCandidate))
}

func (m *Manager) UpdateStylePatternStatus(ctx context.Context, id uint, status StylePatternStatus) error {
	return requireAffected(m.db.WithContext(ctx).Model(&StylePattern{}).Where("id = ?", id).Update("status", status))
}

func (m *Manager) UpdateJargonStatus(ctx context.Context, id uint, status CultureStatus) error {
	return requireAffected(m.db.WithContext(ctx).Model(&Jargon{}).Where("id = ?", id).Update("status", status))
}

func requireAffected(result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (m *Manager) UpsertTopicMemoryCandidate(ctx context.Context, input TopicMemoryCandidateInput) ([]Memory, error) {
	if input.TopicSummaryID == 0 {
		return nil, nil
	}
	preparedItems := make([]preparedMemory, 0, len(input.Claims))
	seen := make(map[string]struct{})
	for _, claim := range input.Claims {
		content := strings.TrimSpace(strings.TrimLeft(claim.Content, "-•*1234567890. "))
		if content == "" {
			continue
		}
		key := NormalizeContent(content) + "|" + allowedKindsPrompt(claim.AllowedKinds)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		prepared, err := m.prepareMemory(ctx, MemoryIngestInput{
			GroupID: input.GroupID, SelfID: input.SelfID, Content: content,
			TopicSummaryID: &input.TopicSummaryID, SubjectCandidates: input.Participants, AllowedKinds: claim.AllowedKinds,
		})
		if err != nil {
			return nil, err
		}
		if prepared != nil {
			preparedItems = append(preparedItems, *prepared)
		}
	}
	result := make([]Memory, 0, len(preparedItems))
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, prepared := range preparedItems {
			item, _, err := applyPreparedMemory(tx, MemoryIngestInput{TopicSummaryID: &input.TopicSummaryID}, prepared)
			if err != nil {
				return err
			}
			result = append(result, *item)
		}
		return tx.Model(&TopicSummaryRecord{}).Where("id = ? AND memory_processed = false", input.TopicSummaryID).
			Update("memory_processed", true).Error
	})
	return result, err
}

func (m *Manager) GetMemberProfile(userID int64) (*MemberProfile, error) {
	var profile MemberProfile
	if err := m.db.First(&profile, "user_id = ?", userID).Error; err != nil {
		return nil, err
	}
	return &profile, nil
}

func (m *Manager) GetOrCreateMemberProfile(userID int64, nickname string, seenAt time.Time) (*MemberProfile, error) {
	profile := MemberProfile{UserID: userID, Nickname: nickname, LastSeenAt: seenAt, MessageCount: 1}
	err := m.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"nickname":      gorm.Expr("CASE WHEN EXCLUDED.last_seen_at >= member_profiles.last_seen_at THEN EXCLUDED.nickname ELSE member_profiles.nickname END"),
			"last_seen_at":  gorm.Expr("GREATEST(member_profiles.last_seen_at, EXCLUDED.last_seen_at)"),
			"message_count": gorm.Expr("member_profiles.message_count + 1"),
		}),
	}).Create(&profile).Error
	if err != nil {
		return nil, err
	}
	return m.GetMemberProfile(userID)
}

func (m *Manager) GetMessageLogByID(messageID int64) (*MessageLog, error) {
	var item MessageLog
	if err := m.db.Where("one_bot_message_id = ?", messageID).First(&item).Error; err != nil {
		return nil, err
	}
	return &item, nil
}

func (m *Manager) MarkMessageRecalled(groupID, messageID int64) (*MessageLog, bool, error) {
	if groupID <= 0 || messageID <= 0 {
		return nil, false, nil
	}
	var item MessageLog
	err := m.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&item).Clauses(clause.Returning{}).
			Where("group_id = ? AND one_bot_message_id = ? AND recalled_at IS NULL", groupID, messageID).
			Updates(map[string]any{
				"recalled_at":         time.Now(),
				"text_content":        "",
				"display_content":     "[该消息已撤回]\n",
				"forward_payload":     nil,
				"reply_to_message_id": nil,
				"is_mentioned":        false,
			})
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&TopicAssignment{MessageLogID: item.ID}).Error
	})
	if err != nil {
		return nil, false, err
	}
	if item.ID == 0 {
		return nil, false, nil
	}
	return &item, true, nil
}

func (m *Manager) Close() error {
	m.stopOnce.Do(func() { close(m.cleanupStop) })
	m.background.Wait()
	sqlDB, err := m.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func (m *Manager) GetDB() *gorm.DB { return m.db }

func (m *Manager) EmbeddingProvider() EmbeddingProvider { return m.embedding }

func createEvidence(tx *gorm.DB, memoryID uint, input MemoryIngestInput) error {
	evidence := MemoryEvidence{MemoryID: memoryID, MessageLogID: input.MessageLogID, TopicSummaryID: input.TopicSummaryID}
	if (evidence.MessageLogID == nil) == (evidence.TopicSummaryID == nil) {
		return errors.New("长期记忆证据必须且只能有一个来源")
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&evidence).Error
}
