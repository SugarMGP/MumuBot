package tools

import (
	"context"
	"fmt"
	"strings"

	projectutils "mumu-bot/internal/utils"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type SendImageInput struct {
	URL string `json:"url" jsonschema:"description=要发送的图片 HTTP/HTTPS 地址"`
}

func sendImage(ctx context.Context, input *SendImageInput) (map[string]any, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.SendImageURLCallback == nil {
		return nil, NewTerminalToolError(fmt.Errorf("图片发送能力未初始化"))
	}
	imageURL := ""
	if input != nil {
		imageURL = strings.TrimSpace(input.URL)
	}
	if imageURL == "" {
		return nil, fmt.Errorf("图片 URL 不能为空")
	}
	if _, err := projectutils.ParseHTTPURL(imageURL); err != nil {
		return nil, fmt.Errorf("图片 URL 无效，仅支持不带凭据的 HTTP/HTTPS 地址；本地表情包请使用 sendSticker")
	}
	if err := tc.SendImageURLCallback(ctx, imageURL); err != nil {
		return nil, NewTerminalToolError(err)
	}
	return map[string]any{"success": true, "message": "图片已发送"}, nil
}

func NewSendImageTool() (tool.InvokableTool, error) {
	return utils.InferTool("sendImage", "将普通图片 HTTP/HTTPS URL 发送到当前会话；本地表情包请使用 sendSticker。", sendImage)
}
