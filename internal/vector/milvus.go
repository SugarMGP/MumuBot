package vector

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/milvus-io/milvus/client/v2/column"
	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"
	"github.com/milvus-io/milvus/client/v2/milvusclient"
)

// MilvusConfig Milvus 配置
type MilvusConfig struct {
	Address        string `yaml:"address"`
	DBName         string `yaml:"db_name"`
	CollectionName string `yaml:"collection_name"`
	VectorDim      int    `yaml:"vector_dim"`
	MetricType     string `yaml:"metric_type"` // IP, L2, COSINE
}

// MilvusClient Milvus 向量存储客户端
type MilvusClient struct {
	client         *milvusclient.Client
	cfg            *MilvusConfig
	collectionName string
	upsertMu       sync.Mutex
}

// NewMilvusClient 创建 Milvus 客户端
func NewMilvusClient(cfg *MilvusConfig) (*MilvusClient, error) {
	if cfg.Address == "" {
		cfg.Address = "localhost:19530"
	}
	if cfg.DBName == "" {
		cfg.DBName = "default"
	}
	if cfg.CollectionName == "" {
		cfg.CollectionName = "mumu_memories"
	}
	if cfg.VectorDim == 0 {
		cfg.VectorDim = 1024
	}
	if cfg.MetricType == "" {
		cfg.MetricType = "COSINE"
	}

	ctx := context.Background()

	// 连接 Milvus
	cli, err := milvusclient.New(ctx, &milvusclient.ClientConfig{
		Address: cfg.Address,
		DBName:  cfg.DBName,
	})
	if err != nil {
		return nil, fmt.Errorf("连接 Milvus 失败: %w", err)
	}

	mc := &MilvusClient{
		client:         cli,
		cfg:            cfg,
		collectionName: cfg.CollectionName,
	}

	// 初始化集合
	if err := mc.initCollection(ctx); err != nil {
		_ = cli.Close(ctx)
		return nil, err
	}

	return mc, nil
}

// initCollection 初始化集合
func (c *MilvusClient) initCollection(ctx context.Context) error {
	// 检查集合是否存在
	has, err := c.client.HasCollection(ctx, milvusclient.NewHasCollectionOption(c.collectionName))
	if err != nil {
		return fmt.Errorf("检查集合存在失败: %w", err)
	}

	if !has {
		// 创建集合
		schema := entity.NewSchema().
			WithName(c.collectionName).
			WithDescription("Mumu bot memory vectors").
			WithField(entity.NewField().
				WithName("id").
				WithDataType(entity.FieldTypeInt64).
				WithIsPrimaryKey(true).
				WithIsAutoID(true)).
			WithField(entity.NewField().
				WithName("memory_id").
				WithDataType(entity.FieldTypeInt64)).
			WithField(entity.NewField().
				WithName("group_id").
				WithDataType(entity.FieldTypeInt64)).
			WithField(entity.NewField().
				WithName("mem_type").
				WithDataType(entity.FieldTypeVarChar).
				WithMaxLength(64)).
			WithField(entity.NewField().
				WithName("embedding").
				WithDataType(entity.FieldTypeFloatVector).
				WithDim(int64(c.cfg.VectorDim)))

		if err := c.client.CreateCollection(ctx, milvusclient.NewCreateCollectionOption(c.collectionName, schema)); err != nil {
			return fmt.Errorf("创建集合失败: %w", err)
		}

		// 创建向量索引
		metricType := entity.COSINE
		switch c.cfg.MetricType {
		case "IP":
			metricType = entity.IP
		case "L2":
			metricType = entity.L2
		}

		indexOption := milvusclient.NewCreateIndexOption(c.collectionName, "embedding", index.NewHNSWIndex(metricType, 16, 256))
		if _, err := c.client.CreateIndex(ctx, indexOption); err != nil {
			return fmt.Errorf("创建索引失败: %w", err)
		}
	}

	// 加载集合到内存
	loadTask, err := c.client.LoadCollection(ctx, milvusclient.NewLoadCollectionOption(c.collectionName))
	if err != nil {
		return fmt.Errorf("加载集合失败: %w", err)
	}
	if err := loadTask.Await(ctx); err != nil {
		return fmt.Errorf("等待加载集合完成失败: %w", err)
	}

	return nil
}

// Insert 插入向量
func (c *MilvusClient) Insert(ctx context.Context, memoryID uint, groupID int64, memType string, embedding []float64) (int64, error) {
	// 转换 float64 到 float32
	emb32 := make([]float32, len(embedding))
	for i, v := range embedding {
		emb32[i] = float32(v)
	}

	// 准备数据
	memoryIDCol := column.NewColumnInt64("memory_id", []int64{int64(memoryID)})
	groupIDCol := column.NewColumnInt64("group_id", []int64{groupID})
	memTypeCol := column.NewColumnVarChar("mem_type", []string{memType})
	embeddingCol := column.NewColumnFloatVector("embedding", c.cfg.VectorDim, [][]float32{emb32})

	// 插入
	result, err := c.client.Insert(ctx, milvusclient.NewColumnBasedInsertOption(c.collectionName, memoryIDCol, groupIDCol, memTypeCol, embeddingCol))
	if err != nil {
		return 0, fmt.Errorf("插入向量失败: %w", err)
	}

	// 返回插入的 ID
	if result.IDs != nil {
		if ids, ok := result.IDs.(*column.ColumnInt64); ok && ids.Len() > 0 {
			return ids.Data()[0], nil
		}
	}
	return 0, nil
}

type vectorReplacement interface {
	ExistingVectorIDs(ctx context.Context, memoryID uint) ([]int64, error)
	Insert(ctx context.Context, memoryID uint, groupID int64, memType string, embedding []float64) (int64, error)
	DeletePrimaryIDs(ctx context.Context, ids []int64) error
}

func replaceVector(ctx context.Context, store vectorReplacement, memoryID uint, groupID int64, memType string, embedding []float64) (int64, error) {
	oldIDs, err := store.ExistingVectorIDs(ctx, memoryID)
	if err != nil {
		return 0, err
	}
	newID, err := store.Insert(ctx, memoryID, groupID, memType, embedding)
	if err != nil {
		return 0, err
	}
	if err := store.DeletePrimaryIDs(ctx, oldIDs); err != nil {
		return newID, fmt.Errorf("清理旧向量失败: %w", err)
	}
	return newID, nil
}

// Upsert 先插入新向量，再按主键清理同一 memory_id 的旧向量。
func (c *MilvusClient) Upsert(ctx context.Context, memoryID uint, groupID int64, memType string, embedding []float64) (int64, error) {
	c.upsertMu.Lock()
	defer c.upsertMu.Unlock()
	return replaceVector(ctx, c, memoryID, groupID, memType, embedding)
}

type memoryVectorRow struct {
	id       int64
	memoryID int64
}

const vectorRepairDeleteBatchSize = 500

type duplicateVectorCollector struct {
	latestIDs map[int64]int64
}

// Add returns the older vector primary key when the row duplicates a memory_id.
func (c *duplicateVectorCollector) Add(row memoryVectorRow) (int64, bool) {
	latestID, ok := c.latestIDs[row.memoryID]
	if !ok {
		c.latestIDs[row.memoryID] = row.id
		return 0, false
	}
	if row.id > latestID {
		c.latestIDs[row.memoryID] = row.id
		return latestID, true
	}
	return row.id, true
}

func (c *MilvusClient) forEachMemoryVectorRow(ctx context.Context, filter string, visit func(memoryVectorRow) error) error {
	iterator, err := c.client.QueryIterator(ctx, milvusclient.NewQueryIteratorOption(c.collectionName).
		WithBatchSize(500).
		WithFilter(filter).
		WithOutputFields("id", "memory_id"))
	if err != nil {
		return fmt.Errorf("创建记忆向量扫描迭代器失败: %w", err)
	}

	for {
		batch, err := iterator.Next(ctx)
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("扫描记忆向量失败: %w", err)
		}
		idColumn, ok := batch.GetColumn("id").(*column.ColumnInt64)
		if !ok {
			return fmt.Errorf("扫描记忆向量缺少 int64 id 字段")
		}
		memoryIDColumn, ok := batch.GetColumn("memory_id").(*column.ColumnInt64)
		if !ok {
			return fmt.Errorf("扫描记忆向量缺少 int64 memory_id 字段")
		}
		if idColumn.Len() != memoryIDColumn.Len() {
			return fmt.Errorf("扫描记忆向量字段长度不一致: id=%d memory_id=%d", idColumn.Len(), memoryIDColumn.Len())
		}
		ids := idColumn.Data()
		memoryIDs := memoryIDColumn.Data()
		for i := range ids {
			if err := visit(memoryVectorRow{id: ids[i], memoryID: memoryIDs[i]}); err != nil {
				return err
			}
		}
	}
	return nil
}

// ExistingVectorIDs 返回 memory_id 当前关联的所有向量主键。
func (c *MilvusClient) ExistingVectorIDs(ctx context.Context, memoryID uint) ([]int64, error) {
	ids := make([]int64, 0, 1)
	err := c.forEachMemoryVectorRow(ctx, fmt.Sprintf("memory_id == %d", memoryID), func(row memoryVectorRow) error {
		ids = append(ids, row.id)
		return nil
	})
	return ids, err
}

// RepairDuplicateMemoryVectors 保留每个 memory_id 最新的向量，并清理较旧副本。
func (c *MilvusClient) RepairDuplicateMemoryVectors(ctx context.Context) error {
	collector := &duplicateVectorCollector{latestIDs: make(map[int64]int64)}
	obsoleteIDs := make([]int64, 0, vectorRepairDeleteBatchSize)
	deleteObsolete := func() error {
		if len(obsoleteIDs) == 0 {
			return nil
		}
		if err := c.DeletePrimaryIDs(ctx, obsoleteIDs); err != nil {
			return err
		}
		obsoleteIDs = obsoleteIDs[:0]
		return nil
	}
	if err := c.forEachMemoryVectorRow(ctx, "", func(row memoryVectorRow) error {
		obsoleteID, duplicate := collector.Add(row)
		if !duplicate {
			return nil
		}
		obsoleteIDs = append(obsoleteIDs, obsoleteID)
		if len(obsoleteIDs) < vectorRepairDeleteBatchSize {
			return nil
		}
		return deleteObsolete()
	}); err != nil {
		return err
	}
	return deleteObsolete()
}

// SearchResult 搜索结果
type SearchResult struct {
	MemoryID uint    `json:"memory_id"`
	Score    float32 `json:"score"`
}

// Search 向量搜索
func (c *MilvusClient) Search(ctx context.Context, embedding []float64, groupID int64, memType string, topK int, threshold float64) ([]SearchResult, error) {
	// 转换 float64 到 float32
	emb32 := make([]float32, len(embedding))
	for i, v := range embedding {
		emb32[i] = float32(v)
	}

	// 构建过滤条件
	var filterParts []string
	if groupID != 0 {
		filterParts = append(filterParts, fmt.Sprintf("group_id == %d", groupID))
	}
	if memType != "" {
		filterParts = append(filterParts, fmt.Sprintf("mem_type == \"%s\"", memType))
	}
	filter := ""
	if len(filterParts) > 0 {
		filter = filterParts[0]
		for i := 1; i < len(filterParts); i++ {
			filter += " && " + filterParts[i]
		}
	}

	// 搜索
	searchOption := milvusclient.NewSearchOption(c.collectionName, topK, []entity.Vector{entity.FloatVector(emb32)}).
		WithOutputFields("memory_id")
	if filter != "" {
		searchOption = searchOption.WithFilter(filter)
	}

	results, err := c.client.Search(ctx, searchOption)
	if err != nil {
		return nil, fmt.Errorf("向量搜索失败: %w", err)
	}

	var searchResults []SearchResult
	for _, result := range results {
		for i := 0; i < result.ResultCount; i++ {
			score := result.Scores[i]
			// 根据相似度阈值过滤
			if float64(score) < threshold {
				continue
			}

			// 获取 memory_id
			memIDCol := result.GetColumn("memory_id")
			if memIDCol != nil {
				if memIDs, ok := memIDCol.(*column.ColumnInt64); ok && i < memIDs.Len() {
					searchResults = append(searchResults, SearchResult{
						MemoryID: uint(memIDs.Data()[i]),
						Score:    score,
					})
				}
			}
		}
	}

	return searchResults, nil
}

// Delete 删除向量
func (c *MilvusClient) Delete(ctx context.Context, memoryIDs []uint) error {
	if len(memoryIDs) == 0 {
		return nil
	}

	// 构建 ID 列表字符串
	idsStr := ""
	for i, id := range memoryIDs {
		if i > 0 {
			idsStr += ", "
		}
		idsStr += fmt.Sprintf("%d", id)
	}
	filter := fmt.Sprintf("memory_id in [%s]", idsStr)

	// 删除
	_, err := c.client.Delete(ctx, milvusclient.NewDeleteOption(c.collectionName).WithExpr(filter))
	if err != nil {
		return fmt.Errorf("删除向量失败: %w", err)
	}

	return nil
}

// DeletePrimaryIDs 按 Milvus 主键删除向量，供安全替换与历史去重使用。
func (c *MilvusClient) DeletePrimaryIDs(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := c.client.Delete(ctx, milvusclient.NewDeleteOption(c.collectionName).WithInt64IDs("id", ids))
	if err != nil {
		return fmt.Errorf("按主键删除向量失败: %w", err)
	}
	return nil
}

// Close 关闭连接
func (c *MilvusClient) Close() error {
	return c.client.Close(context.Background())
}
