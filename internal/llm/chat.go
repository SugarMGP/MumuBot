package llm

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

const chatToolImageNotice = "工具返回了图片结果，图片在紧随其后的用户消息中。"

// chatCompatibleModel 把 Chat tool 消息不能承载的图片移到工具组后的 user 消息
// shortcut: Chat 没有原生工具图片字段，协议补齐后改为直接关联工具回执
type chatCompatibleModel struct {
	model.AgenticModel
}

func (m *chatCompatibleModel) GetType() string {
	name, _ := components.GetType(m.AgenticModel)
	return name
}

func (m *chatCompatibleModel) IsCallbacksEnabled() bool {
	return components.IsCallbacksEnabled(m.AgenticModel)
}

func (m *chatCompatibleModel) Generate(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.AgenticMessage, error) {
	return m.AgenticModel.Generate(ctx, chatCompatibleMessages(input), opts...)
}

func (m *chatCompatibleModel) Stream(ctx context.Context, input []*schema.AgenticMessage, opts ...model.Option) (*schema.StreamReader[*schema.AgenticMessage], error) {
	return m.AgenticModel.Stream(ctx, chatCompatibleMessages(input), opts...)
}

type chatToolMedia struct {
	name   string
	callID string
	blocks []*schema.ContentBlock
}

func chatCompatibleMessages(input []*schema.AgenticMessage) []*schema.AgenticMessage {
	result := make([]*schema.AgenticMessage, 0, len(input)+1)
	pending := make([]chatToolMedia, 0)
	flush := func() {
		if len(pending) == 0 {
			return
		}
		blocks := make([]*schema.ContentBlock, 0, len(pending)*2)
		for _, item := range pending {
			blocks = append(blocks, schema.NewContentBlock(&schema.UserInputText{
				Text: fmt.Sprintf("[工具结果图片｜工具=%s｜CallID=%s]", item.name, item.callID),
			}))
			blocks = append(blocks, item.blocks...)
		}
		result = append(result, &schema.AgenticMessage{
			Role:          schema.AgenticRoleTypeUser,
			ContentBlocks: blocks,
			Extra:         map[string]any{"source": "tool_result_media"},
		})
		pending = pending[:0]
	}

	for _, message := range input {
		rewritten, media, toolResultsOnly := rewriteChatToolResults(message)
		if !toolResultsOnly {
			flush()
		}
		result = append(result, rewritten)
		pending = append(pending, media...)
		if !toolResultsOnly {
			flush()
		}
	}
	flush()
	return result
}

func rewriteChatToolResults(message *schema.AgenticMessage) (*schema.AgenticMessage, []chatToolMedia, bool) {
	if message == nil {
		return nil, nil, false
	}
	blocks := make([]*schema.ContentBlock, 0, len(message.ContentBlocks))
	media := make([]chatToolMedia, 0)
	hasToolResult := false
	toolResultsOnly := true
	changed := false
	for _, block := range message.ContentBlocks {
		if block == nil {
			blocks = append(blocks, nil)
			continue
		}
		if block.Type != schema.ContentBlockTypeFunctionToolResult || block.FunctionToolResult == nil {
			toolResultsOnly = false
			blocks = append(blocks, block)
			continue
		}
		hasToolResult = true
		toolResult := block.FunctionToolResult
		toolContent := make([]*schema.FunctionToolResultContentBlock, 0, len(toolResult.Content))
		mediaBlocks := make([]*schema.ContentBlock, 0)
		for _, part := range toolResult.Content {
			if part == nil {
				continue
			}
			if part.Type == schema.FunctionToolResultContentBlockTypeImage {
				if part.Image != nil {
					mediaBlocks = append(mediaBlocks, &schema.ContentBlock{Type: schema.ContentBlockTypeUserInputImage, UserInputImage: part.Image, Extra: part.Extra})
				}
				continue
			}
			toolContent = append(toolContent, part)
		}
		if len(mediaBlocks) == 0 {
			blocks = append(blocks, block)
			continue
		}
		changed = true
		if len(toolContent) == 0 {
			toolContent = append(toolContent, &schema.FunctionToolResultContentBlock{
				Type: schema.FunctionToolResultContentBlockTypeText,
				Text: &schema.UserInputText{Text: chatToolImageNotice},
			})
		}
		resultCopy := *toolResult
		resultCopy.Content = toolContent
		blockCopy := *block
		blockCopy.FunctionToolResult = &resultCopy
		blocks = append(blocks, &blockCopy)
		media = append(media, chatToolMedia{name: toolResult.Name, callID: toolResult.CallID, blocks: mediaBlocks})
	}
	if !hasToolResult {
		toolResultsOnly = false
	}
	if !changed {
		return message, nil, toolResultsOnly
	}
	messageCopy := *message
	messageCopy.ContentBlocks = blocks
	return &messageCopy, media, toolResultsOnly
}
