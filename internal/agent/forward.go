package agent

import (
	"fmt"
	"strings"

	"mumu-bot/internal/onebot"

	"github.com/zjutjh/onebot-sdk/message"
)

// buildForwardNodes 把转发节点转成可翻阅的结构，嵌套转发单独存一份并给句柄
func (a *Agent) buildForwardNodes(nodes []message.ForwardNode, depth int) []forwardNode {
	result := make([]forwardNode, 0, len(nodes))
	for _, node := range nodes {
		item := forwardNode{Sender: node.Nickname}
		var text strings.Builder
		for _, seg := range node.Message {
			data, ok := seg.Data.(message.ForwardData)
			if !ok {
				text.WriteString(renderForwardSegment(seg))
				continue
			}
			if len(data.Content) > 0 && depth < onebot.MaxForwardDepth {
				nested := a.buildForwardNodes(data.Content, depth+1)
				item.Nested = a.forwards.add(nested)
				text.WriteString(fmt.Sprintf("[合并转发 %s，共 %d 条]", item.Nested, len(nested)))
				continue
			}
			placeholder := "[嵌套合并转发未解析"
			if strings.TrimSpace(data.ID) != "" {
				placeholder += ":" + data.ID
			}
			placeholder += "]"
			text.WriteString(placeholder)
		}
		item.Content = strings.TrimSpace(text.String())
		result = append(result, item)
	}
	return result
}

// renderForwardSegment 渲染转发里的单个消息段
func renderForwardSegment(seg message.Segment) string {
	switch data := seg.Data.(type) {
	case message.TextData:
		return data.Text
	case message.AtData:
		if data.Name != "" {
			return "@" + data.Name
		}
		return "[@]"
	case message.FaceData:
		return onebot.QFaceText(string(data.ID))
	case message.ReplyData:
		return "[回复]"
	case message.ImageData:
		if data.Summary != "" {
			return "[图片:" + data.Summary + "]"
		}
		return "[图片]"
	case message.RecordData:
		return "[语音]"
	case message.VideoData:
		return "[视频]"
	case message.FileData:
		return "[文件]"
	case message.JSONData:
		return "[JSON卡片]"
	case message.MFaceData:
		if data.Summary != "" {
			return "[表情包:" + data.Summary + "]"
		}
		return "[表情包]"
	case message.CardData:
		return "[卡片]"
	case message.UnknownSegmentData:
		return "[未识别消息]"
	}
	return ""
}
