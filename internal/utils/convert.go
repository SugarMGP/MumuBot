package utils

import (
	"strconv"
	"strings"
)

// ParseInt64Value 将常见 JSON/OneBot 数值类型转换为 int64。
func ParseInt64Value(v any) (int64, bool) {
	switch value := v.(type) {
	case int64:
		return value, true
	case int:
		return int64(value), true
	case float64:
		return int64(value), true
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

// FirstNonEmpty 返回 candidates 中第一个 TrimSpace 后非空的字符串。
func FirstNonEmpty(candidates ...string) string {
	for _, s := range candidates {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// UniqueIDs 去重 []uint，跳过零值，保持插入顺序。
func UniqueIDs(ids []uint) []uint {
	seen := make(map[uint]struct{}, len(ids))
	result := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}
