package tools

import (
	"context"
	"mumu-bot/internal/memory"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// ==================== 保存记忆工具 ====================

// SaveMemoryInput 保存记忆的输入参数
type SaveMemoryInput struct {
	// Content 要记住的内容，用自然语言描述
	Content string `json:"content" jsonschema:"description=要记住的内容，用自然语言描述清楚"`
	// RelatedUserID 相关的用户ID（可选）
	RelatedUserID int64 `json:"related_user_id,omitempty" jsonschema:"description=如果这条记忆与某个群友相关，填写其QQ号，否则填0"`
	// EvidenceMessageID 作为事实来源的消息 ID（可选）
	EvidenceMessageID int64 `json:"evidence_message_id,omitempty" jsonschema:"description=证据消息 ID，可从聊天中的 #消息ID 或工具结果获取；留空时使用本轮最后一条非机器人消息"`
}

// SaveMemoryOutput 保存记忆的输出
type SaveMemoryOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// saveMemoryFunc 保存记忆的实际实现
func saveMemoryFunc(ctx context.Context, input *SaveMemoryInput) (*SaveMemoryOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &SaveMemoryOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}

	if input.Content == "" {
		return &SaveMemoryOutput{Success: false, Message: "内容不能为空"}, nil
	}

	evidenceMessageID := input.EvidenceMessageID
	if evidenceMessageID == 0 {
		evidenceMessageID = tc.EvidenceMessageID
	}
	if evidenceMessageID <= 0 {
		return &SaveMemoryOutput{Success: false, Message: "当前消息来源缺失，暂时不能写入长期记忆"}, nil
	}
	source, err := tc.MemoryMgr.GetMessageLogByID(evidenceMessageID)
	if err != nil || source.GroupID != tc.GroupID {
		return &SaveMemoryOutput{Success: false, Message: "证据消息不存在或不属于当前群，暂时不能写入长期记忆"}, nil
	}

	selfID := int64(0)
	if tc.Bot != nil {
		selfID = tc.Bot.GetSelfID()
	}

	mem, action, err := tc.MemoryMgr.IngestMemory(ctx, memory.MemoryIngestInput{
		GroupID:       tc.GroupID,
		RelatedUserID: input.RelatedUserID,
		SelfID:        selfID,
		Content:       input.Content,
		MessageLogID:  &source.ID,
	})
	if err != nil {
		return &SaveMemoryOutput{Success: false, Message: err.Error()}, nil
	}
	if mem == nil {
		return &SaveMemoryOutput{Success: true, Message: "这条信息先不进入长期记忆"}, nil
	}
	tc.MarkActed()

	switch action {
	case "deduplicated":
		return &SaveMemoryOutput{Success: true, Message: "已补充到已有记忆"}, nil
	case "merged":
		return &SaveMemoryOutput{Success: true, Message: "已合并到已有记忆"}, nil
	default:
		return &SaveMemoryOutput{Success: true, Message: "已记住"}, nil
	}
}

// NewSaveMemoryTool 创建保存记忆工具
func NewSaveMemoryTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"saveMemory",
		`保存值得记住的信息。可传 evidence_message_id 指向聊天或工具结果中的具体消息，包括你刚刚说过的话；留空时使用本轮最后一条非机器人消息。
适合保存的内容包括：稳定事实、长期偏好、群规边界、持续目标、值得追踪的经历。
普通闲聊、短期待回复、临时口嗨不要保存。`,
		saveMemoryFunc,
	)
}

// ==================== 查询记忆工具 ====================

// QueryMemoryInput 查询记忆的输入参数
type QueryMemoryInput struct {
	// Query 搜索关键词或描述
	Query string `json:"query" jsonschema:"description=搜索关键词或自然语言描述"`
	// Type 限定记忆类型（可选）
	Scope string `json:"scope,omitempty" jsonschema:"enum=,enum=group,enum=self,enum=member,description=筛选记忆主体范围（空字符串时不筛选）"`
	// Scoped 是否只搜索当前聊天群的记忆
	Scoped bool `json:"scoped,omitempty" jsonschema:"description=是否只搜索当前聊天群的记忆，默认false"`
	// Limit 返回结果数量限制，默认10，最大50
	Limit int `json:"limit,omitempty" jsonschema:"description=返回结果数量限制，默认10，最大50"`
}

// QueryMemoryOutput 查询记忆的输出
type QueryMemoryOutput struct {
	Success  bool                     `json:"success"`
	Count    int                      `json:"count"`
	Memories []map[string]interface{} `json:"memories,omitempty"`
	Message  string                   `json:"message,omitempty"`
}

// queryMemoryFunc 查询记忆的实际实现
func queryMemoryFunc(ctx context.Context, input *QueryMemoryInput) (*QueryMemoryOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil {
		return &QueryMemoryOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}

	if input.Query == "" {
		return &QueryMemoryOutput{Success: false, Message: "查询内容不能为空"}, nil
	}

	// 根据开关决定是否限制群 ID
	groupID := int64(0)
	if input.Scoped {
		groupID = tc.GroupID
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	scope := memory.MemoryScope(strings.TrimSpace(input.Scope))
	memories, err := tc.MemoryMgr.QueryMemory(ctx, input.Query, groupID, scope, limit)
	if err != nil {
		return &QueryMemoryOutput{Success: false, Message: err.Error()}, nil
	}

	results := make([]map[string]interface{}, 0, len(memories))
	for _, m := range memories {
		results = append(results, map[string]interface{}{
			"scope":      m.Scope,
			"kind":       m.Kind,
			"content":    m.Content,
			"created_at": m.CreatedAt.Format("2006-01-02 15:04"),
		})
	}

	return &QueryMemoryOutput{
		Success:  true,
		Count:    len(results),
		Memories: results,
	}, nil
}

// NewQueryMemoryTool 创建查询记忆工具
func NewQueryMemoryTool() (tool.InvokableTool, error) {
	return utils.InferTool(
		"queryMemory",
		`搜索你的记忆，找到相关的信息。可以查询关于某个话题、某个人、或者某次经历的记忆。

【scoped 参数使用指南】
- scoped=false（默认）：搜索所有群的记忆，适合查找自身经历、过往事件等
- scoped=true：只搜索当前群的记忆，大部分时候不需要，因为各个群里的记忆通常是相关联的
`,
		queryMemoryFunc,
	)
}
