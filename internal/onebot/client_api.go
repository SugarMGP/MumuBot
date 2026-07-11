package onebot

import (
	"context"
	"fmt"
	"math"
	"mumu-bot/internal/utils"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/bytedance/sonic"
	"github.com/gorilla/websocket"
)

// callAPI 调用 OneBot API（同步等待响应）
func (c *Client) callAPI(ctx context.Context, action string, params map[string]interface{}) (*APIResponse, error) {
	if ctx == nil {
		ctx = c.ctx
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	echo := fmt.Sprintf("%d", atomic.AddUint64(&c.echoCounter, 1))

	// 创建响应通道
	respCh := make(chan *APIResponse, 1)
	c.pendingReqs.Store(echo, respCh)
	defer c.pendingReqs.Delete(echo)

	// 发送请求
	c.connMu.Lock()
	if c.conn == nil {
		c.connMu.Unlock()
		return nil, fmt.Errorf("未连接到 OneBot 服务")
	}

	req := map[string]interface{}{
		"action": action,
		"params": params,
		"echo":   echo,
	}
	data, err := sonic.Marshal(req)
	if err != nil {
		c.connMu.Unlock()
		return nil, err
	}

	if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		c.connMu.Unlock()
		return nil, err
	}
	c.connMu.Unlock()

	// 等待响应（带超时）
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp := <-respCh:
		if resp.RetCode != 0 {
			return resp, fmt.Errorf("API调用失败[%d]: %s", resp.RetCode, resp.Message)
		}
		return resp, nil
	}
}

// SendGroupMessage 发送群消息
func (c *Client) SendGroupMessage(ctx context.Context, groupID int64, content string, replyTo int64, mentions []int64) (int64, error) {
	// 使用消息段数组格式，更符合 OneBot 11 标准
	var message []map[string]interface{}

	// reply 消息段
	if replyTo > 0 {
		message = append(message, map[string]interface{}{
			"type": "reply",
			"data": map[string]interface{}{
				"id": strconv.FormatInt(replyTo, 10),
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

// SendPrivateMessage 发送私聊消息
func (c *Client) SendPrivateMessage(ctx context.Context, userID int64, content string) (int64, error) {
	resp, err := c.callAPI(ctx, "send_private_msg", map[string]interface{}{
		"user_id": userID,
		"message": content,
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
	subType := 0
	if isSticker {
		subType = 1
	}

	message := []map[string]interface{}{
		{
			"type": "image",
			"data": map[string]interface{}{
				"file":     "file:///" + filePath,
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

func messageIDFromResponse(resp *APIResponse) (int64, error) {
	data := resp.DataMap()
	if data == nil {
		return 0, fmt.Errorf("OneBot 响应缺少有效的 message_id")
	}
	messageID, ok := utils.ParseInt64Value(data["message_id"])
	if value, isFloat := data["message_id"].(float64); isFloat {
		ok = !math.IsNaN(value) && !math.IsInf(value, 0) && math.Trunc(value) == value && value < float64(math.MaxInt64)
	}
	if !ok || messageID <= 0 {
		return 0, fmt.Errorf("OneBot 响应缺少有效的 message_id")
	}
	return messageID, nil
}

// DeleteMsg 撤回消息
func (c *Client) DeleteMsg(ctx context.Context, messageID int64) error {
	_, err := c.callAPI(ctx, "delete_msg", map[string]interface{}{
		"message_id": strconv.FormatInt(messageID, 10),
	})
	return err
}

// GetMsg 获取消息详情
func (c *Client) GetMsg(ctx context.Context, messageID int64) (map[string]interface{}, error) {
	resp, err := c.callAPI(ctx, "get_msg", map[string]interface{}{
		"message_id": messageID,
	})
	if err != nil {
		return nil, err
	}
	return resp.DataMap(), nil
}

// GetLoginInfo 获取登录号信息
func (c *Client) GetLoginInfo(ctx context.Context) (*LoginInfo, error) {
	resp, err := c.callAPI(ctx, "get_login_info", nil)
	if err != nil {
		return nil, err
	}
	data := resp.DataMap()
	if data == nil {
		return nil, fmt.Errorf("无效的响应数据")
	}
	info := &LoginInfo{}
	if userID, ok := utils.ParseInt64Value(data["user_id"]); ok {
		info.UserID = userID
	}
	if nickname, ok := data["nickname"].(string); ok {
		info.Nickname = nickname
	}
	return info, nil
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
	data := resp.DataMap()
	if data == nil {
		return nil, fmt.Errorf("无效的响应数据")
	}
	info := &GroupInfo{}
	if gid, ok := utils.ParseInt64Value(data["group_id"]); ok {
		info.GroupID = gid
	}
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
	resp, err := c.callAPI(ctx, "get_group_member_info", map[string]interface{}{
		"group_id": groupID,
		"user_id":  userID,
		"no_cache": noCache,
	})
	if err != nil {
		return nil, err
	}
	data := resp.DataMap()
	if data == nil {
		return nil, fmt.Errorf("无效的响应数据")
	}
	info := &GroupMemberInfo{}
	if gid, ok := utils.ParseInt64Value(data["group_id"]); ok {
		info.GroupID = gid
	}
	if uid, ok := utils.ParseInt64Value(data["user_id"]); ok {
		info.UserID = uid
	}
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

// GetGroupMemberList 获取群成员列表
func (c *Client) GetGroupMemberList(ctx context.Context, groupID int64, noCache bool) ([]*GroupMemberInfo, error) {
	resp, err := c.callAPI(ctx, "get_group_member_list", map[string]interface{}{
		"group_id": groupID,
		"no_cache": noCache,
	})
	if err != nil {
		return nil, err
	}

	// 响应的 data 是数组
	dataList, ok := resp.Data.([]interface{})
	if !ok {
		return nil, fmt.Errorf("无效的响应数据格式")
	}

	var members []*GroupMemberInfo
	for _, item := range dataList {
		data, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		info := &GroupMemberInfo{}
		if gid, ok := utils.ParseInt64Value(data["group_id"]); ok {
			info.GroupID = gid
		}
		if uid, ok := utils.ParseInt64Value(data["user_id"]); ok {
			info.UserID = uid
		}
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
		members = append(members, info)
	}
	return members, nil
}

// SetMsgEmojiLike 对消息贴表情
func (c *Client) SetMsgEmojiLike(ctx context.Context, messageID int64, emojiID int) error {
	_, err := c.callAPI(ctx, "set_msg_emoji_like", map[string]interface{}{
		"message_id": messageID,
		"emoji_id":   emojiID,
	})
	return err
}

// MarkMsgAsRead 标记消息已读
func (c *Client) MarkMsgAsRead(ctx context.Context, messageID int64) error {
	_, err := c.callAPI(ctx, "mark_msg_as_read", map[string]interface{}{
		"message_id": messageID,
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

	dataList := resp.DataList()
	if dataList == nil {
		return nil, nil
	}

	var notices []GroupNotice
	for _, item := range dataList {
		data, ok := item.(map[string]interface{})
		if !ok {
			continue
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

	dataList := resp.DataList()
	if dataList == nil {
		return nil, nil
	}

	var messages []EssenceMessage
	for _, item := range dataList {
		data, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		msg := EssenceMessage{}
		if msgID, ok := utils.ParseInt64Value(data["message_id"]); ok {
			msg.MessageID = msgID
		}
		if senderID, ok := utils.ParseInt64Value(data["sender_id"]); ok {
			msg.SenderID = senderID
		}
		if senderNick, ok := data["sender_nick"].(string); ok {
			msg.SenderNick = senderNick
		}
		if operatorID, ok := utils.ParseInt64Value(data["operator_id"]); ok {
			msg.OperatorID = operatorID
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
	data := resp.DataMap()
	if data == nil {
		return nil, nil
	}
	return parseForwardMessages(data), nil
}

// GetMessageReactions 获取消息的表情回应
func (c *Client) GetMessageReactions(ctx context.Context, messageID int64) ([]EmojiReaction, error) {
	// 通过 get_msg 获取消息详情，其中包含 emoji_likes_list
	msgData, err := c.GetMsg(ctx, messageID)
	if err != nil {
		return nil, err
	}

	emojiList, ok := msgData["emoji_likes_list"].([]interface{})
	if !ok || len(emojiList) == 0 {
		return nil, nil
	}

	var reactions []EmojiReaction
	for _, item := range emojiList {
		emojiData, ok := item.(map[string]interface{})
		if !ok {
			continue
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
