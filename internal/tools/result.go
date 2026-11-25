package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// PartType 是工具结果内容块类型
type PartType string

const (
	PartTypeText  PartType = "text"
	PartTypeImage PartType = "image"
	PartTypeAudio PartType = "audio"
	PartTypeVideo PartType = "video"
	PartTypeFile  PartType = "file"
)

// Image 是工具结果里的图片
type Image struct {
	URL        string
	Base64Data string
	FileID     string
	MIMEType   string
}

// Audio 是工具结果里的音频
type Audio struct {
	URL        string
	Base64Data string
	FileID     string
	MIMEType   string
}

// Video 是工具结果里的视频
type Video struct {
	URL        string
	Base64Data string
	FileID     string
	MIMEType   string
}

// File 是工具结果里的文件
type File struct {
	URL        string
	Name       string
	Base64Data string
	FileID     string
	MIMEType   string
}

// Part 是工具结果里的单个内容块
type Part struct {
	Type  PartType
	Text  string
	Image *Image
	Audio *Audio
	Video *Video
	File  *File
}

// Result 是多模态工具结果
type Result struct {
	Parts []Part
}

// ToolErrorResult 把可恢复工具错误包成与工具输出一致的 JSON
func ToolErrorResult(err error) string {
	if err == nil {
		return ""
	}
	payload, marshalErr := sonic.MarshalString(struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}{Message: err.Error()})
	if marshalErr != nil {
		return err.Error()
	}
	return payload
}

// TerminalToolError 标记必须结束当前 Agent 轮次的错误
type TerminalToolError struct{ error }

// NewTerminalToolError 标记必须结束当前 Agent 轮次的错误
func NewTerminalToolError(err error) error { return &TerminalToolError{error: err} }

// newFunctionTool 与旧 utils.InferTool 同形的泛型包装，按输入类型生成参数 schema
func newFunctionTool[I, O any](name, description string, fn func(context.Context, I) (O, error), opts ...function.Option) (tool.Tool, error) {
	options := append([]function.Option{function.WithName(name), function.WithDescription(description)}, opts...)
	return function.NewFunctionTool(fn, options...), nil
}

// newEnhancedTool 构造可以返回图片、音频、视频或文件的工具
func newEnhancedTool[I any](name, description string, fn func(context.Context, I) (*Result, error), opts ...function.Option) (tool.Tool, error) {
	options := append([]function.Option{function.WithName(name), function.WithDescription(description)}, opts...)
	return function.NewFunctionTool(fn, options...), nil
}

// ResultOf 取出多模态工具结果
func ResultOf(value any) (*Result, bool) {
	switch result := value.(type) {
	case *Result:
		return result, result != nil
	case Result:
		return &result, true
	default:
		return nil, false
	}
}

// ResultText 把任意工具返回值转成 transcript 文本；普通结构按 JSON 保存
func ResultText(value any) string {
	if result, ok := ResultOf(value); ok {
		return resultText(result)
	}
	if value == nil {
		return ""
	}
	// 字符串本身就是工具结果正文，保持原样；仅结构体等普通值按 JSON 保存
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := sonic.MarshalString(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return encoded
}

// ResultSucceeded 按结果里的 success 字段判断工具是否成功；字段缺失视为成功
func ResultSucceeded(value any) bool {
	text := ResultText(value)
	var outcome struct {
		Success *bool `json:"success"`
	}
	if sonic.UnmarshalString(text, &outcome) != nil || outcome.Success == nil {
		return true
	}
	return *outcome.Success
}

func resultText(result *Result) string {
	if result == nil {
		return ""
	}
	parts := make([]string, 0, len(result.Parts))
	for _, part := range result.Parts {
		switch part.Type {
		case PartTypeText:
			parts = append(parts, part.Text)
		case PartTypeImage:
			parts = append(parts, "[图片工具结果]")
		case PartTypeAudio:
			parts = append(parts, "[音频工具结果]")
		case PartTypeVideo:
			parts = append(parts, "[视频工具结果]")
		case PartTypeFile:
			parts = append(parts, "[文件工具结果]")
		}
	}
	return strings.Join(parts, "\n")
}
