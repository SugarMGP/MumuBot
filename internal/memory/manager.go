package memory

import (
	"context"
	"errors"
	"fmt"
	"mumu-bot/internal/config"
	"mumu-bot/internal/utils"
	"mumu-bot/internal/vector"
	"sort"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/components/model"
	agentreact "github.com/cloudwego/eino/flow/agent/react"
	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func ParseMemberNameRecords(raw string) []MemberNameRecord {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	var records []MemberNameRecord
	if err := sonic.UnmarshalString(raw, &records); err != nil {
		return nil
	}
	return normalizeMemberNameRecords(records)
}

func EncodeMemberNameRecords(records []MemberNameRecord) string {
	normalized := normalizeMemberNameRecords(records)
	if len(normalized) == 0 {
		return ""
	}
	b, err := sonic.MarshalString(normalized)
	if err != nil {
		return ""
	}
	return b
}

func normalizeMemberNameRecords(records []MemberNameRecord) []MemberNameRecord {
	if len(records) == 0 {
		return nil
	}

	type dedupeKey struct {
		content string
		source  MemberNameSource
		groupID int64
	}

	latestByKey := make(map[dedupeKey]MemberNameRecord, len(records))
	for _, record := range records {
		record.Content = strings.TrimSpace(record.Content)
		record.Source = MemberNameSource(strings.TrimSpace(string(record.Source)))
		if record.Content == "" {
			continue
		}
		switch record.Source {
		case MemberNameSourceGroupCard:
			if record.GroupID <= 0 {
				continue
			}
		case MemberNameSourceLearnedAlias:
			record.GroupID = 0
		default:
			continue
		}

		key := dedupeKey{
			content: strings.ToLower(record.Content),
			source:  record.Source,
			groupID: record.GroupID,
		}
		if existing, ok := latestByKey[key]; ok && existing.UpdatedAt.After(record.UpdatedAt) {
			continue
		}
		latestByKey[key] = record
	}

	normalized := make([]MemberNameRecord, 0, len(latestByKey))
	for _, record := range latestByKey {
		normalized = append(normalized, record)
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].UpdatedAt.Equal(normalized[j].UpdatedAt) {
			if normalized[i].Source != normalized[j].Source {
				return normalized[i].Source < normalized[j].Source
			}
			if normalized[i].GroupID != normalized[j].GroupID {
				return normalized[i].GroupID < normalized[j].GroupID
			}
			return normalized[i].Content < normalized[j].Content
		}
		return normalized[i].UpdatedAt.After(normalized[j].UpdatedAt)
	})
	return normalized
}

func UpsertMemberGroupCard(records []MemberNameRecord, groupID int64, card string, updatedAt time.Time) []MemberNameRecord {
	card = strings.TrimSpace(card)
	if groupID <= 0 || card == "" {
		return normalizeMemberNameRecords(records)
	}
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	records = append(records, MemberNameRecord{
		Content:   card,
		Source:    MemberNameSourceGroupCard,
		GroupID:   groupID,
		UpdatedAt: updatedAt,
	})
	return normalizeMemberNameRecords(records)
}

func UpsertMemberLearnedAliases(records []MemberNameRecord, aliases []string, updatedAt time.Time) []MemberNameRecord {
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			continue
		}
		records = append(records, MemberNameRecord{
			Content:   alias,
			Source:    MemberNameSourceLearnedAlias,
			GroupID:   0,
			UpdatedAt: updatedAt,
		})
	}
	return normalizeMemberNameRecords(records)
}

func LatestMemberGroupCard(records []MemberNameRecord, groupID int64) string {
	if groupID <= 0 {
		return ""
	}
	for _, record := range normalizeMemberNameRecords(records) {
		if record.Source == MemberNameSourceGroupCard && record.GroupID == groupID {
			return record.Content
		}
	}
	return ""
}

func MemberLearnedAliases(records []MemberNameRecord) []string {
	normalized := normalizeMemberNameRecords(records)
	if len(normalized) == 0 {
		return nil
	}
	aliases := make([]string, 0, len(normalized))
	for _, record := range normalized {
		if record.Source == MemberNameSourceLearnedAlias {
			aliases = append(aliases, record.Content)
		}
	}
	return aliases
}

func MemberNamesForAdmin(records []MemberNameRecord, nickname string) []string {
	normalized := normalizeMemberNameRecords(records)
	if len(normalized) == 0 && strings.TrimSpace(nickname) == "" {
		return nil
	}

	seen := make(map[string]struct{}, len(normalized)+1)
	names := make([]string, 0, len(normalized)+1)
	push := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		names = append(names, name)
	}

	push(nickname)
	for _, record := range normalized {
		push(record.Content)
	}
	return names
}

func MemberNamesSearchText(records []MemberNameRecord, nickname string) string {
	names := MemberNamesForAdmin(records, nickname)
	if len(names) == 0 {
		return ""
	}
	return strings.Join(names, " ")
}

func (p *MemberProfile) MemberNameRecords() []MemberNameRecord {
	if p == nil {
		return nil
	}
	return ParseMemberNameRecords(p.NameRecords)
}

func (p *MemberProfile) SetMemberNameRecords(records []MemberNameRecord) {
	if p == nil {
		return
	}
	p.NameRecords = EncodeMemberNameRecords(records)
}

func (p *MemberProfile) UpsertGroupCard(groupID int64, card string, updatedAt time.Time) {
	if p == nil {
		return
	}
	p.NameRecords = EncodeMemberNameRecords(UpsertMemberGroupCard(p.MemberNameRecords(), groupID, card, updatedAt))
}

func (p *MemberProfile) UpsertLearnedAliases(aliases []string, updatedAt time.Time) {
	if p == nil {
		return
	}
	p.NameRecords = EncodeMemberNameRecords(UpsertMemberLearnedAliases(p.MemberNameRecords(), aliases, updatedAt))
}

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

// EmbeddingProvider 向量嵌入接口
type EmbeddingProvider interface {
	Embed(ctx context.Context, text string) ([]float64, error)
}

type vectorStore interface {
	Insert(ctx context.Context, memoryID uint, groupID int64, memType string, embedding []float64) (int64, error)
	Search(ctx context.Context, embedding []float64, groupID int64, memType string, topK int, threshold float64) ([]vector.SearchResult, error)
	Delete(ctx context.Context, memoryIDs []uint) error
	DeleteByGroup(ctx context.Context, groupID int64) error
	Close() error
	GetConfig() *vector.MilvusConfig
}

// Manager 记忆系统管理器
type Manager struct {
	db              *gorm.DB
	embedding       EmbeddingProvider
	claimExtractor  *agentreact.Agent
	milvus          vectorStore // Memory 向量存储
	styleCardMilvus vectorStore // StyleCard 向量存储
	topicMilvus     vectorStore // Topic 摘要向量存储
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
		milvus:          milvusClient,
		styleCardMilvus: styleMilvusClient,
		topicMilvus:     topicMilvusClient,
		cleanupStop:     make(chan struct{}),
	}
	if claimExtractor, err := newMemoryClaimExtractor(claimModel); err != nil {
		return nil, fmt.Errorf("初始化长期记忆提取器失败: %w", err)
	} else {
		m.claimExtractor = claimExtractor
	}

	// 启动消息日志清理任务
	m.startMessageLogCleanup()

	// 启动情绪衰减任务
	m.startMoodDecay()
	m.startMemoryConvergence()

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

// UpdateMemoryContent 更新记忆内容（用于合并）
func (m *Manager) UpdateMemoryContent(ctx context.Context, id uint, newContent string) error {
	// 更新数据库
	updates := map[string]any{
		"content":    newContent,
		"updated_at": time.Now(),
	}
	if err := m.db.Model(&Memory{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return err
	}

	m.syncMemoryVectorBestEffort(ctx, id)
	return nil
}

func (m *Manager) UpdateMemberProfileLearned(profile *MemberProfile) error {
	if profile == nil {
		return nil
	}
	var existing MemberProfile
	err := m.db.Where("user_id = ?", profile.UserID).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		profile.Activity = applyActivityUpdate(profile.Activity, time.Time{}, profile.LastSpeak)
		return m.db.Create(profile).Error
	}
	if err != nil {
		return err
	}
	profile.ID = existing.ID
	profile.Activity = applyActivityUpdate(profile.Activity, existing.LastSpeak, profile.LastSpeak)
	return m.db.Save(profile).Error
}

// DeleteMemory 删除记忆
func (m *Manager) DeleteMemory(ctx context.Context, id uint) error {
	if err := m.db.Delete(&Memory{}, id).Error; err != nil {
		return err
	}
	if m.milvus != nil {
		_ = m.milvus.Delete(ctx, []uint{id})
	}
	return nil
}

func (m *Manager) ArchiveMemory(ctx context.Context, id uint) error {
	if err := m.db.WithContext(ctx).
		Model(&Memory{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":     MemoryStatusArchived,
			"updated_at": time.Now(),
		}).Error; err != nil {
		return err
	}
	m.syncMemoryVectorBestEffort(ctx, id)
	return nil
}

func (m *Manager) RestoreMemoryToCandidate(ctx context.Context, id uint) error {
	if err := m.db.WithContext(ctx).
		Model(&Memory{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":     MemoryStatusCandidate,
			"updated_at": time.Now(),
		}).Error; err != nil {
		return err
	}
	m.syncMemoryVectorBestEffort(ctx, id)
	return nil
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

	// 保存到 MySQL
	if err := m.db.Save(mem).Error; err != nil {
		return err
	}

	m.syncMemoryVectorBestEffort(ctx, mem.ID)
	return nil
}

func (m *Manager) IngestMemory(ctx context.Context, input MemoryIngestInput) (*Memory, string, error) {
	content := strings.TrimSpace(input.Content)
	if content == "" {
		return nil, "ignored", nil
	}
	if input.SourceKind == "" {
		input.SourceKind = MemorySourceKindMessage
	}
	if input.SourceRef == "" && input.SourceKind == MemorySourceKindMessage {
		return nil, "", fmt.Errorf("消息来源缺少 source_ref")
	}

	claim := m.extractNormalizedClaim(ctx, input, content)
	if claim.CanonicalType == "" {
		zap.L().Warn("长期记忆候选被忽略：未提取到有效 claim",
			zap.String("source_kind", string(input.SourceKind)),
			zap.String("source_ref", input.SourceRef))
		return nil, "ignored", nil
	}
	if len(input.AllowedCanonicalTypes) > 0 && !containsCanonicalType(input.AllowedCanonicalTypes, claim.CanonicalType) {
		zap.L().Warn("长期记忆候选被忽略：类型不在允许范围内",
			zap.String("canonical_type", string(claim.CanonicalType)),
			zap.String("source_kind", string(input.SourceKind)),
			zap.String("source_ref", input.SourceRef))
		return nil, "ignored", nil
	}
	relatedUserID := input.RelatedUserID
	if relatedUserID == 0 {
		relatedUserID = resolveSubjectCandidateUserID(claim.SubjectName, input.SubjectCandidates)
	}
	subjectClass := claim.SubjectClass
	subjectTokenValue := subjectToken(subjectClass, input.GroupID, relatedUserID, input.SelfID)

	status := MemoryStatusCandidate
	if input.SourceKind == MemorySourceKindMigration {
		status = MemoryStatusLegacy
	}
	if claim.CanonicalType == CanonicalMemoryTypeEpisode && input.SourceKind == MemorySourceKindMessage && subjectClass == MemorySubjectClassSelf {
		status = MemoryStatusActive
	}
	if claim.CanonicalType == CanonicalMemoryTypeGoal && !claim.LongTerm {
		return nil, "ignored", nil
	}

	memType := oldMemoryTypeFromSubject(subjectClass)
	factKey := ""
	if IsKeyedCanonicalType(claim.CanonicalType) && subjectClass != MemorySubjectClassUnknown {
		factKey = buildFactKey(memType, subjectTokenValue, claim.SlotKind, claim.SlotAnchor)
	}

	if claim.CanonicalType == CanonicalMemoryTypeEpisode {
		var existing Memory
		episodeQuery := m.db.WithContext(ctx).Where("group_id = ? AND source_ref = ? AND content = ?", input.GroupID, input.SourceRef, content)
		err := episodeQuery.First(&existing).Error
		if err == nil {
			updates := map[string]any{
				"updated_at":     time.Now(),
				"evidence_count": gorm.Expr("GREATEST(evidence_count, ?)", 1),
				"importance":     importanceForStatus(existing.CanonicalType, existing.EffectiveStatus(), existing.EvidenceCount),
			}
			if updateErr := m.db.WithContext(ctx).Model(&Memory{}).Where("id = ?", existing.ID).Updates(updates).Error; updateErr != nil {
				return nil, "", updateErr
			}
			if fetchErr := m.db.WithContext(ctx).First(&existing, existing.ID).Error; fetchErr == nil {
				return &existing, "deduplicated", nil
			}
			return &existing, "deduplicated", nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", err
		}
		mem := &Memory{
			Type:          memType,
			GroupID:       input.GroupID,
			UserID:        relatedUserID,
			Content:       content,
			CanonicalType: claim.CanonicalType,
			Status:        status,
			EvidenceCount: 1,
			SourceKind:    input.SourceKind,
			SourceRef:     input.SourceRef,
			Importance:    importanceForStatus(claim.CanonicalType, status, 1),
		}
		if err := m.SaveMemory(ctx, mem); err != nil {
			return nil, "", err
		}
		return mem, "created", nil
	}

	if factKey != "" {
		var existing Memory
		err := m.db.WithContext(ctx).
			Where("group_id = ? AND canonical_type = ? AND fact_key = ? AND status <> ?", input.GroupID, claim.CanonicalType, factKey, MemoryStatusArchived).
			Order("id DESC").
			First(&existing).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", err
		}
		if err == nil {
			if m.sameClaimValue(ctx, existing, input, claim, content) {
				updates := map[string]any{
					"updated_at":  time.Now(),
					"source_kind": input.SourceKind,
					"source_ref":  input.SourceRef,
				}
				action := "deduplicated"
				nextEvidenceCount := existing.EvidenceCount
				nextStatus := existing.EffectiveStatus()
				if existing.SourceRef != input.SourceRef {
					updates["evidence_count"] = gorm.Expr("evidence_count + 1")
					nextEvidenceCount++
					action = "reinforced"
				}
				if existing.EffectiveStatus() == MemoryStatusCandidate && canPromoteCandidate(existing, input.SourceKind, nextEvidenceCount) {
					nextStatus = MemoryStatusActive
					updates["status"] = nextStatus
				}
				updates["importance"] = importanceForStatus(existing.CanonicalType, nextStatus, nextEvidenceCount)
				if updateErr := m.db.WithContext(ctx).Model(&Memory{}).Where("id = ?", existing.ID).Updates(updates).Error; updateErr != nil {
					return nil, "", updateErr
				}
				_ = m.db.WithContext(ctx).First(&existing, existing.ID).Error
				if _, ok := updates["status"]; ok {
					m.syncMemoryVectorBestEffort(ctx, existing.ID)
				}
				return &existing, action, nil
			}
			mem := &Memory{
				Type:          memType,
				GroupID:       input.GroupID,
				UserID:        relatedUserID,
				Content:       content,
				CanonicalType: claim.CanonicalType,
				Status:        MemoryStatusCandidate,
				EvidenceCount: 1,
				SourceKind:    input.SourceKind,
				SourceRef:     input.SourceRef,
				FactKey:       factKey,
				Importance:    importanceForStatus(claim.CanonicalType, MemoryStatusCandidate, 1),
			}
			if err := m.SaveMemory(ctx, mem); err != nil {
				return nil, "", err
			}
			return mem, "conflict-candidate", nil
		}
	}

	mem := &Memory{
		Type:          memType,
		GroupID:       input.GroupID,
		UserID:        relatedUserID,
		Content:       content,
		CanonicalType: claim.CanonicalType,
		Status:        status,
		EvidenceCount: 1,
		SourceKind:    input.SourceKind,
		SourceRef:     input.SourceRef,
		FactKey:       factKey,
		Importance:    importanceForStatus(claim.CanonicalType, status, 1),
	}
	if err := m.SaveMemory(ctx, mem); err != nil {
		return nil, "", err
	}
	return mem, "created", nil
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
		if mem.Status == MemoryStatusCandidate && canPromoteCandidate(*mem, MemorySourceKindTopic, mem.EvidenceCount) {
			now := time.Now()
			affectedIDs := []uint{mem.ID}
			if err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if err := tx.Model(&Memory{}).Where("id = ?", mem.ID).Updates(map[string]any{
					"status":     MemoryStatusActive,
					"importance": importanceForStatus(mem.CanonicalType, MemoryStatusActive, mem.EvidenceCount),
					"updated_at": now,
				}).Error; err != nil {
					return err
				}
				if mem.FactKey == "" {
					return nil
				}
				var archivedIDs []uint
				if err := tx.Model(&Memory{}).
					Where("id <> ? AND group_id = ? AND canonical_type = ? AND fact_key = ? AND status = ?", mem.ID, mem.GroupID, mem.CanonicalType, mem.FactKey, MemoryStatusActive).
					Pluck("id", &archivedIDs).Error; err != nil {
					return err
				}
				affectedIDs = append(affectedIDs, archivedIDs...)
				if len(archivedIDs) == 0 {
					return nil
				}
				return tx.Model(&Memory{}).
					Where("id IN ?", archivedIDs).
					Updates(map[string]any{
						"status":     MemoryStatusArchived,
						"updated_at": now,
					}).Error
			}); err == nil && m.db.WithContext(ctx).First(mem, mem.ID).Error == nil {
				m.syncMemoryVectorsBestEffort(ctx, affectedIDs...)
				created = append(created, *mem)
				continue
			}
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

func (m *Manager) RunMemoryConvergence(ctx context.Context, now time.Time) error {
	if now.IsZero() {
		now = time.Now()
	}
	var affectedIDs []uint
	if err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var stale []Memory
		if err := tx.
			Where("status = ? AND updated_at < ?", MemoryStatusCandidate, now.Add(-MemoryOpenLoopGraceWindow)).
			Find(&stale).Error; err != nil {
			return err
		}
		for _, item := range stale {
			if item.FactKey == "" && item.CanonicalType != CanonicalMemoryTypeEpisode {
				if err := tx.Model(&Memory{}).Where("id = ?", item.ID).Updates(map[string]any{
					"status":     MemoryStatusArchived,
					"updated_at": now,
				}).Error; err != nil {
					return err
				}
				affectedIDs = append(affectedIDs, item.ID)
				continue
			}
			if canPromoteCandidate(item, item.SourceKind, item.EvidenceCount) {
				if err := tx.Model(&Memory{}).Where("id = ?", item.ID).Updates(map[string]any{
					"status":     MemoryStatusActive,
					"updated_at": now,
				}).Error; err != nil {
					return err
				}
				affectedIDs = append(affectedIDs, item.ID)
				if item.FactKey != "" {
					var archivedIDs []uint
					if err := tx.Model(&Memory{}).
						Where("id <> ? AND group_id = ? AND canonical_type = ? AND fact_key = ? AND status = ?", item.ID, item.GroupID, item.CanonicalType, item.FactKey, MemoryStatusActive).
						Pluck("id", &archivedIDs).Error; err != nil {
						return err
					}
					affectedIDs = append(affectedIDs, archivedIDs...)
					if len(archivedIDs) > 0 {
						if err := tx.Model(&Memory{}).
							Where("id IN ?", archivedIDs).
							Updates(map[string]any{
								"status":     MemoryStatusArchived,
								"updated_at": now,
							}).Error; err != nil {
							return err
						}
					}
				}
				continue
			}
			if err := tx.Model(&Memory{}).Where("id = ?", item.ID).Updates(map[string]any{
				"status":     MemoryStatusArchived,
				"updated_at": now,
			}).Error; err != nil {
				return err
			}
			affectedIDs = append(affectedIDs, item.ID)
		}
		return nil
	}); err != nil {
		return err
	}
	m.syncMemoryVectorsBestEffort(ctx, affectedIDs...)
	return nil
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

	return prioritizeRecallMemories(memories, limit), nil
}

// startMessageLogCleanup 启动消息日志清理定时任务
func (m *Manager) startMessageLogCleanup() {
	cleanupCfg := config.Get().Memory.MessageLogCleanup
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

func (m *Manager) startMemoryConvergence() {
	ticker := time.NewTicker(15 * time.Minute)

	go func() {
		if err := m.RunMemoryConvergence(context.Background(), time.Now()); err != nil {
			zap.L().Warn("启动时运行长期记忆收敛失败", zap.Error(err))
		}
		for {
			select {
			case <-ticker.C:
				if err := m.RunMemoryConvergence(context.Background(), time.Now()); err != nil {
					zap.L().Warn("长期记忆收敛失败", zap.Error(err))
				}
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
		keepIDs, err := m.protectedTopicMessageIDs(groupID, keepLatest)
		if err != nil {
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
			m.db.Model(&mem).Updates(map[string]any{
				"access_count": gorm.Expr("access_count + 1"),
			})
			sortedMemories = append(sortedMemories, mem)
			if len(sortedMemories) >= limit {
				break
			}
		}
	}

	return sortedMemories, nil
}

// ==================== 风格卡片 ====================

func (m *Manager) SaveStyleCardCandidate(ctx context.Context, card *StyleCard) (bool, error) {
	if card == nil {
		return false, nil
	}
	if m.embedding == nil || m.styleCardMilvus == nil {
		return false, fmt.Errorf("风格卡片向量依赖未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	card.Intent = strings.TrimSpace(card.Intent)
	card.Tone = strings.TrimSpace(card.Tone)
	card.TriggerRule = strings.TrimSpace(card.TriggerRule)
	card.AvoidRule = strings.TrimSpace(card.AvoidRule)
	card.Example = strings.TrimSpace(card.Example)
	card.SourceExcerpt = strings.TrimSpace(card.SourceExcerpt)

	if !IsValidStyleIntent(card.Intent) || !IsValidStyleTone(card.Tone) {
		return false, fmt.Errorf("非法的风格标签")
	}
	if card.TriggerRule == "" || card.AvoidRule == "" || card.Example == "" {
		return false, fmt.Errorf("风格卡片缺少必填字段")
	}

	queryText := styleCardVectorText(card)
	embedding, err := m.embedding.Embed(ctx, queryText)
	if err != nil {
		return false, err
	}

	var existing StyleCard
	searchResults, err := m.styleCardMilvus.Search(
		ctx,
		embedding,
		card.GroupID,
		styleCardVectorKey(card.Intent, card.Tone),
		3,
		0.92,
	)
	if err != nil {
		return false, err
	}
	for _, result := range searchResults {
		if err := m.db.First(&existing, result.MemoryID).Error; err == nil {
			break
		}
	}
	if existing.ID != 0 {
		updates := map[string]any{
			"evidence_count": gorm.Expr("evidence_count + 1"),
			"updated_at":     time.Now(),
		}
		if nextStatus := styleCardStatusOnNewEvidence(existing.Status); nextStatus != existing.Status {
			updates["status"] = nextStatus
		}
		if shouldPreferLongerText(existing.TriggerRule, card.TriggerRule) {
			updates["trigger_rule"] = card.TriggerRule
		}
		if shouldPreferLongerText(existing.AvoidRule, card.AvoidRule) {
			updates["avoid_rule"] = card.AvoidRule
		}
		if shouldPreferShorterText(existing.Example, card.Example) {
			updates["example"] = card.Example
		}
		if mergedExcerpt := mergeStyleCardSourceExcerpt(existing.SourceExcerpt, card.SourceExcerpt); mergedExcerpt != strings.TrimSpace(existing.SourceExcerpt) {
			updates["source_excerpt"] = mergedExcerpt
		}
		if err := m.db.Model(&existing).Updates(updates).Error; err != nil {
			return false, err
		}
		if err := m.db.First(&existing, existing.ID).Error; err == nil {
			if err := m.refreshStyleCardVector(ctx, &existing); err != nil {
				return false, err
			}
		}
		return false, nil
	}

	if card.Status == "" {
		card.Status = StyleCardStatusCandidate
	}
	if card.EvidenceCount <= 0 {
		card.EvidenceCount = 1
	}
	if err := m.db.Create(card).Error; err != nil {
		return false, err
	}
	if err := m.insertStyleCardVector(ctx, card, embedding); err != nil {
		return false, err
	}
	return true, nil
}

func (m *Manager) SearchStyleCards(groupID int64, keyword string, limit int) ([]StyleCard, error) {
	var cards []StyleCard
	q := m.db.Model(&StyleCard{}).Where("status = ?", StyleCardStatusActive)
	if strings.TrimSpace(keyword) != "" {
		keywords := strings.Fields(keyword)
		likeConditions := make([]string, 0, len(keywords))
		args := make([]interface{}, 0, len(keywords)*4)
		for _, kw := range keywords {
			likeConditions = append(likeConditions, "trigger_rule LIKE ? OR avoid_rule LIKE ? OR example LIKE ? OR source_excerpt LIKE ?")
			pattern := "%" + kw + "%"
			args = append(args, pattern, pattern, pattern, pattern)
		}
		q = q.Where(strings.Join(likeConditions, " OR "), args...)
	}
	if limit <= 0 {
		limit = 10
	}

	orderGroup := fmt.Sprintf("CASE WHEN group_id = %d THEN 0 ELSE 1 END", groupID)
	err := q.Order(orderGroup).
		Order("evidence_count DESC").
		Order("use_count DESC").
		Order("updated_at DESC").
		Limit(limit).
		Find(&cards).Error
	return cards, err
}

func (m *Manager) ListUncheckedStyleCards(groupID int64, limit int) ([]StyleCard, error) {
	var cards []StyleCard
	q := m.db.Where("status = ?", StyleCardStatusCandidate)
	if groupID != 0 {
		q = q.Where("group_id = ?", groupID)
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	err := q.Order("updated_at DESC").Find(&cards).Error
	return cards, err
}

func (m *Manager) ReviewStyleCards(ids []uint, approve bool) error {
	if len(ids) == 0 {
		return nil
	}

	var cards []StyleCard
	if err := m.db.Where("id IN ?", ids).Find(&cards).Error; err != nil {
		return err
	}

	for _, card := range cards {
		nextStatus := StyleCardStatusRejected
		if approve {
			nextStatus = StyleCardStatusCandidate
			if card.EvidenceCount >= 2 {
				nextStatus = StyleCardStatusActive
			}
		}
		if err := m.db.Model(&StyleCard{}).Where("id = ?", card.ID).Updates(map[string]any{
			"status":     nextStatus,
			"updated_at": time.Now(),
		}).Error; err != nil {
			return err
		}
	}

	return nil
}

func (m *Manager) ListActiveStyleCardsByIntent(intent string, groupID int64, tone string, limit int) ([]StyleCard, error) {
	var cards []StyleCard
	if limit <= 0 {
		limit = 3
	}

	orderGroup := fmt.Sprintf("CASE WHEN group_id = %d THEN 0 ELSE 1 END", groupID)
	escapedTone := strings.ReplaceAll(tone, "'", "''")
	orderTone := fmt.Sprintf("CASE WHEN tone = '%s' THEN 0 ELSE 1 END", escapedTone)

	err := m.db.Model(&StyleCard{}).
		Where("status = ? AND intent = ?", StyleCardStatusActive, strings.TrimSpace(intent)).
		Order(orderGroup).
		Order(orderTone).
		Order("evidence_count DESC").
		Order("use_count DESC").
		Order("updated_at DESC").
		Limit(limit).
		Find(&cards).Error
	return cards, err
}

func (m *Manager) IncrementStyleCardUsage(ids []uint) error {
	if len(ids) == 0 {
		return nil
	}

	now := time.Now()
	return m.db.Model(&StyleCard{}).
		Where("id IN ?", ids).
		Updates(map[string]any{
			"use_count":    gorm.Expr("use_count + 1"),
			"last_used_at": &now,
		}).Error
}

func styleCardCollectionName(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "mumu_memories"
	}
	return base + "_style_cards"
}

func styleCardStatusOnNewEvidence(status StyleCardStatus) StyleCardStatus {
	if status == StyleCardStatusRejected {
		return StyleCardStatusCandidate
	}
	return status
}

func styleCardVectorKey(intent, tone string) string {
	return strings.TrimSpace(intent) + "|" + strings.TrimSpace(tone)
}

func styleCardVectorText(card *StyleCard) string {
	if card == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf(
		"intent:%s\ntone:%s\ntrigger:%s\navoid:%s\nexample:%s",
		card.Intent,
		card.Tone,
		card.TriggerRule,
		card.AvoidRule,
		card.Example,
	))
}

func (m *Manager) insertStyleCardVector(ctx context.Context, card *StyleCard, embedding []float64) error {
	if card == nil || m.styleCardMilvus == nil {
		return nil
	}
	if _, err := m.styleCardMilvus.Insert(ctx, card.ID, card.GroupID, styleCardVectorKey(card.Intent, card.Tone), embedding); err != nil {
		return fmt.Errorf("插入风格卡片向量失败: %w", err)
	}
	return nil
}

func (m *Manager) refreshStyleCardVector(ctx context.Context, card *StyleCard) error {
	if card == nil || m.embedding == nil || m.styleCardMilvus == nil {
		return nil
	}
	if err := m.styleCardMilvus.Delete(ctx, []uint{card.ID}); err != nil {
		return fmt.Errorf("删除风格卡片旧向量失败: %w", err)
	}
	embedding, err := m.embedding.Embed(ctx, styleCardVectorText(card))
	if err != nil {
		return err
	}
	return m.insertStyleCardVector(ctx, card, embedding)
}

func shouldPreferShorterText(existing, candidate string) bool {
	// 例句类字段越短越像可复用证据，合并风格卡片时优先保留更紧凑的非空表达。
	existing = strings.TrimSpace(existing)
	candidate = strings.TrimSpace(candidate)
	switch {
	case candidate == "":
		return false
	case existing == "":
		return true
	default:
		return len([]rune(candidate)) < len([]rune(existing))
	}
}

func shouldPreferLongerText(existing, candidate string) bool {
	// 规则类字段需要足够上下文才可复用，合并时优先保留信息更完整的非空表达。
	existing = strings.TrimSpace(existing)
	candidate = strings.TrimSpace(candidate)
	switch {
	case candidate == "":
		return false
	case existing == "":
		return true
	default:
		return len([]rune(candidate)) > len([]rune(existing))
	}
}

func mergeStyleCardSourceExcerpt(existing, candidate string) string {
	var parts []string
	if strings.TrimSpace(existing) != "" {
		items := strings.Split(existing, "|")
		parts = make([]string, 0, len(items))
		for _, item := range items {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			parts = append(parts, item)
		}
	}
	candidate = strings.TrimSpace(candidate)
	if candidate != "" {
		parts = append(parts, candidate)
	}
	if len(parts) > 3 {
		parts = parts[len(parts)-3:]
	}
	return strings.Join(parts, "|")
}

// ==================== 黑话管理 ====================

// SearchJargons 搜索黑话（通过关键词匹配，本群优先）
func (m *Manager) SearchJargons(groupID int64, keyword string, limit int) ([]Jargon, error) {
	var jargons []Jargon
	q := m.db.Model(&Jargon{}).Where("rejected = ?", false)

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

	// 本群优先排序：本群的排在前面，然后按 checked 降序
	err := q.Order(fmt.Sprintf("CASE WHEN group_id = %d THEN 0 ELSE 1 END, checked DESC", groupID)).
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
		"meaning":  jargon.Meaning,
		"context":  jargon.Context,
		"checked":  false, // 重置审核状态
		"rejected": false,
	}
	return m.db.Model(&existing).Updates(updates).Error
}

// BatchReviewJargon 批量审核黑话
func (m *Manager) BatchReviewJargon(ids []uint, approve bool) error {
	if len(ids) == 0 {
		return nil
	}
	updates := map[string]any{
		"checked": true,
	}
	if approve {
		updates["rejected"] = false
	} else {
		updates["rejected"] = true
	}
	return m.db.Model(&Jargon{}).Where("id IN ?", ids).Updates(updates).Error
}

// GetUncheckedJargons 获取待审核的黑话
func (m *Manager) GetUncheckedJargons(groupID int64, limit int) ([]Jargon, error) {
	var jargons []Jargon
	err := m.db.Where("group_id = ? AND checked = ?", groupID, false).
		Limit(limit).Find(&jargons).Error
	return jargons, err
}

// GetAllApprovedJargons 获取所有已审核通过的黑话（用于构建 AC 自动机）
func (m *Manager) GetAllApprovedJargons() ([]Jargon, error) {
	var jargons []Jargon
	err := m.db.Where("checked = ? AND rejected = ?", true, false).Find(&jargons).Error
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
	var existing MemberProfile
	if err := m.db.Where("user_id = ?", profile.UserID).First(&existing).Error; err != nil {
		return err
	}

	profile.Activity = applyActivityUpdate(profile.Activity, existing.LastSpeak, profile.LastSpeak)
	return m.db.Save(profile).Error
}

// ==================== 统计 ====================

// GetStats 获取统计信息
func (m *Manager) GetStats() map[string]int64 {
	stats := make(map[string]int64)
	var memories, members, messages, styleCards, jargons int64
	m.db.Model(&Memory{}).Count(&memories)
	m.db.Model(&MemberProfile{}).Count(&members)
	m.db.Model(&MessageLog{}).Count(&messages)
	m.db.Model(&StyleCard{}).Count(&styleCards)
	m.db.Model(&Jargon{}).Count(&jargons)
	stats["memories"] = memories
	stats["members"] = members
	stats["messages"] = messages
	stats["style_cards"] = styleCards
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

func canPromoteCandidate(mem Memory, sourceKind MemorySourceKind, evidenceCount int) bool {
	if mem.CanonicalType == CanonicalMemoryTypeEpisode {
		return sourceKind == MemorySourceKindTopic
	}
	if mem.FactKey == "" {
		return false
	}
	if sourceKind == MemorySourceKindTopic {
		return true
	}
	return evidenceCount >= 2
}

func (m *Manager) syncMemoryVectorBestEffort(ctx context.Context, id uint) {
	if err := m.syncMemoryVector(ctx, id); err != nil {
		zap.L().Warn("同步长期记忆向量失败", zap.Uint("memory_id", id), zap.Error(err))
	}
}

func (m *Manager) syncMemoryVectorsBestEffort(ctx context.Context, ids ...uint) {
	seen := make(map[uint]struct{}, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		m.syncMemoryVectorBestEffort(ctx, id)
	}
}

func (m *Manager) syncMemoryVector(ctx context.Context, id uint) error {
	if id == 0 || m.milvus == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := m.milvus.Delete(ctx, []uint{id}); err != nil {
		return err
	}

	var mem Memory
	if err := m.db.WithContext(ctx).First(&mem, id).Error; err != nil {
		return err
	}
	if !memoryVectorEligible(mem) || m.embedding == nil {
		return nil
	}
	embedding, err := m.embedding.Embed(ctx, mem.Content)
	if err != nil {
		return err
	}
	_, err = m.milvus.Insert(ctx, mem.ID, mem.GroupID, string(mem.Type), embedding)
	return err
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

func normalizeComparableText(raw string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(strings.ToLower(raw))), "")
}

func (m *Manager) sameClaimValue(_ context.Context, existing Memory, _ MemoryIngestInput, incoming NormalizedClaim, content string) bool {
	existingContent := normalizeComparableText(existing.Content)
	incomingContent := normalizeComparableText(content)
	if existingContent != "" && existingContent == incomingContent {
		return true
	}

	incomingValue := normalizeComparableText(incoming.ValueSummary)
	if incomingValue == "" {
		incomingValue = incomingContent
	}
	if incomingValue == "" {
		return false
	}
	if existingContent == incomingValue {
		return true
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

// ==================== 情绪状态管理 ====================

// startMoodDecay 启动情绪衰减定时任务（每五分钟执行一次）
func (m *Manager) startMoodDecay() {
	ticker := time.NewTicker(5 * time.Minute)
	go func() {
		for {
			select {
			case <-ticker.C:
				if err := m.ApplyMoodDecay(); err != nil {
					zap.L().Error("情绪衰减失败", zap.Error(err))
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

// ==================== 学习状态管理 ====================

// GetLearningState 获取群组的学习进度
func (m *Manager) GetLearningState(groupID int64) (*LearningState, error) {
	var state LearningState
	err := m.db.Where("group_id = ?", groupID).First(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &LearningState{GroupID: groupID}, nil
	}
	if err != nil {
		return nil, err
	}
	return &state, nil
}

// UpdateLearningState 更新群组的学习进度
func (m *Manager) UpdateLearningState(groupID int64, lastMessageID uint) error {
	var state LearningState
	err := m.db.Where("group_id = ?", groupID).First(&state).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		state = LearningState{
			GroupID:       groupID,
			LastMessageID: lastMessageID,
		}
		return m.db.Create(&state).Error
	}
	if err != nil {
		return err
	}

	state.LastMessageID = lastMessageID
	return m.db.Save(&state).Error
}
