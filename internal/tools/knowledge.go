package tools

import (
	"context"
	"fmt"
	"strings"

	"mumu-bot/internal/memory"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type SearchMemoryInput struct {
	ItemID        uint   `json:"item_id,omitempty" jsonschema:"description=读取已知知识的依据原文；此时不需要 query"`
	Offset        int    `json:"offset,omitempty" jsonschema:"description=依据组的分页位置"`
	Query         string `json:"query"`
	SubjectUserID *int64 `json:"subject_user_id,omitempty" jsonschema:"description=-1 自身跨群，0 当前群，正数成员"`
	Kind          string `json:"kind,omitempty" jsonschema:"enum=,enum=fact,enum=episode,enum=preference,enum=constraint,enum=goal,enum=term,enum=expression,enum=alias"`
	History       bool   `json:"history,omitempty" jsonschema:"description=追问来源时读取有证据的历史关系，历史解释不能当作当前事实"`
}

func NewSearchMemoryTool() (tool.InvokableTool, error) {
	return utils.InferTool("searchMemory", "统一查询事实、经历、成员偏好、群术语和表达方式。词义可能随语境不同；关系只表示已获得证据的联系，不能自行补全词源。", func(ctx context.Context, in *SearchMemoryInput) (map[string]any, error) {
		tc := GetToolContext(ctx)
		if tc == nil || in == nil || tc.MemoryMgr == nil || tc.Bot == nil {
			return nil, NewTerminalToolError(fmt.Errorf("工具未初始化"))
		}
		query := strings.TrimSpace(in.Query)
		if query == "" && in.ItemID == 0 {
			return nil, fmt.Errorf("查询不能为空")
		}
		if in.Offset < 0 {
			return nil, fmt.Errorf("offset 不得为负数")
		}
		upper, err := tc.MemoryMgr.GetMessageLogByID(tc.GroupID, tc.SnapshotMessageID)
		if err != nil {
			return nil, err
		}
		self := tc.Bot.GetSelfID()
		subject := in.SubjectUserID
		if subject != nil {
			v := *subject
			if v == -1 {
				v = self
			}
			if v < 0 {
				return nil, fmt.Errorf("无效主体")
			}
			subject = &v
		}
		if in.ItemID > 0 {
			items, err := tc.MemoryMgr.SearchKnowledge(ctx, memory.KnowledgeSearchOptions{GroupID: tc.GroupID, SelfID: self, SubjectUserID: subject, ItemID: in.ItemID, IncludeInactive: in.History, ThroughID: upper.ID, Limit: 1})
			if err != nil {
				return nil, fmt.Errorf("读取知识失败：%w", err)
			}
			if len(items) == 0 {
				return nil, fmt.Errorf("知识不存在或不在本次查询范围内")
			}
			sets, err := tc.MemoryMgr.ListKnowledgeEvidence(ctx, items[0].GroupID, in.ItemID, 0)
			if err != nil {
				return nil, fmt.Errorf("读取依据失败：%w", err)
			}
			visible := make([]memory.KnowledgeEvidence, 0, len(sets))
			for _, set := range sets {
				if !set.Valid {
					continue
				}
				allowed := true
				for _, msg := range set.Messages {
					if msg.ID > upper.ID {
						allowed = false
						break
					}
				}
				if allowed {
					visible = append(visible, set)
				}
			}
			start := min(in.Offset, len(visible))
			end := min(start+5, len(visible))
			return map[string]any{"item": items[0], "evidence_sets": visible[start:end], "has_more": end < len(visible), "next_offset": end}, nil
		}
		prepared, err := tc.MemoryMgr.PrepareHybridQuery(ctx, []string{query})
		if err != nil {
			return nil, err
		}
		items, err := tc.MemoryMgr.SearchKnowledge(ctx, memory.KnowledgeSearchOptions{GroupID: tc.GroupID, SelfID: self, SubjectUserID: subject, Kind: in.Kind, Prepared: &prepared, ThroughID: upper.ID, Limit: 6})
		if err != nil {
			return nil, err
		}
		if in.History {
			historical, err := tc.MemoryMgr.SearchKnowledge(ctx, memory.KnowledgeSearchOptions{GroupID: tc.GroupID, SelfID: self, SubjectUserID: subject, Kind: in.Kind, Prepared: &prepared, ThroughID: upper.ID, IncludeInactive: true, Status: "archived", Limit: 6})
			if err != nil {
				return nil, err
			}
			items = append(items, historical...)
		}
		var seeds []uint
		for _, item := range items {
			if item.GroupID == tc.GroupID {
				seeds = append(seeds, item.ID)
			}
		}
		depth := 1
		if in.History {
			depth = 3
		}
		graph, err := tc.MemoryMgr.GetKnowledgeNeighborhood(ctx, tc.GroupID, seeds, depth, in.History, memory.KnowledgeGraphOptions{ThroughID: upper.ID})
		if err != nil {
			return nil, err
		}
		evidence := map[uint][][]string{}
		for _, item := range graph.Items {
			sets, e := tc.MemoryMgr.ListKnowledgeEvidence(ctx, tc.GroupID, item.ID, 0)
			if e != nil {
				return nil, e
			}
			for _, set := range sets {
				if !set.Valid {
					continue
				}
				var refs []string
				valid := true
				for _, msg := range set.Messages {
					if msg.ID > upper.ID {
						valid = false
						break
					}
					refs = append(refs, tc.RegisterMessage(msg.OneBotMessageID))
				}
				if valid {
					evidence[item.ID] = append(evidence[item.ID], refs)
				}
			}
		}
		return map[string]any{"matches": items, "neighborhood": graph, "evidence_sets": evidence}, nil
	})
}

func NewSaveWorkingNoteTool() (tool.InvokableTool, error) {
	type input struct {
		Note string `json:"note" jsonschema:"description=最多300字、可跨轮理解的工作结论；空字符串清除。只写人物、事情和进展，禁止写m1等本轮消息编号、本轮或本session等短期定位、内部推理、秘密和工具轨迹。"`
	}
	return utils.InferTool("saveWorkingNote", "正常结束前更新下一轮工作便签；无待续事项时提交空字符串。便签会跨轮读取，只保留下一轮必需的当前事实或明确未完成请求，已回应的普通闲聊、笑话过程和无人接续的随口追问不复述；跨轮识别人物写当前称呼和 QQ，不同 QQ 不因昵称相似合并，禁止保存 m1 等每轮会变化的消息编号。便签不能触发发言，不是长期事实。", func(ctx context.Context, in *input) (map[string]any, error) {
		tc := GetToolContext(ctx)
		if tc == nil || in == nil || tc.MemoryMgr == nil {
			return nil, NewTerminalToolError(fmt.Errorf("工具未初始化"))
		}
		if err := tc.MemoryMgr.SaveWorkingNote(ctx, tc.GroupID, in.Note); err != nil {
			return nil, NewTerminalToolError(err)
		}
		return map[string]any{"success": true}, nil
	})
}
