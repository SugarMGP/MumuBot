package onebot

import (
	"context"
	"fmt"
	"time"

	"github.com/zjutjh/onebot-sdk/event"
	"go.uber.org/zap"
)

// conversationEvent 已确认会话归属、等待分配到达序号的事件
type conversationEvent struct {
	event      event.Event
	receivedAt time.Time
	arrivalSeq uint64
	kind       string
}

// enqueueEvent 识别强类型事件并按会话分发，无法定位会话或回调未就绪的事件直接丢弃
func (c *Client) enqueueEvent(ev event.Event) {
	switch e := ev.(type) {
	case *event.GroupMessage:
		// 机器人自发消息（post_type 为 message_sent）不作为新消息处理
		if e.PostType() != "message" {
			return
		}
		if c.onMessage == nil {
			return
		}
		c.requireConversationTarget(e.GroupID.Int64(), e.MessageID.Int64(), int64(e.GroupID), "group", e, "群消息")

	case *event.PrivateMessage:
		if e.PostType() != "message" {
			return
		}
		if c.onMessage == nil {
			return
		}
		// target_id 由 SDK 管线按方言补齐，缺失时无法定位会话
		c.requireConversationTarget(e.TargetID.Int64(), e.MessageID.Int64(), int64(e.TargetID), "private", e, "私聊消息")

	case *event.Poke:
		if c.onMessage == nil {
			return
		}
		kind, targetID := pokeConversation(e)
		if targetID <= 0 {
			zap.L().Warn("忽略缺少有效会话目标的戳一戳事件")
			return
		}
		c.dispatchEvent(kind, targetID, conversationEvent{event: e, receivedAt: time.Now(), kind: kind})

	case *event.GroupRecall:
		if c.onRecall == nil || e.GroupID <= 0 {
			return
		}
		c.dispatchEvent("group", int64(e.GroupID), conversationEvent{event: e, receivedAt: time.Now(), kind: "group"})

	case *event.FriendRecall:
		if c.onRecall == nil || e.UserID <= 0 {
			return
		}
		c.dispatchEvent("private", int64(e.UserID), conversationEvent{event: e, receivedAt: time.Now(), kind: "private"})

	case *event.GroupBan:
		c.handleGroupBan(e)
	case *event.GroupDecrease:
		c.handleGroupDecrease(e)
	case *event.FriendAdd:
		c.handleFriendAdd(e)
	case *event.FriendRequest:
		c.handleFriendRequest(e)
	}
}

// requireConversationTarget 校验群聊/私聊消息的会话目标与消息编号后分发
func (c *Client) requireConversationTarget(targetID, messageID, eventTarget int64, kind string, ev event.Event, label string) {
	if eventTarget <= 0 || messageID == 0 {
		zap.L().Warn("忽略缺少有效会话目标或编号的消息", zap.String("label", label))
		return
	}
	c.dispatchEvent(kind, targetID, conversationEvent{event: ev, receivedAt: time.Now(), kind: kind})
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
func (c *Client) dispatchEvent(kind string, targetID int64, queued conversationEvent) {
	queued.arrivalSeq = c.nextArrivalSeq(kind, targetID)
	c.eventWG.Add(1)
	go func() {
		defer c.eventWG.Done()
		c.handleConversationEvent(queued)
	}()
}

func (c *Client) handleConversationEvent(queued conversationEvent) {
	switch e := queued.event.(type) {
	case *event.GroupMessage:
		msg := c.parseGroupMessage(e)
		if msg == nil {
			// 解析失败：记录现场并构造占位消息消费到达序号，避免上层提交重排器死等
			zap.L().Warn("群消息段解析失败，已构造占位消息", zap.Int64("group_id", int64(e.GroupID)), zap.Int64("message_id", int64(e.MessageID)))
			msg = &ConversationMessage{ConversationKind: "group", TargetID: int64(e.GroupID), MessageID: int64(e.MessageID), ParseFailed: true}
		}
		msg.ReceivedAt = queued.receivedAt
		msg.ArrivalSeq = queued.arrivalSeq
		c.onMessage(msg)

	case *event.PrivateMessage:
		msg := c.parsePrivateMessage(e)
		if msg == nil {
			zap.L().Warn("私聊消息段解析失败，已构造占位消息", zap.Int64("target_id", int64(e.TargetID)), zap.Int64("message_id", int64(e.MessageID)))
			msg = &ConversationMessage{ConversationKind: "private", TargetID: int64(e.TargetID), MessageID: int64(e.MessageID), ParseFailed: true}
		}
		msg.ReceivedAt = queued.receivedAt
		msg.ArrivalSeq = queued.arrivalSeq
		c.onMessage(msg)

	case *event.Poke:
		c.handlePoke(e, queued.receivedAt, queued.arrivalSeq)

	case *event.GroupRecall:
		// 有效性由业务层校验（无效撤回会消费序号后跳过）
		c.onRecall("group", int64(e.GroupID), int64(e.MessageID), queued.arrivalSeq)

	case *event.FriendRecall:
		// 好友撤回没有操作人字段，按好友 QQ 定位会话
		c.onRecall("private", int64(e.UserID), int64(e.MessageID), queued.arrivalSeq)
	}
}

// pokeConversation 使用后端上报的群号或好友对端定位会话，好友 user_id 不是动作发送者
func pokeConversation(e *event.Poke) (string, int64) {
	if e.GroupID != 0 {
		return "group", int64(e.GroupID)
	}
	return "private", int64(e.UserID)
}

func (c *Client) handlePoke(e *event.Poke, receivedAt time.Time, arrivalSeq uint64) {
	// 有效性由业务层校验：无效戳一戳也会通过占位路径消费序号，不在此处提前返回
	kind, conversationTarget := pokeConversation(e)
	userID := int64(e.UserID)
	if kind == "private" {
		userID = int64(e.SenderID)
	}
	eventTime := time.Now()
	if seconds := e.Time(); seconds > 0 {
		eventTime = time.Unix(seconds, 0)
	}
	c.onMessage(&ConversationMessage{
		ConversationKind: kind,
		TargetID:         conversationTarget,
		UserID:           userID,
		AtList:           []int64{int64(e.TargetID)},
		Time:             eventTime,
		ReceivedAt:       receivedAt,
		ArrivalSeq:       arrivalSeq,
	})
}

// handleGroupBan 只关心机器人自身被禁言的时长
func (c *Client) handleGroupBan(e *event.GroupBan) {
	groupID := int64(e.GroupID)
	if groupID == 0 || int64(e.UserID) != c.GetSelfID() {
		return
	}
	if e.SubType == "lift_ban" {
		c.clearSelfMuted(groupID)
		return
	}
	if e.SubType != "ban" {
		return
	}
	if e.Duration > 0 {
		c.setSelfMutedUntil(groupID, time.Now().Add(time.Duration(e.Duration)*time.Second))
		return
	}
	c.clearSelfMuted(groupID)
}

// handleGroupDecrease 只在机器人自己退群、被踢或群解散时回调，普通成员进出群与联系人状态无关
func (c *Client) handleGroupDecrease(e *event.GroupDecrease) {
	if c.onGroupLeft == nil {
		return
	}
	groupID := int64(e.GroupID)
	if groupID <= 0 {
		return
	}
	userID := int64(e.UserID)
	selfID := c.GetSelfID()
	if e.SubType != "kick_me" && e.SubType != "disband" && !(selfID > 0 && userID == selfID) {
		return
	}
	c.onGroupLeft(groupID)
}

// handleFriendAdd 好友关系建立后立即放行，避免等下一次联系人同步
func (c *Client) handleFriendAdd(e *event.FriendAdd) {
	if c.onFriendAdd == nil || e.UserID <= 0 {
		return
	}
	c.onFriendAdd(int64(e.UserID))
}

func (c *Client) handleFriendRequest(e *event.FriendRequest) {
	zap.L().Debug("收到请求", zap.String("type", "friend"))
	if c.onFriendRequest == nil || e.UserID <= 0 {
		return
	}
	// 好友申请事件只上报 user_id/comment/flag，昵称需补查；事件流水线是串行的，
	// 补查放到独立协程执行，失败时昵称留空由后台回退展示 QQ 号
	go func() {
		ctx, cancel := context.WithTimeout(c.transportCtx, 5*time.Second)
		defer cancel()
		nickname := c.GetStrangerNickname(ctx, int64(e.UserID))
		c.onFriendRequest(FriendRequestEvent{Flag: e.Flag, UserID: int64(e.UserID), Nickname: nickname, Comment: e.Comment, ReceivedAt: time.Now()})
	}()
}
