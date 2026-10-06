package onebot

import (
	"bytes"
	"fmt"
	"time"

	"mumu-bot/internal/utils"

	"github.com/bytedance/sonic"
	"go.uber.org/zap"
)

func (c *Client) enqueueEvent(raw []byte) {
	receivedAt := time.Now()
	var event map[string]interface{}
	decoder := sonic.ConfigDefault.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&event); err != nil {
		zap.L().Warn("解析事件分组键失败", zap.Error(err))
		return
	}
	postType, _ := event["post_type"].(string)
	switch postType {
	case "meta_event":
		return
	case "notice":
		notice, _ := event["notice_type"].(string)
		sub, _ := event["sub_type"].(string)
		// 这几种通知不参与消息排序，直接处理
		if notice == "group_ban" || notice == "group_decrease" || notice == "friend_add" {
			c.handleNoticeEvent(event, receivedAt, 0)
			return
		}
		if notice != "group_recall" && notice != "friend_recall" && (notice != "notify" || sub != "poke") {
			return
		}
		// 按事件类型确认对应业务回调已就绪，未就绪则直接丢弃且不分配序号，
		// 保证每个已分配序号的事件最终都能进入业务回调消费
		if notice == "group_recall" || notice == "friend_recall" {
			if c.onRecall == nil {
				return
			}
		} else if c.onMessage == nil {
			return
		}
	case "request":
		c.handleRequestEvent(event)
		return
	case "message":
		messageType, _ := event["message_type"].(string)
		if messageType != "group" && messageType != "private" {
			return
		}
		if c.onMessage == nil {
			return
		}
	default:
		return
	}
	kind := "group"
	targetID, targetOK := utils.ParseInt64Value(event["group_id"])
	if postType == "notice" && event["notice_type"] == "friend_recall" {
		kind = "private"
		targetID, targetOK = utils.ParseInt64Value(event["user_id"])
	}
	if postType == "message" && event["message_type"] == "private" {
		kind = "private"
		targetID = privateMessageTarget(event, c.GetSelfID())
		targetOK = targetID > 0
	}
	if postType == "notice" && event["notice_type"] == "notify" && event["sub_type"] == "poke" {
		kind, targetID = pokeConversation(event)
		targetOK = targetID > 0
	}
	if !targetOK || targetID <= 0 {
		zap.L().Warn("忽略缺少有效会话目标的事件")
		return
	}
	if postType == "message" {
		messageID, messageOK := utils.ParseInt64Value(event["message_id"])
		if !messageOK || messageID == 0 {
			zap.L().Warn("忽略缺少有效编号的群消息")
			return
		}
	}
	c.dispatchEvent(targetID, conversationEvent{event: event, receivedAt: receivedAt, kind: kind})
}

// nextArrivalSeq 为每个会话分配统一运行时顺序
func (c *Client) nextArrivalSeq(kind string, targetID int64) uint64 {
	c.seqMu.Lock()
	defer c.seqMu.Unlock()
	key := fmt.Sprintf("%s:%d", kind, targetID)
	c.scopeSeq[key]++
	return c.scopeSeq[key]
}

// dispatchEvent 每条事件直接并发处理，不做按会话串行或并发上限
// 事件入口已确认对应业务回调就绪，分发后该事件必然进入业务回调消费序号
func (c *Client) dispatchEvent(targetID int64, event conversationEvent) {
	event.arrivalSeq = c.nextArrivalSeq(event.kind, targetID)
	c.eventWG.Add(1)
	go func() {
		defer c.eventWG.Done()
		c.handleConversationEvent(event)
	}()
}

func (c *Client) handleConversationEvent(queued conversationEvent) {
	switch queued.event["post_type"] {
	case "message":
		if queued.kind == "private" {
			c.handlePrivateMessageEvent(queued.event, queued.receivedAt, queued.arrivalSeq)
			return
		}
		c.handleMessageEvent(queued.event, queued.receivedAt, queued.arrivalSeq)
	case "notice":
		c.handleNoticeEvent(queued.event, queued.receivedAt, queued.arrivalSeq)
	}
}

func (c *Client) handleMessageEvent(event map[string]interface{}, receivedAt time.Time, arrivalSeq uint64) {
	msg := c.parseGroupMessage(event)
	if msg == nil {
		// 解析失败：记录现场并构造占位消息消费到达序号，避免上层提交重排器死等
		groupID, _ := utils.ParseInt64Value(event["group_id"])
		messageID, _ := utils.ParseInt64Value(event["message_id"])
		zap.L().Warn("群消息段解析失败，已构造占位消息", zap.Int64("group_id", groupID), zap.Int64("message_id", messageID), zap.Any("post_type", event["post_type"]))
		msg = &ConversationMessage{ConversationKind: "group", TargetID: groupID, ParseFailed: true}
	}
	msg.ReceivedAt = receivedAt
	msg.ArrivalSeq = arrivalSeq
	c.onMessage(msg)
}

func (c *Client) handlePrivateMessageEvent(event map[string]interface{}, receivedAt time.Time, arrivalSeq uint64) {
	msg := c.parsePrivateMessage(event)
	if msg == nil {
		targetID := privateMessageTarget(event, c.GetSelfID())
		messageID, _ := utils.ParseInt64Value(event["message_id"])
		zap.L().Warn("私聊消息段解析失败，已构造占位消息", zap.Int64("target_id", targetID), zap.Int64("message_id", messageID))
		msg = &ConversationMessage{ConversationKind: "private", TargetID: targetID, MessageID: messageID, ParseFailed: true}
	}
	msg.ReceivedAt = receivedAt
	msg.ArrivalSeq = arrivalSeq
	c.onMessage(msg)
}
func (c *Client) handleNoticeEvent(event map[string]interface{}, receivedAt time.Time, arrivalSeq uint64) {
	notice, _ := event["notice_type"].(string)
	sub, _ := event["sub_type"].(string)
	switch {
	case notice == "group_ban":
		c.handleGroupBanNotice(event, sub)
	case notice == "notify" && sub == "poke":
		c.handlePokeNotice(event, receivedAt, arrivalSeq)
	case notice == "group_recall":
		c.handleGroupRecallNotice(event, arrivalSeq)
	case notice == "friend_recall":
		c.handleFriendRecallNotice(event, arrivalSeq)
	case notice == "group_decrease":
		c.handleGroupDecreaseNotice(event)
	case notice == "friend_add":
		c.handleFriendAddNotice(event)
	}
}

// handleGroupDecreaseNotice 只在机器人自己退群、被踢或群解散时回调，普通成员进出群与联系人状态无关
func (c *Client) handleGroupDecreaseNotice(event map[string]interface{}) {
	if c.onGroupLeft == nil {
		return
	}
	groupID, _ := utils.ParseInt64Value(event["group_id"])
	if groupID <= 0 {
		return
	}
	subType, _ := event["sub_type"].(string)
	userID, _ := utils.ParseInt64Value(event["user_id"])
	selfID := c.GetSelfID()
	if subType != "kick_me" && subType != "disband" && !(selfID > 0 && userID == selfID) {
		return
	}
	c.onGroupLeft(groupID)
}

// handleFriendAddNotice 好友关系建立后立即放行，避免等下一次联系人同步
func (c *Client) handleFriendAddNotice(event map[string]interface{}) {
	if c.onFriendAdd == nil {
		return
	}
	userID, _ := utils.ParseInt64Value(event["user_id"])
	if userID <= 0 {
		return
	}
	c.onFriendAdd(userID)
}

// pokeConversation 使用 NapCat 的群号或好友对端定位会话，好友 user_id 不是动作发送者
func pokeConversation(event map[string]interface{}) (string, int64) {
	if value, exists := event["group_id"]; exists {
		groupID, _ := utils.ParseInt64Value(value)
		return "group", groupID
	}
	targetID, _ := utils.ParseInt64Value(event["user_id"])
	return "private", targetID
}

func (c *Client) handlePokeNotice(event map[string]interface{}, receivedAt time.Time, arrivalSeq uint64) {
	// 有效性由业务层校验：无效戳一戳也会通过占位路径消费序号，不在此处提前返回
	kind, conversationTarget := pokeConversation(event)
	userID, _ := utils.ParseInt64Value(event["user_id"])
	if kind == "private" {
		userID, _ = utils.ParseInt64Value(event["sender_id"])
	}
	targetID, _ := utils.ParseInt64Value(event["target_id"])
	eventTime := time.Now()
	if seconds, ok := utils.ParseInt64Value(event["time"]); ok && seconds > 0 {
		eventTime = time.Unix(seconds, 0)
	}
	c.onMessage(&ConversationMessage{
		ConversationKind: kind,
		TargetID:         conversationTarget,
		UserID:           userID,
		AtList:           []int64{targetID},
		Time:             eventTime,
		ReceivedAt:       receivedAt,
		ArrivalSeq:       arrivalSeq,
	})
}

func (c *Client) handleGroupRecallNotice(event map[string]interface{}, arrivalSeq uint64) {
	// 有效性由业务层校验（无效撤回会消费序号后跳过）
	groupID, _ := utils.ParseInt64Value(event["group_id"])
	messageID, _ := utils.ParseInt64Value(event["message_id"])
	c.onRecall("group", groupID, messageID, arrivalSeq)
}

func (c *Client) handleFriendRecallNotice(event map[string]interface{}, arrivalSeq uint64) {
	// 好友撤回没有操作人字段，按好友 QQ 定位会话
	userID, _ := utils.ParseInt64Value(event["user_id"])
	messageID, _ := utils.ParseInt64Value(event["message_id"])
	c.onRecall("private", userID, messageID, arrivalSeq)
}
func (c *Client) handleRequestEvent(event map[string]interface{}) {
	request, _ := event["request_type"].(string)
	zap.L().Debug("收到请求", zap.String("type", request))
	if request != "friend" || c.onFriendRequest == nil {
		return
	}
	userID, ok := utils.ParseInt64Value(event["user_id"])
	if !ok || userID <= 0 {
		return
	}
	flag, _ := event["flag"].(string)
	nickname := ""
	if sender, ok := event["sender"].(map[string]interface{}); ok {
		if value, ok := sender["nickname"].(string); ok && value != "" {
			nickname = value
		}
	}
	c.onFriendRequest(FriendRequestEvent{Flag: flag, UserID: userID, Nickname: nickname, Comment: commentText(event), ReceivedAt: time.Now()})
}

func commentText(event map[string]interface{}) string {
	if value, ok := event["comment"].(string); ok {
		return value
	}
	return ""
}

func (c *Client) handleGroupBanNotice(event map[string]interface{}, subType string) {
	groupID, ok := utils.ParseInt64Value(event["group_id"])
	if !ok || groupID == 0 {
		return
	}
	userID, ok := utils.ParseInt64Value(event["user_id"])
	if !ok || userID != c.GetSelfID() {
		return
	}
	if subType == "lift_ban" {
		c.clearSelfMuted(groupID)
		return
	}
	if subType != "ban" {
		return
	}
	if seconds, ok := utils.ParseInt64Value(event["duration"]); ok && seconds > 0 {
		c.setSelfMutedUntil(groupID, time.Now().Add(time.Duration(seconds)*time.Second))
		return
	}
	c.clearSelfMuted(groupID)
}
