package agent

import (
	"context"
	"fmt"
	"sort"
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

// sessionTokenReserve 工具声明与模型响应预留的 token
const sessionTokenReserve = 8000

// sessionUser 一条真人消息在会话里的形态
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
	Msg        *schema.AgenticMessage // 渲染后的模型可见正文，只创建一次
}

// sessionEntry 上下文里的一条消息：真人消息带来源，模型与工具消息只保留原文
type sessionEntry struct {
	user *sessionUser
	msg  *schema.AgenticMessage
}

// sessionSegment 一个连续交互段：真人消息加上随后的模型与工具消息，压缩时整段丢弃
type sessionSegment struct {
	users []*sessionUser
	items []sessionEntry
}

// Session 单个会话的连续上下文：模型看过的消息与还没看到的真人消息分开存放
type Session struct {
	kind     string
	targetID int64

	mu        sync.Mutex
	seen      []sessionEntry // 模型已经看过的全部消息，原样保留
	pending   []*sessionUser // 还没进入上下文的真人消息
	users     map[*schema.AgenticMessage]*sessionUser
	media     *mediaIndex      // 会话内的媒体索引
	refs      map[string]int64 // 会话编号 → 原始消息 id
	nextRef   int
	readSeq   uint64                 // 已消费水位
	observed  uint64                 // getNewMessages 已读水位
	injected  uint64                 // 已注入模型上下文的到达序号
	selfAt    []time.Time            // 机器人自己发言的时间
	notes     []string               // 待附加到下一条消息的撤回说明
	digest    *schema.AgenticMessage // 当前的话题摘要块，只在装配时前置，不进 transcript
	digestTxt string
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
		session = &Session{kind: kind, targetID: targetID, media: newMediaIndex()}
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

// AppendUser 追加一条真人消息并渲染固定的模型可见正文
func (s *Session) AppendUser(msg *onebot.ConversationMessage, logID uint, mediaRefs []media.Ref) *sessionUser {
	if msg == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user := s.newUserLocked(msg, logID, mediaRefs)
	s.pending = append(s.pending, user)
	return user
}

// AppendHistory 追加一条已经进过上下文的真人消息，重启后用它恢复历史
func (s *Session) AppendHistory(msg *onebot.ConversationMessage, logID uint, mediaRefs []media.Ref) *sessionUser {
	if msg == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user := s.newUserLocked(msg, logID, mediaRefs)
	s.seen = append(s.seen, sessionEntry{user: user, msg: user.Msg})
	return user
}

// newUserLocked 渲染一条真人消息并登记会话内的引用，调用方需已持锁
func (s *Session) newUserLocked(msg *onebot.ConversationMessage, logID uint, mediaRefs []media.Ref) *sessionUser {
	s.nextRef++
	ref := "m" + strconv.Itoa(s.nextRef)
	user := &sessionUser{
		LogID:      logID,
		Ref:        ref,
		Content:    strings.TrimSpace(msg.Content),
		At:         msg.Time,
		Media:      mediaRefs,
		Mentioned:  msg.IsMentioned,
		SenderID:   msg.UserID,
		Nickname:   msg.Nickname,
		ArrivalSeq: msg.ArrivalSeq,
		MessageID:  msg.MessageID,
	}
	user.Text = s.renderUserText(msg, ref, mediaRefs)
	user.Msg = schema.UserAgenticMessage(user.Text)
	if s.refs == nil {
		s.refs = map[string]int64{}
	}
	s.refs[ref] = msg.MessageID
	if s.users == nil {
		s.users = map[*schema.AgenticMessage]*sessionUser{}
	}
	s.users[user.Msg] = user
	return user
}

// renderUserText 渲染一条真人消息的模型可见正文，渲染后内容固定不变
func (s *Session) renderUserText(msg *onebot.ConversationMessage, ref string, mediaRefs []media.Ref) string {
	displayName := resolveMessageDisplayName(msg.GroupCard, msg.Nickname)
	if displayName == "" {
		displayName = fmt.Sprintf("%d", msg.UserID)
	}
	var b strings.Builder
	if notes := s.takeNotesLocked(); len(notes) > 0 {
		b.WriteString(strings.Join(notes, "\n"))
		b.WriteString("\n")
	}
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
	if len(msg.Videos) > 0 {
		b.WriteString(" [视频，无法查看]")
	}
	if placeholders := s.media.placeholders(mediaRefs); placeholders != "" {
		b.WriteString(" " + placeholders)
	}
	return strings.TrimSpace(b.String())
}

// takeNotesLocked 取出待附加的撤回说明，调用方需已持锁
func (s *Session) takeNotesLocked() []string {
	if len(s.notes) == 0 {
		return nil
	}
	notes := s.notes
	s.notes = nil
	return notes
}

// CommitRound 用本轮模型实际看到的完整记录替换 transcript
func (s *Session) CommitRound(messages []*schema.AgenticMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := make([]sessionEntry, 0, len(messages))
	included := map[*schema.AgenticMessage]bool{}
	var injected uint64
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		entry := sessionEntry{msg: msg}
		if user, ok := s.users[msg]; ok {
			entry.user = user
			if user.ArrivalSeq > injected {
				injected = user.ArrivalSeq
			}
		}
		included[msg] = true
		entries = append(entries, entry)
	}
	// 摘要块每次装配时重新前置，不留在 transcript 里
	if s.digest != nil && len(entries) > 0 && entries[0].msg == s.digest {
		entries = entries[1:]
	}
	s.seen = entries
	kept := s.pending[:0]
	for _, user := range s.pending {
		if included[user.Msg] {
			continue
		}
		kept = append(kept, user)
	}
	s.pending = kept
	if injected > s.injected {
		s.injected = injected
	}
}

// PendingMessages 返回还没进入模型上下文的真人消息
func (s *Session) PendingMessages() []*sessionUser {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*sessionUser(nil), s.pending...)
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

// MarkInjected 记录已进入模型上下文的到达序号
func (s *Session) MarkInjected(seq uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if seq > s.injected {
		s.injected = seq
	}
}

// PendingAfter 返回到达序号大于给定值、还没进入模型上下文的真人消息
func (s *Session) PendingAfter(seq uint64) []tools.PendingMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	var list []tools.PendingMessage
	for _, user := range s.pending {
		if user.ArrivalSeq <= seq || user.ArrivalSeq <= s.injected {
			continue
		}
		list = append(list, s.pendingMessageLocked(user))
	}
	return list
}

// MessagesAfter 返回到达序号大于给定值的真人消息，包含已进上下文和仍在等待的消息
func (s *Session) MessagesAfter(seq uint64) []*sessionUser {
	s.mu.Lock()
	defer s.mu.Unlock()
	var list []*sessionUser
	for _, entry := range s.seen {
		if entry.user == nil || entry.user.ArrivalSeq <= seq {
			continue
		}
		list = append(list, entry.user)
	}
	for _, user := range s.pending {
		if user.ArrivalSeq <= seq {
			continue
		}
		list = append(list, user)
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].ArrivalSeq < list[j].ArrivalSeq })
	return list
}

// pendingMessageLocked 把真人消息转成工具可读的结构，调用方需已持锁
func (s *Session) pendingMessageLocked(user *sessionUser) tools.PendingMessage {
	return tools.PendingMessage{
		Ref:        user.Ref,
		Content:    user.Text,
		SenderID:   user.SenderID,
		Nickname:   user.Nickname,
		ArrivalSeq: user.ArrivalSeq,
		Msg:        user.Msg,
	}
}

// MarkRecalledByMessageID 按原始消息 id 标记撤回，上下文保留原文
func (s *Session) MarkRecalledByMessageID(messageID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range s.pending {
		if user.MessageID == messageID {
			user.Recalled = true
			s.notes = append(s.notes, fmt.Sprintf("（%s 已撤回，不要再回应或引用其内容）", user.Ref))
			return
		}
	}
	for _, entry := range s.seen {
		if entry.user != nil && entry.user.MessageID == messageID {
			entry.user.Recalled = true
			s.notes = append(s.notes, fmt.Sprintf("（%s 已撤回，不要再回应或引用其内容）", entry.user.Ref))
			return
		}
	}
}

// NoteSelfSpoke 记录机器人自己发言的时间，供发言频率计算使用
func (s *Session) NoteSelfSpoke(at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.selfAt = append(s.selfAt, at)
}

// ResolveMessageRef 按会话内编号找原始消息 id
func (s *Session) ResolveMessageRef(ref string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	messageID, ok := s.refs[strings.TrimSpace(ref)]
	return messageID, ok
}

// RegisterMessage 按原始消息 id 找会话内编号，检索到的历史消息按需补号
func (s *Session) RegisterMessage(messageID int64) string {
	if messageID == 0 {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range s.pending {
		if user.MessageID == messageID {
			return user.Ref
		}
	}
	for _, entry := range s.seen {
		if entry.user != nil && entry.user.MessageID == messageID {
			return entry.user.Ref
		}
	}
	for ref, id := range s.refs {
		if id == messageID {
			return ref
		}
	}
	s.nextRef++
	ref := "m" + strconv.Itoa(s.nextRef)
	if s.refs == nil {
		s.refs = map[string]int64{}
	}
	s.refs[ref] = messageID
	return ref
}

// ResolveMediaByHash 按会话内短哈希取媒体
func (s *Session) ResolveMediaByHash(hash string) (media.Ref, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.media.resolve(hash)
}

// LatestMessageID 返回会话里最近一条消息的原始 id
func (s *Session) LatestMessageID() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var latest int64
	for _, user := range s.pending {
		if user.MessageID > latest {
			latest = user.MessageID
		}
	}
	return latest
}

// RecentWindow 统计最近窗口内的消息数与自己发言占比
func (s *Session) RecentWindow(now time.Time, window time.Duration) (total int, self int, lastSelf time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	since := now.Add(-window)
	for _, entry := range s.seen {
		if entry.user == nil || entry.user.At.Before(since) {
			continue
		}
		total++
	}
	for _, user := range s.pending {
		if user.At.Before(since) {
			continue
		}
		total++
	}
	for _, at := range s.selfAt {
		if at.Before(since) {
			continue
		}
		self++
		if lastSelf.IsZero() || at.After(lastSelf) {
			lastSelf = at
		}
	}
	return total, self, lastSelf
}

// Assemble 装配模型输入：话题摘要 + 未被摘要覆盖的原文 + 待处理的新消息
func (s *Session) Assemble(ctx context.Context, mem *memory.Manager, budgetTokens int) ([]*schema.AgenticMessage, error) {
	s.mu.Lock()
	entries := append([]sessionEntry(nil), s.seen...)
	pending := append([]*sessionUser(nil), s.pending...)
	s.mu.Unlock()

	coverage, err := s.topicCoverage(ctx, mem, entries)
	if err != nil {
		return nil, err
	}
	digestText := renderTopicDigest(coverage.Summaries, budgetTokens/3)
	s.mu.Lock()
	digest := s.digest
	if digestText == "" {
		digest = nil
	} else if digest == nil || s.digestTxt != digestText {
		digest = schema.UserAgenticMessage(digestText)
	}
	s.digest = digest
	s.digestTxt = digestText
	s.mu.Unlock()

	segments := splitSegments(entries)
	kept := make([]*sessionSegment, 0, len(segments))
	for _, segment := range segments {
		if segmentCovered(segment, coverage) {
			continue
		}
		kept = append(kept, segment)
	}

	// 预算不够时从最老的一段开始丢，整段丢弃以免拆散工具调用与工具结果
	pendingCost := 0
	for _, user := range pending {
		pendingCost += estimateTokens(user.Text)
	}
	remaining := budgetTokens - sessionTokenReserve - estimateTokens(digestText) - pendingCost
	selected := make([]*sessionSegment, 0, len(kept))
	for index := len(kept) - 1; index >= 0; index-- {
		cost := segmentTokens(kept[index])
		if remaining-cost < 0 && len(selected) > 0 {
			break
		}
		remaining -= cost
		selected = append(selected, kept[index])
	}

	messages := make([]*schema.AgenticMessage, 0, len(entries)+len(pending)+1)
	if digest != nil {
		messages = append(messages, digest)
	}
	for index := len(selected) - 1; index >= 0; index-- {
		for _, entry := range selected[index].items {
			if entry.msg == nil {
				continue
			}
			messages = append(messages, entry.msg)
		}
	}
	for _, user := range pending {
		if user.Msg == nil {
			continue
		}
		messages = append(messages, user.Msg)
	}

	s.mu.Lock()
	if seq := maxPendingSeq(pending); seq > s.injected {
		s.injected = seq
	}
	s.mu.Unlock()
	return messages, nil
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
