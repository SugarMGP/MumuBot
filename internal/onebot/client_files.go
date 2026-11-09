package onebot

import (
	"encoding/json"
	"strings"

	"mumu-bot/internal/filemeta"

	"github.com/bytedance/sonic"
	"github.com/zjutjh/onebot-sdk/message"
)

// rawFileExtension 只承接 SDK FileData 尚未覆盖的入向字段
type rawFileExtension struct {
	BusID   *message.ID `json:"busid"`
	FileMD5 string      `json:"file_md5"`
}

// rawFileExtensions 与强类型消息段保持相同下标，不再从 map 重读已支持的文件字段
func rawFileExtensions(raw []byte) []rawFileExtension {
	var envelope struct {
		Message json.RawMessage `json:"message"`
	}
	if sonic.Unmarshal(raw, &envelope) != nil || len(envelope.Message) == 0 || envelope.Message[0] != '[' {
		return nil
	}
	var segments []struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if sonic.Unmarshal(envelope.Message, &segments) != nil {
		return nil
	}
	result := make([]rawFileExtension, len(segments))
	for index, segment := range segments {
		if segment.Type == "file" {
			_ = sonic.Unmarshal(segment.Data, &result[index])
		}
	}
	return result
}

// FileInfoFromData 保留普通消息和转发共用的文件定位字段
func FileInfoFromData(data message.FileData) filemeta.Ref {
	ref := filemeta.Ref{
		FileID: data.FileID, Name: strings.TrimSpace(data.Name), FileHash: data.FileHash,
	}
	if ref.Name == "" {
		ref.Name = data.File
	}
	if size, ok := parseStrNum(data.FileSize); ok {
		ref.Size = &size
	} else if data.Size > 0 {
		ref.Size = &data.Size
	}
	return ref
}
