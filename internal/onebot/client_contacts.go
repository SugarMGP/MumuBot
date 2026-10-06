package onebot

import (
	"context"
	"fmt"
	"time"

	"mumu-bot/internal/utils"
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
	data, err := c.callAPI(ctx, "get_group_list", map[string]interface{}{"no_cache": true})
	if err != nil {
		return nil, err
	}
	return parseContactList(data, "group", "get_group_list")
}

func (c *Client) getFriendContacts(ctx context.Context) ([]Contact, error) {
	data, err := c.callAPI(ctx, "get_friend_list", map[string]interface{}{"no_cache": true})
	if err != nil {
		return nil, err
	}
	return parseContactList(data, "private", "get_friend_list")
}

// parseContactList 任一畸形项都拒绝全量同步，真实空数组仍表示联系人已清空
func parseContactList(data interface{}, kind, action string) ([]Contact, error) {
	rows, err := responseDataList(data, action)
	if err != nil {
		return nil, err
	}
	idKey, nameKey, remarkKey := "user_id", "nickname", "remark"
	if kind == "group" {
		idKey, nameKey, remarkKey = "group_id", "group_name", "group_remark"
	}
	result := make([]Contact, 0, len(rows))
	seen := make(map[int64]bool, len(rows))
	for i, raw := range rows {
		row, ok := raw.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("%s 第 %d 项不是对象", action, i)
		}
		id, ok := utils.ParseInt64Value(row[idKey])
		if !ok || id <= 0 || seen[id] {
			return nil, fmt.Errorf("%s 第 %d 项的 %s 无效或重复", action, i, idKey)
		}
		name, ok := row[nameKey].(string)
		if !ok {
			return nil, fmt.Errorf("%s 第 %d 项的 %s 不是字符串", action, i, nameKey)
		}
		remark := ""
		if value, exists := row[remarkKey]; exists {
			remark, ok = value.(string)
			if !ok {
				return nil, fmt.Errorf("%s 第 %d 项的 %s 不是字符串", action, i, remarkKey)
			}
		}
		seen[id] = true
		result = append(result, Contact{Kind: kind, TargetID: id, Name: name, RemoteRemark: remark, Active: true, LastSeenAt: time.Now()})
	}
	return result, nil
}

func (c *Client) SetFriendRequest(ctx context.Context, flag string, approve bool, remark string) error {
	_, err := c.callAPI(ctx, "set_friend_add_request", map[string]interface{}{
		"flag":    flag,
		"approve": approve,
		"remark":  remark,
	})
	return err
}
