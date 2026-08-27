package agent

import (
	"mumu-bot/internal/onebot"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// buildNativeModelMessages 按快照顺序构造模型消息。
func buildNativeModelMessages(buffer []*onebot.GroupMessage, lastReadMessage *onebot.GroupMessage) []*schema.Message {
	readMessages, currentMessages := splitMessageSnapshot(buffer, lastReadMessage)
	messages := make([]*schema.Message, 0, len(buffer))
	var text strings.Builder
	flushText := func() {
		if text.Len() == 0 {
			return
		}
		messages = append(messages, &schema.Message{Role: schema.User, Content: text.String()})
		text.Reset()
	}
	appendMessages := func(items []*onebot.GroupMessage, old bool) {
		for _, item := range items {
			if item == nil || strings.TrimSpace(item.FinalContent) == "" {
				continue
			}
			content := item.FinalContent
			if old {
				content = "(OLD)" + content
			}
			if len(item.Images) == 0 {
				text.WriteString(content)
				continue
			}

			flushText()
			parts := make([]schema.MessageInputPart, 0, len(item.Images)+1)
			parts = append(parts, schema.MessageInputPart{Type: schema.ChatMessagePartTypeText, Text: content})
			for _, image := range item.Images {
				url := strings.TrimSpace(image.URL)
				if url == "" {
					continue
				}
				parts = append(parts, schema.MessageInputPart{
					Type: schema.ChatMessagePartTypeImageURL,
					Image: &schema.MessageInputImage{
						MessagePartCommon: schema.MessagePartCommon{URL: &url},
						Detail:            schema.ImageURLDetailAuto,
					},
				})
			}
			if len(parts) == 1 {
				messages = append(messages, &schema.Message{Role: schema.User, Content: content})
			} else {
				messages = append(messages, &schema.Message{Role: schema.User, UserInputMultiContent: parts})
			}
		}
	}

	appendMessages(readMessages, true)
	flushText()
	appendMessages(currentMessages, false)
	flushText()
	return messages
}
