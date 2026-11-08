package logger

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var logYAMLKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func logFieldText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return logJSONText(value)
}

func logJSONText(value any) string {
	text, err := logJSONAPI.MarshalToString(value)
	if err != nil {
		return fmt.Sprintf("[日志内容编码失败: %v]", err)
	}
	return text
}

func readableLogValue(value any) string {
	text := renderLogYAML(expandLogJSON(value), "")
	if strings.Contains(text, "\n") {
		text += "\n"
	}
	return text
}

func expandLogJSON(value any) any {
	if text, ok := value.(string); ok {
		var nested any
		if err := logJSONAPI.UnmarshalFromString(text, &nested); err == nil {
			switch nested.(type) {
			case map[string]any, []any:
				return nested
			}
		}
	}
	return value
}

// renderLogYAML 只改变日志展示，数值保留原精度，多行和 emoji 不经过转义字符串中转
func renderLogYAML(value any, indent string) string {
	switch value := value.(type) {
	case map[string]any:
		if len(value) == 0 {
			return "{}"
		}
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		lines := make([]string, 0, len(keys))
		for _, key := range keys {
			label := key
			if !logYAMLKey.MatchString(key) {
				label = logJSONText(key)
			}
			item := expandLogJSON(value[key])
			rendered := renderLogYAML(item, indent+"  ")
			prefix := indent + label + ": "
			switch item := item.(type) {
			case map[string]any:
				if len(item) > 0 {
					prefix = indent + label + ":\n"
				}
			case []any:
				if len(item) > 0 {
					prefix = indent + label + ":\n"
				}
			}
			lines = append(lines, prefix+rendered)
		}
		return strings.Join(lines, "\n")
	case []any:
		if len(value) == 0 {
			return "[]"
		}
		lines := make([]string, 0, len(value))
		for _, item := range value {
			rendered := renderLogYAML(expandLogJSON(item), indent+"  ")
			lines = append(lines, indent+"- "+strings.TrimPrefix(rendered, indent+"  "))
		}
		return strings.Join(lines, "\n")
	case string:
		if strings.Contains(value, "\n") {
			marker := "|2-"
			if strings.HasSuffix(value, "\n\n") || strings.Trim(value, "\n") == "" {
				marker = "|2+"
			} else if strings.HasSuffix(value, "\n") {
				marker = "|2"
			}
			if indent == "" {
				indent = "  "
			}
			return marker + "\n" + indent + strings.ReplaceAll(strings.TrimSuffix(value, "\n"), "\n", "\n"+indent)
		}
		return logJSONText(value)
	default:
		return logFieldText(value)
	}
}
