package mcp

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// saveResultImages 在结果序列化前把图片固定为引用，避免 Base64 挤进文本上下文
func (m *Manager) saveResultImages(ctx context.Context, name string, result *mcp.CallToolResult) (*mcp.CallToolResult, error) {
	for index, content := range result.Content {
		picture, ok := content.(mcp.ImageContent)
		if !ok {
			continue
		}
		reader := base64.NewDecoder(base64.StdEncoding, strings.NewReader(picture.Data))
		ref, err := m.refs.CreateReady(ctx, name, reader, 10*1024*1024, nil)
		text := ""
		switch {
		case err != nil:
			text = fmt.Sprintf("图片保存失败：%v", err)
		case !ref.Image:
			text = "图片保存失败：返回内容不是支持的位图"
		default:
			text = "图片：" + ref.ImageRef() + "，可用 viewImage 按需查看"
		}
		result.Content[index] = mcp.NewTextContent(text)
	}
	return result, nil
}
