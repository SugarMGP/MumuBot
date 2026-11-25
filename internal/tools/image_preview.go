package tools

import (
	"encoding/base64"

	"mumu-bot/internal/media"
)

func imagePreviewResult(data []byte, mime string) (*Result, error) {
	data, mime, note, err := media.ImagePreview(data, mime)
	if err != nil {
		return nil, err
	}
	parts := make([]Part, 0, 2)
	if note != "" {
		parts = append(parts, Part{Type: PartTypeText, Text: note})
	}
	parts = append(parts, Part{Type: PartTypeImage, Image: &Image{
		Base64Data: base64.StdEncoding.EncodeToString(data), MIMEType: mime,
	}})
	return &Result{Parts: parts}, nil
}
