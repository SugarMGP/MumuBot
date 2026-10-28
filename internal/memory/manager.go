package memory

import (
	"context"
	"fmt"
	"log"
	"os"
	"slices"
	"time"

	"mumu-bot/internal/config"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

type Manager struct {
	db *gorm.DB
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

func NewManager(db *gorm.DB) (*Manager, error) {
	if db == nil {
		return nil, fmt.Errorf("PostgreSQL 未初始化")
	}
	return &Manager{db: db}, nil
}

func (m *Manager) GetRecentMessagesScope(ctx context.Context, kind string, targetID, throughOneBotMessageID int64, limit, offset int) ([]MessageLog, error) {
	var items []MessageLog
	q := m.db.WithContext(ctx).Where("conversation_kind=? AND target_id=?", kind, targetID).Order("id DESC").Limit(limit)
	if throughOneBotMessageID != 0 {
		upperBound := m.db.Model(&MessageLog{}).Select("id").Where("conversation_kind=? AND target_id=? AND one_bot_message_id=?", kind, targetID, throughOneBotMessageID)
		q = q.Where("message_logs.id<=(?)", upperBound)
	}
	if offset > 0 {
		q = q.Offset(offset)
	}
	if err := q.Find(&items).Error; err != nil {
		return nil, err
	}
	slices.Reverse(items)
	return items, nil
}

func (m *Manager) GetMessageCountByTime(groupID, userID int64, start time.Time) (int64, error) {
	var count int64
	err := m.db.Model(&MessageLog{}).Where("conversation_kind=? AND target_id=? AND user_id=? AND message_time>=?", ConversationKindGroup, groupID, userID, start).Count(&count).Error
	return count, err
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
	if err := m.db.First(&profile, "user_id=?", userID).Error; err != nil {
		return nil, err
	}
	return &profile, nil
}

func (m *Manager) GetOrCreateMemberProfile(userID int64, nickname string, seenAt time.Time) (*MemberProfile, error) {
	profile := MemberProfile{UserID: userID, Nickname: nickname, LastSeenAt: seenAt, MessageCount: 1}
	err := m.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"nickname":      gorm.Expr("CASE WHEN EXCLUDED.last_seen_at>=member_profiles.last_seen_at THEN EXCLUDED.nickname ELSE member_profiles.nickname END"),
			"last_seen_at":  gorm.Expr("GREATEST(member_profiles.last_seen_at,EXCLUDED.last_seen_at)"),
			"message_count": gorm.Expr("member_profiles.message_count+1"),
		}),
	}, clause.Returning{}).Create(&profile).Error
	if err != nil {
		return nil, err
	}
	return &profile, nil
}

func (m *Manager) GetMessageLogByScope(kind string, targetID, messageID int64) (*MessageLog, error) {
	var item MessageLog
	if kind == "" {
		kind = ConversationKindGroup
	}
	if err := m.db.Where("conversation_kind=? AND target_id=? AND one_bot_message_id=?", kind, targetID, messageID).First(&item).Error; err != nil {
		return nil, err
	}
	return &item, nil
}

func (m *Manager) SchemaVersion(ctx context.Context) (int, error) {
	var version int
	err := m.db.WithContext(ctx).Model(&SchemaMigration{}).Select("COALESCE(max(version),0)").Scan(&version).Error
	return version, err
}

// MarkMessageRecalledScope 只标记撤回，不改写已经进入上下文的历史原文
func (m *Manager) MarkMessageRecalledScope(kind string, targetID, messageID int64) (*MessageLog, bool, error) {
	if kind == "" {
		kind = ConversationKindGroup
	}
	if targetID <= 0 || messageID == 0 {
		return nil, false, nil
	}
	var item MessageLog
	changed := false
	err := m.db.Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("conversation_kind=? AND target_id=? AND one_bot_message_id=?", kind, targetID, messageID).Take(&item)
		if result.Error == gorm.ErrRecordNotFound {
			return nil
		}
		if result.Error != nil {
			return result.Error
		}
		if item.RecalledAt != nil {
			return nil
		}
		now := time.Now()
		if err := tx.Model(&item).Update("recalled_at", now).Error; err != nil {
			return err
		}
		item.RecalledAt = &now
		changed = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if item.ID == 0 {
		return nil, false, nil
	}
	return &item, changed, nil
}

func (m *Manager) Close() error {
	sqlDB, err := m.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func (m *Manager) GetDB() *gorm.DB { return m.db }

func (m *Manager) GetMessageLogByInternalID(id uint) (*MessageLog, error) {
	var item MessageLog
	if err := m.db.Where("id=?", id).First(&item).Error; err != nil {
		return nil, err
	}
	return &item, nil
}

// WithContext 返回绑定请求上下文的轻量管理器
func (m *Manager) WithContext(ctx context.Context) *Manager {
	return &Manager{db: m.db.WithContext(ctx)}
}
