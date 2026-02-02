package llm

import (
	"amu-bot/internal/config"
	"context"
	"fmt"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

// Client LLM客户端
type Client struct {
	cfg       *config.Config
	chatModel model.ToolCallingChatModel
}

// NewClient 创建LLM客户端
func NewClient(cfg *config.Config) (*Client, error) {
	ctx := context.Background()

	// 使用Eino的OpenAI兼容客户端
	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL: cfg.LLM.BaseURL,
		APIKey:  cfg.LLM.APIKey,
		Model:   cfg.LLM.Model,
		ExtraFields: map[string]any{
			"thinking": map[string]any{
				"type": "disabled",
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("创建ChatModel失败: %w", err)
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
