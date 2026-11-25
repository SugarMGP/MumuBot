// Package agentic 定义 transcript 与模型之间的消息契约。
//
// 结构和 JSON tag 与既有 payload 保持一致，新增字段全部可选，旧数据可直接解码。
package agentic

import "encoding/json"

// Role 是消息角色
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// BlockType 是内容块类型
type BlockType string

const (
	BlockTypeReasoning          BlockType = "reasoning"
	BlockTypeUserInputText      BlockType = "user_input_text"
	BlockTypeUserInputImage     BlockType = "user_input_image"
	BlockTypeUserInputAudio     BlockType = "user_input_audio"
	BlockTypeUserInputVideo     BlockType = "user_input_video"
	BlockTypeUserInputFile      BlockType = "user_input_file"
	BlockTypeAssistantGenText   BlockType = "assistant_gen_text"
	BlockTypeAssistantGenImage  BlockType = "assistant_gen_image"
	BlockTypeAssistantGenAudio  BlockType = "assistant_gen_audio"
	BlockTypeAssistantGenVideo  BlockType = "assistant_gen_video"
	BlockTypeFunctionToolCall   BlockType = "function_tool_call"
	BlockTypeFunctionToolResult BlockType = "function_tool_result"
	BlockTypeServerToolCall     BlockType = "server_tool_call"
	BlockTypeServerToolResult   BlockType = "server_tool_result"
	BlockTypeMCPToolCall        BlockType = "mcp_tool_call"
	BlockTypeMCPToolResult      BlockType = "mcp_tool_result"
	BlockTypeMCPListToolsResult BlockType = "mcp_list_tools_result"
)

// ToolResultPart 是工具回执内部内容块类型
const (
	ToolResultPartText  = "text"
	ToolResultPartImage = "image"
	ToolResultPartAudio = "audio"
	ToolResultPartVideo = "video"
	ToolResultPartFile  = "file"
)

// Message 是一条 transcript 消息；Provider 与 ProviderData 记录 assistant 的协议原生状态
type Message struct {
	Role          Role            `json:"role"`
	ContentBlocks []*ContentBlock `json:"content_blocks,omitempty"`
	ResponseMeta  *ResponseMeta   `json:"response_meta,omitempty"`
	Extra         map[string]any  `json:"extra,omitempty"`
	// Provider 记录产生该 assistant 消息的协议；旧数据为空
	Provider string `json:"provider,omitempty"`
	// ProviderData 是 Responses 的原始输出 item 数组，原样回传
	ProviderData *ProviderData `json:"provider_data,omitempty"`
}

// ResponseMeta 保留 token 用量与旧数据的协议标记
type ResponseMeta struct {
	TokenUsage *TokenUsage `json:"token_usage,omitempty"`
	// OpenAIExtension 只用于识别旧 Responses 行和估算旧 reasoning 用量
	OpenAIExtension *OpenAIResponseMetaExtension `json:"openai_extension,omitempty"`
}

// OpenAIResponseMetaExtension 是旧数据里 openai_extension 的最小可读子集
type OpenAIResponseMetaExtension struct {
	ID     string `json:"id,omitempty"`
	Status string `json:"status,omitempty"`
}

// ProviderData 是模型适配器返回的协议原生记录
type ProviderData struct {
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	Payload  json.RawMessage `json:"payload"`
}

// ContentBlock 是消息里的单个内容块
type ContentBlock struct {
	Type BlockType `json:"type"`

	Reasoning          *Reasoning          `json:"reasoning,omitempty"`
	UserInputText      *UserInputText      `json:"user_input_text,omitempty"`
	UserInputImage     *UserInputImage     `json:"user_input_image,omitempty"`
	UserInputAudio     *UserInputAudio     `json:"user_input_audio,omitempty"`
	UserInputVideo     *UserInputVideo     `json:"user_input_video,omitempty"`
	UserInputFile      *UserInputFile      `json:"user_input_file,omitempty"`
	AssistantGenText   *AssistantGenText   `json:"assistant_gen_text,omitempty"`
	AssistantGenImage  *AssistantGenImage  `json:"assistant_gen_image,omitempty"`
	AssistantGenAudio  *AssistantGenAudio  `json:"assistant_gen_audio,omitempty"`
	AssistantGenVideo  *AssistantGenVideo  `json:"assistant_gen_video,omitempty"`
	FunctionToolCall   *FunctionToolCall   `json:"function_tool_call,omitempty"`
	FunctionToolResult *FunctionToolResult `json:"function_tool_result,omitempty"`

	// 服务端工具与 MCP 记录只用于旧数据解码与切换材料展开
	ServerToolCall          *ServerToolCall          `json:"server_tool_call,omitempty"`
	ServerToolResult        *ServerToolResult        `json:"server_tool_result,omitempty"`
	MCPToolCall             *MCPToolCall             `json:"mcp_tool_call,omitempty"`
	MCPToolResult           *MCPToolResult           `json:"mcp_tool_result,omitempty"`
	MCPListToolsResult      *MCPListToolsResult      `json:"mcp_list_tools_result,omitempty"`
	MCPToolApprovalRequest  *MCPToolApprovalRequest  `json:"mcp_tool_approval_request,omitempty"`
	MCPToolApprovalResponse *MCPToolApprovalResponse `json:"mcp_tool_approval_response,omitempty"`

	Extra map[string]any `json:"extra,omitempty"`
}

// Reasoning 是模型推理内容
type Reasoning struct {
	Text string `json:"text,omitempty"`
	// Signature 是 Gemini 的消息级 thought signature（base64）
	Signature string `json:"signature,omitempty"`
	// OpenAIExtension 只用于旧数据的 reasoning 原文估算
	OpenAIExtension *OpenAIReasoningExtension `json:"openai_extension,omitempty"`
}

// OpenAIReasoningExtension 是旧数据里 reasoning.openai_extension 的最小可读子集
type OpenAIReasoningExtension struct {
	Content []*OpenAIReasoningContent `json:"content,omitempty"`
}

// OpenAIReasoningContent 是旧数据里的一段 reasoning 原文
type OpenAIReasoningContent struct {
	Text string `json:"text,omitempty"`
}

// UserInputText 是文本输入
type UserInputText struct {
	Text string `json:"text,omitempty"`
}

// UserInputImage 是图片输入
type UserInputImage struct {
	URL        string `json:"url,omitempty"`
	Base64Data string `json:"base64_data,omitempty"`
	FileID     string `json:"file_id,omitempty"`
	MIMEType   string `json:"mime_type,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

// UserInputAudio 是音频输入
type UserInputAudio struct {
	URL        string `json:"url,omitempty"`
	Base64Data string `json:"base64_data,omitempty"`
	FileID     string `json:"file_id,omitempty"`
	MIMEType   string `json:"mime_type,omitempty"`
}

// UserInputVideo 是视频输入
type UserInputVideo struct {
	URL        string `json:"url,omitempty"`
	Base64Data string `json:"base64_data,omitempty"`
	FileID     string `json:"file_id,omitempty"`
	MIMEType   string `json:"mime_type,omitempty"`
}

// UserInputFile 是文件输入
type UserInputFile struct {
	URL        string `json:"url,omitempty"`
	Name       string `json:"name,omitempty"`
	Base64Data string `json:"base64_data,omitempty"`
	FileID     string `json:"file_id,omitempty"`
	MIMEType   string `json:"mime_type,omitempty"`
}

// AssistantGenText 是模型生成的文本
type AssistantGenText struct {
	Text string `json:"text,omitempty"`
}

// AssistantGenImage 是模型生成的图片
type AssistantGenImage struct {
	URL        string `json:"url,omitempty"`
	Base64Data string `json:"base64_data,omitempty"`
	MIMEType   string `json:"mime_type,omitempty"`
}

// AssistantGenAudio 是模型生成的音频
type AssistantGenAudio struct {
	URL        string `json:"url,omitempty"`
	Base64Data string `json:"base64_data,omitempty"`
	MIMEType   string `json:"mime_type,omitempty"`
}

// AssistantGenVideo 是模型生成的视频
type AssistantGenVideo struct {
	URL        string `json:"url,omitempty"`
	Base64Data string `json:"base64_data,omitempty"`
	MIMEType   string `json:"mime_type,omitempty"`
}

// FunctionToolCall 是模型发起的函数工具调用
type FunctionToolCall struct {
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
	// Signature 是 Gemini 每个函数调用的 thought signature（base64）
	Signature string `json:"signature,omitempty"`
}

// FunctionToolResult 是函数工具回执
type FunctionToolResult struct {
	CallID  string                            `json:"call_id,omitempty"`
	Name    string                            `json:"name"`
	Content []*FunctionToolResultContentBlock `json:"content,omitempty"`
}

// FunctionToolResultContentBlock 是工具回执里的一个内容块
type FunctionToolResultContentBlock struct {
	Type  string          `json:"type"`
	Text  *UserInputText  `json:"text,omitempty"`
	Image *UserInputImage `json:"image,omitempty"`
	Audio *UserInputAudio `json:"audio,omitempty"`
	Video *UserInputVideo `json:"video,omitempty"`
	File  *UserInputFile  `json:"file,omitempty"`
	Extra map[string]any  `json:"extra,omitempty"`
}

// ServerToolCall 是模型服务端工具调用记录
type ServerToolCall struct {
	Name      string `json:"name"`
	CallID    string `json:"call_id,omitempty"`
	Arguments any    `json:"arguments,omitempty"`
}

// ServerToolResult 是模型服务端工具结果记录
type ServerToolResult struct {
	Name    string `json:"name"`
	CallID  string `json:"call_id,omitempty"`
	Content any    `json:"content,omitempty"`
}

// MCPToolCall 是模型服务端 MCP 工具调用记录
type MCPToolCall struct {
	ServerLabel       string `json:"server_label,omitempty"`
	ApprovalRequestID string `json:"approval_request_id,omitempty"`
	CallID            string `json:"call_id,omitempty"`
	Name              string `json:"name"`
	Arguments         string `json:"arguments,omitempty"`
}

// MCPToolResult 是模型服务端 MCP 工具结果记录
type MCPToolResult struct {
	ServerLabel string `json:"server_label,omitempty"`
	CallID      string `json:"call_id,omitempty"`
	Name        string `json:"name"`
	Content     string `json:"content,omitempty"`
	Error       any    `json:"error,omitempty"`
}

// MCPListToolsResult 是模型服务端 MCP 工具清单记录
type MCPListToolsResult struct {
	ServerLabel string `json:"server_label,omitempty"`
	Tools       any    `json:"tools,omitempty"`
	Error       string `json:"error,omitempty"`
}

// MCPToolApprovalRequest 是模型服务端 MCP 审批请求记录
type MCPToolApprovalRequest struct {
	ServerLabel       string `json:"server_label,omitempty"`
	ApprovalRequestID string `json:"approval_request_id,omitempty"`
	Name              string `json:"name,omitempty"`
}

// MCPToolApprovalResponse 是模型服务端 MCP 审批结果记录
type MCPToolApprovalResponse struct {
	ApprovalRequestID string `json:"approval_request_id,omitempty"`
	Approve           bool   `json:"approve,omitempty"`
}

// TokenUsage 是模型请求的 token 用量
type TokenUsage struct {
	PromptTokens            int                     `json:"prompt_tokens"`
	PromptTokenDetails      PromptTokenDetails      `json:"prompt_token_details"`
	CompletionTokens        int                     `json:"completion_tokens"`
	TotalTokens             int                     `json:"total_tokens"`
	CompletionTokensDetails CompletionTokensDetails `json:"completion_token_details"`
}

// PromptTokenDetails 是输入 token 的细分
type PromptTokenDetails struct {
	CachedTokens     int `json:"cached_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
}

// CompletionTokensDetails 是输出 token 的细分
type CompletionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens,omitempty"`
}

// NewContentBlock 按内容类型构造内容块
func NewContentBlock(content any) *ContentBlock {
	switch value := content.(type) {
	case *Reasoning:
		return &ContentBlock{Type: BlockTypeReasoning, Reasoning: value}
	case *UserInputText:
		return &ContentBlock{Type: BlockTypeUserInputText, UserInputText: value}
	case *UserInputImage:
		return &ContentBlock{Type: BlockTypeUserInputImage, UserInputImage: value}
	case *UserInputAudio:
		return &ContentBlock{Type: BlockTypeUserInputAudio, UserInputAudio: value}
	case *UserInputVideo:
		return &ContentBlock{Type: BlockTypeUserInputVideo, UserInputVideo: value}
	case *UserInputFile:
		return &ContentBlock{Type: BlockTypeUserInputFile, UserInputFile: value}
	case *AssistantGenText:
		return &ContentBlock{Type: BlockTypeAssistantGenText, AssistantGenText: value}
	case *AssistantGenImage:
		return &ContentBlock{Type: BlockTypeAssistantGenImage, AssistantGenImage: value}
	case *AssistantGenAudio:
		return &ContentBlock{Type: BlockTypeAssistantGenAudio, AssistantGenAudio: value}
	case *AssistantGenVideo:
		return &ContentBlock{Type: BlockTypeAssistantGenVideo, AssistantGenVideo: value}
	case *FunctionToolCall:
		return &ContentBlock{Type: BlockTypeFunctionToolCall, FunctionToolCall: value}
	case *FunctionToolResult:
		return &ContentBlock{Type: BlockTypeFunctionToolResult, FunctionToolResult: value}
	default:
		return &ContentBlock{}
	}
}

// UserAgenticMessage 构造用户文本消息
func UserAgenticMessage(text string) *Message {
	return &Message{Role: RoleUser, ContentBlocks: []*ContentBlock{{Type: BlockTypeUserInputText, UserInputText: &UserInputText{Text: text}}}}
}

// SystemAgenticMessage 构造系统文本消息
func SystemAgenticMessage(text string) *Message {
	return &Message{Role: RoleSystem, ContentBlocks: []*ContentBlock{{Type: BlockTypeUserInputText, UserInputText: &UserInputText{Text: text}}}}
}

// 协议标识；模型档案与 transcript 共用同一组值
const (
	ProviderResponses = "openai_responses"
	ProviderChat      = "openai_chat_completions"
	ProviderGemini    = "gemini"
)

// EffectiveProvider 返回 assistant 行的协议来源；旧数据按 response_meta 推断
func (m *Message) EffectiveProvider() string {
	if m == nil {
		return ""
	}
	if m.Provider != "" {
		return m.Provider
	}
	if m.ResponseMeta != nil && m.ResponseMeta.OpenAIExtension != nil {
		return ProviderResponses
	}
	return ProviderChat
}
