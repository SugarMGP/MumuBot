package onebot

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mumu-bot/internal/utils"
	"os"
	"strconv"
	"time"

	"github.com/bytedance/sonic"
	"github.com/jellydator/ttlcache/v3"
)

// callAPI 调用 OneBot API（同步等待响应）
func (c *Client) callAPI(ctx context.Context, action string, params map[string]interface{}) (interface{}, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	sdk, err := c.currentSDK()
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err := sdk.Call(ctx, action, params, &raw); err != nil {
		return nil, err
	}
	var data interface{}
	if len(raw) > 0 && string(raw) != "null" {
		decoder := sonic.ConfigDefault.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&data); err != nil {
			return nil, fmt.Errorf("解析 %s 返回体失败: %w", action, err)
		}
	}
	return data, nil
}

func responseDataMap(data interface{}, action string) (map[string]interface{}, error) {
	if data == nil {
		return nil, fmt.Errorf("%s 返回空响应", action)
	}
	result, ok := data.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("%s 返回的 data 不是对象", action)
	}
	return result, nil
}

func responseDataList(data interface{}, action string) ([]interface{}, error) {
	if data == nil {
		return nil, fmt.Errorf("%s 返回空响应", action)
	}
	result, ok := data.([]interface{})
	if !ok {
		return nil, fmt.Errorf("%s 返回的 data 不是数组", action)
	}
	return result, nil
}

func positiveID(data map[string]interface{}, field, action string) (int64, error) {
	id, ok := utils.ParseInt64Value(data[field])
	if !ok || id <= 0 {
		return 0, fmt.Errorf("%s 返回无效的 %s", action, field)
	}
	return id, nil
}

func oneBotID(id int64) string { return strconv.FormatInt(id, 10) }

// SendGroupMessage 发送群消息
func (c *Client) SendGroupMessage(ctx context.Context, groupID int64, content string, replyTo int64, mentions []int64) (int64, error) {
	// 使用消息段数组格式，更符合 OneBot 11 标准
	var message []map[string]interface{}

	// reply 消息段
	if replyTo > 0 {
		message = append(message, map[string]interface{}{
			"type": "reply",
			"data": map[string]interface{}{
				"id": oneBotID(replyTo),
			},
		})
	}

	// @ 消息段
	for _, uid := range mentions {
		if uid <= 0 {
			continue
		}
		message = append(message, map[string]interface{}{
			"type": "at",
			"data": map[string]interface{}{
				"qq": strconv.FormatInt(uid, 10),
			},
		}, map[string]interface{}{
			"type": "text",
			"data": map[string]interface{}{
				"text": " ",
			},
		})
	}

	// 文本消息段
	if content != "" {
		message = append(message, map[string]interface{}{
			"type": "text",
			"data": map[string]interface{}{
				"text": content,
			},
		})
	}

	resp, err := c.callAPI(ctx, "send_group_msg", map[string]interface{}{
		"group_id": groupID,
		"message":  message,
	})
	if err != nil {
		return 0, err
	}
	return messageIDFromResponse(resp)
}

// SendImageMessage 发送图片/表情包消息
// filePath: 本地文件绝对路径
// isSticker: true 时作为表情包发送 (sub_type=1)
func (c *Client) SendImageMessage(ctx context.Context, groupID int64, filePath string, isSticker bool) (int64, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return 0, fmt.Errorf("读取待发送图片失败: %w", err)
	}

	subType := 0
	if isSticker {
		subType = 1
	}

	message := []map[string]interface{}{
		{
			"type": "image",
			"data": map[string]interface{}{
				"file":     "base64://" + base64.StdEncoding.EncodeToString(data),
				"sub_type": subType,
			},
		},
	}

	resp, err := c.callAPI(ctx, "send_group_msg", map[string]interface{}{
		"group_id": groupID,
		"message":  message,
	})
	if err != nil {
		return 0, err
	}
	return messageIDFromResponse(resp)
}

func messageIDFromResponse(resp interface{}) (int64, error) {
	data, err := responseDataMap(resp, "发送消息")
	if err != nil {
		return 0, err
	}
	return positiveID(data, "message_id", "发送消息")
}

// DeleteMsg 撤回消息
func (c *Client) DeleteMsg(ctx context.Context, messageID int64) error {
	_, err := c.callAPI(ctx, "delete_msg", map[string]interface{}{
		"message_id": oneBotID(messageID),
	})
	return err
}

// GetMsg 获取消息详情
func (c *Client) GetMsg(ctx context.Context, messageID int64) (map[string]interface{}, error) {
	resp, err := c.callAPI(ctx, "get_msg", map[string]interface{}{
		"message_id": oneBotID(messageID),
	})
	if err != nil {
		return nil, err
	}
	return responseDataMap(resp, "get_msg")
}

// GetGroupInfo 获取群信息
func (c *Client) GetGroupInfo(ctx context.Context, groupID int64, noCache bool) (*GroupInfo, error) {
	resp, err := c.callAPI(ctx, "get_group_info", map[string]interface{}{
		"group_id": groupID,
		"no_cache": noCache,
	})
	if err != nil {
		return nil, err
	}
	data, err := responseDataMap(resp, "get_group_info")
	if err != nil {
		return nil, err
	}
	groupID, err = positiveID(data, "group_id", "get_group_info")
	if err != nil {
		return nil, err
	}
	info := &GroupInfo{GroupID: groupID}
	if name, ok := data["group_name"].(string); ok {
		info.GroupName = name
	}
	if count, ok := parseInt(data["member_count"]); ok {
		info.MemberCount = count
	}
	if m, ok := parseInt(data["max_member_count"]); ok {
		info.MaxMemberCount = m
	}
	return info, nil
}

// GetGroupMemberInfo 获取群成员信息
func (c *Client) GetGroupMemberInfo(ctx context.Context, groupID, userID int64, noCache bool) (*GroupMemberInfo, error) {
	cacheKey := groupMemberCacheKey(groupID, userID)
	if !noCache {
		if item := c.memberInfoCache.Get(cacheKey); item != nil {
			return item.Value(), nil
		}
	}
	resp, err := c.callAPI(ctx, "get_group_member_info", map[string]interface{}{
		"group_id": groupID,
		"user_id":  userID,
		"no_cache": noCache,
	})
	if err != nil {
		return nil, err
	}
	data, err := responseDataMap(resp, "get_group_member_info")
	if err != nil {
		return nil, err
	}
	info, err := parseGroupMemberInfo(data, "get_group_member_info")
	if err != nil {
		return nil, err
	}
	c.memberInfoCache.Set(cacheKey, info, ttlcache.DefaultTTL)
	return info, nil
}

func parseGroupMemberInfo(data map[string]interface{}, action string) (*GroupMemberInfo, error) {
	groupID, err := positiveID(data, "group_id", action)
	if err != nil {
		return nil, err
	}
	userID, err := positiveID(data, "user_id", action)
	if err != nil {
		return nil, err
	}
	info := &GroupMemberInfo{GroupID: groupID, UserID: userID}
	if nickname, ok := data["nickname"].(string); ok {
		info.Nickname = nickname
	}
	if card, ok := data["card"].(string); ok {
		info.Card = card
	}
	if role, ok := data["role"].(string); ok {
		info.Role = role
	}
	if joinTime, ok := utils.ParseInt64Value(data["join_time"]); ok {
		info.JoinTime = joinTime
	}
	if lastSentTime, ok := utils.ParseInt64Value(data["last_sent_time"]); ok {
		info.LastSentTime = lastSentTime
	}
	if level, ok := data["level"].(string); ok {
		info.Level = level
	}
	if title, ok := data["title"].(string); ok {
		info.Title = title
	}
	return info, nil
}

// SetMsgEmojiLike 对消息贴表情
func (c *Client) SetMsgEmojiLike(ctx context.Context, messageID int64, emojiID int) error {
	_, err := c.callAPI(ctx, "set_msg_emoji_like", map[string]interface{}{
		"message_id": oneBotID(messageID),
		"emoji_id":   emojiID,
	})
	return err
}

// MarkMsgAsRead 标记消息已读
func (c *Client) MarkMsgAsRead(ctx context.Context, messageID int64) error {
	_, err := c.callAPI(ctx, "mark_msg_as_read", map[string]interface{}{
		"message_id": oneBotID(messageID),
	})
	return err
}

// GroupPoke 群戳一戳
func (c *Client) GroupPoke(ctx context.Context, groupID, userID int64) error {
	_, err := c.callAPI(ctx, "group_poke", map[string]interface{}{
		"group_id": groupID,
		"user_id":  userID,
	})
	return err
}

// GetGroupNotice 获取群公告
func (c *Client) GetGroupNotice(ctx context.Context, groupID int64) ([]GroupNotice, error) {
	resp, err := c.callAPI(ctx, "_get_group_notice", map[string]interface{}{
		"group_id": groupID,
	})
	if err != nil {
		return nil, err
	}

	dataList, err := responseDataList(resp, "_get_group_notice")
	if err != nil {
		return nil, err
	}

	var notices []GroupNotice
	for i, item := range dataList {
		data, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("_get_group_notice 第 %d 项不是对象", i)
		}
		notice := GroupNotice{}
		if noticeID, ok := data["notice_id"].(string); ok {
			notice.NoticeID = noticeID
		}
		if senderID, ok := utils.ParseInt64Value(data["sender_id"]); ok {
			notice.SenderID = senderID
		}
		if publishTime, ok := utils.ParseInt64Value(data["publish_time"]); ok {
			notice.PublishTime = publishTime
		}
		if msg, ok := data["message"].(map[string]interface{}); ok {
			if text, ok := msg["text"].(string); ok {
				notice.Content = text
			}
		}
		notices = append(notices, notice)
	}
	return notices, nil
}

// GetEssenceMessages 获取群精华消息
func (c *Client) GetEssenceMessages(ctx context.Context, groupID int64) ([]EssenceMessage, error) {
	resp, err := c.callAPI(ctx, "get_essence_msg_list", map[string]interface{}{
		"group_id": groupID,
	})
	if err != nil {
		return nil, err
	}

	dataList, err := responseDataList(resp, "get_essence_msg_list")
	if err != nil {
		return nil, err
	}

	var messages []EssenceMessage
	for i, item := range dataList {
		data, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("get_essence_msg_list 第 %d 项不是对象", i)
		}
		messageID, err := positiveID(data, "message_id", fmt.Sprintf("get_essence_msg_list 第 %d 项", i))
		if err != nil {
			return nil, err
		}
		msg := EssenceMessage{MessageID: messageID}
		if senderNick, ok := data["sender_nick"].(string); ok {
			msg.SenderNick = senderNick
		}
		if operatorNick, ok := data["operator_nick"].(string); ok {
			msg.OperatorNick = operatorNick
		}
		if operatorTime, ok := utils.ParseInt64Value(data["operator_time"]); ok {
			msg.OperatorTime = operatorTime
		}
		// 解析消息内容
		if content, ok := data["content"].([]interface{}); ok {
			msg.Content = extractTextFromSegments(content)
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

// GetForwardMsg 获取合并转发消息内容
func (c *Client) GetForwardMsg(ctx context.Context, forwardID string) ([]ForwardMessage, error) {
	if forwardID == "" {
		return nil, nil
	}
	resp, err := c.callAPI(ctx, "get_forward_msg", map[string]interface{}{
		"message_id": forwardID,
	})
	if err != nil {
		return nil, err
	}
	data, err := responseDataMap(resp, "get_forward_msg")
	if err != nil {
		return nil, err
	}
	return parseForwardMessages(data)
}

// GetMessageReactions 获取消息的表情回应
func (c *Client) GetMessageReactions(ctx context.Context, messageID int64) ([]EmojiReaction, error) {
	// 通过 get_msg 获取消息详情，其中包含 emoji_likes_list
	msgData, err := c.GetMsg(ctx, messageID)
	if err != nil {
		return nil, err
	}

	rawEmojiList, exists := msgData["emoji_likes_list"]
	if !exists || rawEmojiList == nil {
		return nil, nil
	}
	emojiList, ok := rawEmojiList.([]interface{})
	if !ok {
		return nil, fmt.Errorf("get_msg 返回的 emoji_likes_list 不是数组")
	}

	var reactions []EmojiReaction
	for i, item := range emojiList {
		emojiData, ok := item.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("get_msg 的 emoji_likes_list 第 %d 项不是对象", i)
		}
		reaction := EmojiReaction{}
		if emojiID, ok := parseInt(emojiData["emoji_id"]); ok {
			reaction.EmojiID = emojiID
		}

		if count, ok := parseInt(emojiData["likes_cnt"]); ok {
			reaction.Count = count
		}
		if reaction.EmojiID > 0 {
			reactions = append(reactions, reaction)
		}
	}
	return reactions, nil
}
