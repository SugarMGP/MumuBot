package memory

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"sync"
	"time"

	"mumu-bot/internal/config"

	pgvector "github.com/pgvector/pgvector-go"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

type EmbeddingProvider interface {
	Embed(ctx context.Context, text string) ([]float64, error)
}

type StoreClaimsContext struct {
	GroupID                 int64
	SelfID                  int64
	SnapshotOneBotMessageID int64
}

type Manager struct {
	db          *gorm.DB
	embedding   EmbeddingProvider
	cleanupStop chan struct{}
	stopOnce    sync.Once
	background  sync.WaitGroup
}

func OpenDB() (*gorm.DB, error) {
	cfg := config.Get()
	db, err := gorm.Open(postgres.Open(cfg.Database.DSN), &gorm.Config{
		Logger: gormlogger.New(log.New(os.Stdout, "\r\n", log.LstdFlags), gormlogger.Config{
			SlowThreshold:             200 * time.Millisecond,
			IgnoreRecordNotFoundError: true,
			LogLevel:                  gormlogger.Warn,
		}),
	})
	if err != nil {
		return nil, fmt.Errorf("连接 PostgreSQL 数据库失败: %w", err)
	}
	return db, nil
}

func NewManager(db *gorm.DB, embedding EmbeddingProvider) (*Manager, error) {
	if db == nil {
		return nil, fmt.Errorf("PostgreSQL 未初始化")
	}
	if embedding == nil {
		return nil, fmt.Errorf("embedding 未初始化")
	}
	m := &Manager{db: db, embedding: embedding, cleanupStop: make(chan struct{})}
	m.startMessageLogCleanup()
	m.startMoodDecay()
	return m, nil
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
	if throughOneBotMessageID != 0 {
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

func requireAffected(result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
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

func (m *Manager) GetMessageLogByID(groupID, messageID int64) (*MessageLog, error) {
	var item MessageLog
	if err := m.db.Where("group_id=? AND one_bot_message_id = ?", groupID, messageID).First(&item).Error; err != nil {
		return nil, err
	}
	return &item, nil
}

func SubjectLabel(subjectUserID, selfID int64) string {
	switch {
	case subjectUserID == 0:
		return "group"
	case selfID > 0 && subjectUserID == selfID:
		return "self"
	default:
		return "member"
	}
}

func (m *Manager) SchemaVersion(ctx context.Context) (int, error) {
	var version int
	err := m.db.WithContext(ctx).Model(&SchemaMigration{}).Select("COALESCE(max(version),0)").Scan(&version).Error
	return version, err
}

func (m *Manager) MarkMessageRecalled(groupID, messageID int64) (*MessageLog, bool, error) {
	if groupID <= 0 || messageID == 0 {
		return nil, false, nil
	}
	var item MessageLog
	err := m.db.Transaction(func(tx *gorm.DB) error {
		if err := LockKnowledgeGroup(tx, groupID); err != nil {
			return err
		}
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("group_id = ? AND one_bot_message_id = ? AND recalled_at IS NULL", groupID, messageID).
			First(&item)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil
		}
		if result.Error != nil {
			return result.Error
		}

		recalledAt := time.Now()
		displayContent := RecalledMessageDisplayContent
		if err := tx.Model(&item).Updates(map[string]any{
			"recalled_at":         recalledAt,
			"text_content":        "",
			"display_content":     displayContent,
			"reply_to_message_id": nil,
			"is_mentioned":        false,
		}).Error; err != nil {
			return err
		}
		item.RecalledAt = &recalledAt
		item.TextContent = ""
		item.DisplayContent = displayContent
		item.ReplyToMessageID = nil
		item.IsMentioned = false
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&TopicAssignment{MessageLogID: item.ID}).Error; err != nil {
			return err
		}
		return InvalidateKnowledgeEvidence(tx, groupID)
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
