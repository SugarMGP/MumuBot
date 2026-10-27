package agent

import (
	"fmt"
	"strings"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"

	"github.com/zjutjh/onebot-sdk/message"
)

// buildForwardNodes 把转发节点转成可翻阅的结构，嵌套转发单独存一份并给句柄
func buildForwardNodes(nodes []message.ForwardNode, depth int, parent string) (string, int, []forwardDraft) {
	handle := newForwardHandle()
	draft := forwardDraft{Handle: handle, Parent: parent}
	var drafts []forwardDraft
	for _, node := range nodes {
		item := memory.ForwardNode{Sender: node.Nickname}
		var text strings.Builder
		for _, seg := range node.Message {
			data, ok := seg.Data.(message.ForwardData)
			if !ok {
				text.WriteString(renderForwardSegment(seg))
				continue
			}
			if len(data.Content) > 0 && depth < onebot.MaxForwardDepth {
				nestedHandle, nestedCount, nestedDrafts := buildForwardNodes(data.Content, depth+1, handle)
				if nestedHandle == "" {
					continue
				}
				drafts = append(drafts, nestedDrafts...)
				item.Nested = append(item.Nested, nestedHandle)
				text.WriteString(fmt.Sprintf("[合并转发 %s，共 %d 条]", nestedHandle, nestedCount))
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
		draft.Nodes = append(draft.Nodes, item)
	}
	return handle, len(draft.Nodes), append([]forwardDraft{draft}, drafts...)
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
