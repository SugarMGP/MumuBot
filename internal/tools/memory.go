package tools

import (
	"context"
	"errors"
	"strings"

	"mumu-bot/internal/memory"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type SaveMemoryInput struct {
	SubjectUserID       *int64   `json:"subject_user_id" jsonschema:"description=必填；-1 表示你自己，0 表示群组，正数表示成员 QQ"`
	Kind                string   `json:"kind" jsonschema:"enum=fact,enum=episode,enum=preference,enum=constraint,enum=goal,description=记忆类型；偏好和目标不能写成 fact"`
	Content             string   `json:"content" jsonschema:"description=包含当前昵称、脱离原句仍可理解的完整自然语言命题"`
	EvidenceMessageRefs []string `json:"evidence_message_refs" jsonschema:"description=必填，1 到 8 条聊天中的消息编号"`
}

type SaveMemoryOutput struct {
	Success bool   `json:"success"`
	Status  string `json:"status"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

func saveMemoryFunc(ctx context.Context, input *SaveMemoryInput) (*SaveMemoryOutput, error) {
	if input == nil {
		return rejectedMemory("invalid_input", "记忆参数不能为空"), nil
	}
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return rejectedMemory("unavailable", "工具上下文未初始化"), nil
	}
	evidenceMessageIDs := make([]int64, 0, len(input.EvidenceMessageRefs))
	for _, ref := range input.EvidenceMessageRefs {
		messageID, ok := tc.ResolveMessageRef(ref)
		if !ok {
			return rejectedMemory("invalid_evidence", "证据中包含不属于当前对话的消息编号"), nil
		}
		evidenceMessageIDs = append(evidenceMessageIDs, messageID)
	}
	selfID := int64(0)
	if tc.Bot != nil {
		selfID = tc.Bot.GetSelfID()
	}
	raw := memory.RawMemoryClaim{SubjectUserID: input.SubjectUserID, Kind: input.Kind, Content: input.Content, EvidenceMessageIDs: evidenceMessageIDs}
	storeCtx := memory.StoreClaimsContext{GroupID: tc.GroupID, SelfID: selfID, SnapshotOneBotMessageID: tc.SnapshotMessageID}
	claims, err := tc.MemoryMgr.NormalizeAndValidateClaims(ctx, storeCtx, []memory.RawMemoryClaim{raw})
	if err != nil {
		return memoryErrorOutput(err), nil
	}
	results, err := tc.MemoryMgr.StoreClaims(ctx, storeCtx, claims)
	if err != nil {
		return memoryErrorOutput(err), nil
	}
	if len(results) != 1 {
		return rejectedMemory("rejected", "记忆没有通过验证"), nil
	}
	tc.MarkActed()
	message := "已记住"
	if results[0].Action == "deduplicated" {
		message = "已补充到已有记忆"
	} else if results[0].Action == "merged" {
		message = "已合并到已有记忆"
	}
	return &SaveMemoryOutput{Success: true, Status: results[0].Action, Message: message}, nil
}

func rejectedMemory(code, message string) *SaveMemoryOutput {
	return &SaveMemoryOutput{Success: false, Status: "rejected", Code: code, Message: message}
}

func memoryErrorOutput(err error) *SaveMemoryOutput {
	var validation *memory.ClaimValidationError
	if errors.As(err, &validation) {
		return rejectedMemory(validation.Code, validation.Error())
	}
	return rejectedMemory("store_failed", err.Error())
}

func NewSaveMemoryTool() (tool.InvokableTool, error) {
	return utils.InferTool("saveMemory", "保存值得跨会话记住的信息。\n长期记忆命题必须遵守以下规则：\n- 只记录跨会话仍有用的稳定信息。一次性动作、临时情绪、调侃、口嗨、争辩、是否在线和普通聊天过程不保存为长期记忆。当前对话中的临时意见、即时状态、过程性讨论和对本轮交流的评论，也不要保存；不确定时不要提交。\n- subject_user_id 必须是真正执行、持有或经历该命题的主体：-1 表示机器人自身，0 表示群组；正数只能是证据消息作者或回复目标。不能把别人说到的人、别人准备做的事或对机器人的讨论记到当前说话者或机器人名下；无法从证据可靠确定 QQ 时省略该命题。\n- content 直接写包含当前昵称的完整自然语言命题，例如“小明偏好简短直接的回复”。命题必须脱离原对话仍可独立理解；把“我、你、他、对方、这个、那个”等依赖上下文的指代改成证据中明确的人或事，无法消解时省略。\n- kind 必须互斥地判断：持续喜欢、厌恶或选择倾向用 preference；必须、禁止或边界用 constraint；尚未完成的计划或承诺用 goal；有明确边界的过去经历用 episode；只有前四类都不成立的稳定属性或关系才用 fact。\n- 同一事件或命题应合并成一条，不按单句拆分。每条命题提供 1 到 8 条原始消息证据，这些证据必须共同支持该条命题的主体、指代和正文；证据不足或含义不确定时不要提交。", saveMemoryFunc)
}

type QueryMemoryInput struct {
	Query         string            `json:"query" jsonschema:"description=搜索关键词或自然语言描述"`
	SubjectUserID *int64            `json:"subject_user_id,omitempty" jsonschema:"description=可选；-1 表示你自己，0 表示群组，正数表示成员 QQ"`
	Kind          memory.MemoryKind `json:"kind,omitempty" jsonschema:"enum=,enum=fact,enum=episode,enum=preference,enum=constraint,enum=goal"`
	Limit         int               `json:"limit,omitempty" jsonschema:"description=返回数量，默认 10，最大 20"`
}

type QueryMemoryOutput struct {
	Success  bool                     `json:"success"`
	Count    int                      `json:"count"`
	Memories []map[string]interface{} `json:"memories,omitempty"`
	Message  string                   `json:"message,omitempty"`
}

func queryMemoryFunc(ctx context.Context, input *QueryMemoryInput) (*QueryMemoryOutput, error) {
	if input == nil {
		return &QueryMemoryOutput{Success: false, Message: "查询参数不能为空"}, nil
	}
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return &QueryMemoryOutput{Success: false, Message: "工具上下文未初始化"}, nil
	}
	query := strings.TrimSpace(input.Query)
	if query == "" {
		return &QueryMemoryOutput{Success: false, Message: "查询内容不能为空"}, nil
	}
	kind := memory.NormalizeMemoryKind(string(input.Kind))
	if input.Kind != "" && kind == "" {
		return &QueryMemoryOutput{Success: false, Message: "记忆类型无效"}, nil
	}
	limit := input.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 20 {
		limit = 20
	}
	selfID := int64(0)
	if tc.Bot != nil {
		selfID = tc.Bot.GetSelfID()
	}
	items, err := tc.MemoryMgr.QueryMemory(ctx, query, tc.GroupID, input.SubjectUserID, kind, selfID, limit)
	if err != nil {
		return &QueryMemoryOutput{Success: false, Message: err.Error()}, nil
	}
	results := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		result := map[string]interface{}{
			"subject_user_id": item.SubjectUserID,
			"subject":         memory.SubjectLabel(item.SubjectUserID, selfID),
			"kind":            item.Kind,
			"content":         item.Content,
			"updated_at":      item.UpdatedAt.Format("2006-01-02 15:04"),
		}
		if item.GroupID == tc.GroupID {
			evidence, _ := tc.MemoryMgr.ListMemoryEvidenceOneBotIDs(ctx, item.ID)
			evidenceRefs := make([]string, 0, len(evidence))
			for _, messageID := range evidence {
				evidenceRefs = append(evidenceRefs, tc.RegisterMessage(messageID))
			}
			result["evidence_message_refs"] = evidenceRefs
		}
		results = append(results, result)
	}
	return &QueryMemoryOutput{Success: true, Count: len(results), Memories: results}, nil
}

func NewQueryMemoryTool() (tool.InvokableTool, error) {
	return utils.InferTool("queryMemory", `搜索长期记忆。默认只查当前群；按成员查时填写其 QQ，查群级记忆填 0，查你自己的跨群记忆固定填 -1。只有昵称没有可靠 QQ 时，把昵称写进 query 并接受可能重名。`, queryMemoryFunc)
}
