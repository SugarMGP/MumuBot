package llm

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"mumu-bot/internal/config"

	"github.com/cloudwego/eino-ext/components/model/agenticopenai"
	"github.com/cloudwego/eino/components/model"
)

var clientSlot = &clientHolder{}

type clientHolder struct {
	client model.AgenticModel
	err    error
	once   sync.Once
}

// NewClient 创建唯一的主模型客户端
func NewClient() (model.AgenticModel, error) {
	cfg := config.Get()
	if cfg == nil {
		return nil, fmt.Errorf("配置未加载")
	}
	clientSlot.once.Do(func() {
		modelCfg := cfg.ModelTiers.High
		apiKey := strings.TrimSpace(modelCfg.APIKey)
		baseURL := strings.TrimSpace(modelCfg.BaseURL)
		modelName := strings.TrimSpace(modelCfg.Model)
		if apiKey == "" || baseURL == "" || modelName == "" {
			clientSlot.err = fmt.Errorf("主模型配置不完整")
			return
		}
		client, err := agenticopenai.NewResponsesModel(context.Background(), &agenticopenai.ResponsesConfig{
			BaseURL:     baseURL,
			APIKey:      apiKey,
			Model:       modelName,
			ExtraFields: modelCfg.ExtraFields,
		})
		if err != nil {
			clientSlot.err = fmt.Errorf("创建主模型失败: %w", err)
			return
		}
		clientSlot.client = client
	})
	return clientSlot.client, clientSlot.err
}
