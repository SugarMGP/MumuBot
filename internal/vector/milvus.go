package vector

import (
	"context"
	"fmt"

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
}

// MemoryVector 记忆向量结构
type MemoryVector struct {
	ID        int64     `json:"id"`
	MemoryID  uint      `json:"memory_id"`
	GroupID   int64     `json:"group_id"`
	MemType   string    `json:"mem_type"`
	Embedding []float32 `json:"embedding"`
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
		cfg.CollectionName = "amu_memories"
	}
	if cfg.VectorDim == 0 {
		cfg.VectorDim = 1536
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
		cli.Close(ctx)
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
			WithDescription("Amu bot memory vectors").
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

// DeleteByGroup 按群删除向量
func (c *MilvusClient) DeleteByGroup(ctx context.Context, groupID int64) error {
	filter := fmt.Sprintf("group_id == %d", groupID)
	_, err := c.client.Delete(ctx, milvusclient.NewDeleteOption(c.collectionName).WithExpr(filter))
	if err != nil {
		return fmt.Errorf("按群删除向量失败: %w", err)
	}
	return nil
}

// Close 关闭连接
func (c *MilvusClient) Close() error {
	return c.client.Close(context.Background())
}

// GetConfig 获取配置
func (c *MilvusClient) GetConfig() *MilvusConfig {
	return c.cfg
}
