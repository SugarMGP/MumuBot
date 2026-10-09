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
	"github.com/zjutjh/onebot-sdk/message"
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

func (a *Agent) buildConversationMemoryContext(ctx context.Context, kind string, targetID int64, snapshot []*onebot.ConversationMessage, query memory.HybridQuery, upper uint) ([]memory.KnowledgeItem, []memory.KnowledgeItem, []memory.KnowledgeRelation) {
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
	related = append(related, selfID)
	if kind == memory.ConversationKindGroup {
		related = append(related, 0)
	}
	local, err := a.memory.SearchKnowledge(ctx, memory.KnowledgeSearchOptions{ConversationKind: kind, TargetID: targetID, SubjectIDs: related, Prepared: &query, ThroughID: upper, Limit: 6})
	direct := local
	// 为关联知识预留两个名额，未用完的名额再由直接命中结果补齐
	if len(local) > 4 {
		local = append([]memory.KnowledgeItem(nil), local[:4]...)
	}
	var cross []memory.KnowledgeItem
	if err == nil && kind == memory.ConversationKindGroup && len(local) < 2 {
		found, e := a.memory.SearchKnowledge(ctx, memory.KnowledgeSearchOptions{ConversationKind: kind, TargetID: targetID, SelfID: selfID, SubjectUserID: &selfID, Prepared: &query, ThroughID: upper, Limit: 3})
		if e != nil {
			err = e
		} else {
			// 跨会话查询的 SQL 条件已排除当前会话，结果无需再次过滤
			cross = found
		}
	}
	if err != nil {
		zap.L().Warn("主动记忆检索失败", zap.String("conversation_kind", kind), zap.Int64("target_id", targetID), zap.Error(err))
	}
	var relations []memory.KnowledgeRelation
	var seeds []uint
	for _, item := range local {
		seeds = append(seeds, item.ID)
	}
	if graph, e := a.memory.GetKnowledgeNeighborhoodScope(ctx, kind, targetID, seeds, 1, false, memory.KnowledgeGraphOptions{ThroughID: upper, SubjectIDs: related}); e != nil {
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

func (a *Agent) memorySubjectNames(ctx context.Context, groups ...[]memory.KnowledgeItem) map[int64]string {
	result := make(map[int64]string)
	mem := a.memory.WithContext(ctx)
	selfID := a.bot.GetSelfID()
	for _, items := range groups {
		for _, item := range items {
			if item.SubjectUserID <= 0 || item.SubjectUserID == selfID {
				continue
			}
			if _, exists := result[item.SubjectUserID]; exists {
				continue
			}
			if profile, err := mem.GetMemberProfile(item.SubjectUserID); err == nil {
				result[item.SubjectUserID] = profile.Nickname
			}
		}
	}
	return result
}

func collectTextFragments(msgs []*onebot.ConversationMessage) []string {
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

func collectTextContext(msgs []*onebot.ConversationMessage) string {
	return strings.Join(collectTextFragments(msgs), "\n")
}

func collectRetrievalTextFragments(readMessages, currentMessages []*onebot.ConversationMessage, bufferSize int) []string {
	window := bufferSize / 2
	if window < 10 {
		window = 10
	} else if window > 30 {
		window = 30
	}
	if len(readMessages) > window {
		readMessages = readMessages[len(readMessages)-window:]
	}
	messages := make([]*onebot.ConversationMessage, 0, len(readMessages)+len(currentMessages))
	messages = append(messages, readMessages...)
	messages = append(messages, currentMessages...)
	return collectTextFragments(messages)
}

// splitMessageSnapshot 使用本进程到达序号划分快照，不比较 OneBot message_id
func splitMessageSnapshot(buffer []*onebot.ConversationMessage, readSeq uint64, selfID int64) (readMessages, currentMessages []*onebot.ConversationMessage) {
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

func hasDisplayContext(messages []*onebot.ConversationMessage) bool {
	for _, message := range messages {
		if message != nil && strings.TrimSpace(message.FinalContent) != "" {
			return true
		}
	}
	return false
}

func (a *Agent) renderModelMessage(message *onebot.ConversationMessage, tc *tools.ToolContext) string {
	if message == nil {
		return ""
	}
	final := strings.TrimSpace(message.FinalContent)
	if final == "" {
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
	return fmt.Sprintf("%s[%s] %s(%s): %s%s\n", ref, message.Time.Format("15:04:05"), displayName, userID, reply, final)
}

func (a *Agent) renderChatContext(buffer []*onebot.ConversationMessage, readSeq uint64, tc *tools.ToolContext) string {
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

func (a *Agent) buildRecentPeopleContext(ctx context.Context, buffer []*onebot.ConversationMessage, groupID int64) string {
	if len(buffer) == 0 {
		return ""
	}

	mem := a.memory.WithContext(ctx)
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

	latestNames := make(map[int64]*onebot.ConversationMessage, len(ids))
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
		profile, err := mem.GetMemberProfile(userID)
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
			currentGroupName, _ = mem.LatestMemberGroupCard(userID, groupID)
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

func (a *Agent) describeWebImageCached(ctx context.Context, imageURL string) (string, error) {
	imageURL = strings.TrimSpace(imageURL)
	if imageURL == "" {
		return "", fmt.Errorf("图片 URL 为空")
	}
	return a.describeImageCached(ctx, onebot.ImageInfo{URL: imageURL})
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

// collectForwardMedia 递归收集合并转发节点中的图片与视频 URL
func collectForwardMedia(nodes []message.ForwardNode, imageURLs, videoURLs *[]string) {
	for _, node := range nodes {
		for _, seg := range node.Message {
			switch data := seg.Data.(type) {
			case message.ImageData:
				if strings.TrimSpace(data.URL) != "" {
					*imageURLs = append(*imageURLs, data.URL)
				}
			case message.VideoData:
				if strings.TrimSpace(data.URL) != "" {
					*videoURLs = append(*videoURLs, data.URL)
				}
			case message.ForwardData:
				collectForwardMedia(data.Content, imageURLs, videoURLs)
			}
		}
	}
}

func (a *Agent) summarizeForwardMessages(ctx context.Context, content []message.ForwardNode) (string, error) {
	if len(content) == 0 {
		return "", nil
	}
	var imageURLs, videoURLs []string
	collectForwardMedia(content, &imageURLs, &videoURLs)
	// ForwardNode 自带线报文序列化，视觉模型按原始结构识别
	raw, err := sonic.MarshalString(content)
	if err != nil {
		return "", err
	}
	return a.vision.SummarizeForward(ctx, raw, imageURLs, videoURLs)
}

func (a *Agent) buildConversationToolContext(ctx context.Context, kind string, targetID, snapshotMessageID int64, messages []*onebot.ConversationMessage) context.Context {
	tc := &tools.ToolContext{
		ConversationKind:  kind,
		TargetID:          targetID,
		MemoryMgr:         a.memory.WithContext(ctx),
		Bot:               a.bot,
		SnapshotMessageID: snapshotMessageID,
		SpeakCallback: func(callCtx context.Context, gid int64, content string, replyTo int64, mentions []int64) error {
			if kind == memory.ConversationKindPrivate {
				return a.doPrivateSpeak(callCtx, gid, content, replyTo)
			}
			return a.doSpeak(callCtx, gid, content, replyTo, mentions)
		},
		SendStickerCallback: func(callCtx context.Context, gid int64, filePath string, description string) error {
			if kind == memory.ConversationKindPrivate {
				return a.doPrivateSticker(callCtx, gid, filePath, description)
			}
			return a.doSendSticker(callCtx, gid, filePath, description)
		},
		InspectImageCallback: func(callCtx context.Context, imageURL string) (string, error) {
			return a.describeWebImageCached(callCtx, imageURL)
		},
		SendImageURLCallback: func(callCtx context.Context, imageURL string) error {
			if kind == memory.ConversationKindPrivate {
				return a.doPrivateImage(callCtx, targetID, imageURL)
			}
			return a.doSendImageURL(callCtx, targetID, imageURL)
		},
		MessageRecalledCallback: a.syncRecalledMessage,
	}
	for _, message := range messages {
		if message == nil {
			continue
		}
		tc.ThinkStartArrivalSeq = max(tc.ThinkStartArrivalSeq, message.ArrivalSeq)
		tc.RegisterMessage(message.MessageID)
	}
	tc.GetNewMessagesCallback = func(callCtx context.Context) (any, error) {
		current, _, trimmed := a.getConversationSnapshot(kind, targetID)
		selfID := a.bot.GetSelfID()
		newMessages := make([]map[string]any, 0)
		observed := tc.ObservedThroughSeq
		floor := max(tc.ThinkStartArrivalSeq, tc.ObservedThroughSeq)
		for _, message := range current {
			if message == nil || message.UserID == selfID || message.ArrivalSeq <= floor {
				continue
			}
			ref := tc.RegisterMessage(message.MessageID)
			newMessages = append(newMessages, map[string]any{"message_ref": ref, "sender_id": message.UserID, "nickname": message.Nickname, "content": message.FinalContent, "arrival_seq": message.ArrivalSeq})
			observed = max(observed, message.ArrivalSeq)
		}
		if trimmed > floor {
			// 缓冲窗口外的消息已被裁剪，本轮无法完整收集，不提交观察水位
			tc.ObservationIncomplete = true
			return map[string]any{"success": true, "complete": false, "messages": newMessages}, nil
		}
		tc.ObservedThroughSeq = observed
		return map[string]any{"success": true, "complete": true, "messages": newMessages}, nil
	}
	return tools.WithToolContext(ctx, tc)
}
