package utils

import (
	"strings"
)

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
