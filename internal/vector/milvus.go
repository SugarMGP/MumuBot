package vector

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/milvus-io/milvus/client/v2/column"
	"github.com/milvus-io/milvus/client/v2/entity"
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
	cli, err := newMilvusSDKClient(ctx, cfg)
	if err != nil {
		return nil, err
	}
	mc := &MilvusClient{client: cli, cfg: cfg, collectionName: cfg.CollectionName}
	if err := mc.ensureV2Collection(ctx); err != nil {
		_ = cli.Close(ctx)
		return nil, err
	}

	// 删除旧集合和创建别名不会刷新 SDK 的 schema 缓存，迁移后重新建连。
	if err := cli.Close(ctx); err != nil {
		return nil, fmt.Errorf("关闭 Milvus 迁移连接失败: %w", err)
	}
	cli, err = newMilvusSDKClient(ctx, cfg)
	if err != nil {
		return nil, err
	}
	mc.client = cli
	if err := mc.loadCollection(ctx, mc.collectionName); err != nil {
		_ = cli.Close(ctx)
		return nil, err
	}
	return mc, nil
}

func newMilvusSDKClient(ctx context.Context, cfg *MilvusConfig) (*milvusclient.Client, error) {
	cli, err := milvusclient.New(ctx, &milvusclient.ClientConfig{Address: cfg.Address, DBName: cfg.DBName})
	if err != nil {
		return nil, fmt.Errorf("连接 Milvus 失败: %w", err)
	}
	return cli, nil
}

func (c *MilvusClient) loadCollection(ctx context.Context, collectionName string) error {
	loadTask, err := c.client.LoadCollection(ctx, milvusclient.NewLoadCollectionOption(collectionName))
	if err != nil {
		return fmt.Errorf("加载集合 %s 失败: %w", collectionName, err)
	}
	if err := loadTask.Await(ctx); err != nil {
		return fmt.Errorf("等待集合 %s 加载完成失败: %w", collectionName, err)
	}
	return nil
}

func (c *MilvusClient) vectorColumns(memoryID uint, groupID int64, memType string, embedding []float64) ([]column.Column, error) {
	if uint64(memoryID) > math.MaxInt64 {
		return nil, fmt.Errorf("向量业务 ID 超出 Milvus Int64 范围: %d", memoryID)
	}
	emb32 := make([]float32, len(embedding))
	for i, value := range embedding {
		emb32[i] = float32(value)
	}
	return []column.Column{
		column.NewColumnInt64("id", []int64{int64(memoryID)}),
		column.NewColumnInt64("group_id", []int64{groupID}),
		column.NewColumnVarChar("mem_type", []string{memType}),
		column.NewColumnFloatVector("embedding", c.cfg.VectorDim, [][]float32{emb32}),
	}, nil
}

// Upsert 按业务 ID 主键插入或替换向量。
func (c *MilvusClient) Upsert(ctx context.Context, memoryID uint, groupID int64, memType string, embedding []float64) (int64, error) {
	columns, err := c.vectorColumns(memoryID, groupID, memType, embedding)
	if err != nil {
		return 0, err
	}
	result, err := c.client.Upsert(ctx, milvusclient.NewColumnBasedInsertOption(c.collectionName, columns...))
	if err != nil {
		return 0, fmt.Errorf("更新向量失败: %w", err)
	}
	if result.UpsertCount != 1 {
		return 0, fmt.Errorf("更新向量数量异常: %d", result.UpsertCount)
	}
	return int64(memoryID), nil
}

// SearchResult 搜索结果
type SearchResult struct {
	MemoryID uint    `json:"memory_id"`
	Score    float32 `json:"score"`
}

// Search 向量搜索
func (c *MilvusClient) Search(ctx context.Context, embedding []float64, groupID int64, memType string, topK int, threshold float64) ([]SearchResult, error) {
	emb32 := make([]float32, len(embedding))
	for i, value := range embedding {
		emb32[i] = float32(value)
	}

	var filterParts []string
	if groupID != 0 {
		filterParts = append(filterParts, fmt.Sprintf("group_id == %d", groupID))
	}
	if memType != "" {
		filterParts = append(filterParts, fmt.Sprintf("mem_type == \"%s\"", memType))
	}
	filter := strings.Join(filterParts, " && ")

	searchOption := milvusclient.NewSearchOption(c.collectionName, topK, []entity.Vector{entity.FloatVector(emb32)}).
		WithOutputFields("id")
	if filter != "" {
		searchOption = searchOption.WithFilter(filter)
	}
	results, err := c.client.Search(ctx, searchOption)
	if err != nil {
		return nil, fmt.Errorf("向量搜索失败: %w", err)
	}

	var searchResults []SearchResult
	for _, result := range results {
		idColumn, ok := result.GetColumn("id").(*column.ColumnInt64)
		if !ok {
			continue
		}
		for i := 0; i < result.ResultCount && i < idColumn.Len() && i < len(result.Scores); i++ {
			score := result.Scores[i]
			if float64(score) >= threshold {
				searchResults = append(searchResults, SearchResult{MemoryID: uint(idColumn.Data()[i]), Score: score})
			}
		}
	}
	return searchResults, nil
}

// Delete 按业务 ID 主键删除向量。
func (c *MilvusClient) Delete(ctx context.Context, memoryIDs []uint) error {
	if len(memoryIDs) == 0 {
		return nil
	}
	ids := make([]int64, len(memoryIDs))
	for i, id := range memoryIDs {
		if uint64(id) > math.MaxInt64 {
			return fmt.Errorf("向量业务 ID 超出 Milvus Int64 范围: %d", id)
		}
		ids[i] = int64(id)
	}
	if _, err := c.client.Delete(ctx, milvusclient.NewDeleteOption(c.collectionName).WithInt64IDs("id", ids)); err != nil {
		return fmt.Errorf("删除向量失败: %w", err)
	}
	return nil
}

// Close 关闭连接
func (c *MilvusClient) Close() error {
	return c.client.Close(context.Background())
}
