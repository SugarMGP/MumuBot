package llm

import (
	"context"
	"fmt"
	"mumu-bot/internal/config"

	"github.com/cloudwego/eino-ext/components/embedding/openai"
)

// EmbeddingClient 向量嵌入客户端
type EmbeddingClient struct {
	client *openai.Embedder
}

// NewEmbeddingClient 创建 Embedding 客户端
func NewEmbeddingClient() (*EmbeddingClient, error) {
	cfg := config.Get()
	if cfg.Embedding.APIKey == "" || cfg.Embedding.BaseURL == "" || cfg.Embedding.Model == "" {
		return nil, fmt.Errorf("embedding 配置不完整")
	}

	ctx := context.Background()

	embedder, err := openai.NewEmbedder(ctx, &openai.EmbeddingConfig{
		BaseURL:    cfg.Embedding.BaseURL,
		APIKey:     cfg.Embedding.APIKey,
		Model:      cfg.Embedding.Model,
		Dimensions: &cfg.Embedding.Dimensions,
	})
	if err != nil {
		return nil, fmt.Errorf("创建 Embedder 失败: %w", err)
	}

	return &EmbeddingClient{
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
		return nil, fmt.Errorf("embedding 结果为空")
	}
	return vectors[0], nil
}
