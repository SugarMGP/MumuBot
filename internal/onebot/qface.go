package onebot

import (
	"strconv"
	"strings"

	"github.com/zjutjh/onebot-sdk/message"
)

const (
	// QFaceMarker QQ 表情标记的起始，完整形式是 %qface:名称或编号%
	QFaceMarker = "%qface:"
	// QFaceMarkerEnd QQ 表情标记的结束
	QFaceMarkerEnd = "%"
)

// QFaceText 把 QQ 表情编号渲染成 %qface:名称% 标记，编号不在目录内时退回编号
func QFaceText(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if number, err := strconv.Atoi(id); err == nil {
		if name := QQFaceName(number); name != "" {
			return QFaceMarker + name + QFaceMarkerEnd
		}
	}
	return QFaceMarker + id + QFaceMarkerEnd
}

// SplitQFace 把文本里的 %qface:名称或编号% 拆成消息段，无法识别的标记按原文保留
func SplitQFace(content string) []message.Segment {
	segments := make([]message.Segment, 0, 3)
	rest := content
	text := ""
	for {
		start := strings.Index(rest, QFaceMarker)
		if start < 0 {
			text += rest
			break
		}
		after := start + len(QFaceMarker)
		end := strings.Index(rest[after:], QFaceMarkerEnd)
		if end < 0 {
			text += rest
			break
		}
		id, ok := QQFaceResolve(rest[after : after+end])
		if !ok {
			// 无法识别的标记整段按原文保留，继续往后找下一个
			text += rest[:after+end+len(QFaceMarkerEnd)]
			rest = rest[after+end+len(QFaceMarkerEnd):]
			continue
		}
		text += rest[:start]
		if text != "" {
			segments = append(segments, message.Text(text))
			text = ""
		}
		segments = append(segments, message.Face(strconv.Itoa(id)))
		rest = rest[after+end+len(QFaceMarkerEnd):]
	}
	if text != "" {
		segments = append(segments, message.Text(text))
	}
	return segments
}
