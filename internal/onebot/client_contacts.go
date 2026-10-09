package onebot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zjutjh/onebot-sdk/api"
	"github.com/zjutjh/onebot-sdk/message"
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

// GetStrangerNickname 查询陌生人昵称，供好友申请展示补全；查询失败返回空串由上层回退
func (c *Client) GetStrangerNickname(ctx context.Context, userID int64) string {
	client, err := c.apiClient()
	if err != nil {
		return ""
	}
	resp, err := client.GetStrangerInfo(ctx, api.GetStrangerInfoRequest{
		UserID:  message.ID(userID),
		NoCache: rawUnion[api.GetStrangerInfoRequestNoCacheUnion](false),
	})
	if err != nil || resp == nil {
		return ""
	}
	return strings.TrimSpace(resp.Nickname)
}

// SetFriendRequest 处理好友请求，remark 的方言差异由 SDK 门面归一
func (c *Client) SetFriendRequest(ctx context.Context, flag string, approve bool, remark string) error {
	sdk, err := c.currentSDK()
	if err != nil {
		return err
	}
	return sdk.SetFriendRequest(ctx, flag, approve, remark)
}
