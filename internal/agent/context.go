package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"
	"mumu-bot/internal/tools"
	"mumu-bot/internal/utils"

	"go.uber.org/zap"
)

func (a *Agent) buildGroupContext(groupID int64) string {
	if a.bot == nil {
		return ""
	}

	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()

	info, err := a.bot.GetGroupInfo(ctx, groupID)
	if err != nil {
		zap.L().Debug("获取群基础信息失败", zap.Int64("group_id", groupID), zap.Error(err))
		return ""
	}

	if info == nil {
		return ""
	}

	var parts []string
	if info.GroupName != "" {
		parts = append(parts, fmt.Sprintf("- 群名: %s", info.GroupName))
	}
	if info.MaxMemberCount > 0 {
		parts = append(parts, fmt.Sprintf("- 群人数: %d/%d", info.MemberCount, info.MaxMemberCount))
	} else if info.MemberCount > 0 {
		parts = append(parts, fmt.Sprintf("- 群人数: %d", info.MemberCount))
	}

	return strings.Join(parts, "\n")
}

func resolveMessageDisplayName(groupCard, nickname string) string {
	if card := strings.TrimSpace(groupCard); card != "" {
		return card
	}
	if name := strings.TrimSpace(nickname); name != "" {
		return name
	}
	return ""
}

func (a *Agent) resolveMentionDisplayName(ctx context.Context, msg *onebot.ConversationMessage, userID int64) string {
	selfID := a.bot.GetSelfID()
	if selfID > 0 && userID == selfID {
		return botMentionDisplayName(a.persona.GetName())
	}
	if displayName := strings.TrimSpace(msg.AtNames[userID]); displayName != "" {
		return displayName
	}
	if msg.ConversationKind == memory.ConversationKindPrivate {
		if userID == msg.UserID && msg.Nickname != "" {
			return msg.Nickname
		}
		return fmt.Sprintf("%d", userID)
	}
	if info, err := a.bot.GetGroupMemberInfo(ctx, msg.TargetID, userID, false); err == nil {
		if displayName := utils.FirstNonEmpty(info.Card, info.Nickname); displayName != "" {
			return displayName
		}
	} else {
		zap.L().Debug("补全提及成员显示名失败", zap.Int64("group_id", msg.TargetID), zap.Int64("user_id", userID), zap.Error(err))
	}
	return fmt.Sprintf("%d", userID)
}

// buildConversationToolContext 构造工具执行上下文，会话内的消息编号与媒体都由会话维护
func (a *Agent) buildConversationToolContext(ctx context.Context, kind string, targetID int64, session *Session) context.Context {
	tc := &tools.ToolContext{
		ConversationKind:  kind,
		TargetID:          targetID,
		MemoryMgr:         a.memory.WithContext(ctx),
		Bot:               a.bot,
		Session:           session,
		SnapshotMessageID: session.LatestMessageID(),
		SpeakCallback: func(callCtx context.Context, gid int64, content string, replyTo int64, mentions []int64) error {
			if kind == memory.ConversationKindPrivate {
				return a.doPrivateSpeak(callCtx, gid, content, replyTo)
			}
			return a.doSpeak(callCtx, gid, content, replyTo, mentions)
		},
		SendStickerCallback: func(callCtx context.Context, gid int64, fileName string, description string) error {
			if kind == memory.ConversationKindPrivate {
				return a.doPrivateSticker(callCtx, gid, fileName, description)
			}
			return a.doSendSticker(callCtx, gid, fileName, description)
		},
		SendImageURLCallback: func(callCtx context.Context, imageURL string) error {
			if kind == memory.ConversationKindPrivate {
				return a.doPrivateImage(callCtx, targetID, imageURL)
			}
			return a.doSendImageURL(callCtx, targetID, imageURL)
		},
		GetNewMessagesCallback: func(callCtx context.Context) (any, error) {
			return a.readNewMessages(callCtx, session)
		},
		ReadForwardCallback: func(callCtx context.Context, id string, index int) (any, error) {
			return a.readForward(callCtx, id, index)
		},
		GroupInfoCallback: func(callCtx context.Context) string {
			if kind != memory.ConversationKindGroup {
				return ""
			}
			return a.buildGroupContext(targetID)
		},
		RecallCallback: func(messageID int64, arrivalSeq uint64) {
			a.onRecall(kind, targetID, messageID, arrivalSeq)
		},
	}
	return tools.WithToolContext(ctx, tc)
}

// readForward 读取合并转发内容，index 为负表示先看这份转发的概览
func (a *Agent) readForward(ctx context.Context, id string, index int) (any, error) {
	forward, err := a.memory.WithContext(ctx).GetMessageForward(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("合并转发 %s 不存在或已过期", id)
	}
	nodes := forward.Nodes
	if index < 0 {
		summaries := make([]tools.ForwardMessageSummary, 0, len(nodes))
		for position, node := range nodes {
			summaries = append(summaries, tools.ForwardMessageSummary{
				Index:   position,
				Sender:  node.Sender,
				Preview: forwardPreview(node.Content),
				Nested:  node.Nested,
			})
		}
		return &tools.ReadForwardOutput{Success: true, ID: id, Count: len(nodes), Messages: summaries}, nil
	}
	if index >= len(nodes) {
		return nil, fmt.Errorf("合并转发 %s 一共 %d 条消息", id, len(nodes))
	}
	node := nodes[index]
	return &tools.ReadForwardOutput{
		Success: true,
		ID:      id,
		Index:   index,
		Sender:  node.Sender,
		Content: node.Content,
		Nested:  node.Nested,
	}, nil
}

// forwardPreview 截取转发消息的预览
func forwardPreview(content string) string {
	runes := []rune(content)
	if len(runes) > 40 {
		return string(runes[:40]) + "…"
	}
	return content
}

// readNewMessages 读取本轮思考开始后到达的新消息
func (a *Agent) readNewMessages(ctx context.Context, session *Session) (any, error) {
	tc := tools.GetToolContext(ctx)
	if tc == nil {
		return nil, tools.NewTerminalToolError(fmt.Errorf("消息读取能力未初始化"))
	}
	floor := tc.ThinkStartArrivalSeq
	messages := make([]map[string]any, 0, 4)
	observed := floor
	for _, user := range session.MessagesAfter(floor) {
		messages = append(messages, map[string]any{
			"message_ref": user.Ref,
			"sender_id":   user.SenderID,
			"nickname":    user.Nickname,
			"content":     user.Text,
			"arrival_seq": user.ArrivalSeq,
		})
		observed = max(observed, user.ArrivalSeq)
	}
	tc.ObservedThroughSeq = observed
	return map[string]any{"success": true, "complete": true, "messages": messages}, nil
}
