package agent

import (
	"fmt"
	"strings"

	"mumu-bot/internal/onebot"

	"github.com/zjutjh/onebot-sdk/message"
)

// flattenForwardNodes 展平所有已补齐的嵌套转发节点，保留原节点顺序和发送者信息
func flattenForwardNodes(nodes []message.ForwardNode) []message.ForwardNode {
	return flattenForwardNodesAtDepth(nodes, 1)
}

func flattenForwardNodesAtDepth(nodes []message.ForwardNode, depth int) []message.ForwardNode {
	flattened := make([]message.ForwardNode, 0, len(nodes))
	for _, node := range nodes {
		plain := make(message.Chain, 0, len(node.Message))
		flush := func() {
			if len(plain) == 0 {
				return
			}
			copyNode := node
			copyNode.Message = plain
			flattened = append(flattened, copyNode)
			plain = make(message.Chain, 0, len(node.Message))
		}
		for _, seg := range node.Message {
			data, ok := seg.Data.(message.ForwardData)
			if !ok {
				plain = append(plain, seg)
				continue
			}
			flush()
			if len(data.Content) > 0 && depth < onebot.MaxForwardDepth {
				flattened = append(flattened, flattenForwardNodesAtDepth(data.Content, depth+1)...)
				continue
			}
			placeholder := "[嵌套合并转发未解析"
			if strings.TrimSpace(data.ID) != "" {
				placeholder += ":" + data.ID
			}
			placeholder += "]"
			plain = append(plain, message.Text(placeholder))
		}
		flush()
	}
	return flattened
}

// forwardMedia 收集转发节点里的图片，转发内容也要能查看
func forwardMedia(nodes []message.ForwardNode) []pendingMedia {
	var pending []pendingMedia
	for _, node := range nodes {
		for _, seg := range node.Message {
			data, ok := seg.Data.(message.ImageData)
			if !ok {
				continue
			}
			if strings.TrimSpace(data.URL) == "" {
				continue
			}
			pending = append(pending, pendingMedia{URL: data.URL})
		}
	}
	return pending
}

func formatForwardNodes(nodes []message.ForwardNode) string {
	var b strings.Builder
	for _, node := range nodes {
		if node.Nickname != "" {
			b.WriteString(node.Nickname)
			b.WriteString(": ")
		}
		for _, seg := range node.Message {
			switch data := seg.Data.(type) {
			case message.TextData:
				b.WriteString(data.Text)
			case message.AtData:
				if data.Name != "" {
					b.WriteByte('@')
					b.WriteString(data.Name)
				} else {
					b.WriteString("[@]")
				}
			case message.FaceData:
				b.WriteString(fmt.Sprintf("[表情:%s]", data.ID))
			case message.ReplyData:
				b.WriteString("[回复]")
			case message.ImageData:
				if data.Summary != "" {
					b.WriteString("[图片:")
					b.WriteString(data.Summary)
					b.WriteByte(']')
				} else {
					b.WriteString("[图片]")
				}
			case message.RecordData:
				b.WriteString("[语音]")
			case message.VideoData:
				b.WriteString("[视频]")
			case message.FileData:
				b.WriteString("[文件]")
			case message.JSONData:
				b.WriteString("[JSON卡片]")
			case message.MFaceData:
				if data.Summary != "" {
					b.WriteString("[表情:")
					b.WriteString(data.Summary)
					b.WriteByte(']')
				} else {
					b.WriteString("[表情]")
				}
			case message.CardData:
				b.WriteString("[卡片]")
			case message.UnknownSegmentData:
				b.WriteString("[未识别消息]")
			}
		}
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}
