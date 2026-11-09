package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"mumu-bot/internal/media"
	"mumu-bot/internal/onebot"
	"mumu-bot/internal/refstore"

	"go.uber.org/zap"
)

// pendingMedia 待下载缓存的媒体
type pendingMedia struct {
	URL     string
	File    string
	Sticker bool
}

// contentPiece 展示正文的一段：普通文本或媒体占位
type contentPiece struct {
	text     string
	mediaIdx int    // >=0 表示这是待缓存媒体里的第几份，-1 表示纯文本
	fallback string // 媒体缓存失败时保留的文字占位
}

// prepareMessageContent 群聊与私聊复用回复补全和展示处理，原文保持独立
func (a *Agent) prepareMessageContent(ctx context.Context, msg *onebot.ConversationMessage) ([]media.Ref, []forwardDraft) {
	if err := a.resolveReplyInfo(ctx, msg); err != nil {
		zap.L().Debug("解析回复消息失败", zap.String("conversation_kind", msg.ConversationKind), zap.Int64("target_id", msg.TargetID), zap.Int64("message_id", msg.MessageID), zap.Error(err))
	}
	a.ensureIncomingFileRefs(msg)
	pieces, pending, forwards := a.parseMessageContent(ctx, msg)
	refs := a.cacheMedia(ctx, pending)
	a.markCollectedStickers(ctx, refs)
	session := a.sessions.get(msg.ConversationKind, msg.TargetID)
	for index := range refs {
		if refs[index].Hash == "" {
			continue
		}
		refs[index].Short = session.MediaShortHash(refs[index])
	}
	a.ensureIncomingImageRefs(ctx, refs)
	msg.FinalContent = renderContentPieces(pieces, refs)
	for _, draft := range forwards {
		for _, node := range draft.Nodes {
			refs = append(refs, node.Images...)
		}
	}
	return refs, forwards
}

// ensureIncomingFileRefs 给真实 QQ 文件登记 pending file_ref，不自动下载
func (a *Agent) ensureIncomingFileRefs(msg *onebot.ConversationMessage) {
	if a == nil || a.memory == nil || msg == nil {
		return
	}
	store := a.memory.RefStore()
	if store == nil {
		return
	}
	for index := range msg.Files {
		file := &msg.Files[index]
		if file.SnapshotRef != "" || strings.TrimSpace(file.FileID) == "" {
			continue
		}
		size := file.Size
		ref, err := store.CreatePending(file.Name, refstore.Locator{
			ConversationKind: msg.ConversationKind, TargetID: msg.TargetID,
			FileID: file.FileID, FileHash: file.FileHash, BusID: file.BusID,
			Name: file.Name, Size: size,
		})
		if err != nil {
			zap.L().Warn("登记 QQ 文件引用失败", zap.Int64("message_id", msg.MessageID), zap.Error(err))
			continue
		}
		file.SnapshotRef = ref.FileRef()
	}
}

// ensureIncomingImageRefs 把已缓存的当前展示图片固定成 image_ref
func (a *Agent) ensureIncomingImageRefs(ctx context.Context, refs []media.Ref) {
	if a == nil || a.memory == nil {
		return
	}
	store := a.memory.RefStore()
	if store == nil {
		return
	}
	for index := range refs {
		item := &refs[index]
		if item.Path == "" || item.ImageRef != "" {
			continue
		}
		name := item.Short + filepath.Ext(item.Path)
		ref, err := store.CreateReadyFromFile(ctx, name, item.Path, 10*1024*1024, nil)
		if err != nil {
			zap.L().Warn("保存图片引用失败", zap.String("short", item.Short), zap.Error(err))
			continue
		}
		item.ImageRef = ref.ImageRef()
		if item.Sticker {
			if err := store.MarkSticker(ref.ImageRef()); err != nil {
				zap.L().Warn("标记表情包引用失败", zap.String("image_ref", ref.ImageRef()), zap.Error(err))
			}
		}
	}
}

// formatFileSize 生成文件正文里的可读大小
func formatFileSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit && exp < 3; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}

// renderContentPieces 渲染展示正文，媒体占位带上会话内短哈希
func renderContentPieces(pieces []contentPiece, refs []media.Ref) string {
	var b strings.Builder
	for _, piece := range pieces {
		if piece.mediaIdx < 0 {
			b.WriteString(piece.text)
			continue
		}
		if piece.mediaIdx >= len(refs) {
			continue
		}
		ref := refs[piece.mediaIdx]
		if ref.Hash == "" || ref.Short == "" {
			// 缓存失败时保留原文字占位，不伪造图片
			b.WriteString(piece.fallback)
			continue
		}
		if ref.Sticker {
			if ref.Collected {
				b.WriteString(" [表情包:" + ref.Short + "(已收藏)]")
				continue
			}
			b.WriteString(" [表情包:" + ref.Short + "]")
			continue
		}
		if ref.ImageRef != "" {
			b.WriteString(" [图片:" + ref.ImageRef + "]")
			continue
		}
		b.WriteString(" [图片:" + ref.Short + "]")
	}
	return strings.TrimSpace(b.String())
}

// cacheMedia 下载并缓存消息里的媒体，失败的位置留空以便和展示占位对齐
func (a *Agent) cacheMedia(ctx context.Context, pending []pendingMedia) []media.Ref {
	if len(pending) == 0 {
		return nil
	}
	refs := make([]media.Ref, len(pending))
	for index, item := range pending {
		if ctx.Err() != nil {
			break
		}
		mediaMgr := a.currentMedia()
		if mediaMgr == nil {
			continue
		}
		ref, err := mediaMgr.Ensure(ctx, media.Source{URL: item.URL, File: item.File, Sticker: item.Sticker})
		if err != nil {
			zap.L().Debug("缓存媒体失败", zap.String("url", item.URL), zap.Error(err))
			continue
		}
		refs[index] = ref
	}
	return refs
}

// parseMessageContent 解析消息段，产出展示正文片段、待缓存的媒体与待落库的转发
func (a *Agent) parseMessageContent(ctx context.Context, msg *onebot.ConversationMessage) ([]contentPiece, []pendingMedia, []forwardDraft) {
	var pieces []contentPiece
	var pending []pendingMedia
	var forwards []forwardDraft

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
		forwardTotal += end - start
	}
	for _, part := range msg.MessageParts {
		switch part.Kind {
		case "text":
			pieces = append(pieces, contentPiece{text: part.Text, mediaIdx: -1})
		case "at":
			if part.AtUserID == onebot.AtAllUserID {
				pieces = append(pieces, contentPiece{text: "@全体成员", mediaIdx: -1})
				continue
			}
			if part.AtUserID > 0 {
				pieces = append(pieces, contentPiece{text: "@" + a.resolveMentionDisplayName(ctx, msg, part.AtUserID), mediaIdx: -1})
			}
		case "face":
			if part.Index < 0 || part.Index >= len(msg.Faces) {
				continue
			}
			face := msg.Faces[part.Index]
			switch {
			case face.Name != "":
				pieces = append(pieces, contentPiece{text: fmt.Sprintf(" %s%s%s", onebot.QFaceMarker, face.Name, onebot.QFaceMarkerEnd), mediaIdx: -1})
			case face.ID > 0:
				pieces = append(pieces, contentPiece{text: fmt.Sprintf(" %s%d%s", onebot.QFaceMarker, face.ID, onebot.QFaceMarkerEnd), mediaIdx: -1})
			default:
				pieces = append(pieces, contentPiece{text: " [无法识别的表情]", mediaIdx: -1})
			}
		case "image":
			if part.Index < 0 || part.Index >= len(msg.Images) {
				continue
			}
			image := msg.Images[part.Index]
			sticker := image.SubType == 1
			fallback := " [图片]"
			if sticker {
				fallback = " [表情包]"
			}
			pending = append(pending, pendingMedia{URL: image.URL, File: image.File, Sticker: sticker})
			pieces = append(pieces, contentPiece{mediaIdx: len(pending) - 1, fallback: fallback})
		case "video":
			pieces = append(pieces, contentPiece{text: " [视频，无法查看]", mediaIdx: -1})
		case "record":
			pieces = append(pieces, contentPiece{text: " [语音]", mediaIdx: -1})
		case "file":
			if part.Index < 0 || part.Index >= len(msg.Files) {
				continue
			}
			file := msg.Files[part.Index]
			name := file.Name
			if name == "" {
				name = "未命名文件"
			}
			text := " [文件:" + name
			if file.SnapshotRef != "" {
				text += "，file_ref=" + file.SnapshotRef + "（待取）"
			}
			if file.Size != nil && *file.Size > 0 {
				text += "，" + formatFileSize(*file.Size)
			}
			text += "，消息 " + strconv.FormatInt(msg.MessageID, 10)
			text += "，附件下标 " + strconv.Itoa(part.Index) + "]"
			pieces = append(pieces, contentPiece{text: text, mediaIdx: -1})
		case "card":
			if part.Index < 0 || part.Index >= len(msg.Cards) {
				continue
			}
			pieces = append(pieces, contentPiece{text: " " + msg.Cards[part.Index].Format(), mediaIdx: -1})
		case "forward":
			start := max(part.Index, 0)
			end := min(start+max(part.Count, 0), len(msg.ForwardContent))
			if start >= end {
				continue
			}
			handle, count, drafts := a.buildForwardNodes(ctx, msg.ForwardContent[start:end], 1, "")
			if handle == "" || count == 0 {
				continue
			}
			forwards = append(forwards, drafts...)
			pieces = append(pieces, contentPiece{text: fmt.Sprintf(" [合并转发 %s，共 %d 条]", handle, count), mediaIdx: -1})
		}
	}
	if forwardTotal > 0 {
		msg.ForwardContent = nil
	}
	return pieces, pending, forwards
}
