package onebot

import (
	"context"
	"fmt"
	"time"

	"github.com/zjutjh/onebot-sdk/api"
)

type Contact struct {
	Kind         string
	TargetID     int64
	Name         string
	RemoteRemark string
	Active       bool
	LastSeenAt   time.Time
}

func (c *Client) GetConversationContacts(ctx context.Context) ([]Contact, error) {
	groups, err := c.getGroupContacts(ctx)
	if err != nil {
		return nil, err
	}
	friends, err := c.getFriendContacts(ctx)
	if err != nil {
		return nil, err
	}
	return append(groups, friends...), nil
}

func (c *Client) getGroupContacts(ctx context.Context) ([]Contact, error) {
	client, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	resp, err := client.GetGroupList(ctx, api.GetGroupListRequest{
		NoCache: rawUnionPtr[api.GetGroupListRequestNoCacheUnion](true),
	})
	if err != nil {
		return nil, err
	}
	result := make([]Contact, 0, len(*resp))
	seen := make(map[int64]bool, len(*resp))
	for i, row := range *resp {
		id := int64(row.GroupID)
		// 任一畸形项都拒绝全量同步，真实空数组仍表示联系人已清空
		if id <= 0 || seen[id] {
			return nil, fmt.Errorf("get_group_list 第 %d 项的 group_id 无效或重复", i)
		}
		seen[id] = true
		result = append(result, Contact{
			Kind:         "group",
			TargetID:     id,
			Name:         row.GroupName,
			RemoteRemark: row.GroupRemark,
			Active:       true,
			LastSeenAt:   time.Now(),
		})
	}
	return result, nil
}

func (c *Client) getFriendContacts(ctx context.Context) ([]Contact, error) {
	client, err := c.apiClient()
	if err != nil {
		return nil, err
	}
	resp, err := client.GetFriendList(ctx, api.GetFriendListRequest{
		NoCache: rawUnionPtr[api.GetFriendListRequestNoCacheUnion](true),
	})
	if err != nil {
		return nil, err
	}
	result := make([]Contact, 0, len(*resp))
	seen := make(map[int64]bool, len(*resp))
	for i, row := range *resp {
		id := int64(row.UserID)
		// 任一畸形项都拒绝全量同步，真实空数组仍表示联系人已清空
		if id <= 0 || seen[id] {
			return nil, fmt.Errorf("get_friend_list 第 %d 项的 user_id 无效或重复", i)
		}
		seen[id] = true
		remark := ""
		if row.Remark != nil {
			remark = *row.Remark
		}
		result = append(result, Contact{
			Kind:         "private",
			TargetID:     id,
			Name:         row.Nickname,
			RemoteRemark: remark,
			Active:       true,
			LastSeenAt:   time.Now(),
		})
	}
	return result, nil
}
