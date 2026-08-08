package llm

import (
	"context"
	"fmt"
	"mumu-bot/internal/config"
	"mumu-bot/internal/modelstats"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
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
				Text: "请用中文尽可能地描述这张图片的内容和内涵，输出一段紧凑纯文本，300字以内，不要分点、换行或使用多余空格。优先说明关键事件、关键角色或物体、表情、情绪、画面文字、梗点。",
			},
		},
	}

	ctx = callbacks.InitCallbacks(ctx, &callbacks.RunInfo{Component: components.ComponentOfChatModel}, modelstats.Handler("vision", config.Get().VisionLLM.Model))
	resp, err := v.model.Generate(ctx, []*schema.Message{msg})
	if err != nil {
		return "", logVisionError("image", err)
	}
	if resp == nil {
		return "", logVisionError("image", fmt.Errorf("视觉模型返回空响应"))
	}
	content := compactVisionText(resp.Content)
	if content == "" {
		return "", logVisionError("image", fmt.Errorf("视觉模型返回空响应"))
	}
	return content, nil
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
				Text: "请用中文尽可能地描述这段视频的内容和内涵，输出一段紧凑纯文本，300字以内，不要分点、换行或使用多余空格。优先说明关键事件、关键角色或物体、情绪、画面文字、梗点。",
			},
		},
	}

	ctx = callbacks.InitCallbacks(ctx, &callbacks.RunInfo{Component: components.ComponentOfChatModel}, modelstats.Handler("vision", config.Get().VisionLLM.Model))
	resp, err := v.model.Generate(ctx, []*schema.Message{msg})
	if err != nil {
		return "", logVisionError("video", err)
	}
	if resp == nil {
		return "", logVisionError("video", fmt.Errorf("视觉模型返回空响应"))
	}
	content := compactVisionText(resp.Content)
	if content == "" {
		return "", logVisionError("video", fmt.Errorf("视觉模型返回空响应"))
	}
	return content, nil
}

func compactVisionText(raw string) string {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return ""
	}
	var result strings.Builder
	result.Grow(len(raw))
	for i, field := range fields {
		if i > 0 {
			left, _ := utf8.DecodeLastRuneInString(fields[i-1])
			right, _ := utf8.DecodeRuneInString(field)
			leftIsWord := left >= 'a' && left <= 'z' || left >= 'A' && left <= 'Z' || left >= '0' && left <= '9'
			rightIsWord := right >= 'a' && right <= 'z' || right >= 'A' && right <= 'Z' || right >= '0' && right <= '9'
			if leftIsWord && rightIsWord {
				result.WriteByte(' ')
			}
		}
		result.WriteString(field)
	}
	return result.String()
}

func logVisionError(mediaType string, err error) error {
	zap.L().Error("视觉模型调用失败", zap.String("media_type", mediaType), zap.String("model", config.Get().VisionLLM.Model), zap.Error(err))
	return err
}
