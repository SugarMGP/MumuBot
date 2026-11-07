package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"mumu-bot/internal/config"
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
		return botMentionDisplayName(a.botName())
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

// buildConversationToolContext 构造工具执行上下文，会话内的消息编号与媒体都由会话维护；
// 历史读取快照取本轮开始时数据库的真实最大原文编号，睡眠期间只落日志的消息也能被查到
func (a *Agent) buildConversationToolContext(ctx context.Context, kind string, targetID int64, session *Session) (context.Context, error) {
	snapshot, err := a.memory.LatestMessageLogID(ctx)
	if err != nil {
		return ctx, fmt.Errorf("读取历史读取快照失败: %w", err)
	}
	tc := &tools.ToolContext{
		ConversationKind:       kind,
		TargetID:               targetID,
		MemoryMgr:              a.memory.WithContext(ctx),
		Bot:                    a.bot,
		Session:                session,
		StickerDir:             config.Boot().StickerDir,
		SnapshotLogID:          snapshot,
		SnapshotLogIDSet:       true,
		RunID:                  newRunID(),
		MemoryReadVersions:     map[uint]uint64{},
		SessionRunningCallback: a.sessionRunning,
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
		SendImageBytesCallback: func(callCtx context.Context, imageRef, name string, data []byte) error {
			_, err := a.sendPersonalFileWithRef(callCtx, kind, targetID, name, data, true, imageRef)
			return err
		},
		SendFileCallback:       a.sendPersonalFile,
		SnapshotFiles:          nil,
		SessionMessageCallback: a.deliverSessionMessage,
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
	return tools.WithToolContext(ctx, tc), nil
}

// newRunID 生成当前 ReAct 的幂等分组标识
func newRunID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("run-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw[:])
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
	// 转发原图随父原文保留，只有实际读取时才建立由工具轨迹保活的快照
	a.ensureIncomingImageRefs(ctx, node.Images)
	for _, picture := range node.Images {
		replacement := "[图片不可用]"
		if picture.ImageRef != "" {
			replacement = "[图片:" + picture.ImageRef + "]"
		}
		node.Content = strings.ReplaceAll(node.Content, "[图片:"+picture.Hash+"]", replacement)
	}
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
