package llm

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"mumu-bot/internal/agentic"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	toolImageNotice = "工具返回了图片结果，图片在紧随其后的用户消息中。"
	toolMediaNotice = "工具返回了附件结果，附件在紧随其后的用户消息中。"
	toolImageLabel  = "[工具结果图片｜工具=%s｜CallID=%s]"
	toolFileLabel   = "[工具结果附件｜工具=%s｜CallID=%s]"
)

// ToModelMessages 把 transcript 消息转成模型请求；守卫、媒体外移和原生状态都在这里收口
func ToModelMessages(provider, modelName string, input []*agentic.Message) []model.Message {
	guarded := guardToolChains(input)
	adjusted := moveToolMedia(guarded)
	out := make([]model.Message, 0, len(adjusted))
	for _, message := range adjusted {
		out = append(out, toModelMessage(provider, modelName, message))
	}
	return out
}

// FromModelResponse 把模型响应写回 transcript 契约
func FromModelResponse(provider string, response *model.Response) *agentic.Message {
	if response == nil || len(response.Choices) == 0 {
		return nil
	}
	message := fromModelAssistant(provider, response.Choices[0].Message)
	if usage := response.Usage; usage != nil {
		message.ResponseMeta = &agentic.ResponseMeta{TokenUsage: &agentic.TokenUsage{
			PromptTokens:            usage.PromptTokens,
			CompletionTokens:        usage.CompletionTokens,
			TotalTokens:             usage.TotalTokens,
			PromptTokenDetails:      agentic.PromptTokenDetails{CachedTokens: usage.PromptTokensDetails.CachedTokens},
			CompletionTokensDetails: agentic.CompletionTokensDetails{ReasoningTokens: usage.CompletionTokensDetails.ReasoningTokens},
		}}
	}
	return message
}

// fromModelAssistant 把模型侧的 assistant 消息完整还原成 transcript 契约
// 主链响应和压缩器的多步合并共用这一处，原生状态（Responses provider data、Chat reasoning、Gemini 签名）不再各写一份
func fromModelAssistant(provider string, source model.Message) *agentic.Message {
	message := &agentic.Message{Role: agentic.RoleAssistant, Provider: provider}
	var texts []string
	if source.Content != "" {
		texts = append(texts, source.Content)
	}
	for _, part := range source.ContentParts {
		switch part.Type {
		case model.ContentTypeText:
			if part.Text != nil && *part.Text != "" {
				texts = append(texts, *part.Text)
			}
		case model.ContentTypeImage:
			if part.Image != nil {
				message.ContentBlocks = append(message.ContentBlocks, agentic.NewContentBlock(&agentic.AssistantGenImage{
					URL: part.Image.URL, Base64Data: base64.StdEncoding.EncodeToString(part.Image.Data), MIMEType: part.Image.Format,
				}))
			}
		}
	}
	for _, text := range texts {
		message.ContentBlocks = append(message.ContentBlocks, agentic.NewContentBlock(&agentic.AssistantGenText{Text: text}))
	}
	if source.ReasoningContent != "" || source.ReasoningSignature != "" {
		message.ContentBlocks = append(message.ContentBlocks, agentic.NewContentBlock(&agentic.Reasoning{
			Text: source.ReasoningContent, Signature: source.ReasoningSignature,
		}))
	}
	for _, call := range source.ToolCalls {
		toolCall := &agentic.FunctionToolCall{CallID: call.ID, Name: call.Function.Name, Arguments: string(call.Function.Arguments)}
		if signature := toolSignature(call.ExtraFields); signature != "" {
			toolCall.Signature = signature
		}
		message.ContentBlocks = append(message.ContentBlocks, agentic.NewContentBlock(toolCall))
	}
	if source.ProviderData != nil {
		message.ProviderData = &agentic.ProviderData{
			Provider: source.ProviderData.Protocol, Model: source.ProviderData.Model, Payload: source.ProviderData.Payload,
		}
	}
	return message
}

func fromModelTool(source model.Message) *agentic.Message {
	return &agentic.Message{Role: agentic.RoleUser, ContentBlocks: []*agentic.ContentBlock{
		agentic.NewContentBlock(&agentic.FunctionToolResult{
			CallID: source.ToolID, Name: source.ToolName,
			Content: []*agentic.FunctionToolResultContentBlock{{
				Type: agentic.ToolResultPartText,
				Text: &agentic.UserInputText{Text: source.Content},
			}},
		}),
	}}
}

func toolSignature(extra map[string]any) string {
	raw := extra["thought_signature"]
	switch value := raw.(type) {
	case string:
		return value
	case []byte:
		return base64.StdEncoding.EncodeToString(value)
	default:
		return ""
	}
}

func toModelMessage(provider, modelName string, message *agentic.Message) model.Message {
	if message == nil {
		return model.Message{}
	}
	if result := toolResultOf(message); result != nil {
		return model.Message{Role: model.RoleTool, ToolID: result.CallID, ToolName: result.Name, Content: toolResultText(result)}
	}

	out := model.Message{}
	switch message.Role {
	case agentic.RoleSystem:
		out.Role = model.RoleSystem
	case agentic.RoleAssistant:
		out.Role = model.RoleAssistant
	default:
		out.Role = model.RoleUser
	}
	var texts []string
	hasMedia := false
	for _, block := range message.ContentBlocks {
		if block == nil {
			continue
		}
		switch {
		case block.UserInputText != nil:
			texts = append(texts, block.UserInputText.Text)
		case block.AssistantGenText != nil:
			texts = append(texts, block.AssistantGenText.Text)
		case block.UserInputImage != nil, block.UserInputAudio != nil, block.UserInputVideo != nil, block.UserInputFile != nil, block.AssistantGenImage != nil:
			hasMedia = true
		case block.FunctionToolCall != nil && out.Role == model.RoleAssistant:
			out.ToolCalls = append(out.ToolCalls, model.ToolCall{
				Type: "function", ID: block.FunctionToolCall.CallID,
				Function: model.FunctionDefinitionParam{Name: block.FunctionToolCall.Name, Arguments: []byte(validJSONObject(block.FunctionToolCall.Arguments))},
			})
		}
	}
	if !hasMedia {
		out.Content = strings.Join(texts, "\n")
	} else {
		parts := make([]model.ContentPart, 0, len(message.ContentBlocks))
		for _, block := range message.ContentBlocks {
			if block == nil {
				continue
			}
			switch {
			case block.UserInputText != nil:
				parts = append(parts, model.ContentPart{Type: model.ContentTypeText, Text: stringPtr(block.UserInputText.Text)})
			case block.AssistantGenText != nil:
				parts = append(parts, model.ContentPart{Type: model.ContentTypeText, Text: stringPtr(block.AssistantGenText.Text)})
			case block.UserInputImage != nil:
				parts = append(parts, imagePart(block.UserInputImage))
			case block.UserInputAudio != nil:
				parts = append(parts, audioPart(block.UserInputAudio))
			case block.UserInputVideo != nil:
				parts = append(parts, videoPart(block.UserInputVideo))
			case block.UserInputFile != nil:
				parts = append(parts, filePart(block.UserInputFile))
			case block.AssistantGenImage != nil:
				parts = append(parts, model.ContentPart{Type: model.ContentTypeImage, Image: &model.Image{
					URL: block.AssistantGenImage.URL, Format: block.AssistantGenImage.MIMEType,
				}})
			}
		}
		out.ContentParts = parts
	}
	if out.Role == model.RoleAssistant {
		applyNativeState(provider, modelName, message, &out)
	}
	return out
}

func applyNativeState(provider, modelName string, source *agentic.Message, target *model.Message) {
	effective := source.EffectiveProvider()
	switch provider {
	case agentic.ProviderResponses:
		data := source.ProviderData
		if effective == agentic.ProviderResponses && data != nil && data.Model == modelName {
			target.ProviderData = &model.ProviderData{Protocol: data.Provider, Model: data.Model, Payload: data.Payload}
		}
	case agentic.ProviderChat:
		if effective == agentic.ProviderChat {
			if reasoning := reasoningOf(source); reasoning != nil {
				target.ReasoningContent = reasoning.Text
			}
		}
	case agentic.ProviderGemini:
		if effective != agentic.ProviderGemini {
			return
		}
		if reasoning := reasoningOf(source); reasoning != nil {
			target.ReasoningSignature = reasoning.Signature
		}
		calls := toolCallsOf(source)
		for index := range target.ToolCalls {
			if index >= len(calls) || calls[index].Signature == "" {
				continue
			}
			target.ToolCalls[index].ExtraFields = map[string]any{"thought_signature": calls[index].Signature}
		}
	}
}

func reasoningOf(message *agentic.Message) *agentic.Reasoning {
	for _, block := range message.ContentBlocks {
		if block != nil && block.Reasoning != nil {
			return block.Reasoning
		}
	}
	return nil
}

func toolCallsOf(message *agentic.Message) []*agentic.FunctionToolCall {
	calls := make([]*agentic.FunctionToolCall, 0)
	for _, block := range message.ContentBlocks {
		if block != nil && block.FunctionToolCall != nil {
			calls = append(calls, block.FunctionToolCall)
		}
	}
	return calls
}

func toolResultOf(message *agentic.Message) *agentic.FunctionToolResult {
	for _, block := range message.ContentBlocks {
		if block != nil && block.FunctionToolResult != nil {
			return block.FunctionToolResult
		}
	}
	return nil
}

func toolResultText(result *agentic.FunctionToolResult) string {
	var texts []string
	for _, part := range result.Content {
		if part != nil && part.Text != nil {
			texts = append(texts, part.Text.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func imagePart(image *agentic.UserInputImage) model.ContentPart {
	part := model.ContentPart{Type: model.ContentTypeImage, Image: &model.Image{URL: image.URL, Detail: image.Detail, Format: image.MIMEType}}
	if image.Base64Data != "" {
		if data, err := base64.StdEncoding.DecodeString(image.Base64Data); err == nil {
			part.Image.Data = data
		}
	}
	return part
}

func audioPart(audio *agentic.UserInputAudio) model.ContentPart {
	part := model.ContentPart{Type: model.ContentTypeAudio, Audio: &model.Audio{URL: audio.URL, Format: audio.MIMEType}}
	if audio.Base64Data != "" {
		if data, err := base64.StdEncoding.DecodeString(audio.Base64Data); err == nil {
			part.Audio.Data = data
		}
	}
	return part
}

func videoPart(video *agentic.UserInputVideo) model.ContentPart {
	part := model.ContentPart{Type: model.ContentTypeVideo, Video: &model.Video{URL: video.URL, Format: video.MIMEType}}
	if video.Base64Data != "" {
		if data, err := base64.StdEncoding.DecodeString(video.Base64Data); err == nil {
			part.Video.Data = data
		}
	}
	return part
}

func filePart(file *agentic.UserInputFile) model.ContentPart {
	part := model.ContentPart{Type: model.ContentTypeFile, File: &model.File{Name: file.Name, URL: file.URL, FileID: file.FileID, MimeType: file.MIMEType}}
	if file.Base64Data != "" {
		if data, err := base64.StdEncoding.DecodeString(file.Base64Data); err == nil {
			part.File.Data = data
		}
	}
	return part
}

func validJSONObject(arguments string) string {
	trimmed := strings.TrimSpace(arguments)
	if trimmed == "" {
		return "{}"
	}
	var value map[string]any
	if json.Unmarshal([]byte(trimmed), &value) != nil {
		return "{}"
	}
	return trimmed
}

func stringPtr(value string) *string { return &value }

type callRef struct {
	assistant int
	original  string
	wire      string
	name      string
	matched   bool
}

// guardToolChains 保证每个调用都有唯一 CallID 和对应回执，孤立回执不会发往模型
func guardToolChains(input []*agentic.Message) []*agentic.Message {
	used := map[string]bool{}
	byOriginal := map[string][]*callRef{}
	out := make([]*agentic.Message, 0, len(input))
	for _, message := range input {
		if message == nil {
			continue
		}
		if result := toolResultOf(message); result != nil {
			ref := matchCall(byOriginal[result.CallID])
			if ref == nil {
				continue
			}
			ref.matched = true
			if ref.wire == result.CallID {
				out = append(out, message)
				continue
			}
			copied := cloneMessage(message)
			toolResultOf(copied).CallID = ref.wire
			out = append(out, copied)
			continue
		}
		if message.Role == agentic.RoleAssistant && hasToolCall(message) {
			copied := cloneMessage(message)
			position := len(out)
			for _, block := range copied.ContentBlocks {
				if block == nil || block.FunctionToolCall == nil {
					continue
				}
				call := block.FunctionToolCall
				original := call.CallID
				if original == "" {
					original = fmt.Sprintf("call_missing_%d", position)
				}
				wire := original
				for suffix := 1; used[wire]; suffix++ {
					wire = fmt.Sprintf("%s_dup%d", original, suffix)
				}
				used[wire] = true
				call.CallID = wire
				ref := &callRef{assistant: position, original: original, wire: wire, name: call.Name}
				byOriginal[original] = append(byOriginal[original], ref)
			}
			out = append(out, copied)
			continue
		}
		out = append(out, message)
	}
	repairs := map[int][]*agentic.Message{}
	for _, refs := range byOriginal {
		for _, ref := range refs {
			if ref.matched {
				continue
			}
			repairs[ref.assistant] = append(repairs[ref.assistant], syntheticToolResult(ref))
		}
	}
	if len(repairs) == 0 {
		return out
	}
	final := make([]*agentic.Message, 0, len(out)+len(repairs))
	for index, message := range out {
		final = append(final, message)
		final = append(final, repairs[index]...)
	}
	return final
}

func hasToolCall(message *agentic.Message) bool {
	for _, block := range message.ContentBlocks {
		if block != nil && block.FunctionToolCall != nil {
			return true
		}
	}
	return false
}

func matchCall(refs []*callRef) *callRef {
	for _, ref := range refs {
		if !ref.matched {
			return ref
		}
	}
	return nil
}

func syntheticToolResult(ref *callRef) *agentic.Message {
	return &agentic.Message{Role: agentic.RoleUser, ContentBlocks: []*agentic.ContentBlock{
		agentic.NewContentBlock(&agentic.FunctionToolResult{
			CallID: ref.wire, Name: ref.name,
			Content: []*agentic.FunctionToolResultContentBlock{{
				Type: agentic.ToolResultPartText,
				Text: &agentic.UserInputText{Text: "工具未能执行：会话在回执前中断，没有可用结果。"},
			}},
		}),
	}}
}

func cloneMessage(message *agentic.Message) *agentic.Message {
	if message == nil {
		return nil
	}
	copied := *message
	copied.ContentBlocks = make([]*agentic.ContentBlock, len(message.ContentBlocks))
	copy(copied.ContentBlocks, message.ContentBlocks)
	for index, block := range copied.ContentBlocks {
		if block == nil {
			continue
		}
		blockCopy := *block
		copied.ContentBlocks[index] = &blockCopy
	}
	return &copied
}

// moveToolMedia 把工具回执里的媒体移到整组回执之后的 user 消息；三个适配器都不接受工具结果媒体
func moveToolMedia(input []*agentic.Message) []*agentic.Message {
	out := make([]*agentic.Message, 0, len(input)+1)
	pending := make([]*agentic.ContentBlock, 0)
	flush := func() {
		if len(pending) == 0 {
			return
		}
		message := &agentic.Message{Role: agentic.RoleUser, Extra: map[string]any{"source": "tool_result_media"}}
		message.ContentBlocks = append(message.ContentBlocks, pending...)
		out = append(out, message)
		pending = pending[:0]
	}
	for _, message := range input {
		if message == nil {
			continue
		}
		rewritten, media, toolOnly := rewriteToolMedia(message)
		if !toolOnly {
			flush()
		}
		out = append(out, rewritten)
		pending = append(pending, media...)
		if !toolOnly {
			flush()
		}
	}
	flush()
	return out
}

func rewriteToolMedia(message *agentic.Message) (*agentic.Message, []*agentic.ContentBlock, bool) {
	blocks := make([]*agentic.ContentBlock, 0, len(message.ContentBlocks))
	var media []*agentic.ContentBlock
	hasResult := false
	toolOnly := true
	changed := false
	for _, block := range message.ContentBlocks {
		if block == nil || block.FunctionToolResult == nil {
			toolOnly = false
			blocks = append(blocks, block)
			continue
		}
		hasResult = true
		result := block.FunctionToolResult
		content := make([]*agentic.FunctionToolResultContentBlock, 0, len(result.Content))
		var moved []*agentic.ContentBlock
		imageOnly := true
		for _, part := range result.Content {
			if part == nil {
				continue
			}
			switch part.Type {
			case agentic.ToolResultPartText:
				content = append(content, part)
			case agentic.ToolResultPartImage:
				if part.Image != nil {
					moved = append(moved, agentic.NewContentBlock(part.Image))
				}
			case agentic.ToolResultPartAudio:
				if part.Audio != nil {
					imageOnly = false
					moved = append(moved, agentic.NewContentBlock(part.Audio))
				}
			case agentic.ToolResultPartVideo:
				if part.Video != nil {
					imageOnly = false
					moved = append(moved, agentic.NewContentBlock(part.Video))
				}
			case agentic.ToolResultPartFile:
				if part.File != nil {
					imageOnly = false
					moved = append(moved, agentic.NewContentBlock(part.File))
				}
			}
		}
		if len(moved) == 0 {
			blocks = append(blocks, block)
			continue
		}
		changed = true
		if len(content) == 0 {
			text := toolMediaNotice
			if imageOnly {
				text = toolImageNotice
			}
			content = append(content, &agentic.FunctionToolResultContentBlock{
				Type: agentic.ToolResultPartText,
				Text: &agentic.UserInputText{Text: text},
			})
		}
		blockCopy := *block
		resultCopy := *result
		resultCopy.Content = content
		blockCopy.FunctionToolResult = &resultCopy
		blocks = append(blocks, &blockCopy)
		label := toolImageLabel
		if !imageOnly {
			label = toolFileLabel
		}
		media = append(media, agentic.NewContentBlock(&agentic.UserInputText{Text: fmt.Sprintf(label, result.Name, result.CallID)}))
		media = append(media, moved...)
	}
	if !hasResult {
		toolOnly = false
	}
	if !changed {
		return message, nil, toolOnly
	}
	copied := cloneMessage(message)
	copied.ContentBlocks = blocks
	return copied, media, toolOnly
}

// FromModelMessages 把运行时内部补出的 assistant/tool 消息还原成 transcript 契约
// 压缩器的工具结果不落 transcript，需要在下一次请求前拼回压缩器自己的输入
// provider 决定 assistant 的原生状态（Responses provider data、Gemini 签名）能否继续回放
func FromModelMessages(provider string, messages []model.Message) []*agentic.Message {
	out := make([]*agentic.Message, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case model.RoleAssistant:
			item := fromModelAssistant(provider, message)
			if len(item.ContentBlocks) > 0 || item.ProviderData != nil {
				out = append(out, item)
			}
		case model.RoleTool:
			out = append(out, fromModelTool(message))
		}
	}
	return out
}
