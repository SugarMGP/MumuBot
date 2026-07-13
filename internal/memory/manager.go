package memory

import (
	"context"
	"errors"
	"fmt"
	"mumu-bot/internal/config"
	"mumu-bot/internal/vector"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

type MemoryIngestInput struct {
	GroupID               int64
	RelatedUserID         int64
	SelfID                int64
	Content               string
	SourceKind            MemorySourceKind
	SourceRef             string
	SubjectCandidates     []TopicParticipantRef
	AllowedCanonicalTypes []CanonicalMemoryType
}

type TopicMemoryCandidateInput struct {
	GroupID               int64
	TopicID               uint
	SelfID                int64
	Claims                []string
	Participants          []TopicParticipantRef
	AllowedCanonicalTypes []CanonicalMemoryType
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

// EmbeddingProvider 向量嵌入接口
type EmbeddingProvider interface {
	Embed(ctx context.Context, text string) ([]float64, error)
}

type VectorStore interface {
	Upsert(ctx context.Context, memoryID uint, groupID int64, memType string, embedding []float64) (int64, error)
	Search(ctx context.Context, embedding []float64, groupID int64, memType string, topK int, threshold float64) ([]vector.SearchResult, error)
	Delete(ctx context.Context, memoryIDs []uint) error
	Close() error
}

type MemoryCandidateWriter interface {
	UpsertTopicMemoryCandidate(ctx context.Context, input TopicMemoryCandidateInput) ([]Memory, error)
}

// Manager 记忆系统管理器
type Manager struct {
	db              *gorm.DB
	embedding       EmbeddingProvider
	claimModel      model.BaseChatModel
	milvus          VectorStore // Memory 向量存储
	styleCardMilvus VectorStore // StyleCard 向量存储
	topicMilvus     VectorStore // Topic 摘要向量存储
	cleanupStop     chan struct{}
}

// NewManager 创建记忆管理器
func NewManager(embedding EmbeddingProvider, claimModel model.ToolCallingChatModel) (*Manager, error) {
	if embedding == nil {
		return nil, fmt.Errorf("embedding 未初始化，Milvus 为强制依赖")
	}
	if claimModel == nil {
		return nil, fmt.Errorf("claimModel 未初始化")
	}

	// 构建 MySQL DSN
	cfg := config.Get()
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
		&StyleCard{},
		&Jargon{},
		&MessageLog{},
		&TopicThread{},
		&Sticker{},
		&MoodState{},
		&LearningState{},
	); err != nil {
		return nil, fmt.Errorf("数据库迁移失败: %w", err)
	}
	milvusCfg := &vector.MilvusConfig{
		Address:        cfg.Memory.Milvus.Address,
		DBName:         cfg.Memory.Milvus.DBName,
		CollectionName: cfg.Memory.Milvus.CollectionName,
		VectorDim:      cfg.Memory.Milvus.VectorDim,
		MetricType:     cfg.Memory.Milvus.MetricType,
	}
	milvusClient, err := vector.NewMilvusClient(milvusCfg)
	if err != nil {
		return nil, fmt.Errorf("连接记忆 Milvus 失败: %w", err)
	}

	styleMilvusCfg := &vector.MilvusConfig{
		Address:        cfg.Memory.Milvus.Address,
		DBName:         cfg.Memory.Milvus.DBName,
		CollectionName: styleCardCollectionName(cfg.Memory.Milvus.CollectionName),
		VectorDim:      cfg.Memory.Milvus.VectorDim,
		MetricType:     cfg.Memory.Milvus.MetricType,
	}
	styleMilvusClient, err := vector.NewMilvusClient(styleMilvusCfg)
	if err != nil {
		_ = milvusClient.Close()
		return nil, fmt.Errorf("连接风格卡片 Milvus 失败: %w", err)
	}

	topicMilvusCfg := &vector.MilvusConfig{
		Address:        cfg.Memory.Milvus.Address,
		DBName:         cfg.Memory.Milvus.DBName,
		CollectionName: topicSummaryCollectionName(cfg.Memory.Milvus.CollectionName),
		VectorDim:      cfg.Memory.Milvus.VectorDim,
		MetricType:     cfg.Memory.Milvus.MetricType,
	}
	topicMilvusClient, err := vector.NewMilvusClient(topicMilvusCfg)
	if err != nil {
		_ = styleMilvusClient.Close()
		_ = milvusClient.Close()
		return nil, fmt.Errorf("连接话题摘要 Milvus 失败: %w", err)
	}

	zap.L().Info("Milvus 向量存储已连接",
		zap.String("memory_collection", milvusCfg.CollectionName),
		zap.String("style_card_collection", styleMilvusCfg.CollectionName),
		zap.String("topic_summary_collection", topicMilvusCfg.CollectionName))

	m := &Manager{
		db:              db,
		embedding:       embedding,
		claimModel:      claimModel,
		milvus:          milvusClient,
		styleCardMilvus: styleMilvusClient,
		topicMilvus:     topicMilvusClient,
		cleanupStop:     make(chan struct{}),
	}
	// 启动消息日志清理任务
	m.startMessageLogCleanup()

	// 启动情绪衰减任务
	m.startMoodDecay()

	return m, nil
}

// ==================== 短期记忆 ====================

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

func (m *Manager) GetProcessableLearningMessages(groupID int64, selfID int64, lastID uint, limit int) ([]MessageLog, error) {
	var dbMsgs []MessageLog
	err := m.db.Where("group_id = ? AND id > ? AND user_id != ?", groupID, lastID, selfID).
		Order("id ASC").Limit(limit).Find(&dbMsgs).Error
	if err != nil || len(dbMsgs) == 0 {
		return dbMsgs, err
	}

	ready := make([]MessageLog, 0, len(dbMsgs))
	for _, msg := range dbMsgs {
		if msg.TopicThreadID == 0 && strings.TrimSpace(msg.TopicMatchReason) == "" {
			break
		}
		ready = append(ready, msg)
	}
	return ready, nil
}

// GetMessageCountByTime 获取指定用户在指定群组一段时间内的消息数量
func (m *Manager) GetMessageCountByTime(groupID, userID int64, startTime time.Time) (int64, error) {
	var count int64
	err := m.db.Model(&MessageLog{}).
		Where("group_id = ? AND user_id = ? AND created_at >= ?", groupID, userID, startTime).
		Count(&count).Error
	return count, err
}

// ==================== 长期记忆 ====================

// SearchSimilarMemories 按群和记忆类型搜索相似记忆
func (m *Manager) SearchSimilarMemories(ctx context.Context, text string, groupID int64, memType MemoryType, limit int, threshold float64) ([]Memory, error) {
	if m.milvus == nil || m.embedding == nil {
		return nil, errors.New("向量检索未启用")
	}
	if limit <= 0 {
		limit = 15
	}

	emb, err := m.embedding.Embed(ctx, text)
	if err != nil {
		return nil, err
	}

	results, err := m.milvusVectorSearch(ctx, emb, groupID, string(memType), limit, threshold)
	if err != nil {
		return nil, err
	}
	memories := prioritizeRecallMemories(results, limit)
	if len(memories) < limit {
		exclude := memoryIDSet(memories)
		extra, err := m.keywordSearchMemories(ctx, text, groupID, memType, limit-len(memories), exclude)
		if err != nil {
			return nil, err
		}
		memories = append(memories, extra...)
	}
	return memories, nil
}

// DeleteMemory 删除记忆
func (m *Manager) DeleteMemory(ctx context.Context, id uint) error {
	if id == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var old Memory
	if err := m.db.WithContext(ctx).First(&old, id).Error; err != nil {
		return err
	}
	if err := m.db.WithContext(ctx).Delete(&Memory{}, id).Error; err != nil {
		return err
	}
	if err := m.deleteMemoryVector(ctx, id); err != nil {
		m.restoreDeletedMemoryRowsBestEffort(ctx, old)
		return err
	}
	return nil
}

func (m *Manager) updateMemoryFields(ctx context.Context, id uint, modifier func(*Memory)) (*Memory, error) {
	if id == 0 {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var old Memory
	if err := m.db.WithContext(ctx).First(&old, id).Error; err != nil {
		return nil, err
	}
	next := old
	modifier(&next)
	prepared, err := m.prepareMemoryVector(ctx, next)
	if err != nil {
		return nil, err
	}
	var updated Memory
	err = m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&next).Error; err != nil {
			return err
		}
		updated = next
		return nil
	})
	if err != nil {
		return nil, err
	}
	if prepared != nil {
		prepared.memory = updated
	}
	if err := m.upsertMemoryVector(ctx, prepared); err != nil {
		m.restoreExistingMemoryRowBestEffort(ctx, old)
		m.restoreMemoryVectorsBestEffort(ctx, old)
		return nil, err
	}
	return &updated, nil
}

func (m *Manager) ArchiveMemory(ctx context.Context, id uint) error {
	_, err := m.updateMemoryFields(ctx, id, func(mem *Memory) {
		mem.Status = MemoryStatusArchived
		mem.UpdatedAt = time.Now()
	})
	return err
}

func (m *Manager) RestoreMemoryToCandidate(ctx context.Context, id uint) error {
	_, err := m.updateMemoryFields(ctx, id, func(mem *Memory) {
		mem.Status = MemoryStatusCandidate
		mem.UpdatedAt = time.Now()
	})
	return err
}

// SaveMemory 保存长期记忆
func (m *Manager) SaveMemory(ctx context.Context, mem *Memory) error {
	if mem == nil {
		return nil
	}
	mem.Content = strings.TrimSpace(mem.Content)
	if mem.Content == "" {
		return fmt.Errorf("记忆内容不能为空")
	}
	if mem.EvidenceCount <= 0 {
		mem.EvidenceCount = 1
	}
	if mem.Status == "" {
		mem.Status = MemoryStatusActive
	}
	if mem.CanonicalType == "" {
		return fmt.Errorf("记忆规范类型不能为空")
	}
	if mem.Importance <= 0 {
		mem.Importance = importanceForStatus(mem.CanonicalType, mem.Status, mem.EvidenceCount)
	}

	if mem.ID == 0 {
		created, err := m.createMemoryWithVector(ctx, mem)
		if err != nil {
			return err
		}
		if created != nil {
			*mem = *created
		}
		return nil
	}
	updated, err := m.updateMemoryFields(ctx, mem.ID, func(existing *Memory) {
		existing.Type = mem.Type
		existing.GroupID = mem.GroupID
		existing.UserID = mem.UserID
		existing.Content = mem.Content
		existing.Importance = mem.Importance
		existing.AccessCount = mem.AccessCount
		existing.CanonicalType = mem.CanonicalType
		existing.Status = mem.Status
		existing.EvidenceCount = mem.EvidenceCount
		existing.SourceKind = mem.SourceKind
		existing.SourceRef = mem.SourceRef
		existing.FactKey = mem.FactKey
	})
	if err != nil {
		return err
	}
	if updated != nil {
		*mem = *updated
	}
	return nil
}

func (m *Manager) UpsertTopicMemoryCandidate(ctx context.Context, input TopicMemoryCandidateInput) ([]Memory, error) {
	if input.TopicID == 0 || input.GroupID == 0 || len(input.Claims) == 0 {
		return nil, nil
	}

	created := make([]Memory, 0, len(input.Claims))
	seen := make(map[string]struct{}, len(input.Claims))
	for idx, line := range input.Claims {
		line = strings.TrimSpace(strings.TrimLeft(line, "-•*1234567890. "))
		if line == "" {
			continue
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}

		mem, action, err := m.IngestMemory(ctx, MemoryIngestInput{
			GroupID:               input.GroupID,
			SelfID:                input.SelfID,
			Content:               line,
			SourceKind:            MemorySourceKindTopic,
			SourceRef:             fmt.Sprintf("topic:%d", input.TopicID),
			SubjectCandidates:     input.Participants,
			AllowedCanonicalTypes: input.AllowedCanonicalTypes,
		})
		if err != nil {
			return nil, err
		}
		if mem == nil {
			continue
		}
		if action != "ignored" {
			created = append(created, *mem)
		}
		if idx >= 11 {
			break
		}
	}
	return created, nil
}

// QueryMemory 查询相关记忆
func (m *Manager) QueryMemory(ctx context.Context, query string, groupID int64, memType MemoryType, limit int) ([]Memory, error) {
	if limit <= 0 {
		limit = 10
	}
	var memories []Memory
	// 尝试 Milvus 向量搜索
	if m.milvus != nil && m.embedding != nil {
		if emb, err := m.embedding.Embed(ctx, query); err == nil {
			if results, err := m.milvusVectorSearch(ctx, emb, groupID, string(memType), limit, 0.7); err == nil && len(results) > 0 {
				memories = prioritizeRecallMemories(results, limit)
			}
		}
	}
	if len(memories) >= limit {
		return memories, nil
	}

	extra, err := m.keywordSearchMemories(ctx, query, groupID, memType, limit-len(memories), memoryIDSet(memories))
	if err != nil {
		return memories, err
	}
	memories = append(memories, extra...)
	return prioritizeRecallMemories(memories, limit), nil
}

func (m *Manager) keywordSearchMemories(ctx context.Context, query string, groupID int64, memType MemoryType, limit int, exclude map[uint]struct{}) ([]Memory, error) {
	var memories []Memory
	if limit <= 0 {
		return memories, nil
	}
	q := m.db.Model(&Memory{})
	if groupID != 0 {
		q = q.Where("group_id = ?", groupID)
	}
	if memType != "" {
		q = q.Where("type = ?", memType)
	}
	q = q.Where("(status = ? OR status = ? OR status = '')", MemoryStatusActive, MemoryStatusLegacy)
	if len(exclude) > 0 {
		ids := make([]uint, 0, len(exclude))
		for id := range exclude {
			ids = append(ids, id)
		}
		q = q.Where("id NOT IN ?", ids)
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
	err := q.WithContext(ctx).Where(strings.Join(likeConditions, " OR "), args...).
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
		_ = m.db.WithContext(ctx).Model(&Memory{}).Where("id IN ?", memoryIDs).Updates(map[string]any{
			"access_count": gorm.Expr("access_count + 1"),
		}).Error
	}

	return prioritizeRecallMemories(memories, limit), nil
}

// milvusVectorSearch 使用 Milvus 进行向量搜索并返回完整的 Memory 对象
func (m *Manager) milvusVectorSearch(ctx context.Context, queryEmb []float64, groupID int64, memType string, limit int, threshold float64) ([]Memory, error) {
	searchLimit := limit * 3
	if searchLimit < limit+20 {
		searchLimit = limit + 20
	}
	results, err := m.milvus.Search(ctx, queryEmb, groupID, memType, searchLimit, threshold)
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
	if err := m.db.Where("id IN ? AND (status = ? OR status = ? OR status = '')", memoryIDs, MemoryStatusActive, MemoryStatusLegacy).Find(&memories).Error; err != nil {
		return nil, err
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
			if len(sortedMemories) >= limit {
				break
			}
		}
	}
	if len(sortedMemories) > 0 {
		memoryIDs = memoryIDs[:0]
		for _, mem := range sortedMemories {
			memoryIDs = append(memoryIDs, mem.ID)
		}
		_ = m.db.WithContext(ctx).Model(&Memory{}).Where("id IN ?", memoryIDs).Updates(map[string]any{
			"access_count": gorm.Expr("access_count + 1"),
		}).Error
	}

	return sortedMemories, nil
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
	var existing MemberProfile
	if err := m.db.Where("user_id = ?", profile.UserID).First(&existing).Error; err != nil {
		return err
	}

	profile.Activity = applyActivityUpdate(profile.Activity, existing.LastSpeak, profile.LastSpeak)
	return m.db.Save(profile).Error
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

func (m *Manager) ListMemberProfiles(page, pageSize int) ([]MemberProfile, int64, error) {
	var items []MemberProfile
	var total int64

	q := m.db.Model(&MemberProfile{})
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
	if m.styleCardMilvus != nil {
		_ = m.styleCardMilvus.Close()
	}
	if m.topicMilvus != nil {
		_ = m.topicMilvus.Close()
	}
	// 关闭 MySQL 连接
	if sqlDB, err := m.db.DB(); err == nil {
		return sqlDB.Close()
	}
	return nil
}

func (m *Manager) GetDB() *gorm.DB { return m.db }

func (m *Manager) EmbeddingProvider() EmbeddingProvider { return m.embedding }

func (m *Manager) TopicVectorStore() VectorStore {
	return m.topicMilvus
}

func memoryVectorEligible(mem Memory) bool {
	switch mem.EffectiveStatus() {
	case MemoryStatusActive, MemoryStatusLegacy:
		return true
	default:
		return false
	}
}

func memoryIDSet(memories []Memory) map[uint]struct{} {
	ids := make(map[uint]struct{}, len(memories))
	for _, mem := range memories {
		if mem.ID != 0 {
			ids[mem.ID] = struct{}{}
		}
	}
	return ids
}

func containsCanonicalType(allowed []CanonicalMemoryType, kind CanonicalMemoryType) bool {
	for _, candidate := range allowed {
		if candidate == kind {
			return true
		}
	}
	return false
}

func prioritizeRecallMemories(memories []Memory, limit int) []Memory {
	if limit <= 0 || limit > len(memories) {
		limit = len(memories)
	}

	active := make([]Memory, 0, len(memories))
	legacy := make([]Memory, 0, len(memories))
	for _, mem := range memories {
		switch mem.EffectiveStatus() {
		case MemoryStatusActive:
			active = append(active, mem)
		case MemoryStatusLegacy:
			legacy = append(legacy, mem)
		}
	}

	result := make([]Memory, 0, limit)
	for _, mem := range active {
		if len(result) >= limit {
			return result
		}
		result = append(result, mem)
	}
	for _, mem := range legacy {
		if len(result) >= limit {
			return result
		}
		result = append(result, mem)
	}
	return result
}
