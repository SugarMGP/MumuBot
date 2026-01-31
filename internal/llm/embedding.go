package llm

import (
	"amu-bot/internal/config"
	"amu-bot/internal/memory"
	"context"
	"fmt"

	"github.com/cloudwego/eino-ext/components/embedding/openai"
)

// EmbeddingClient 向量嵌入客户端
type EmbeddingClient struct {
	cfg    *config.Config
	client *openai.Embedder
}

// NewEmbeddingClient 创建Embedding客户端
func NewEmbeddingClient(cfg *config.Config) (*EmbeddingClient, error) {
	// 检查是否启用
	if !cfg.Embedding.Enabled {
		return nil, nil
	}

	ctx := context.Background()

	embedder, err := openai.NewEmbedder(ctx, &openai.EmbeddingConfig{
		BaseURL: cfg.Embedding.BaseURL,
		APIKey:  cfg.Embedding.APIKey,
		Model:   cfg.Embedding.Model,
	})
	if err != nil {
		return nil, fmt.Errorf("创建Embedder失败: %w", err)
	}

	return &EmbeddingClient{
		cfg:    cfg,
		client: embedder,
	}, nil
}

// Embed 生成文本的向量表示
func (c *EmbeddingClient) Embed(ctx context.Context, text string) ([]float64, error) {
	vectors, err := c.client.EmbedStrings(ctx, []string{text})
	if err != nil {
		return nil, err
	}

	if len(vectors) == 0 || len(vectors[0]) == 0 {
		return nil, fmt.Errorf("embedding结果为空")
	}

	// 转换为float64
	result := make([]float64, len(vectors[0]))
	for i, v := range vectors[0] {
		result[i] = float64(v)
	}

	return result, nil
}

// 确保实现了接口
var _ memory.EmbeddingProvider = (*EmbeddingClient)(nil)
