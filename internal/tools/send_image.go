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
	URL      string `json:"url,omitempty" jsonschema:"description=图片 HTTP/HTTPS 地址，与 image_ref 二选一"`
	ImageRef string `json:"image_ref,omitempty" jsonschema:"description=消息里的 image_ref，与 url 二选一"`
}

func sendImage(ctx context.Context, input *SendImageInput) (map[string]any, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.SendImageURLCallback == nil {
		return nil, NewTerminalToolError(fmt.Errorf("图片发送能力未初始化"))
	}
	url := ""
	imageRef := ""
	if input != nil {
		url = strings.TrimSpace(input.URL)
		imageRef = strings.TrimSpace(input.ImageRef)
	}
	if (url == "") == (imageRef == "") {
		return nil, fmt.Errorf("url 与 image_ref 必须且只能提供一个")
	}
	if imageRef != "" {
		data, _, name, err := refImageBytes(ctx, tc, imageRef)
		if err != nil {
			return nil, err
		}
		if tc.SendImageBytesCallback == nil {
			return nil, NewTerminalToolError(fmt.Errorf("图片发送能力未初始化"))
		}
		if err := tc.SendImageBytesCallback(ctx, imageRef, name, data); err != nil {
			return nil, NewTerminalToolError(err)
		}
		return map[string]any{"success": true, "message": "图片已发送", "image_ref": imageRef}, nil
	}
	if tc.SendImageURLCallback == nil {
		return nil, NewTerminalToolError(fmt.Errorf("图片发送能力未初始化"))
	}
	if _, err := projectutils.ParseHTTPURL(url); err != nil {
		return nil, fmt.Errorf("图片 URL 无效，仅支持不带凭据的 HTTP/HTTPS 地址；本地表情包请使用 sendSticker")
	}
	if err := tc.SendImageURLCallback(ctx, url); err != nil {
		return nil, NewTerminalToolError(err)
	}
	return map[string]any{"success": true, "message": "图片已发送"}, nil
}

func NewSendImageTool() (tool.InvokableTool, error) {
	return utils.InferTool("sendImage", "把 HTTP/HTTPS 图片 URL 或消息里的 image_ref 发到当前会话；两者恰选一个，本地表情包请使用 sendSticker。", sendImage)
}
