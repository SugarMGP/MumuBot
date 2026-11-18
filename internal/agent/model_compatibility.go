package agent

import (
	"fmt"

	"mumu-bot/internal/llm"
	"mumu-bot/internal/modelprofiles"

	"github.com/cloudwego/eino/schema"
)

// ModelTarget 保留未保存的目标档案；密钥仅用于本次构建客户端
type ModelTarget struct {
	modelprofiles.Profile
	APIKey            string
	ShrinkWindow      bool
	ReuseUsage        bool
	ConnectionChanged bool
	KeyChanged        bool
}

func lastIncompatibleMessage(messages []*schema.AgenticMessage, target ModelTarget) int {
	last := -1
	for index, message := range messages {
		if message == nil || message.Role != schema.AgenticRoleTypeAssistant {
			continue
		}
		fromResponses := message.ResponseMeta != nil && message.ResponseMeta.OpenAIExtension != nil
		for _, block := range message.ContentBlocks {
			if block == nil {
				continue
			}
			switch block.Type {
			case schema.ContentBlockTypeReasoning:
				if target.ConnectionChanged || (target.KeyChanged && block.Reasoning != nil && block.Reasoning.Signature != "") || fromResponses != (target.Provider == llm.ProviderResponses) {
					last = index
				}
			case schema.ContentBlockTypeAssistantGenText, schema.ContentBlockTypeFunctionToolCall:
			default:
				if target.ConnectionChanged || target.Provider == llm.ProviderChatCompletions {
					last = index
				}
			}
		}
	}
	return last
}

// protocolSummaryPrefix 只消费覆盖不兼容内容所需的闭合前缀
func protocolSummaryPrefix(messages []*schema.AgenticMessage, target ModelTarget) ([]*schema.AgenticMessage, error) {
	last := lastIncompatibleMessage(messages, target)
	if last < 0 {
		return nil, nil
	}
	open := map[string]bool{}
	throughID := uint(0)
	prefix := make([]*schema.AgenticMessage, 0, len(messages))
	for index, message := range messages {
		if message == nil || isHeadEntry(message) {
			continue
		}
		prefix = append(prefix, message)
		for _, block := range message.ContentBlocks {
			if block == nil {
				continue
			}
			if block.FunctionToolCall != nil {
				open[block.FunctionToolCall.CallID] = true
			}
			if block.FunctionToolResult != nil {
				delete(open, block.FunctionToolResult.CallID)
			}
		}
		throughID = max(throughID, transcriptEntryID(message))
		if index >= last && len(open) == 0 {
			end := index
			// ID 检查点必须覆盖实际读取的完整前缀，不能跨过较早入库的 pending
			for next := index + 1; next < len(messages); next++ {
				if id := transcriptEntryID(messages[next]); !isHeadEntry(messages[next]) && id > 0 && id <= throughID {
					end = next
				}
			}
			if end == index {
				return prefix, nil
			}
			last = end
		}
	}
	return nil, fmt.Errorf("incompatible history has an unfinished tool chain")
}
