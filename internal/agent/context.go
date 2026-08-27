package agent

import (
	"context"
	"errors"
	"fmt"
	"mumu-bot/internal/config"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"
	"mumu-bot/internal/utils"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/jellydator/ttlcache/v3"
	"go.uber.org/zap"
)

func (a *Agent) buildGroupContext(groupID int64) string {
	if a.bot == nil {
		return ""
	}

	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()

	info, err := a.bot.GetGroupInfo(ctx, groupID, false)
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

func (a *Agent) buildMemoryContext(ctx context.Context, groupID int64, snapshot []*onebot.GroupMessage, query memory.HybridQuery) ([]memory.Memory, []memory.Memory) {
	if query.Empty() {
		return nil, nil
	}
	selfID := a.bot.GetSelfID()
	related := make([]int64, 0, len(snapshot)*2)
	for _, msg := range snapshot {
		if msg == nil {
			continue
		}
		if msg.UserID > 0 && msg.UserID != selfID {
			related = append(related, msg.UserID)
		}
		if msg.Reply != nil && msg.Reply.SenderID > 0 && msg.Reply.SenderID != selfID {
			related = append(related, msg.Reply.SenderID)
		}
	}
	local, cross, err := a.memory.RecallContext(ctx, groupID, selfID, related, query)
	if err != nil {
		zap.L().Warn("主动记忆检索失败", zap.Int64("group_id", groupID), zap.Error(err))
	}
	return local, cross
}

func (a *Agent) memorySubjectNames(groups ...[]memory.Memory) map[int64]string {
	result := make(map[int64]string)
	selfID := a.bot.GetSelfID()
	for _, items := range groups {
		for _, item := range items {
			if item.SubjectUserID <= 0 || item.SubjectUserID == selfID {
				continue
			}
			if _, exists := result[item.SubjectUserID]; exists {
				continue
			}
			if profile, err := a.memory.GetMemberProfile(item.SubjectUserID); err == nil {
				result[item.SubjectUserID] = profile.Nickname
			}
		}
	}
	return result
}

func collectTextFragments(msgs []*onebot.GroupMessage) []string {
	if len(msgs) == 0 {
		return nil
	}
	parts := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		if text := strings.TrimSpace(msg.Content); text != "" {
			parts = append(parts, text)
		}
	}
	return parts
}

func collectTextContext(msgs []*onebot.GroupMessage) string {
	return strings.Join(collectTextFragments(msgs), "\n")
}

func collectRetrievalTextFragments(readMessages, currentMessages []*onebot.GroupMessage, bufferSize int) []string {
	window := bufferSize / 2
	if window < 10 {
		window = 10
	} else if window > 30 {
		window = 30
	}
	if len(readMessages) > window {
		readMessages = readMessages[len(readMessages)-window:]
	}
	messages := make([]*onebot.GroupMessage, 0, len(readMessages)+len(currentMessages))
	messages = append(messages, readMessages...)
	messages = append(messages, currentMessages...)
	return collectTextFragments(messages)
}

func splitMessageSnapshot(buffer []*onebot.GroupMessage, lastReadMessage *onebot.GroupMessage) (readMessages, currentMessages []*onebot.GroupMessage) {
	cursor := -1
	for i, msg := range buffer {
		if msg == lastReadMessage {
			cursor = i
			break
		}
	}
	for i, msg := range buffer {
		if msg == nil {
			continue
		}
		if i <= cursor {
			readMessages = append(readMessages, msg)
		} else {
			currentMessages = append(currentMessages, msg)
		}
	}
	return readMessages, currentMessages
}

func renderChatContext(buffer []*onebot.GroupMessage, lastReadMessage *onebot.GroupMessage) string {
	if len(buffer) == 0 {
		return ""
	}

	readMessages, currentMessages := splitMessageSnapshot(buffer, lastReadMessage)
	var b strings.Builder
	for _, message := range readMessages {
		if message == nil || strings.TrimSpace(message.FinalContent) == "" {
			continue
		}
		b.WriteString("(OLD)")
		b.WriteString(message.FinalContent)
	}
	for _, message := range currentMessages {
		if message == nil || strings.TrimSpace(message.FinalContent) == "" {
			continue
		}
		b.WriteString(message.FinalContent)
	}
	return b.String()
}

func (a *Agent) buildRecentPeopleContext(buffer []*onebot.GroupMessage, groupID int64) string {
	if len(buffer) == 0 {
		return ""
	}

	seenIDs := make(map[int64]struct{}, 3)
	ids := make([]int64, 0, 3)
	selfID := a.bot.GetSelfID()
	for i := len(buffer) - 1; i >= 0; i-- {
		userID := buffer[i].UserID
		if userID == 0 || userID == selfID {
			continue
		}
		if _, ok := seenIDs[userID]; ok {
			continue
		}
		seenIDs[userID] = struct{}{}
		ids = append(ids, userID)
		if len(ids) >= 3 {
			break
		}
	}
	if len(ids) == 0 {
		return ""
	}

	latestNames := make(map[int64]*onebot.GroupMessage, len(ids))
	for i := len(buffer) - 1; i >= 0; i-- {
		if _, ok := latestNames[buffer[i].UserID]; ok {
			continue
		}
		latestNames[buffer[i].UserID] = buffer[i]
	}

	lines := make([]string, 0, len(ids))
	for _, userID := range ids {
		latestMsg := latestNames[userID]
		nickname := ""
		groupCard := ""
		if latestMsg != nil {
			nickname = latestMsg.Nickname
			groupCard = latestMsg.GroupCard
		}
		profile, err := a.memory.GetMemberProfile(userID)
		if err != nil {
			name := utils.FirstNonEmpty(groupCard, nickname)
			if name == "" {
				name = fmt.Sprintf("%d", userID)
			}
			lines = append(lines, fmt.Sprintf("- %s：最近在场。", name))
			continue
		}

		currentGroupName := strings.TrimSpace(groupCard)
		if currentGroupName == "" {
			currentGroupName, _ = a.memory.LatestMemberGroupCard(userID, groupID)
		}
		displayName := currentGroupName
		traits, _ := a.memory.ListMemberTraits(userID)
		if displayName == "" {
			for _, trait := range traits {
				if trait.Kind == "alias" {
					displayName = trait.Value
					break
				}
			}
		}
		if displayName == "" {
			displayName = strings.TrimSpace(nickname)
		}
		if displayName == "" {
			displayName = fmt.Sprintf("%d", userID)
		}
		originalNickname := strings.TrimSpace(profile.Nickname)
		if originalNickname == "" {
			originalNickname = strings.TrimSpace(nickname)
		}

		details := make([]string, 0, 4)
		if originalNickname != "" && originalNickname != displayName {
			details = append(details, "原昵称: "+originalNickname)
		}
		for _, trait := range traits {
			if trait.Kind == "speaking" || trait.Kind == "phrase" {
				details = append(details, trait.Kind+": "+trait.Value)
			}
		}

		lines = append(lines, fmt.Sprintf("- %s：%s。", displayName, strings.Join(details, "，")))
	}

	return strings.Join(lines, "\n")
}

func (a *Agent) getMemberProfileForDisplay(userID int64) (*memory.MemberProfile, error) {
	if userID <= 0 {
		return nil, errors.New("invalid user id")
	}
	if a == nil || a.memory == nil {
		return nil, errors.New("member profile lookup unavailable")
	}
	return a.memory.GetMemberProfile(userID)
}

func (a *Agent) resolveRenderedDisplayName(userID int64, groupCard, nickname string) string {
	if card := strings.TrimSpace(groupCard); card != "" {
		return card
	}
	if name := strings.TrimSpace(nickname); name != "" {
		return name
	}
	if profile, err := a.getMemberProfileForDisplay(userID); err == nil {
		if name := strings.TrimSpace(profile.Nickname); name != "" {
			return name
		}
	}
	return ""
}

func (a *Agent) resolveMentionDisplayName(ctx context.Context, msg *onebot.GroupMessage, userID int64) string {
	selfID := a.bot.GetSelfID()
	if selfID > 0 && userID == selfID {
		return botMentionDisplayName(a.persona.GetName())
	}
	if displayName := strings.TrimSpace(msg.AtNames[userID]); displayName != "" {
		return displayName
	}
	if info, err := a.bot.GetGroupMemberInfo(ctx, msg.GroupID, userID, false); err == nil {
		if displayName := utils.FirstNonEmpty(info.Card, info.Nickname); displayName != "" {
			return displayName
		}
	} else {
		zap.L().Debug("补全提及成员显示名失败", zap.Int64("group_id", msg.GroupID), zap.Int64("user_id", userID), zap.Error(err))
	}
	if displayName := a.resolveRenderedDisplayName(userID, "", ""); displayName != "" {
		return displayName
	}
	return fmt.Sprintf("%d", userID)
}

func visionCacheKey(kind string, remoteURL string, file string) string {
	key := strings.TrimSpace(remoteURL)
	if key == "" {
		key = strings.TrimSpace(file)
	}
	if key == "" {
		return ""
	}
	return kind + ":" + key
}

func (a *Agent) describeImageCached(ctx context.Context, img onebot.ImageInfo) (string, error) {
	if img.URL == "" {
		return "", nil
	}

	cacheKey := visionCacheKey("image", img.URL, img.File)
	if cacheKey != "" {
		if cached := a.visionCache.Get(cacheKey); cached != nil {
			return cached.Value(), nil
		}
	}

	desc, err := a.vision.DescribeImage(ctx, img.URL)
	if err == nil && cacheKey != "" && strings.TrimSpace(desc) != "" {
		a.visionCache.Set(cacheKey, desc, ttlcache.DefaultTTL)
	}
	return desc, err
}

func (a *Agent) describeVideoCached(ctx context.Context, vid onebot.VideoInfo) (string, error) {
	if vid.URL == "" {
		return "", nil
	}

	cacheKey := visionCacheKey("video", vid.URL, vid.File)
	if cacheKey != "" {
		if cached := a.visionCache.Get(cacheKey); cached != nil {
			return cached.Value(), nil
		}
	}

	desc, err := a.vision.DescribeVideo(ctx, vid.URL)
	if err == nil && cacheKey != "" && strings.TrimSpace(desc) != "" {
		a.visionCache.Set(cacheKey, desc, ttlcache.DefaultTTL)
	}
	return desc, err
}

func collectForwardMedia(value interface{}, imageURLs, videoURLs *[]string) {
	switch current := value.(type) {
	case []interface{}:
		for _, item := range current {
			collectForwardMedia(item, imageURLs, videoURLs)
		}
	case map[string]interface{}:
		segmentType, isSegment := current["type"].(string)
		if isSegment {
			data, _ := current["data"].(map[string]interface{})
			switch segmentType {
			case "image", "mface":
				if url, ok := data["url"].(string); ok && strings.TrimSpace(url) != "" {
					*imageURLs = append(*imageURLs, url)
				}
			case "video":
				if url, ok := data["url"].(string); ok && strings.TrimSpace(url) != "" {
					*videoURLs = append(*videoURLs, url)
				}
			case "forward":
				collectForwardMedia(data["content"], imageURLs, videoURLs)
			}
			return
		}
		collectForwardMedia(current["message"], imageURLs, videoURLs)
	}
}

func (a *Agent) summarizeForwardMessages(ctx context.Context, content []interface{}) (string, error) {
	if len(content) == 0 {
		return "", nil
	}
	var imageURLs, videoURLs []string
	collectForwardMedia(content, &imageURLs, &videoURLs)
	raw, err := sonic.MarshalString(content)
	if err != nil {
		return "", err
	}
	return a.vision.SummarizeForward(ctx, raw, imageURLs, videoURLs)
}

func (a *Agent) parseMessageContent(msg *onebot.GroupMessage) string {
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()

	cfg := config.Get()

	replyInfo := ""
	if msg.Reply != nil {
		replyDisplayName := a.resolveRenderedDisplayName(msg.Reply.SenderID, msg.Reply.GroupCard, msg.Reply.Nickname)
		replyInfo = fmt.Sprintf(" [回复 #%d]", msg.Reply.MessageID)
		if replyDisplayName != "" {
			replyInfo = fmt.Sprintf(" [回复 #%d %s]", msg.Reply.MessageID, replyDisplayName)
		}
	}

	content := msg.Content
	if len(msg.AtList) > 0 {
		mentions := make([]string, 0, len(msg.AtList))
		for _, userID := range msg.AtList {
			if userID == onebot.AtAllUserID {
				mentions = append(mentions, "@全体成员")
				continue
			}
			if userID <= 0 {
				continue
			}
			displayName := a.resolveMentionDisplayName(ctx, msg, userID)
			mentions = append(mentions, "@"+displayName)
		}
		if len(mentions) > 0 {
			content = strings.Join(mentions, " ") + " " + content
		}
	}

	for _, face := range msg.Faces {
		if face.Name != "" {
			content += fmt.Sprintf(" [表情:%s]", face.Name)
		} else if face.ID > 0 {
			content += fmt.Sprintf(" [表情:%d]", face.ID)
		} else {
			content += " [表情]"
		}
	}

	for _, img := range msg.Images {
		if img.SubType == 1 {
			if img.Desc != "" {
				if cfg.Agent.UseNativeMultimodal {
					content += " [表情包]"
				} else {
					content += fmt.Sprintf(" [表情包:%s]", img.Desc)
				}
				continue
			}
			var visionDesc string
			if d, err := a.describeImageCached(ctx, img); err == nil {
				visionDesc = d
			}
			if img.URL != "" && visionDesc != "" && cfg.Sticker.AutoSave && a.ctx.Err() == nil {
				a.wg.Add(1)
				go func(url string, stickerDesc string) {
					defer a.wg.Done()
					a.autoSaveSticker(a.ctx, url, stickerDesc)
				}(img.URL, visionDesc)
			}
			if cfg.Agent.UseNativeMultimodal {
				content += " [表情包]"
			} else if visionDesc != "" {
				content += fmt.Sprintf(" [表情包:%s]", visionDesc)
			} else {
				content += " [表情包]"
			}
		} else {
			if img.Desc != "" {
				if cfg.Agent.UseNativeMultimodal {
					content += " [图片]"
				} else {
					content += fmt.Sprintf(" [图片:%s]", img.Desc)
				}
				continue
			}
			var visionDesc string
			if !cfg.Agent.UseNativeMultimodal {
				if d, err := a.describeImageCached(ctx, img); err == nil {
					visionDesc = d
				}
			}
			if visionDesc != "" {
				content += fmt.Sprintf(" [图片:%s]", visionDesc)
			} else {
				content += " [图片]"
			}
		}
	}

	for _, vid := range msg.Videos {
		if desc, err := a.describeVideoCached(ctx, vid); err == nil && desc != "" {
			content += fmt.Sprintf(" [视频:%s]", desc)
		} else {
			content += " [视频]"
		}
	}
	if msg.HasRecord {
		content += " [语音]"
	}
	for _, fileName := range msg.FileNames {
		fileName = strings.TrimSpace(fileName)
		if fileName == "" {
			content += " [文件]"
			continue
		}
		content += fmt.Sprintf(" [文件:%s]", fileName)
	}
	for _, card := range msg.Cards {
		content += " " + card.Format()
	}
	if len(msg.ForwardContent) > 0 {
		count := len(msg.ForwardContent)
		summary, err := a.summarizeForwardMessages(ctx, msg.ForwardContent)
		if err != nil {
			zap.L().Error("总结合并转发消息失败", zap.Int64("group_id", msg.GroupID), zap.Int64("message_id", msg.MessageID), zap.Error(err))
		}
		msg.ForwardContent = nil
		if summary != "" {
			content += fmt.Sprintf(" [合并转发，共%d条:%s]", count, summary)
		} else {
			content += fmt.Sprintf(" [合并转发，共%d条]", count)
		}
	}

	var qid string
	if msg.UserID == a.bot.GetSelfID() {
		qid = "你"
	} else {
		qid = fmt.Sprintf("%d", msg.UserID)
	}
	displayName := a.resolveRenderedDisplayName(msg.UserID, msg.GroupCard, msg.Nickname)
	if displayName == "" {
		displayName = qid
	}

	return fmt.Sprintf("[%s] #%d %s(%s):%s %s\n",
		msg.Time.Format("15:04:05"), msg.MessageID, displayName, qid, replyInfo, content)
}
