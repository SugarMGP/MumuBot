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

// buildRecentPeopleContext 汇总最近在场的人与好感度
func (a *Agent) buildRecentPeopleContext(ctx context.Context, session *Session, groupID int64) string {
	users := session.RecentUsers(3)
	if len(users) == 0 {
		return ""
	}
	mem := a.memory.WithContext(ctx)
	seenIDs := make(map[int64]struct{}, len(users))
	lines := make([]string, 0, len(users))
	for _, user := range users {
		if user.SenderID == 0 || user.SenderID == a.bot.GetSelfID() {
			continue
		}
		if _, exists := seenIDs[user.SenderID]; exists {
			continue
		}
		seenIDs[user.SenderID] = struct{}{}
		displayName := strings.TrimSpace(user.Nickname)
		profile, err := mem.GetMemberProfile(user.SenderID)
		if err != nil || profile == nil {
			if displayName == "" {
				displayName = fmt.Sprintf("%d", user.SenderID)
			}
			lines = append(lines, fmt.Sprintf("- %s：最近在场。", displayName))
			continue
		}
		currentName := strings.TrimSpace(profile.Nickname)
		if currentName == "" {
			currentName = displayName
		}
		if currentName == "" {
			currentName = fmt.Sprintf("%d", user.SenderID)
		}
		details := []string{fmt.Sprintf("好感度 %.2f（%d级·%s）", profile.Intimacy, memory.IntimacyLevel(profile.Intimacy), memory.IntimacyLevelName(profile.Intimacy))}
		if profile.MessageCount > 0 {
			details = append(details, fmt.Sprintf("历史发言 %d 次", profile.MessageCount))
		}
		lines = append(lines, fmt.Sprintf("- %s：%s。", currentName, strings.Join(details, "，")))
	}
	return strings.Join(lines, "\n")
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
		RecallCallback: func(messageID int64, arrivalSeq uint64) {
			a.onRecall(kind, targetID, messageID, arrivalSeq)
		},
	}
	return tools.WithToolContext(ctx, tc)
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
			"content":     user.Content,
			"arrival_seq": user.ArrivalSeq,
		})
		observed = max(observed, user.ArrivalSeq)
	}
	tc.ObservedThroughSeq = observed
	return map[string]any{"success": true, "complete": true, "messages": messages}, nil
}
