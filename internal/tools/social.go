package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"mumu-bot/internal/media"
	workerproto "mumu-bot/worker/proto"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
	ob "github.com/zjutjh/onebot-sdk"
)

type SetSelfProfileInput struct {
	Field    string `json:"field" jsonschema:"description=avatar、nickname 或 signature，一次只改一个"`
	ImageRef string `json:"image_ref,omitempty" jsonschema:"description=field=avatar 时必填的图片引用"`
	Value    string `json:"value,omitempty" jsonschema:"description=field=nickname 或 signature 时的新内容"`
}

type SetSelfProfileOutput struct {
	Success bool           `json:"success"`
	Field   string         `json:"field"`
	Value   string         `json:"value,omitempty"`
	Result  map[string]any `json:"result,omitempty"`
	Note    string         `json:"note,omitempty"`
}

func setSelfProfile(ctx context.Context, input *SetSelfProfileInput) (*SetSelfProfileOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.Bot == nil {
		return nil, NewTerminalToolError(fmt.Errorf("QQ 资料能力未初始化"))
	}
	if input == nil {
		return nil, fmt.Errorf("请提供要修改的字段")
	}
	switch strings.ToLower(strings.TrimSpace(input.Field)) {
	case "avatar":
		refValue := strings.TrimSpace(input.ImageRef)
		if refValue == "" {
			return nil, fmt.Errorf("修改头像必须提供 image_ref")
		}
		data, _, _, err := refImageBytes(ctx, tc, refValue)
		if err != nil {
			return nil, err
		}
		if len(data) > workerproto.MaxImageBytes {
			return nil, fmt.Errorf("头像超过 10 MiB")
		}
		if media.ImageMIME(data) == "" {
			return nil, fmt.Errorf("这个文件不是可识别的图片")
		}
		if err := tc.Bot.SetAvatarBytes(ctx, data); err != nil {
			return nil, socialError(err)
		}
		return &SetSelfProfileOutput{Success: true, Field: "avatar", Note: "平台已接受设置；头像显示刷新可能受 CDN 缓存影响"}, nil
	case "nickname":
		value := strings.TrimSpace(input.Value)
		if value == "" {
			return nil, fmt.Errorf("昵称不能为空")
		}
		result, err := tc.Bot.SetNickname(ctx, value)
		if err != nil {
			return nil, socialError(err)
		}
		return &SetSelfProfileOutput{Success: true, Field: "nickname", Value: value, Result: result, Note: "已请求修改；需要展示当前值时重新查询"}, nil
	case "signature":
		value := strings.TrimSpace(input.Value)
		result, err := tc.Bot.SetSignature(ctx, value)
		if err != nil {
			return nil, socialError(err)
		}
		return &SetSelfProfileOutput{Success: true, Field: "signature", Value: value, Result: result, Note: "已请求修改；需要展示当前值时重新查询"}, nil
	default:
		return nil, fmt.Errorf("field 只能是 avatar、nickname 或 signature")
	}
}

// socialError 保留平台真实失败，并对不支持与结果未知给出可核查提示
func socialError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *ob.APIError
	if errors.As(err, &apiErr) && apiErr.RetCode == 1404 {
		return fmt.Errorf("后端不支持这个 QQ 空间或资料动作（可能版本过旧）: %w", err)
	}
	if errors.Is(err, ob.ErrTimeout) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("请求结果未确认，可能已经送达；先核实目标再决定是否重做: %w", err)
	}
	return err
}

func NewSetSelfProfileTool() (tool.InvokableTool, error) {
	return utils.InferTool("setSelfProfile", "一次修改自己的一个资料字段：avatar 用 image_ref，nickname 或 signature 用 value；不提交其他字段覆盖旧值。", setSelfProfile)
}
