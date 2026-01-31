package memory

import (
	"amu-bot/internal/config"
	"amu-bot/internal/vector"
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// EmbeddingProvider 向量嵌入接口
type EmbeddingProvider interface {
	Embed(ctx context.Context, text string) ([]float64, error)
}

// Manager 记忆系统管理器
type Manager struct {
	db        *gorm.DB
	cfg       *config.Config
	embedding EmbeddingProvider
	milvus    *vector.MilvusClient // Milvus 向量存储
}

// NewManager 创建记忆管理器
func NewManager(cfg *config.Config, embedding EmbeddingProvider) (*Manager, error) {
	// 构建 MySQL DSN
	mysqlCfg := cfg.Memory.MySQL
	if mysqlCfg.Host == "" {
		mysqlCfg.Host = "127.0.0.1"
	}
	if mysqlCfg.Port == 0 {
		mysqlCfg.Port = 3306
	}
	if mysqlCfg.DBName == "" {
		mysqlCfg.DBName = "amu_bot"
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		mysqlCfg.User,
		mysqlCfg.Password,
		mysqlCfg.Host,
		mysqlCfg.Port,
		mysqlCfg.DBName,
	)

	db, err := gorm.Open(mysql.Open(dsn))
	if err != nil {
		return nil, fmt.Errorf("连接 MySQL 数据库失败: %w", err)
	}

	// 迁移所有表
	if err := db.AutoMigrate(
		&Memory{},
		&MemberProfile{},
		&Expression{},
		&Jargon{},
		&MessageLog{},
	); err != nil {
		return nil, fmt.Errorf("数据库迁移失败: %w", err)
	}

	// 初始化 Milvus 向量存储
	var milvusClient *vector.MilvusClient
	if cfg.Memory.Milvus.Enabled && embedding != nil {
		milvusCfg := &vector.MilvusConfig{
			Address:        cfg.Memory.Milvus.Address,
			DBName:         cfg.Memory.Milvus.DBName,
			CollectionName: cfg.Memory.Milvus.CollectionName,
			VectorDim:      cfg.Memory.Milvus.VectorDim,
			MetricType:     cfg.Memory.Milvus.MetricType,
		}
		milvusClient, err = vector.NewMilvusClient(milvusCfg)
		if err != nil {
			// Milvus 连接失败不影响整体运行，但向量检索功能将不可用
			zap.L().Warn("Milvus连接失败，向量检索功能将不可用", zap.Error(err))
		} else {
			zap.L().Info("Milvus向量存储已连接")
		}
	}

	m := &Manager{
		db:        db,
		cfg:       cfg,
		embedding: embedding,
		milvus:    milvusClient,
	}

	go m.cleanupLoop()
	return m, nil
}

// ==================== 短期记忆 ====================

// AddMessage 添加消息到短期记忆
func (m *Manager) AddMessage(msg MessageLog) error {
	return m.db.Create(&msg).Error
}

// GetRecentMessages 获取最近的消息记录
func (m *Manager) GetRecentMessages(groupID int64, limit, offset int) []MessageLog {
	var dbMsgs []MessageLog
	q := m.db.Where("group_id = ?", groupID).Order("created_at DESC").Limit(limit)
	if offset > 0 {
		q = q.Offset(offset)
	}
	q.Find(&dbMsgs)

	// 反转，按时间正序排列
	for i, j := 0, len(dbMsgs)-1; i < j; i, j = i+1, j-1 {
		dbMsgs[i], dbMsgs[j] = dbMsgs[j], dbMsgs[i]
	}
	return dbMsgs
}

// ==================== 长期记忆 ====================

// SaveMemory 保存长期记忆
func (m *Manager) SaveMemory(ctx context.Context, mem *Memory) error {
	// 生成 embedding
	var embedding []float64
	if m.embedding != nil {
		if emb, err := m.embedding.Embed(ctx, mem.Content); err == nil {
			embedding = emb
		}
	}

	mem.LastAccess = time.Now()

	// 保存到 MySQL
	if err := m.db.Save(mem).Error; err != nil {
		return err
	}

	// 保存向量到 Milvus
	if m.milvus != nil && len(embedding) > 0 {
		if _, err := m.milvus.Insert(ctx, mem.ID, mem.GroupID, string(mem.Type), embedding); err != nil {
			// 向量插入失败只记录日志，不影响主流程
			zap.L().Warn("Milvus插入向量失败", zap.Error(err))
		}
	}

	return nil
}

// QueryMemory 查询相关记忆
func (m *Manager) QueryMemory(ctx context.Context, query string, groupID int64, memType MemoryType, limit int) ([]Memory, error) {
	// 尝试 Milvus 向量搜索
	if m.milvus != nil && m.embedding != nil {
		if emb, err := m.embedding.Embed(ctx, query); err == nil {
			if results, err := m.milvusVectorSearch(ctx, emb, groupID, memType, limit); err == nil && len(results) > 0 {
				return results, nil
			}
		}
	}

	// 回退到关键词搜索
	var memories []Memory
	q := m.db.Where("group_id = ?", groupID)
	if memType != "" {
		q = q.Where("type = ?", memType)
	}
	err := q.Where("content LIKE ? OR keywords LIKE ?",
		"%"+query+"%", "%"+query+"%").
		Order("importance DESC, updated_at DESC").
		Limit(limit).
		Find(&memories).Error
	return memories, err
}

// milvusVectorSearch 使用 Milvus 进行向量搜索
func (m *Manager) milvusVectorSearch(ctx context.Context, queryEmb []float64, groupID int64, memType MemoryType, limit int) ([]Memory, error) {
	if m.milvus == nil {
		return nil, errors.New("Milvus 未初始化")
	}

	// 在 Milvus 中搜索
	results, err := m.milvus.Search(ctx, queryEmb, groupID, string(memType), limit, m.cfg.Memory.LongTerm.SimilarityThreshold)
	if err != nil {
		return nil, err
	}

	if len(results) == 0 {
		return nil, nil
	}

	// 获取对应的记忆
	memoryIDs := make([]uint, 0, len(results))
	for _, r := range results {
		memoryIDs = append(memoryIDs, r.MemoryID)
	}

	var memories []Memory
	if err := m.db.Where("id IN ?", memoryIDs).Find(&memories).Error; err != nil {
		return nil, err
	}

	// 更新访问计数
	for _, mem := range memories {
		m.db.Model(&mem).Updates(map[string]any{
			"access_count": gorm.Expr("access_count + 1"),
			"last_access":  time.Now(),
		})
	}

	// 按照搜索结果的顺序排序
	memoryMap := make(map[uint]Memory)
	for _, mem := range memories {
		memoryMap[mem.ID] = mem
	}

	sortedMemories := make([]Memory, 0, len(results))
	for _, r := range results {
		if mem, ok := memoryMap[r.MemoryID]; ok {
			sortedMemories = append(sortedMemories, mem)
		}
	}

	return sortedMemories, nil
}

// GetMemoriesByType 按类型获取记忆
func (m *Manager) GetMemoriesByType(groupID int64, memType MemoryType, limit int) ([]Memory, error) {
	var memories []Memory
	err := m.db.Where("group_id = ? AND type = ?", groupID, memType).
		Order("importance DESC, updated_at DESC").Limit(limit).Find(&memories).Error
	return memories, err
}

// ==================== 表达学习 ====================

// GetExpressions 获取表达方式（已验证的优先）
func (m *Manager) GetExpressions(groupID int64, limit int) ([]Expression, error) {
	var expressions []Expression
	err := m.db.Where("group_id = ? AND rejected = ?", groupID, false).
		Order("count DESC, updated_at DESC").Limit(limit).Find(&expressions).Error
	return expressions, err
}

// SaveExpression 保存表达方式
func (m *Manager) SaveExpression(exp *Expression) error {
	return m.db.Save(exp).Error
}

// ==================== 黑话管理 ====================

// GetJargons 获取黑话列表
func (m *Manager) GetJargons(groupID int64, limit int) ([]Jargon, error) {
	var jargons []Jargon
	err := m.db.Where("group_id = ?", groupID).
		Order("count DESC").Limit(limit).Find(&jargons).Error
	return jargons, err
}

// SaveJargon 保存黑话/术语
func (m *Manager) SaveJargon(jargon *Jargon) error {
	var existing Jargon
	err := m.db.Where("group_id = ? AND content = ?", jargon.GroupID, jargon.Content).First(&existing).Error

	if err == gorm.ErrRecordNotFound {
		return m.db.Create(jargon).Error
	} else if err != nil {
		return err
	}

	// 已存在，更新含义和上下文，增加计数
	return m.db.Model(&existing).Updates(map[string]any{
		"count":   gorm.Expr("count + 1"),
		"meaning": jargon.Meaning,
		"context": jargon.Context,
	}).Error
}

// ==================== 成员画像 ====================

// GetMemberProfile 获取成员画像
func (m *Manager) GetMemberProfile(groupID, userID int64) (*MemberProfile, error) {
	var profile MemberProfile
	err := m.db.Where("group_id = ? AND user_id = ?", groupID, userID).First(&profile).Error
	if err != nil {
		return nil, err
	}
	return &profile, nil
}

// GetOrCreateMemberProfile 获取或创建成员画像
func (m *Manager) GetOrCreateMemberProfile(groupID, userID int64, nickname string) (*MemberProfile, error) {
	var profile MemberProfile
	err := m.db.Where("group_id = ? AND user_id = ?", groupID, userID).First(&profile).Error

	if err == gorm.ErrRecordNotFound {
		profile = MemberProfile{
			GroupID:   groupID,
			UserID:    userID,
			Nickname:  nickname,
			LastSpeak: time.Now(),
		}
		if err := m.db.Create(&profile).Error; err != nil {
			return nil, err
		}
		return &profile, nil
	}
	return &profile, err
}

// UpdateMemberProfile 更新成员画像
func (m *Manager) UpdateMemberProfile(profile *MemberProfile) error {
	return m.db.Save(profile).Error
}

// ==================== 统计 ====================

// GetStats 获取统计信息
func (m *Manager) GetStats() map[string]int64 {
	stats := make(map[string]int64)
	var memories, members, messages, expressions, jargons int64
	m.db.Model(&Memory{}).Count(&memories)
	m.db.Model(&MemberProfile{}).Count(&members)
	m.db.Model(&MessageLog{}).Count(&messages)
	m.db.Model(&Expression{}).Count(&expressions)
	m.db.Model(&Jargon{}).Count(&jargons)
	stats["memories"] = memories
	stats["members"] = members
	stats["messages"] = messages
	stats["expressions"] = expressions
	stats["jargons"] = jargons
	return stats
}

// ==================== 列表查询（供管理界面用）====================

func (m *Manager) ListMemories(groupID int64, memType string, page, pageSize int) ([]Memory, int64, error) {
	var items []Memory
	var total int64

	q := m.db.Model(&Memory{})
	if groupID > 0 {
		q = q.Where("group_id = ?", groupID)
	}
	if memType != "" {
		q = q.Where("type = ?", memType)
	}
	q.Count(&total)

	err := q.Order("updated_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error
	return items, total, err
}

func (m *Manager) ListMemberProfiles(groupID int64, page, pageSize int) ([]MemberProfile, int64, error) {
	var items []MemberProfile
	var total int64

	q := m.db.Model(&MemberProfile{})
	if groupID > 0 {
		q = q.Where("group_id = ?", groupID)
	}
	q.Count(&total)

	err := q.Order("msg_count DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error
	return items, total, err
}

func (m *Manager) ListMessageLogs(groupID int64, page, pageSize int) ([]MessageLog, int64, error) {
	var items []MessageLog
	var total int64

	q := m.db.Model(&MessageLog{})
	if groupID > 0 {
		q = q.Where("group_id = ?", groupID)
	}
	q.Count(&total)

	err := q.Order("created_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error
	return items, total, err
}

// ==================== 清理 ====================

func (m *Manager) cleanupLoop() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		// 清理7天前未概括的消息
		m.db.Where("created_at < ? AND summarized = ?", time.Now().Add(-7*24*time.Hour), false).
			Delete(&MessageLog{})
	}
}

func (m *Manager) Close() error {
	// 关闭 Milvus 连接
	if m.milvus != nil {
		_ = m.milvus.Close()
	}
	// 关闭 MySQL 连接
	if sqlDB, err := m.db.DB(); err == nil {
		return sqlDB.Close()
	}
	return nil
}

func (m *Manager) GetDB() *gorm.DB { return m.db }
