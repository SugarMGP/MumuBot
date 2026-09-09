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
	Query         string `json:"query"`
	SubjectUserID *int64 `json:"subject_user_id,omitempty" jsonschema:"description=-1 自身跨群，0 当前群，正数成员"`
	Kind          string `json:"kind,omitempty" jsonschema:"enum=,enum=fact,enum=episode,enum=preference,enum=constraint,enum=goal,enum=term,enum=expression,enum=alias"`
	History       bool   `json:"history,omitempty" jsonschema:"description=追问来源时读取有证据的历史关系，历史解释不能当作当前事实"`
}

func NewSearchMemoryTool() (tool.InvokableTool, error) {
	return utils.InferTool("searchMemory", "统一查询事实、经历、成员偏好、群术语和表达方式。词义可能随语境不同；关系只表示已获得证据的联系，不能自行补全词源。", func(ctx context.Context, in *SearchMemoryInput) (map[string]any, error) {
		tc := GetToolContext(ctx)
		if tc == nil || in == nil || tc.MemoryMgr == nil {
			return nil, fmt.Errorf("工具未初始化")
		}
		query := strings.TrimSpace(in.Query)
		if query == "" {
			return nil, fmt.Errorf("查询不能为空")
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
		Note string `json:"note" jsonschema:"description=最多300字的下一轮工作结论；空字符串清除。不要写内部推理、秘密或工具轨迹。"`
	}
	return utils.InferTool("saveWorkingNote", "正常结束前更新下一轮工作便签；无待续事项时提交空字符串。便签不能触发发言，不是长期事实。", func(ctx context.Context, in *input) (map[string]any, error) {
		tc := GetToolContext(ctx)
		if tc == nil || in == nil || tc.MemoryMgr == nil {
			return nil, fmt.Errorf("工具未初始化")
		}
		if err := tc.MemoryMgr.SaveWorkingNote(ctx, tc.GroupID, in.Note); err != nil {
			return map[string]any{"success": false, "message": err.Error()}, nil
		}
		return map[string]any{"success": true}, nil
	})
}
