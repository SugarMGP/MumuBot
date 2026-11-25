package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"mumu-bot/internal/media"
	workerproto "mumu-bot/worker/proto"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// LifeViewImageInput 是 life 查看图片的两种来源，必须恰选一个
type LifeViewImageInput struct {
	Hash     string `json:"hash,omitempty" description:"旧 QQ 图片或表情包的会话短哈希"`
	ImageRef string `json:"image_ref,omitempty" description:"文件或图片引用"`
	Path     string `json:"path,omitempty" description:"个人目录中的图片相对路径"`
}

// NewLifeViewImageTool 返回 life 专用看图工具，额外支持个人目录路径
func NewLifeViewImageTool() (tool.Tool, error) {
	return newEnhancedTool("viewImage", "实际查看一张支持的位图，图像内容会进入上下文。image_ref 读快照，path 读 /home/worker 中的相对路径，hash 仅指已登记的旧 QQ 媒体；三者恰选一个，最大10 MiB。拿到引用或文件名还不算看过图片。", lifeViewImageRun)
}

func lifeViewImageRun(ctx context.Context, input *LifeViewImageInput) (*Result, error) {
	data, mime, err := resolveLifeImage(ctx, input)
	if err != nil {
		return nil, err
	}
	return imagePreviewResult(data, mime)
}

func resolveLifeImage(ctx context.Context, input *LifeViewImageInput) ([]byte, string, error) {
	if input == nil {
		return nil, "", fmt.Errorf("请提供图片 hash 或 path")
	}
	hasHash := strings.TrimSpace(input.Hash) != ""
	hasRef := strings.TrimSpace(input.ImageRef) != ""
	hasPath := strings.TrimSpace(input.Path) != ""
	if boolCount(hasHash, hasRef, hasPath) != 1 {
		return nil, "", fmt.Errorf("hash、image_ref 与 path 必须且只能提供一个")
	}
	tc := GetToolContext(ctx)
	if tc == nil {
		return nil, "", fmt.Errorf("媒体读取能力未初始化")
	}
	if hasRef {
		data, mimeType, _, err := refImageBytes(ctx, tc, input.ImageRef)
		if err != nil {
			return nil, "", err
		}
		if len(data) > workerproto.MaxImageBytes {
			return nil, "", fmt.Errorf("图片超过 10 MiB")
		}
		return data, mimeType, nil
	}
	if hasHash {
		item, err := resolveMedia(ctx, &ViewImageInput{Hash: input.Hash})
		if err != nil {
			return nil, "", err
		}
		data, err := os.ReadFile(item.Path)
		if err != nil {
			return nil, "", fmt.Errorf("读取图片失败: %w", err)
		}
		mime := item.Mime
		if mime == "" {
			mime = "image/jpeg"
		}
		return data, mime, nil
	}
	if tc.PersonalFiles == nil {
		return nil, "", fmt.Errorf("个人文件能力未初始化")
	}
	content, err := tc.PersonalFiles.FileContent(ctx, input.Path)
	if err != nil {
		return nil, "", err
	}
	if len(content.Data) > workerproto.MaxImageBytes {
		return nil, "", fmt.Errorf("图片超过 10 MiB")
	}
	mimeType := media.ImageMIME(content.Data)
	if mimeType == "" {
		return nil, "", fmt.Errorf("这个文件不是可识别的图片")
	}
	return content.Data, mimeType, nil
}

func boolCount(values ...bool) int {
	count := 0
	for _, value := range values {
		if value {
			count++
		}
	}
	return count
}
