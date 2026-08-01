package llm

import (
	"context"
	"fmt"
	"mumu-bot/internal/config"
	"strings"
	"sync"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

var (
	highTierClientSlot = &tierClientSlot{}
	lowTierClientSlot  = &tierClientSlot{}
)

type Tier string

const (
	TierHigh Tier = "high"
	TierLow  Tier = "low"
)

type tierClientSlot struct {
	client model.ToolCallingChatModel
	err    error
	once   sync.Once
}

func TierDisplayName(tier Tier) string {
	switch tier {
	case TierHigh:
		return "高档模型"
	case TierLow:
		return "轻量模型"
	default:
		return "模型"
	}
}

func NewClientForTier(tier Tier) (model.ToolCallingChatModel, error) {
	cfg := config.Get()
	if cfg == nil {
		return nil, fmt.Errorf("配置未加载")
	}

	var modelCfg config.ModelConfig
	var slot *tierClientSlot
	switch tier {
	case TierHigh:
		modelCfg = cfg.ModelTiers.High
		slot = highTierClientSlot
	case TierLow:
		modelCfg = cfg.ModelTiers.Low
		slot = lowTierClientSlot
	default:
		return nil, fmt.Errorf("未知模型档位: %s", tier)
	}

	slot.once.Do(func() {
		apiKey := strings.TrimSpace(modelCfg.APIKey)
		baseURL := strings.TrimSpace(modelCfg.BaseURL)
		modelName := strings.TrimSpace(modelCfg.Model)
		if apiKey == "" || baseURL == "" || modelName == "" {
			slot.err = fmt.Errorf("%s配置不完整", TierDisplayName(tier))
			return
		}

		chatModel, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
			BaseURL:     baseURL,
			APIKey:      apiKey,
			Model:       modelName,
			ExtraFields: modelCfg.ExtraFields,
		})
		if err != nil {
			slot.err = fmt.Errorf("创建%s失败: %w", TierDisplayName(tier), err)
			return
		}
		slot.client = chatModel
	})

	return slot.client, slot.err
}
