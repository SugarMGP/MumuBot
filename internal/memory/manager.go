package memory

import (
	"context"
	"errors"
	"fmt"
	"mumu-bot/internal/config"
	"mumu-bot/internal/utils"
	"mumu-bot/internal/vector"
	"strings"
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
	db          *gorm.DB
	cfg         *config.Config
	embedding   EmbeddingProvider
	milvus      *vector.MilvusClient // Milvus 向量存储
	cleanupStop chan struct{}
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
		mysqlCfg.DBName = "mumu_bot"
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
		&Sticker{},
		&MoodState{},
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
			zap.L().Warn("Milvus 连接失败，向量检索功能将不可用", zap.Error(err))
		} else {
			zap.L().Info("Milvus 向量存储已连接")
		}
	}

	m := &Manager{
		db:          db,
		cfg:         cfg,
		embedding:   embedding,
		milvus:      milvusClient,
		cleanupStop: make(chan struct{}),
	}

	// 启动消息日志清理任务
	m.startMessageLogCleanup()

	// 启动情绪衰减任务
	m.startMoodDecay()

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

	// 保存到 MySQL
	if err := m.db.Save(mem).Error; err != nil {
		return err
	}

	// 保存向量到 Milvus
	if m.milvus != nil && len(embedding) > 0 {
		if _, err := m.milvus.Insert(ctx, mem.ID, mem.GroupID, string(mem.Type), embedding); err != nil {
			// 向量插入失败只记录日志，不影响主流程
			zap.L().Warn("Milvus 插入向量失败", zap.Error(err))
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
	q := m.db.Model(&Memory{})
	if groupID != 0 {
		q = q.Where("group_id = ?", groupID)
	}
	if memType != "" {
		q = q.Where("type = ?", memType)
	}
	keywords := strings.Fields(query)
	if len(keywords) == 0 {
		return memories, nil
	}
	likeConditions := make([]string, 0, len(keywords))
	args := make([]interface{}, 0, len(keywords))
	for _, kw := range keywords {
		likeConditions = append(likeConditions, "content LIKE ?")
		args = append(args, "%"+kw+"%")
	}
	err := q.Where(strings.Join(likeConditions, " OR "), args...).
		Order("importance DESC, updated_at DESC").
		Limit(limit).
		Find(&memories).Error
	if err != nil {
		return memories, err
	}

	if len(memories) > 0 {
		memoryIDs := make([]uint, 0, len(memories))
		for _, mem := range memories {
			memoryIDs = append(memoryIDs, mem.ID)
		}
		_ = m.db.Model(&Memory{}).Where("id IN ?", memoryIDs).Updates(map[string]any{
			"access_count": gorm.Expr("access_count + 1"),
		}).Error
	}

	return memories, nil
}

// startMessageLogCleanup 启动消息日志清理定时任务
func (m *Manager) startMessageLogCleanup() {
	if m == nil || m.cfg == nil {
		return
	}

	cleanupCfg := m.cfg.Memory.MessageLogCleanup
	enabled := true
	if cleanupCfg.Enabled != nil {
		enabled = *cleanupCfg.Enabled
	}
	if !enabled {
		return
	}

	intervalHours := cleanupCfg.IntervalHours
	if intervalHours <= 0 {
		intervalHours = 6
	}
	keepLatest := cleanupCfg.KeepLatest
	if keepLatest <= 0 {
		keepLatest = 500
	}

	// 启动后立即清理一次
	go m.cleanupMessageLogs(keepLatest)

	ticker := time.NewTicker(time.Duration(intervalHours) * time.Hour)
	go func() {
		for {
			select {
			case <-ticker.C:
				m.cleanupMessageLogs(keepLatest)
			case <-m.cleanupStop:
				ticker.Stop()
				return
			}
		}
	}()
}

// cleanupMessageLogs 清理消息日志，仅保留每个群最新的 keepLatest 条
func (m *Manager) cleanupMessageLogs(keepLatest int) {
	if keepLatest <= 0 {
		return
	}

	var groupIDs []int64
	if err := m.db.Model(&MessageLog{}).Distinct("group_id").Pluck("group_id", &groupIDs).Error; err != nil {
		zap.L().Warn("清理消息日志失败：获取群列表失败", zap.Error(err))
		return
	}

	for _, groupID := range groupIDs {
		var keepIDs []uint
		if err := m.db.Model(&MessageLog{}).
			Where("group_id = ?", groupID).
			Order("created_at DESC").
			Limit(keepLatest).
			Pluck("id", &keepIDs).Error; err != nil {
			zap.L().Warn("清理消息日志失败：获取保留ID失败", zap.Int64("group_id", groupID), zap.Error(err))
			continue
		}
		if len(keepIDs) == 0 {
			continue
		}

		result := m.db.Where("group_id = ? AND id NOT IN ?", groupID, keepIDs).Delete(&MessageLog{})
		if result.Error != nil {
			zap.L().Warn("清理消息日志失败：删除旧记录失败", zap.Int64("group_id", groupID), zap.Error(result.Error))
			continue
		}
		if result.RowsAffected > 0 {
			zap.L().Info("消息日志已清理", zap.Int64("group_id", groupID), zap.Int("deleted", int(result.RowsAffected)))
		}
	}
}

// milvusVectorSearch 使用 Milvus 进行向量搜索
func (m *Manager) milvusVectorSearch(ctx context.Context, queryEmb []float64, groupID int64, memType MemoryType, limit int) ([]Memory, error) {
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

// ==================== 表达学习 ====================

// SaveExpression 保存表达方式
func (m *Manager) SaveExpression(exp *Expression) (bool, error) {
	if exp == nil {
		return false, nil
	}

	if exp.GroupID != 0 && exp.Situation != "" && exp.Style != "" {
		var existing Expression
		err := m.db.Where("group_id = ? AND situation = ? AND style = ?", exp.GroupID, exp.Situation, exp.Style).
			First(&existing).Error
		if err == nil {
			if existing.Examples == "" && exp.Examples != "" {
				if err := m.db.Model(&existing).Updates(map[string]any{
					"examples": exp.Examples,
				}).Error; err != nil {
					return false, err
				}
				return true, nil
			}
			return false, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return false, err
		}
	}

	if err := m.db.Create(exp).Error; err != nil {
		return false, err
	}
	return true, nil
}

// SearchExpressions 搜索表达方式（关键词匹配）
func (m *Manager) SearchExpressions(groupID int64, keyword string, limit int) ([]Expression, error) {
	var expressions []Expression
	q := m.db.Model(&Expression{}).
		Where("group_id = ? AND rejected = ?", groupID, false)

	if keyword != "" {
		keywords := strings.Fields(keyword)
		if len(keywords) > 0 {
			likeConditions := make([]string, 0, len(keywords))
			args := make([]interface{}, 0, len(keywords))
			for _, kw := range keywords {
				likeConditions = append(likeConditions, "situation LIKE ? OR style LIKE ? OR examples LIKE ?")
				args = append(args, "%"+kw+"%", "%"+kw+"%", "%"+kw+"%")
			}
			q = q.Where(strings.Join(likeConditions, " OR "), args...)
		}
	}

	err := q.Order("checked DESC, updated_at DESC").Limit(limit).Find(&expressions).Error
	return expressions, err
}

// ReviewExpression 审核表达方式
func (m *Manager) ReviewExpression(id uint, approve bool) error {
	updates := map[string]any{
		"checked": true,
	}
	if approve {
		updates["rejected"] = false
	} else {
		updates["rejected"] = true
	}
	return m.db.Model(&Expression{}).Where("id = ?", id).Updates(updates).Error
}

// GetUncheckedExpressions 获取待审核的表达方式
func (m *Manager) GetUncheckedExpressions(groupID int64, limit int) ([]Expression, error) {
	var expressions []Expression
	err := m.db.Where("group_id = ? AND checked = ?", groupID, false).
		Limit(limit).Find(&expressions).Error
	return expressions, err
}

// ==================== 黑话管理 ====================

// SearchJargons 搜索黑话（通过关键词匹配，本群优先）
func (m *Manager) SearchJargons(groupID int64, keyword string, limit int) ([]Jargon, error) {
	var jargons []Jargon
	q := m.db.Model(&Jargon{})

	// 使用 strings.Fields 切割关键词，挨个模糊匹配
	if keyword != "" {
		keywords := strings.Fields(keyword)
		if len(keywords) > 0 {
			likeConditions := make([]string, 0, len(keywords))
			args := make([]interface{}, 0, len(keywords))
			for _, kw := range keywords {
				likeConditions = append(likeConditions, "content LIKE ?")
				args = append(args, "%"+kw+"%")
			}
			q = q.Where(strings.Join(likeConditions, " OR "), args...)
		}
	}

	// 本群优先排序：本群的排在前面，然后按 verified 降序
	err := q.Order(fmt.Sprintf("CASE WHEN group_id = %d THEN 0 ELSE 1 END, verified DESC", groupID)).
		Limit(limit).Find(&jargons).Error
	return jargons, err
}

// SaveJargon 保存黑话/术语
func (m *Manager) SaveJargon(jargon *Jargon) error {
	var existing Jargon
	err := m.db.Where("group_id = ? AND content = ?", jargon.GroupID, jargon.Content).First(&existing).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return m.db.Create(jargon).Error
	} else if err != nil {
		return err
	}

	updates := map[string]any{
		"meaning": jargon.Meaning,
		"context": jargon.Context,
	}
	return m.db.Model(&existing).Updates(updates).Error
}

// ReviewJargon 审核黑话
func (m *Manager) ReviewJargon(id uint, approve bool) error {
	updates := map[string]any{
		"verified": approve,
	}
	return m.db.Model(&Jargon{}).Where("id = ?", id).Updates(updates).Error
}

// GetUnverifiedJargons 获取待审核的黑话
func (m *Manager) GetUnverifiedJargons(groupID int64, limit int) ([]Jargon, error) {
	var jargons []Jargon
	err := m.db.Where("group_id = ? AND verified = ?", groupID, false).
		Limit(limit).Find(&jargons).Error
	return jargons, err
}

// ==================== 成员画像 ====================

// GetMemberProfile 获取成员画像
func (m *Manager) GetMemberProfile(userID int64) (*MemberProfile, error) {
	var profile MemberProfile
	err := m.db.Where("user_id = ?", userID).First(&profile).Error
	if err != nil {
		return nil, err
	}
	return &profile, nil
}

// GetOrCreateMemberProfile 获取或创建成员画像
func (m *Manager) GetOrCreateMemberProfile(userID int64, nickname string) (*MemberProfile, error) {
	var profile MemberProfile
	err := m.db.Where("user_id = ?", userID).First(&profile).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		profile = MemberProfile{
			UserID:    userID,
			Nickname:  nickname,
			Activity:  0.5, // 初始活跃度
			Intimacy:  0.3, // 初始亲密度
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
	// 计算活跃度：基于最近发言时间和消息数量
	// 活跃度衰减：每天降低0.1，最低0.1
	daysSinceLastSpeak := time.Since(profile.LastSpeak).Hours() / 24
	if daysSinceLastSpeak > 0 {
		profile.Activity -= 0.1 * daysSinceLastSpeak
		if profile.Activity < 0.1 {
			profile.Activity = 0.1
		}
	}
	// 发言增加活跃度
	if time.Since(profile.LastSpeak) < time.Hour {
		profile.Activity += 0.05
		if profile.Activity > 1.0 {
			profile.Activity = 1.0
		}
	}
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

// GetMessageLogByID 根据消息ID获取消息日志
func (m *Manager) GetMessageLogByID(messageID string) (*MessageLog, error) {
	var log MessageLog
	err := m.db.Where("message_id = ?", messageID).First(&log).Error
	if err != nil {
		return nil, err
	}
	return &log, nil
}

// Close 关闭连接
func (m *Manager) Close() error {
	// 停止清理任务
	if m.cleanupStop != nil {
		close(m.cleanupStop)
		m.cleanupStop = nil
	}
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

// ==================== 表情包管理 ====================

// SaveSticker 保存表情包（通过哈希去重）
func (m *Manager) SaveSticker(sticker *Sticker) (bool, error) {
	// 先检查哈希是否已存在
	var existing Sticker
	err := m.db.Where("file_hash = ?", sticker.FileHash).First(&existing).Error
	if err == nil {
		// 已存在，返回重复标记
		return true, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}

	// 不存在，创建新记录
	if err := m.db.Create(sticker).Error; err != nil {
		return false, err
	}
	return false, nil
}

// GetStickerByID 根据ID获取表情包
func (m *Manager) GetStickerByID(id uint) (*Sticker, error) {
	var sticker Sticker
	err := m.db.First(&sticker, id).Error
	if err != nil {
		return nil, err
	}
	return &sticker, nil
}

// SearchStickers 搜索表情包
func (m *Manager) SearchStickers(keyword string, limit int) ([]Sticker, error) {
	var stickers []Sticker
	q := m.db.Model(&Sticker{})
	if keyword != "" {
		keywords := strings.Fields(keyword)
		likeConditions := make([]string, 0, len(keywords))
		args := make([]interface{}, 0, len(keywords))
		for _, kw := range keywords {
			likeConditions = append(likeConditions, "description LIKE ?")
			args = append(args, "%"+kw+"%")
		}
		q = q.Where(strings.Join(likeConditions, " OR "), args...)
	}
	err := q.Order("use_count DESC, updated_at DESC").Limit(limit).Find(&stickers).Error
	return stickers, err
}

// UpdateStickerUsage 更新表情包使用记录
func (m *Manager) UpdateStickerUsage(id uint) error {
	return m.db.Model(&Sticker{}).Where("id = ?", id).Updates(map[string]any{
		"use_count": gorm.Expr("use_count + 1"),
	}).Error
}

// GetStickerByHash 通过哈希获取表情包
func (m *Manager) GetStickerByHash(hash string) (*Sticker, error) {
	var sticker Sticker
	err := m.db.Where("file_hash = ?", hash).First(&sticker).Error
	if err != nil {
		return nil, err
	}
	return &sticker, nil
}

// ==================== 情绪状态管理 ====================

// startMoodDecay 启动情绪衰减定时任务（每分钟执行一次）
func (m *Manager) startMoodDecay() {
	ticker := time.NewTicker(1 * time.Minute)
	go func() {
		for {
			select {
			case <-ticker.C:
				if err := m.ApplyMoodDecay(); err != nil {
					zap.L().Warn("情绪衰减失败", zap.Error(err))
				}
			case <-m.cleanupStop:
				ticker.Stop()
				return
			}
		}
	}()
	zap.L().Info("情绪衰减任务已启动")
}

// GetMoodState 获取当前情绪状态
func (m *Manager) GetMoodState() (*MoodState, error) {
	var mood MoodState
	err := m.db.First(&mood).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// 不存在则创建默认情绪
		mood = MoodState{
			Valence:     0.0,
			Energy:      0.5,
			Sociability: 0.5,
		}
		if err := m.db.Create(&mood).Error; err != nil {
			return nil, err
		}
		return &mood, nil
	}
	if err != nil {
		return nil, err
	}
	return &mood, nil
}

// UpdateMoodState 更新情绪状态（增量更新）
func (m *Manager) UpdateMoodState(valenceDelta, energyDelta, sociabilityDelta float64, reason string) (*MoodState, error) {
	mood, err := m.GetMoodState()
	if err != nil {
		return nil, err
	}

	// 应用增量
	mood.Valence = utils.ClampFloat64(mood.Valence+valenceDelta, -1.0, 1.0)
	mood.Energy = utils.ClampFloat64(mood.Energy+energyDelta, 0.0, 1.0)
	mood.Sociability = utils.ClampFloat64(mood.Sociability+sociabilityDelta, 0.0, 1.0)
	mood.LastReason = reason

	if err := m.db.Save(mood).Error; err != nil {
		return nil, err
	}
	return mood, nil
}

// ApplyMoodDecay 应用情绪自然衰减
func (m *Manager) ApplyMoodDecay() error {
	mood, err := m.GetMoodState()
	if err != nil {
		return err
	}

	// 衰减公式：
	// valence *= 0.95 (向0衰减)
	// energy += (0.5 - energy) * 0.05 (向0.5衰减)
	// sociability += (0.5 - sociability) * 0.05 (向0.5衰减)
	mood.Valence *= 0.95
	mood.Energy += (0.5 - mood.Energy) * 0.05
	mood.Sociability += (0.5 - mood.Sociability) * 0.05

	return m.db.Save(mood).Error
}
