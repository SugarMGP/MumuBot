package agent

import (
	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/schema"
)

const estimatedImageTokens = 1200

// splitSegments 按真人消息把连续记录切成交互段
func splitSegments(entries []sessionEntry) []*sessionSegment {
	segments := make([]*sessionSegment, 0, len(entries))
	for _, entry := range entries {
		if len(segments) == 0 || entry.user != nil {
			segments = append(segments, &sessionSegment{})
		}
		segment := segments[len(segments)-1]
		segment.items = append(segment.items, entry)
	}
	return segments
}

func segmentTokens(segment *sessionSegment, scale float64) int {
	total := 0
	for _, entry := range segment.items {
		total += messageTokensScaled(entry.msg, scale)
	}
	return total
}

// estimateTokens 粗略估算文本 token 数，中文按每字 2/3、其余按每 4 字符 1 token
func estimateTokens(text string) int {
	if text == "" {
		return 0
	}
	cjk, other := 0, 0
	for _, r := range text {
		if r >= 0x4E00 && r <= 0x9FFF {
			cjk++
			continue
		}
		other++
	}
	return cjk*2/3 + other/4
}

// messageTokens 估算一条消息占用的 token，图片按固定视觉预算而不是 base64 长度
func messageTokens(message *schema.AgenticMessage) int {
	return messageTokensScaled(message, 1)
}

// messageTokensScaled 只缩放文本部分，图片、音频、视频保持固定预算
func messageTokensScaled(message *schema.AgenticMessage, scale float64) int {
	if message == nil {
		return 0
	}
	if scale <= 0 {
		scale = 1
	}
	textTokens := func(text string) int {
		return int(float64(estimateTokens(text)) * scale)
	}
	total := 0
	for _, block := range message.ContentBlocks {
		if block == nil {
			continue
		}
		switch {
		case block.AssistantGenText != nil:
			total += textTokens(block.AssistantGenText.Text)
		case block.AssistantGenImage != nil:
			total += estimatedImageTokens
		case block.UserInputText != nil:
			total += textTokens(block.UserInputText.Text)
		case block.UserInputImage != nil:
			total += estimatedImageTokens
		case block.UserInputAudio != nil:
			total += 1000
		case block.UserInputVideo != nil:
			total += 2000
		case block.Reasoning != nil:
			total += textTokens(block.Reasoning.Text)
		case block.FunctionToolCall != nil:
			total += textTokens(block.FunctionToolCall.Name + block.FunctionToolCall.Arguments)
		case block.FunctionToolResult != nil:
			for _, part := range block.FunctionToolResult.Content {
				if part == nil {
					continue
				}
				switch {
				case part.Text != nil:
					total += textTokens(part.Text.Text)
				case part.Image != nil:
					total += estimatedImageTokens
				case part.Audio != nil:
					total += 1000
				case part.Video != nil:
					total += 2000
				}
			}
		}
	}
	return total
}

// requestTokens 估算一次完整请求的输入占用，包含消息和工具 schema
func requestTokens(messages []*schema.AgenticMessage, toolInfos []*schema.ToolInfo) int {
	total := 0
	for _, msg := range messages {
		total += messageTokens(msg)
	}
	if len(toolInfos) > 0 {
		if raw, err := sonic.MarshalString(toolInfos); err == nil {
			total += estimateTokens(raw)
		}
	}
	return total
}
