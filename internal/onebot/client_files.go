package onebot

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"mumu-bot/internal/filemeta"

	"github.com/bytedance/sonic"
)

// rawFileSegments 按消息段顺序提取原始文件段，强类型 FileData 只保留 file_id
func rawFileSegments(raw []byte) []map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var envelope struct {
		Message json.RawMessage `json:"message"`
	}
	if err := sonic.Unmarshal(raw, &envelope); err != nil {
		return nil
	}
	payload := bytes.TrimSpace(envelope.Message)
	if len(payload) == 0 || payload[0] != '[' {
		return nil
	}
	var segments []struct {
		Type string         `json:"type"`
		Data map[string]any `json:"data"`
	}
	if err := sonic.Unmarshal(payload, &segments); err != nil {
		return nil
	}
	result := make([]map[string]any, len(segments))
	for index, segment := range segments {
		if segment.Type == "file" {
			result[index] = segment.Data
		}
	}
	return result
}

func rawFileAt(rawFiles []map[string]any, index int) map[string]any {
	if index < 0 || index >= len(rawFiles) {
		return nil
	}
	return rawFiles[index]
}

// fileRefFromRaw 读取后端在普通消息段上给出的文件定位字段
func fileRefFromRaw(data map[string]any) filemeta.Ref {
	if data == nil {
		return filemeta.Ref{}
	}
	ref := filemeta.Ref{
		FileID:   rawString(data["file_id"]),
		Name:     rawString(data["file"]),
		FileHash: rawString(data["file_hash"]),
	}
	if ref.FileHash == "" {
		ref.FileHash = rawString(data["file_md5"])
	}
	ref.Size = rawInt64Ptr(data["file_size"])
	ref.BusID = rawInt64Ptr(data["busid"])
	return ref
}

func rawString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	case int64:
		return strconv.FormatInt(typed, 10)
	case int:
		return strconv.Itoa(typed)
	default:
		return ""
	}
}

func rawInt64Ptr(value any) *int64 {
	var number int64
	switch typed := value.(type) {
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		if err != nil {
			return nil
		}
		number = parsed
	case float64:
		number = int64(typed)
	case int64:
		number = typed
	case int:
		number = int64(typed)
	default:
		return nil
	}
	return &number
}
