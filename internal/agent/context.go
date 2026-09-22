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

func (a *Agent) buildMemoryContext(ctx context.Context, groupID int64, snapshot []*onebot.GroupMessage, query memory.HybridQuery, upper uint) ([]memory.KnowledgeItem, []memory.KnowledgeItem, []memory.KnowledgeRelation) {
	if query.Empty() {
		return nil, nil, nil
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
	related = append(related, 0, selfID)
	local, err := a.memory.SearchKnowledge(ctx, memory.KnowledgeSearchOptions{GroupID: groupID, SubjectIDs: related, Prepared: &query, ThroughID: upper, Limit: 6})
	direct := local
	// 为关联知识预留两个名额，未用完的名额再由直接命中结果补齐
	if len(local) > 4 {
		local = append([]memory.KnowledgeItem(nil), local[:4]...)
	}
	var cross []memory.KnowledgeItem
	if err == nil && len(local) < 2 {
		found, e := a.memory.SearchKnowledge(ctx, memory.KnowledgeSearchOptions{GroupID: groupID, SelfID: selfID, SubjectUserID: &selfID, Prepared: &query, ThroughID: upper, Limit: 3})
		if e != nil {
			err = e
		} else {
			for _, item := range found {
				if item.GroupID != groupID {
					cross = append(cross, item)
				}
			}
		}
	}
	if err != nil {
		zap.L().Warn("主动记忆检索失败", zap.Int64("group_id", groupID), zap.Error(err))
	}
	var relations []memory.KnowledgeRelation
	var seeds []uint
	for _, item := range local {
		seeds = append(seeds, item.ID)
	}
	if graph, e := a.memory.GetKnowledgeNeighborhood(ctx, groupID, seeds, 1, false, memory.KnowledgeGraphOptions{ThroughID: upper, SubjectIDs: related}); e != nil {
		zap.L().Warn("关联记忆检索失败", zap.Error(e))
		local = direct
	} else {
		seen := map[uint]bool{}
		for _, item := range local {
			seen[item.ID] = true
		}
		for _, item := range graph.Items {
			if !seen[item.ID] && len(local) < 6 {
				local = append(local, item)
				seen[item.ID] = true
			}
		}
		for _, item := range direct {
			if !seen[item.ID] && len(local) < 6 {
				local = append(local, item)
				seen[item.ID] = true
			}
		}
		for _, rel := range graph.Relations {
			if seen[rel.SourceItemID] && seen[rel.TargetItemID] && len(relations) < 8 {
				relations = append(relations, rel)
			}
		}
	}
	return local, cross, relations
}

func (a *Agent) memorySubjectNames(groups ...[]memory.KnowledgeItem) map[int64]string {
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

// splitMessageSnapshot 使用本进程到达序号划分快照，不比较 OneBot message_id
func splitMessageSnapshot(buffer []*onebot.GroupMessage, readSeq uint64, selfID int64) (readMessages, currentMessages []*onebot.GroupMessage) {
	for _, msg := range buffer {
		if msg == nil {
			continue
		}
		if msg.UserID == selfID || msg.ArrivalSeq <= readSeq {
			readMessages = append(readMessages, msg)
		} else {
			currentMessages = append(currentMessages, msg)
		}
	}
	return readMessages, currentMessages
}

func hasDisplayContext(messages []*onebot.GroupMessage) bool {
	for _, message := range messages {
		if message != nil && strings.TrimSpace(message.FinalContent) != "" {
			return true
		}
	}
	return false
}

func (a *Agent) renderModelMessage(message *onebot.GroupMessage, tc *tools.ToolContext) string {
	if message == nil || strings.TrimSpace(message.FinalContent) == "" {
		return ""
	}
	userID := fmt.Sprintf("%d", message.UserID)
	if message.UserID == a.bot.GetSelfID() {
		userID = "你"
	}
	displayName := resolveMessageDisplayName(message.GroupCard, message.Nickname)
	if displayName == "" {
		displayName = userID
	}
	ref := tc.MessageRef(message.MessageID)
	if ref != "" {
		ref = "[" + ref + "] "
	}
	reply := ""
	if message.Reply != nil {
		if replyRef := tc.MessageRef(message.Reply.MessageID); replyRef != "" {
			reply = "[回复 " + replyRef + "] "
		} else if message.Reply.SenderID != 0 || strings.TrimSpace(message.Reply.Nickname) != "" || strings.TrimSpace(message.Reply.Content) != "" {
			replyName := resolveMessageDisplayName(message.Reply.GroupCard, message.Reply.Nickname)
			if replyName == "" {
				replyName = "未知用户"
			}
			replyContent := strings.TrimSpace(message.Reply.Content)
			if runes := []rune(replyContent); len(runes) > 20 {
				replyContent = string(runes[:20])
			}
			reply = fmt.Sprintf("[回复 %s(%d): %s] ", replyName, message.Reply.SenderID, replyContent)
		} else {
			reply = "[回复历史消息] "
		}
	}
	return fmt.Sprintf("%s[%s] %s(%s): %s%s\n", ref, message.Time.Format("15:04:05"), displayName, userID, reply, strings.TrimSpace(message.FinalContent))
}

func (a *Agent) renderChatContext(buffer []*onebot.GroupMessage, readSeq uint64, tc *tools.ToolContext) string {
	if len(buffer) == 0 {
		return ""
	}

	readMessages, currentMessages := splitMessageSnapshot(buffer, readSeq, a.bot.GetSelfID())
	var b strings.Builder
	for _, message := range readMessages {
		content := a.renderModelMessage(message, tc)
		if content == "" {
			continue
		}
		b.WriteString("(OLD)")
		b.WriteString(content)
	}
	for _, message := range currentMessages {
		b.WriteString(a.renderModelMessage(message, tc))
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

		details := make([]string, 0, 5)
		details = append(details, fmt.Sprintf("好感度 %.2f（%d级·%s）", profile.Intimacy, memory.IntimacyLevel(profile.Intimacy), memory.IntimacyLevelName(profile.Intimacy)))
		if originalNickname != "" && originalNickname != displayName {
			details = append(details, "原昵称: "+originalNickname)
		}

		lines = append(lines, fmt.Sprintf("- %s：%s。", displayName, strings.Join(details, "，")))
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

	describe := a.vision.DescribeImage
	kind := "image"
	if img.SubType == 1 {
		describe = a.vision.DescribeSticker
		kind = "sticker"
	}
	cacheKey := visionCacheKey(kind, img.URL, img.File)
	if cacheKey != "" {
		if cached := a.visionCache.Get(cacheKey); cached != nil {
			return cached.Value(), nil
		}
	}

	desc, err := describe(ctx, img.URL)
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
