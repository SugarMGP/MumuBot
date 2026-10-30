package llm

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/model/agenticopenai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/openai/openai-go/v3/responses"
)

const handshakeTimeout = 10 * time.Second

// ModelConfig 是一次模型客户端构建所需的非敏感配置
type ModelConfig struct {
	APIKey          string
	BaseURL         string
	Model           string
	ThinkingType    string
	ReasoningEffort string
}

// NewClient 按指定档案创建模型客户端；不做进程级缓存
func NewClient(cfg ModelConfig) (model.AgenticModel, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	baseURL := strings.TrimSpace(cfg.BaseURL)
	modelName := strings.TrimSpace(cfg.Model)
	if apiKey == "" || baseURL == "" || modelName == "" {
		return nil, fmt.Errorf("主模型配置不完整")
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: handshakeTimeout, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = handshakeTimeout
	httpClient := &http.Client{Transport: transport}

	extraFields := map[string]any{}
	if thinking := strings.TrimSpace(cfg.ThinkingType); thinking != "" {
		extraFields["thinking"] = map[string]any{"type": thinking}
	}
	var reasoning *responses.ReasoningParam
	if effort := strings.TrimSpace(cfg.ReasoningEffort); effort != "" {
		reasoning = &responses.ReasoningParam{Effort: responses.ReasoningEffort(effort)}
	}

	client, err := agenticopenai.NewResponsesModel(context.Background(), &agenticopenai.ResponsesConfig{
		BaseURL:     baseURL,
		APIKey:      apiKey,
		Model:       modelName,
		HTTPClient:  httpClient,
		ExtraFields: extraFields,
		Reasoning:   reasoning,
	})
	if err != nil {
		return nil, fmt.Errorf("创建主模型失败: %w", err)
	}
	return client, nil
}

// Test 用极短请求验证模型连接；仅用于后台测试，不参与机器人运行
func Test(ctx context.Context, cfg ModelConfig) error {
	client, err := NewClient(cfg)
	if err != nil {
		return err
	}
	_, err = client.Generate(ctx, []*schema.AgenticMessage{schema.UserAgenticMessage("ping")})
	if err != nil {
		return fmt.Errorf("模型测试失败: %w", err)
	}
	return nil
}
