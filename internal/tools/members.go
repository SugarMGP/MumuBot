package tools

import (
	"context"
	"fmt"
	"time"

	"mumu-bot/internal/memory"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

type MemberRoster struct {
	GroupID   int64     `json:"group_id"`
	MemberIDs []int64   `json:"-"`
	FetchedAt time.Time `json:"fetched_at"`
}

type SearchMembersInput struct {
	Keyword string `json:"keyword,omitempty" jsonschema:"description=昵称或历史群名片子串，也可填精确 QQ；留空浏览"`
	GroupID int64  `json:"group_id,omitempty" jsonschema:"description=默认0搜索全部已认识的人；正整数限定该群当前成员并按需补齐名单"`
	Cursor  string `json:"cursor,omitempty"`
	Limit   int    `json:"limit,omitempty" jsonschema:"description=默认12，最大99"`
}

type SearchMembersOutput struct {
	Success bool `json:"success"`
	*memory.MemberSearchPage
	Roster  *MemberRoster `json:"roster,omitempty"`
	Warning string        `json:"warning,omitempty"`
}

func searchMembers(ctx context.Context, input *SearchMembersInput) (*SearchMembersOutput, error) {
	tc := GetToolContext(ctx)
	if tc == nil || tc.MemoryMgr == nil {
		return nil, NewTerminalToolError(fmt.Errorf("熟人查找能力未初始化"))
	}
	if input == nil {
		input = &SearchMembersInput{}
	}
	if input.GroupID < 0 {
		return nil, fmt.Errorf("group_id 不能为负数")
	}
	groupID := input.GroupID
	if groupID == 0 && tc.ConversationKind == memory.ConversationKindGroup {
		groupID = tc.TargetID
	}
	var roster *MemberRoster
	var warning string
	if groupID > 0 {
		var err error
		if tc.MemberRosterCallback == nil {
			err = fmt.Errorf("群名单能力未初始化")
		} else {
			roster, err = tc.MemberRosterCallback(ctx, groupID)
		}
		if err != nil {
			if input.GroupID > 0 {
				return nil, err
			}
			warning = "当前群名单未补齐，仅搜索已有熟人资料：" + err.Error()
		}
	}
	query := memory.MemberSearchQuery{Keyword: input.Keyword, GroupID: input.GroupID, Cursor: input.Cursor, Limit: input.Limit}
	if roster != nil {
		query.MemberIDs = roster.MemberIDs
	}
	page, err := tc.MemoryMgr.SearchMembers(ctx, query)
	if err != nil {
		return nil, err
	}
	if roster != nil && tc.ConversationKind == memory.ConversationKindGroup && roster.GroupID == tc.TargetID {
		members := make(map[int64]bool, len(roster.MemberIDs))
		for _, id := range roster.MemberIDs {
			members[id] = true
		}
		for i := range page.Members {
			current := members[page.Members[i].UserID]
			page.Members[i].CurrentGroupMember = &current
		}
	}
	return &SearchMembersOutput{Success: true, MemberSearchPage: page, Roster: roster, Warning: warning}, nil
}

func NewSearchMembersTool() (tool.InvokableTool, error) {
	return utils.InferTool("searchMembers", "按昵称、历史群名片或 QQ 查找熟人，所有场合默认全局搜索。群聊会按十分钟缓存自动补齐当前群完整名单；group_id 可限定其他可用群的当前成员。结果是身份线索，不代表亲密程度；需要详细背景再用 getUserProfile 或记忆工具。", searchMembers)
}
