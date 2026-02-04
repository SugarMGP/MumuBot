package llm

import (
	"context"
	"fmt"
	"mumu-bot/internal/config"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

// Client LLM 客户端
type Client struct {
	cfg       *config.Config
	chatModel model.ToolCallingChatModel
}

// NewClient 创建 LLM 客户端
func NewClient(cfg *config.Config) (*Client, error) {
	ctx := context.Background()

	// 使用 Eino 的 OpenAI 兼容客户端
	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL:     cfg.LLM.BaseURL,
		APIKey:      cfg.LLM.APIKey,
		Model:       cfg.LLM.Model,
		ExtraFields: cfg.LLM.ExtraFields,
	})
	if err != nil {
		return nil, fmt.Errorf("创建 ChatModel 失败: %w", err)
	}

	return &Client{
		cfg:       cfg,
		chatModel: chatModel,
	}, nil
}

// GetModel 获取底层模型（支持工具调用）
func (c *Client) GetModel() model.ToolCallingChatModel {
	return c.chatModel
}
