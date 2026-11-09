package onebot

import (
	"context"
	"fmt"
	"strings"

	"github.com/zjutjh/onebot-sdk/api"
	"github.com/zjutjh/onebot-sdk/message"
)

// GroupMember 保存名单查询所需的成员资料，不把获取时间当成最后发言时间
type GroupMember struct {
	UserID   message.ID `json:"user_id"`
	Nickname string     `json:"nickname"`
	Card     string     `json:"card"`
}

// GetGroupMembers 用强类型响应读取名单，避免 SDK 的 []any 丢失整数精度
func (c *Client) GetGroupMembers(ctx context.Context, groupID int64) ([]GroupMember, error) {
	client, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	var rows []GroupMember
	err = client.Call(ctx, string(api.ActionGetGroupMemberList), api.GetGroupMemberListRequest{
		GroupID: message.ID(groupID), NoCache: rawUnionPtr[api.GetGroupMemberListRequestNoCacheUnion](true),
	}, &rows)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		return nil, fmt.Errorf("get_group_member_list 返回无效的空响应")
	}
	seen := make(map[message.ID]bool, len(rows))
	for i := range rows {
		row := &rows[i]
		if row.UserID <= 0 || seen[row.UserID] {
			return nil, fmt.Errorf("get_group_member_list 第 %d 项的 user_id 无效或重复", i)
		}
		seen[row.UserID] = true
		row.Nickname = strings.TrimSpace(row.Nickname)
		row.Card = strings.TrimSpace(row.Card)
	}
	return rows, nil
}
