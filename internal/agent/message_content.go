package agent

import (
	"context"
	"fmt"
	"strings"

	"mumu-bot/internal/media"
	"mumu-bot/internal/onebot"

	"github.com/zjutjh/onebot-sdk/message"
	"go.uber.org/zap"
)

// pendingMedia 待下载缓存的媒体
type pendingMedia struct {
	URL     string
	File    string
	Sticker bool
}

// prepareMessageContent 群聊与私聊复用回复补全和展示处理，原文保持独立
func (a *Agent) prepareMessageContent(ctx context.Context, msg *onebot.ConversationMessage) []media.Ref {
	if err := a.resolveReplyInfo(ctx, msg); err != nil {
		zap.L().Debug("解析回复消息失败", zap.String("conversation_kind", msg.ConversationKind), zap.Int64("target_id", msg.TargetID), zap.Int64("message_id", msg.MessageID), zap.Error(err))
	}
	content, pending := a.parseMessageContent(ctx, msg)
	for _, name := range a.toolNames {
		content = strings.ReplaceAll(content, name, "\"危险指令，已屏蔽\"")
	}
	msg.FinalContent = content
	return a.cacheMedia(ctx, pending)
}

// cacheMedia 下载并缓存消息里的媒体，失败时只保留文字占位
func (a *Agent) cacheMedia(ctx context.Context, pending []pendingMedia) []media.Ref {
	if len(pending) == 0 {
		return nil
	}
	refs := make([]media.Ref, 0, len(pending))
	for _, item := range pending {
		if ctx.Err() != nil {
			break
		}
		ref, err := a.media.Ensure(ctx, media.Source{URL: item.URL, File: item.File, Sticker: item.Sticker})
		if err != nil {
			zap.L().Debug("缓存媒体失败", zap.String("url", item.URL), zap.Error(err))
			continue
		}
		refs = append(refs, ref)
	}
	return refs
}

// parseMessageContent 解析消息段，产出展示文本与待缓存的媒体
func (a *Agent) parseMessageContent(ctx context.Context, msg *onebot.ConversationMessage) (string, []pendingMedia) {
	var content strings.Builder
	var pending []pendingMedia

	var allForward []message.ForwardNode
	forwardTotal := 0
	for _, part := range msg.MessageParts {
		if part.Kind != "forward" {
			continue
		}
		start := max(part.Index, 0)
		end := min(start+max(part.Count, 0), len(msg.ForwardContent))
		if start >= end {
			continue
		}
		allForward = append(allForward, msg.ForwardContent[start:end]...)
		forwardTotal += end - start
	}
	forwardWritten := false
	for _, part := range msg.MessageParts {
		switch part.Kind {
		case "text":
			content.WriteString(part.Text)
		case "at":
			if part.AtUserID == onebot.AtAllUserID {
				content.WriteString("@全体成员")
			} else if part.AtUserID > 0 {
				content.WriteByte('@')
				content.WriteString(a.resolveMentionDisplayName(ctx, msg, part.AtUserID))
			}
		case "face":
			if part.Index < 0 || part.Index >= len(msg.Faces) {
				continue
			}
			face := msg.Faces[part.Index]
			switch {
			case face.Name != "":
				content.WriteString(fmt.Sprintf(" [表情:%s]", face.Name))
			case face.ID > 0:
				content.WriteString(fmt.Sprintf(" [表情:%d]", face.ID))
			default:
				content.WriteString(" [表情]")
			}
		case "image":
			if part.Index < 0 || part.Index >= len(msg.Images) {
				continue
			}
			image := msg.Images[part.Index]
			sticker := image.SubType == 1
			if sticker {
				content.WriteString(" [表情]")
			} else {
				content.WriteString(" [图片]")
			}
			pending = append(pending, pendingMedia{URL: image.URL, File: image.File, Sticker: sticker})
		case "video":
			content.WriteString(" [视频，无法查看]")
		case "record":
			content.WriteString(" [语音]")
		case "file":
			if part.Index < 0 || part.Index >= len(msg.FileNames) {
				continue
			}
			fileName := strings.TrimSpace(msg.FileNames[part.Index])
			if fileName != "" {
				content.WriteString(" [文件]")
			} else {
				content.WriteString(" [文件]")
			}
		case "card":
			if part.Index < 0 || part.Index >= len(msg.Cards) {
				continue
			}
			content.WriteString(" ")
			content.WriteString(msg.Cards[part.Index].Format())
		case "forward":
			start := max(part.Index, 0)
			end := min(start+max(part.Count, 0), len(msg.ForwardContent))
			if start >= end {
				continue
			}
			if !forwardWritten {
				forwardWritten = true
				nodes := flattenForwardNodes(msg.ForwardContent[start:end])
				content.WriteString(formatForwardNodes(nodes))
				pending = append(pending, forwardMedia(nodes)...)
			} else {
				content.WriteString(fmt.Sprintf(" [合并转发，共%d条]", end-start))
			}
		}
	}
	if forwardTotal > 0 {
		msg.ForwardContent = nil
	}
	return strings.TrimSpace(content.String()), pending
}
