package llm

import (
	"context"
	"fmt"
	"mumu-bot/internal/config"
	"strings"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
)

// VisionClient 多模态视觉模型客户端
type VisionClient struct {
	model *openai.ChatModel
}

// NewVisionClient 创建视觉模型客户端
func NewVisionClient() (*VisionClient, error) {
	cfg := config.Get().VisionLLM
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.APIKey == "" || cfg.BaseURL == "" || cfg.Model == "" {
		return nil, fmt.Errorf("视觉模型配置不完整")
	}

	ctx := context.Background()
	model, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL: cfg.BaseURL,
		APIKey:  cfg.APIKey,
		Model:   cfg.Model,
	})
	if err != nil {
		return nil, fmt.Errorf("创建 VisionModel 失败: %w", err)
	}

	return &VisionClient{
		model: model,
	}, nil
}

// DescribeImage 描述图片内容
func (v *VisionClient) DescribeImage(ctx context.Context, imageURL string) (string, error) {
	if v == nil || v.model == nil {
		return "", nil
	}

	// 构建多模态消息
	msg := &schema.Message{
		Role: schema.User,
		UserInputMultiContent: []schema.MessageInputPart{
			{
				Type: schema.ChatMessagePartTypeImageURL,
				Image: &schema.MessageInputImage{
					MessagePartCommon: schema.MessagePartCommon{
						URL: &imageURL,
					},
					Detail: schema.ImageURLDetailHigh,
				},
			},
			{
				Type: schema.ChatMessagePartTypeText,
				Text: "请用中文尽可能地描述这张图片的内容和内涵，输出一段纯文本，300字以内，不要分点。优先说明关键事件、关键角色或物体、表情、情绪、画面文字、梗点。",
			},
		},
	}

	resp, err := v.model.Generate(ctx, []*schema.Message{msg})
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(resp.Content), nil
}

// DescribeVideo 描述视频内容
func (v *VisionClient) DescribeVideo(ctx context.Context, videoURL string) (string, error) {
	if v == nil || v.model == nil {
		return "", nil
	}

	msg := &schema.Message{
		Role: schema.User,
		UserInputMultiContent: []schema.MessageInputPart{
			{
				Type: schema.ChatMessagePartTypeVideoURL,
				Video: &schema.MessageInputVideo{
					MessagePartCommon: schema.MessagePartCommon{
						URL: &videoURL,
					},
				},
			},
			{
				Type: schema.ChatMessagePartTypeText,
				Text: "请用中文尽可能地描述这段视频的内容和内涵，输出一段纯文本，300字以内，不要分点。优先说明关键事件、关键角色或物体、情绪、画面文字、梗点。",
			},
		},
	}

	resp, err := v.model.Generate(ctx, []*schema.Message{msg})
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(resp.Content), nil
}
