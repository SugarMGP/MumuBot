package llm

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"mumu-bot/internal/agentic"

	"google.golang.org/genai"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/gemini"
	chatopenai "trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses"
)

const handshakeTimeout = 10 * time.Second

const (
	// ProviderResponses 使用 Responses 接口
	ProviderResponses = agentic.ProviderResponses
	// ProviderChatCompletions 使用 Chat Completions 接口
	ProviderChatCompletions = agentic.ProviderChat
	// ProviderGemini 使用 Gemini 接口
	ProviderGemini = agentic.ProviderGemini
)

// ModelConfig 是一次模型客户端构建所需的非敏感配置
type ModelConfig struct {
	Provider        string
	APIKey          string
	BaseURL         string
	Model           string
	ReasoningEffort string
	ThinkingMode    string
	BodyOverride    string
}

// NewClient 按指定档案创建模型客户端；不做进程级缓存
func NewClient(cfg ModelConfig) (model.Model, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	baseURL := strings.TrimSpace(cfg.BaseURL)
	modelName := strings.TrimSpace(cfg.Model)
	if apiKey == "" || baseURL == "" || modelName == "" {
		return nil, fmt.Errorf("主模型配置不完整")
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: handshakeTimeout, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = handshakeTimeout
	fields, err := ParseBodyOverride(cfg.BodyOverride)
	if err != nil {
		return nil, err
	}
	var handler http.RoundTripper = transport
	if len(fields) > 0 {
		handler = &bodyOverrideTransport{base: transport, fields: fields}
	}
	httpClient := &http.Client{Transport: &requestTransport{base: handler}}
	httpOpts := []model.HTTPClientOption{model.WithHTTPClientTransport(httpClient.Transport)}

	switch cfg.Provider {
	case ProviderResponses:
		// 沿用适配器默认 store=false，不向服务端留存响应；历史回放依赖 include 的加密 reasoning
		return responses.New(modelName,
			responses.WithAPIKey(apiKey),
			responses.WithBaseURL(baseURL),
			responses.WithHTTPClientOptions(httpOpts...),
		), nil
	case ProviderChatCompletions:
		// thinking 形态由档案显式控制；不跟随 BaseURL 推断变体，避免出站字段漂移
		extra := map[string]any{}
		if cfg.ThinkingMode != "" {
			extra["thinking"] = map[string]string{"type": cfg.ThinkingMode}
		}
		if cfg.ReasoningEffort != "" {
			extra["reasoning_effort"] = cfg.ReasoningEffort
		}
		return chatopenai.New(modelName,
			chatopenai.WithVariant(chatopenai.VariantOpenAI),
			chatopenai.WithAPIKey(apiKey),
			chatopenai.WithBaseURL(baseURL),
			chatopenai.WithExtraFields(extra),
			chatopenai.WithHTTPClientOptions(httpOpts...),
		), nil
	case ProviderGemini:
		client, err := gemini.New(context.Background(), modelName, gemini.WithGeminiClientConfig(&genai.ClientConfig{
			APIKey:     apiKey,
			Backend:    genai.BackendGeminiAPI,
			HTTPClient: httpClient,
			HTTPOptions: genai.HTTPOptions{
				BaseURL: baseURL,
			},
		}))
		if err != nil {
			return nil, fmt.Errorf("创建主模型失败: %w", err)
		}
		return client, nil
	default:
		return nil, fmt.Errorf("unknown model protocol: %s", cfg.Provider)
	}
}

// Test 用极短请求验证模型连接；仅用于后台测试，不参与机器人运行
func Test(ctx context.Context, cfg ModelConfig) error {
	client, err := NewClient(cfg)
	if err != nil {
		return err
	}
	stream, err := client.GenerateContent(ctx, &model.Request{Messages: []model.Message{model.NewUserMessage("ping")}})
	if err != nil {
		return fmt.Errorf("模型测试失败: %w", err)
	}
	for response := range stream {
		if response == nil {
			continue
		}
		if response.Error != nil {
			return fmt.Errorf("模型测试失败: %s", response.Error.Message)
		}
		if len(response.Choices) > 0 {
			return nil
		}
	}
	return fmt.Errorf("模型测试失败: 没有返回内容")
}
