package memory

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mumu-bot/internal/refstore"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

type Manager struct {
	db       *gorm.DB
	mediaDir string
	refs     *refstore.Store
}

func OpenDB(dsn string) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
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

func NewManager(db *gorm.DB, mediaDir string) (*Manager, error) {
	if db == nil {
		return nil, fmt.Errorf("PostgreSQL 未初始化")
	}
	return &Manager{db: db, mediaDir: mediaDir, refs: refstore.New(mediaDir)}, nil
}

// LatestMessageLogID 返回当前数据库全部原文的最大内部编号，作为本轮工具读取快照；
// 空库返回 0，调用方必须把它当成真实的空快照而不是“未设上限”
func (m *Manager) LatestMessageLogID(ctx context.Context) (uint, error) {
	var id uint
	err := m.db.WithContext(ctx).Model(&MessageLog{}).Select("COALESCE(MAX(id),0)").Scan(&id).Error
	return id, err
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
	profile := MemberProfile{UserID: userID, Nickname: nickname, UpdatedAt: seenAt}
	err := m.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"nickname":   gorm.Expr("CASE WHEN EXCLUDED.updated_at>=member_profiles.updated_at THEN EXCLUDED.nickname ELSE member_profiles.nickname END"),
			"updated_at": gorm.Expr("GREATEST(member_profiles.updated_at, EXCLUDED.updated_at)"),
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
	if m == nil {
		return nil
	}
	// 媒体目录要跟着轻量 Manager 一起走，否则工具回读大结果会落到相对路径
	return &Manager{db: m.db.WithContext(ctx), mediaDir: m.mediaDir, refs: m.refs}
}

// RefStore 返回 MediaDir 下的文件/图片快照仓库
func (m *Manager) RefStore() *refstore.Store {
	if m == nil {
		return nil
	}
	return m.refs
}

// TranscriptSnapshotRoots 返回现存 transcript 中所有 file_ref/image_ref 及 r 文本闭包内的引用
func (m *Manager) TranscriptSnapshotRoots(ctx context.Context) (map[string]bool, error) {
	roots := map[string]bool{}
	// 摘要、代头、系统提醒、记忆和日记不是快照生命周期根；只有当前对话输入和执行轨迹可以保活 ref
	rows, err := m.db.WithContext(ctx).Raw(`
		SELECT payload::text AS content FROM conversation_transcript
			WHERE entry_kind IN ('user','observation','agent_message','assistant','tool')
		UNION ALL SELECT result_ref FROM conversation_transcript
			WHERE result_ref<>'' AND entry_kind IN ('assistant','tool','user','observation','agent_message')`).Rows()
	if err != nil {
		return nil, err
	}
	toolRoots := make([]string, 0)
	seenTool := map[string]bool{}
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			rows.Close()
			return nil, err
		}
		for _, token := range refstore.ExtractRefs(text) {
			roots[token] = true
		}
		for _, ref := range toolResultRefPattern.FindAllString(text, -1) {
			if !seenTool[ref] {
				seenTool[ref] = true
				toolRoots = append(toolRoots, ref)
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	dir := filepath.Join(m.mediaDir, "tool-results")
	reachable, err := expandToolResultRefs(dir, toolRoots)
	if err != nil {
		return nil, err
	}
	for ref := range reachable {
		data, err := os.ReadFile(filepath.Join(dir, strings.TrimPrefix(ref, "r")+".txt"))
		if err != nil {
			continue
		}
		for _, token := range refstore.ExtractRefs(string(data)) {
			roots[token] = true
		}
	}
	return roots, nil
}

// CleanupSnapshotRefs 回收没有现存 transcript 根且超过宽限期的快照
func (m *Manager) CleanupSnapshotRefs(ctx context.Context, minAge time.Duration) (int, int, error) {
	if m == nil || m.refs == nil {
		return 0, 0, nil
	}
	roots, err := m.TranscriptSnapshotRoots(ctx)
	if err != nil {
		return 0, 0, err
	}
	return m.refs.Cleanup(roots, minAge)
}
