package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"mumu-bot/internal/media"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"
	"mumu-bot/internal/tools"

	"github.com/cloudwego/eino/schema"
)

// 会话装配的固定预留：工具声明与模型响应开销
const sessionTokenReserve = 8000

// sessionUser 一条人发消息在会话里的形态
type sessionUser struct {
	LogID      uint
	Ref        string
	Text       string
	Content    string
	At         time.Time
	Media      []media.Ref
	Recalled   bool
	Mentioned  bool
	SenderID   int64
	Nickname   string
	ArrivalSeq uint64
	MessageID  int64
}

// sessionSegment 一个连续交互段：若干条人消息加上模型回复，淘汰时整段丢弃
type sessionSegment struct {
	users   []*sessionUser
	model   []*schema.AgenticMessage
	modelAt time.Time
}

// Session 单个会话的连续上下文
type Session struct {
	kind     string
	targetID int64

	mu         sync.Mutex
	segments   []*sessionSegment
	open       *sessionSegment
	nextRef    int
	readSeq    uint64
	observed   uint64
	injected   uint64
	messageIDs map[string]int64
}

// sessionStore 管理全部会话
type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

func newSessionStore() *sessionStore {
	return &sessionStore{sessions: map[string]*Session{}}
}

// get 取会话，不存在时创建
func (s *sessionStore) get(kind string, targetID int64) *Session {
	key := sessionKey(kind, targetID)
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[key]
	if !ok {
		session = &Session{kind: kind, targetID: targetID}
		s.sessions[key] = session
	}
	return session
}

// all 返回全部会话
func (s *sessionStore) all() []*Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]*Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		list = append(list, session)
	}
	return list
}

func sessionKey(kind string, targetID int64) string {
	return kind + ":" + strconv.FormatInt(targetID, 10)
}

// AppendUser 追加一条人消息并渲染固定的模型可见正文
func (s *Session) AppendUser(msg *onebot.ConversationMessage, logID uint, media []media.Ref) *sessionUser {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextRef++
	ref := "m" + strconv.Itoa(s.nextRef)
	user := &sessionUser{
		LogID:      logID,
		Ref:        ref,
		Text:       renderSessionUserText(msg, ref, media),
		Content:    strings.TrimSpace(msg.Content),
		At:         msg.Time,
		Media:      media,
		Mentioned:  msg.IsMentioned,
		SenderID:   msg.UserID,
		Nickname:   msg.Nickname,
		ArrivalSeq: msg.ArrivalSeq,
		MessageID:  msg.MessageID,
	}
	if s.messageIDs == nil {
		s.messageIDs = map[string]int64{}
	}
	s.messageIDs[ref] = msg.MessageID
	if s.open == nil {
		s.open = &sessionSegment{}
	}
	s.open.users = append(s.openUsers(), user)
	return user
}

// AppendModel 追加模型产生的消息，落在当前交互段末尾
func (s *Session) AppendModel(at time.Time, messages ...*schema.AgenticMessage) {
	if len(messages) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.open == nil {
		s.open = &sessionSegment{}
	}
	s.open.model = append(s.open.model, messages...)
	s.open.modelAt = at
	s.segments = append(s.segments, s.open)
	s.open = nil
}

// MarkInjected 记录已进入模型上下文的消息水位
func (s *Session) MarkInjected(seq uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if seq > s.injected {
		s.injected = seq
	}
}

// UninjectedMessages 返回尚未进入模型上下文的用户消息
func (s *Session) UninjectedMessages() []*sessionUser {
	s.mu.Lock()
	defer s.mu.Unlock()
	var list []*sessionUser
	for _, segment := range s.segments {
		for _, user := range segment.users {
			if user.ArrivalSeq > s.injected {
				list = append(list, user)
			}
		}
	}
	for _, user := range s.openUsers() {
		if user.ArrivalSeq > s.injected {
			list = append(list, user)
		}
	}
	return list
}

// ResolveMessageRef 按会话内编号找原始消息 id
func (s *Session) ResolveMessageRef(ref string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	messageID, ok := s.messageIDs[strings.TrimSpace(ref)]
	return messageID, ok
}

// RegisterMessage 按原始消息 id 找会话内编号，检索到的历史消息按需补号
func (s *Session) RegisterMessage(messageID int64) string {
	if messageID == 0 {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, segment := range s.segments {
		for _, user := range segment.users {
			if user.MessageID == messageID {
				return user.Ref
			}
		}
	}
	for _, user := range s.openUsers() {
		if user.MessageID == messageID {
			return user.Ref
		}
	}
	for ref, id := range s.messageIDs {
		if id == messageID {
			return ref
		}
	}
	s.nextRef++
	ref := "m" + strconv.Itoa(s.nextRef)
	if s.messageIDs == nil {
		s.messageIDs = map[string]int64{}
	}
	s.messageIDs[ref] = messageID
	return ref
}

// ResolveMedia 取出会话里的媒体
func (s *Session) ResolveMedia(ref string, index int) (media.Ref, bool) {
	user, ok := s.FindUser(ref)
	if !ok || index < 0 || index >= len(user.Media) {
		return media.Ref{}, false
	}
	item := user.Media[index]
	return media.Ref{
		Hash:    item.Hash,
		Path:    item.Path,
		Mime:    item.Mime,
		Bytes:   item.Bytes,
		Sticker: item.Sticker,
	}, true
}

// MessagesAfter 返回到达序号大于给定值的用户消息
func (s *Session) MessagesAfter(seq uint64) []*sessionUser {
	s.mu.Lock()
	defer s.mu.Unlock()
	var list []*sessionUser
	for _, segment := range s.segments {
		for _, user := range segment.users {
			if user.ArrivalSeq > seq {
				list = append(list, user)
			}
		}
	}
	for _, user := range s.openUsers() {
		if user.ArrivalSeq > seq {
			list = append(list, user)
		}
	}
	return list
}

// LatestMessageID 返回会话里最近一条消息的原始 id
func (s *Session) LatestMessageID() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var latest int64
	for _, segment := range s.segments {
		for _, user := range segment.users {
			if user.MessageID > latest {
				latest = user.MessageID
			}
		}
	}
	for _, user := range s.openUsers() {
		if user.MessageID > latest {
			latest = user.MessageID
		}
	}
	return latest
}

// maxPendingSeq 取待处理消息里的最大到达序号
func maxPendingSeq(pending []*sessionUser) uint64 {
	var seq uint64
	for _, user := range pending {
		if user.ArrivalSeq > seq {
			seq = user.ArrivalSeq
		}
	}
	return seq
}

// RecentUsers 返回最近若干条用户消息
func (s *Session) RecentUsers(limit int) []*sessionUser {
	s.mu.Lock()
	defer s.mu.Unlock()
	var list []*sessionUser
	for i := len(s.segments) - 1; i >= 0 && len(list) < limit; i-- {
		users := s.segments[i].users
		for j := len(users) - 1; j >= 0 && len(list) < limit; j-- {
			list = append(list, users[j])
		}
	}
	return list
}

// RecentWindow 统计最近窗口内的消息数与自己发言占比
func (s *Session) RecentWindow(now time.Time, window time.Duration) (total int, self int, lastSelf time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	since := now.Add(-window)
	for _, segment := range s.segments {
		for _, user := range segment.users {
			if user.At.Before(since) {
				continue
			}
			total++
		}
		if !segment.modelAt.IsZero() && !segment.modelAt.Before(since) {
			total++
			self++
			if segment.modelAt.After(lastSelf) {
				lastSelf = segment.modelAt
			}
		}
	}
	for _, user := range s.openUsers() {
		if user.At.Before(since) {
			continue
		}
		total++
	}
	return total, self, lastSelf
}

// PendingMessages 返回尚未进入模型上下文的用户消息
func (s *Session) PendingMessages() []*sessionUser {
	s.mu.Lock()
	defer s.mu.Unlock()
	var pending []*sessionUser
	if s.open != nil {
		pending = append(pending, s.openUsers()...)
	}
	return pending
}

// MarkConsumed 推进已消费水位
func (s *Session) MarkConsumed(seq uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if seq > s.readSeq {
		s.readSeq = seq
	}
}

// ReadSeq 返回已消费水位
func (s *Session) ReadSeq() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readSeq
}

// MarkObserved 推进 getNewMessages 的观察水位
func (s *Session) MarkObserved(seq uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if seq > s.observed {
		s.observed = seq
	}
}

// ObservedSeq 返回观察水位
func (s *Session) ObservedSeq() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.observed
}

// PendingAfter 返回到达序号大于给定值、尚未进入模型上下文的��息
func (s *Session) PendingAfter(seq uint64) []tools.PendingMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	var list []tools.PendingMessage
	for _, segment := range s.segments {
		for _, user := range segment.users {
			if user.ArrivalSeq > seq && user.ArrivalSeq > s.injected {
				list = append(list, tools.PendingMessage{
					Ref:        user.Ref,
					Content:    user.Text,
					SenderID:   user.SenderID,
					Nickname:   user.Nickname,
					ArrivalSeq: user.ArrivalSeq,
				})
			}
		}
	}
	for _, user := range s.openUsers() {
		if user.ArrivalSeq > seq && user.ArrivalSeq > s.injected {
			list = append(list, tools.PendingMessage{
				Ref:        user.Ref,
				Content:    user.Text,
				SenderID:   user.SenderID,
				Nickname:   user.Nickname,
				ArrivalSeq: user.ArrivalSeq,
			})
		}
	}
	return list
}

// MarkRecalledByMessageID 按原始消息 id 标记撤回
func (s *Session) MarkRecalledByMessageID(messageID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mark := func(user *sessionUser) {
		if user.MessageID == messageID {
			user.Recalled = true
		}
	}
	for _, segment := range s.segments {
		for _, user := range segment.users {
			mark(user)
		}
	}
	for _, user := range s.openUsers() {
		mark(user)
	}
}

// MarkRecalled 标记某条消息已撤回，上下文保留原文，只在后续消息里说明
func (s *Session) MarkRecalled(logID uint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, segment := range s.segments {
		for _, user := range segment.users {
			if user.LogID == logID {
				user.Recalled = true
			}
		}
	}
	if s.open != nil {
		for _, user := range s.openUsers() {
			if user.LogID == logID {
				user.Recalled = true
			}
		}
	}
}

// RecalledNotes 返回本会话已撤回消息的说明文本
func (s *Session) RecalledNotes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var notes []string
	for _, segment := range s.segments {
		for _, user := range segment.users {
			if user.Recalled {
				notes = append(notes, fmt.Sprintf("（%s 已撤回，不要再回应或引用其内容）", user.Ref))
			}
		}
	}
	for _, user := range s.openUsers() {
		if user.Recalled {
			notes = append(notes, fmt.Sprintf("（%s 已撤回，不要再回应或引用其内容）", user.Ref))
		}
	}
	return notes
}

// FindUser 按引用找用户消息
func (s *Session) FindUser(ref string) (*sessionUser, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, segment := range s.segments {
		for _, user := range segment.users {
			if user.Ref == ref {
				return user, true
			}
		}
	}
	for _, user := range s.openUsers() {
		if user.Ref == ref {
			return user, true
		}
	}
	return nil, false
}

// openUsers 返回当前未闭合交互段里的用户消息
func (s *Session) openUsers() []*sessionUser {
	if s.open == nil {
		return nil
	}
	return s.open.users
}

// logIDs 返回会话内所有消息的库内 id
func (s *Session) logIDs() []uint {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.logIDsLocked()
}

// logIDsLocked 返回会话内所有消息的库内 id，调用方需已持锁
func (s *Session) logIDsLocked() []uint {
	var ids []uint
	for _, segment := range s.segments {
		for _, user := range segment.users {
			if user.LogID > 0 {
				ids = append(ids, user.LogID)
			}
		}
	}
	for _, user := range s.openUsers() {
		if user.LogID > 0 {
			ids = append(ids, user.LogID)
		}
	}
	return ids
}

// Assemble 装配模型输入：话题摘要块 + 未被摘要覆盖的原文 + 新消息
func (s *Session) Assemble(ctx context.Context, mem *memory.Manager, budgetTokens int, prefix string, newUsers []*sessionUser) ([]*schema.AgenticMessage, error) {
	s.mu.Lock()
	ids := s.logIDsLocked()
	s.mu.Unlock()

	coverage := memory.TopicCoverage{Covered: map[uint]bool{}}
	if mem != nil && len(ids) > 0 {
		result, err := mem.TopicCoverage(ctx, s.kind, s.targetID, ids)
		if err != nil {
			return nil, err
		}
		coverage = result
	}

	messages := make([]*schema.AgenticMessage, 0, 16)
	digest, err := s.topicDigest(ctx, coverage, budgetTokens/3)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if digest != "" {
		messages = append(messages, schema.UserAgenticMessage(digest))
	}

	remaining := budgetTokens - sessionTokenReserve - estimateTokens(digest)
	segments := make([]*sessionSegment, 0, len(s.segments))
	for i := len(s.segments) - 1; i >= 0; i-- {
		segment := s.segments[i]
		if segmentCovered(segment, coverage) {
			// 已被话题摘要覆盖的老对话不再原文进上下文
			continue
		}
		cost := segmentTokens(segment)
		if remaining-cost < 0 && len(segments) > 0 {
			break
		}
		remaining -= cost
		segments = append(segments, segment)
	}
	for i := len(segments) - 1; i >= 0; i-- {
		segment := segments[i]
		for _, user := range segment.users {
			if coverage.Covered[user.LogID] {
				continue
			}
			messages = append(messages, schema.UserAgenticMessage(user.Text))
		}
		messages = append(messages, segment.model...)
	}
	for index, user := range newUsers {
		text := user.Text
		if index == 0 && prefix != "" {
			text = prefix + "\n" + text
		}
		messages = append(messages, schema.UserAgenticMessage(text))
	}
	return messages, nil
}

// topicDigest 生成会话早期的话题摘要块，水位之前的话题用摘要代替原文
func (s *Session) topicDigest(ctx context.Context, coverage memory.TopicCoverage, budgetTokens int) (string, error) {
	if len(coverage.Summaries) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("【本会话早期摘要】以下是本会话较早内容的要点，只在没有原文可查时参考：\n")
	for _, summary := range coverage.Summaries {
		b.WriteString(renderTopicDigest(summary))
	}
	text := strings.TrimSpace(b.String())
	for estimateTokens(text) > budgetTokens && len(coverage.Summaries) > 1 {
		coverage.Summaries = coverage.Summaries[1:]
		b.Reset()
		b.WriteString("【本会话早期摘要】以下是本会话较早内容的要点，只在没有原文可查时参考：\n")
		for _, summary := range coverage.Summaries {
			b.WriteString(renderTopicDigest(summary))
		}
		text = strings.TrimSpace(b.String())
	}
	return text, nil
}

// renderTopicDigest 渲染一条话题摘要
func renderTopicDigest(summary memory.TopicDigestSummary) string {
	var b strings.Builder
	title := strings.TrimSpace(summary.Title)
	if title == "" {
		title = "话题"
	}
	b.WriteString("### " + title + "\n")
	if gist := strings.TrimSpace(summary.Gist); gist != "" {
		b.WriteString("概况：" + gist + "\n")
	}
	if len(summary.OpenLoops) > 0 {
		b.WriteString("未完事项：" + strings.Join(summary.OpenLoops, "；") + "\n")
	}
	if len(summary.Participants) > 0 {
		b.WriteString("在场：" + strings.Join(summary.Participants, "、") + "\n")
	}
	return b.String()
}

// segmentCovered 判断一个交互段是否已被话题摘要覆盖
func segmentCovered(segment *sessionSegment, coverage memory.TopicCoverage) bool {
	if len(segment.users) == 0 {
		return false
	}
	for _, user := range segment.users {
		if user.LogID == 0 || !coverage.Covered[user.LogID] {
			return false
		}
	}
	return true
}

// segmentTokens 估算一个交互段的 token 数
func segmentTokens(segment *sessionSegment) int {
	total := 0
	for _, user := range segment.users {
		total += estimateTokens(user.Text)
	}
	for _, message := range segment.model {
		total += estimateTokens(agenticMessageText(message))
	}
	return total
}

// estimateTokens 粗略估算文本 token 数，中文按每字 2/3、其余按每 4 字符 1 token
func estimateTokens(text string) int {
	if text == "" {
		return 0
	}
	cjk, other := 0, 0
	for _, r := range text {
		if r >= 0x4E00 && r <= 0x9FFF {
			cjk++
			continue
		}
		other++
	}
	return cjk*2/3 + other/4
}

// agenticMessageText 取出 agentic 消息里的文本内容
func agenticMessageText(message *schema.AgenticMessage) string {
	if message == nil {
		return ""
	}
	var b strings.Builder
	for _, block := range message.ContentBlocks {
		if block == nil {
			continue
		}
		switch {
		case block.AssistantGenText != nil:
			b.WriteString(block.AssistantGenText.Text)
		case block.UserInputText != nil:
			b.WriteString(block.UserInputText.Text)
		case block.FunctionToolCall != nil:
			b.WriteString(block.FunctionToolCall.Name + block.FunctionToolCall.Arguments)
		case block.FunctionToolResult != nil:
			for _, part := range block.FunctionToolResult.Content {
				if part != nil && part.Text != nil {
					b.WriteString(part.Text.Text)
				}
			}
		}
	}
	return b.String()
}

// renderSessionUserText 渲染一条人消息的模型可见正文
func renderSessionUserText(msg *onebot.ConversationMessage, ref string, media []media.Ref) string {
	if msg == nil {
		return ""
	}
	displayName := resolveMessageDisplayName(msg.GroupCard, msg.Nickname)
	if displayName == "" {
		displayName = fmt.Sprintf("%d", msg.UserID)
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("[%s] %s %s(%d): ", ref, msg.Time.Format("15:04:05"), displayName, msg.UserID))
	if msg.Reply != nil {
		if msg.Reply.MessageID != 0 {
			b.WriteString(fmt.Sprintf("[回复 %d] ", msg.Reply.MessageID))
		} else if name := resolveMessageDisplayName(msg.Reply.GroupCard, msg.Reply.Nickname); name != "" {
			b.WriteString(fmt.Sprintf("[回复 %s(%d)] ", name, msg.Reply.SenderID))
		}
	}
	content := strings.TrimSpace(msg.Content)
	if content != "" {
		b.WriteString(content)
	}
	if len(media) > 0 {
		b.WriteString(" " + mediaPlaceholders(media))
		b.WriteString(fmt.Sprintf("\n【图片/表情】本条消息包含 %d 个图片/表情（编号 %s）。需要识别内容时调用 viewImage，参数 message_ref=%q。", len(media), ref, ref))
	}
	return strings.TrimSpace(b.String())
}

// mediaPlaceholders 渲染媒体占位
func mediaPlaceholders(media []media.Ref) string {
	parts := make([]string, 0, len(media))
	for _, item := range media {
		if item.Sticker {
			parts = append(parts, "[表情]")
			continue
		}
		parts = append(parts, "[图片]")
	}
	return strings.Join(parts, " ")
}
