package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"mumu-bot/internal/config"
	"mumu-bot/internal/onebot"

	"go.uber.org/zap"
)

func (a *Agent) parseMessageContent(msg *onebot.GroupMessage) string {
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()

	cfg := config.Get()
	parts := msg.MessageParts

	var content strings.Builder
	hasForward := false
	for _, part := range parts {
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
			a.appendImageContent(ctx, cfg, msg, msg.Images[part.Index], &content)
		case "video":
			if part.Index < 0 || part.Index >= len(msg.Videos) {
				continue
			}
			if desc, err := a.describeVideoCached(ctx, msg.Videos[part.Index]); err == nil && desc != "" {
				content.WriteString(fmt.Sprintf(" [视频:%s]", desc))
			} else {
				content.WriteString(" [视频]")
			}
		case "record":
			content.WriteString(" [语音]")
		case "file":
			if part.Index < 0 || part.Index >= len(msg.FileNames) {
				continue
			}
			fileName := strings.TrimSpace(msg.FileNames[part.Index])
			if fileName == "" {
				content.WriteString(" [文件]")
			} else {
				content.WriteString(fmt.Sprintf(" [文件:%s]", fileName))
			}
		case "card":
			if part.Index >= 0 && part.Index < len(msg.Cards) {
				content.WriteByte(' ')
				content.WriteString(msg.Cards[part.Index].Format())
			}
		case "forward":
			start := max(part.Index, 0)
			end := min(start+max(part.Count, 0), len(msg.ForwardContent))
			if start >= end {
				continue
			}
			hasForward = true
			forward := msg.ForwardContent[start:end]
			summary, err := a.summarizeForwardMessages(ctx, forward)
			if err != nil {
				zap.L().Error("总结合并转发消息失败", zap.Int64("group_id", msg.GroupID), zap.Int64("message_id", msg.MessageID), zap.Error(err))
			}
			if summary != "" {
				content.WriteString(fmt.Sprintf(" [合并转发，共%d条:%s]", len(forward), summary))
			} else {
				content.WriteString(fmt.Sprintf(" [合并转发，共%d条]", len(forward)))
			}
		}
	}
	if hasForward {
		msg.ForwardContent = nil
	}
	return strings.TrimSpace(content.String())
}

func (a *Agent) appendImageContent(ctx context.Context, cfg *config.Config, msg *onebot.GroupMessage, img onebot.ImageInfo, content *strings.Builder) {
	if img.SubType == 1 {
		if img.Desc != "" {
			content.WriteString(fmt.Sprintf(" [表情包:%s]", img.Desc))
			return
		}
		visionDesc := ""
		if d, err := a.describeImageCached(ctx, img); err == nil {
			visionDesc = d
		}
		if img.URL != "" && visionDesc != "" && cfg != nil && cfg.Sticker.AutoSave && a.ctx.Err() == nil {
			a.wg.Add(1)
			go func(url string, stickerDesc string) {
				defer a.wg.Done()
				a.autoSaveSticker(a.ctx, url, stickerDesc)
			}(img.URL, visionDesc)
		}
		if visionDesc != "" {
			content.WriteString(fmt.Sprintf(" [表情包:%s]", visionDesc))
		} else {
			content.WriteString(" [表情包]")
		}
		return
	}
	if img.Desc != "" {
		content.WriteString(fmt.Sprintf(" [图片:%s]", img.Desc))
		return
	}
	visionDesc := ""
	if d, err := a.describeImageCached(ctx, img); err == nil {
		visionDesc = d
	}
	if visionDesc != "" {
		content.WriteString(fmt.Sprintf(" [图片:%s]", visionDesc))
	} else {
		content.WriteString(" [图片]")
	}
}
