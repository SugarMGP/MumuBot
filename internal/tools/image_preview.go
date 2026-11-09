package tools

import (
	"encoding/base64"

	"mumu-bot/internal/media"

	"github.com/cloudwego/eino/schema"
)

func imagePreviewResult(data []byte, mime string) (*schema.ToolResult, error) {
	data, mime, note, err := media.ImagePreview(data, mime)
	if err != nil {
		return nil, err
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	parts := make([]schema.ToolOutputPart, 0, 2)
	if note != "" {
		parts = append(parts, schema.ToolOutputPart{Type: schema.ToolPartTypeText, Text: note})
	}
	parts = append(parts, schema.ToolOutputPart{Type: schema.ToolPartTypeImage, Image: &schema.ToolOutputImage{MessagePartCommon: schema.MessagePartCommon{Base64Data: &encoded, MIMEType: mime}}})
	return &schema.ToolResult{Parts: parts}, nil
}
