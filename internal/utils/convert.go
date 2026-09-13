package utils

import (
	"encoding/json"
	"strconv"
	"strings"
)

// ParseInt64Value 将常见 JSON/OneBot 数值类型转换为 int64
func ParseInt64Value(v any) (int64, bool) {
	switch value := v.(type) {
	case int64:
		return value, true
	case int:
		return int64(value), true
	case json.Number:
		parsed, err := value.Int64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

// FirstNonEmpty 返回 candidates 中第一个 TrimSpace 后非空的字符串
func FirstNonEmpty(candidates ...string) string {
	for _, s := range candidates {
		trimmed := strings.TrimSpace(s)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}
