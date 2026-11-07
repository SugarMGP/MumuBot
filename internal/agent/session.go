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

type sessionUser struct {
	LogID             uint
	TranscriptID      uint
	Ref               string
	Text              string
	Content           string
	At                time.Time
	ReceivedAt        time.Time
	Mentioned         bool
	SenderID          int64
	Nickname          string
	ReferencedIDs     []int64
	ArrivalSeq        uint64
	Order             uint64
	MessageID         int64
	Msg               *schema.AgenticMessage
	IsObservation     bool
	IsAgentMessage    bool
	IsMaintenanceHint bool
	IsSelfCheck       bool
	Wake              bool
	AllowSleep        bool
}

type sessionEntry struct {
	user         *sessionUser
	msg          *schema.AgenticMessage
	transcriptID uint
	visibleSeq   uint64
}

type sessionSegment struct {
	items []sessionEntry
}

type Session struct {
	kind     string
	targetID int64
	memory   *memory.Manager

	mu                       sync.Mutex
	seen                     []sessionEntry
	pending                  []*sessionUser
	media                    *mediaIndex
	injected                 uint64
	selfAt                   []time.Time
	notes                    []string
	nextOrder                uint64
	fullScale                float64
	tokenBase                *tokenBase
	compactionRetryAfter     time.Time
	compactionBeforeModel    bool
	failedBatchThrough       uint64
	requestActionableThrough uint64
	turnSeq                  uint64
	lastMemoryTurn           uint64
	lastTopicTurn            uint64
	lastMemoryHintTurn       uint64
	lastTopicHintTurn        uint64
	lastActivityAt           time.Time
	maintenanceMu            sync.Mutex
	contextStartID           uint
	idleDoneForActivity      bool
	idleCancel               context.CancelFunc
}

type tokenBase struct {
	message    *schema.AgenticMessage
	prompt     int
	completion int
	total      int
}

type sessionStore struct {
	mu       sync.Mutex
	memory   *memory.Manager
	sessions map[string]*Session
}

func newSessionStore(mem *memory.Manager) *sessionStore {
	return &sessionStore{memory: mem, sessions: map[string]*Session{}}
}

func (s *sessionStore) snapshot() []*Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]*Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		list = append(list, session)
	}
	return list
}

func (s *sessionStore) resetCalibration() {
	for _, session := range s.snapshot() {
		session.ResetCalibration()
	}
}

func (s *sessionStore) get(kind string, targetID int64) *Session {
	key := sessionKey(kind, targetID)
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[key]
	if !ok {
		session = &Session{kind: kind, targetID: targetID, memory: s.memory, media: newMediaIndex()}
		s.sessions[key] = session
	}
	return session
}

func sessionKey(kind string, targetID int64) string {
	return kind + ":" + strconv.FormatInt(targetID, 10)
}

func (s *Session) AppendUser(msg *onebot.ConversationMessage, logID uint, mediaRefs []media.Ref) *sessionUser {
	if msg == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user := s.newUserLocked(msg, logID, mediaRefs)
	s.pending = append(s.pending, user)
	if !user.IsObservation {
		if msg.ReceivedAt.After(s.lastActivityAt) {
			s.lastActivityAt = msg.ReceivedAt
		}
		s.idleDoneForActivity = false
		if s.idleCancel != nil {
			s.idleCancel()
			s.idleCancel = nil
		}
	}
	return user
}

// AppendAgentMessage 把另一个会话投递的内部消息加入待处理工作集；
// wake=false 只保存不触发新回合，allowSleep 表示允许在 life 睡眠时仍启动这一轮
func (s *Session) AppendAgentMessage(message *schema.AgenticMessage, transcriptID uint, at time.Time, wake, allowSleep bool) *sessionUser {
	if message == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if at.IsZero() {
		at = time.Now()
	}
	tagTranscriptEntryID(message, transcriptID)
	user := &sessionUser{
		Text: agenticMessageText(message), Msg: message, IsAgentMessage: true, Wake: wake, AllowSleep: allowSleep,
		TranscriptID: transcriptID, ReceivedAt: time.Now(), At: at, Order: s.nextOrderLocked(),
	}
	s.pending = append(s.pending, user)
	if at.After(s.lastActivityAt) {
		s.lastActivityAt = at
	}
	s.idleDoneForActivity = false
	if s.idleCancel != nil {
		s.idleCancel()
		s.idleCancel = nil
	}
	return user
}

// RestoreUser 用 transcript 中保存的原始消息体恢复用户条目
func (s *Session) RestoreUser(msg *onebot.ConversationMessage, logID uint, mediaRefs []media.Ref, transcriptID uint, visibleSeq uint64, visible bool, payload *schema.AgenticMessage) *sessionUser {
	if msg == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user := s.newUserLocked(msg, logID, mediaRefs)
	user.TranscriptID = transcriptID
	if payload != nil {
		user.Msg = payload
		if text := agenticMessageText(payload); text != "" {
			user.Text = text
		}
	}
	if visible {
		s.seen = append(s.seen, sessionEntry{user: user, msg: user.Msg, transcriptID: transcriptID, visibleSeq: visibleSeq})
	} else {
		s.pending = append(s.pending, user)
	}
	return user
}

func (s *Session) RestorePendingObservation(payload *schema.AgenticMessage, transcriptID uint) {
	if payload == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, &sessionUser{Msg: payload, TranscriptID: transcriptID, IsObservation: true, Order: s.nextOrderLocked()})
}

func (s *Session) RestorePendingAgentMessage(payload *schema.AgenticMessage, transcriptID uint, wake, allowSleep bool) {
	if payload == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// 生活时钟可能在恢复前已经投递并写入内存，同一 transcript 行不重复入队
	if s.hasTranscriptLocked(transcriptID) {
		return
	}
	s.pending = append(s.pending, &sessionUser{Msg: payload, Text: agenticMessageText(payload), IsAgentMessage: true, Wake: wake, AllowSleep: allowSleep, TranscriptID: transcriptID, Order: s.nextOrderLocked()})
}

// HasSleepWakePending 判断是否有明确允许在睡眠中启动生活回合的待处理输入
func (s *Session) HasSleepWakePending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range s.pending {
		if user == nil || !user.IsAgentMessage || !user.Wake {
			continue
		}
		if user.AllowSleep {
			return true
		}
	}
	return false
}

func (s *Session) nextOrderLocked() uint64 {
	s.nextOrder++
	return s.nextOrder
}

func (s *Session) newUserLocked(msg *onebot.ConversationMessage, logID uint, mediaRefs []media.Ref) *sessionUser {
	ref := ""
	if msg.MessageID != 0 {
		ref = strconv.FormatInt(msg.MessageID, 10)
	}
	referenced := append([]int64(nil), msg.AtList...)
	if msg.Reply != nil && msg.Reply.SenderID > 0 {
		referenced = append(referenced, msg.Reply.SenderID)
	}
	user := &sessionUser{
		LogID: logID, Ref: ref, Content: strings.TrimSpace(msg.Content), At: msg.Time,
		ReceivedAt: msg.ReceivedAt, Mentioned: msg.IsMentioned, SenderID: msg.UserID,
		Nickname: msg.Nickname, ReferencedIDs: referenced, ArrivalSeq: msg.ArrivalSeq,
		MessageID: msg.MessageID, IsObservation: msg.MessageID == 0, Wake: true, Order: s.nextOrderLocked(),
	}
	user.Text = s.renderUserText(msg, ref, mediaRefs)
	user.Msg = schema.UserAgenticMessage(user.Text)
	for _, item := range mediaRefs {
		s.media.register(item)
	}
	return user
}

func (s *Session) renderUserText(msg *onebot.ConversationMessage, ref string, _ []media.Ref) string {
	displayName := resolveMessageDisplayName(msg.GroupCard, msg.Nickname)
	if displayName == "" {
		displayName = fmt.Sprintf("%d", msg.UserID)
	}
	var b strings.Builder
	if notes := s.takeNotesLocked(); len(notes) > 0 {
		b.WriteString(strings.Join(notes, "\n"))
		b.WriteString("\n")
	}
	if ref != "" {
		b.WriteString(fmt.Sprintf("[%s] %s %s(%d): ", ref, msg.Time.Format("2006-01-02 15:04:05"), displayName, msg.UserID))
	} else {
		b.WriteString(fmt.Sprintf("[观察] %s %s(%d): ", msg.Time.Format("2006-01-02 15:04:05"), displayName, msg.UserID))
	}
	if msg.Reply != nil {
		if msg.Reply.MessageID != 0 {
			b.WriteString(fmt.Sprintf("[回复消息 %d] ", msg.Reply.MessageID))
		} else if name := resolveMessageDisplayName(msg.Reply.GroupCard, msg.Reply.Nickname); name != "" {
			b.WriteString(fmt.Sprintf("[回复 %s(%d)] ", name, msg.Reply.SenderID))
		}
	}
	body := strings.TrimSpace(msg.FinalContent)
	if body == "" {
		body = strings.TrimSpace(msg.Content)
	}
	b.WriteString(body)
	return strings.TrimSpace(b.String())
}

func (s *Session) MediaShortHash(ref media.Ref) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.media.register(ref)
}

func (s *Session) takeNotesLocked() []string {
	if len(s.notes) == 0 {
		return nil
	}
	notes := s.notes
	s.notes = nil
	return notes
}

func (s *Session) PendingMessages() []*sessionUser {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]*sessionUser, 0, len(s.pending))
	for _, user := range s.pending {
		if user != nil && !user.IsMaintenanceHint {
			list = append(list, user)
		}
	}
	return list
}

// HasActionablePending 判断是否有能触发 ReAct 的输入；观察和维护提醒不算
func (s *Session) HasActionablePending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return hasActionableMessage(s.pending)
}

func (s *Session) MarkInjected(seq uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if seq > s.injected {
		s.injected = seq
	}
}

func (s *Session) PendingAfter(seq uint64) []tools.PendingMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	var list []tools.PendingMessage
	for _, user := range s.pending {
		if user == nil || user.IsMaintenanceHint || user.ArrivalSeq <= seq || user.ArrivalSeq <= s.injected {
			continue
		}
		list = append(list, tools.PendingMessage{ArrivalSeq: user.ArrivalSeq, Order: user.Order, Msg: user.Msg, TranscriptID: user.TranscriptID, Actionable: user.MessageID != 0 || (user.IsAgentMessage && user.Wake)})
	}
	return list
}

// PendingAgentMessages 返回尚未被当前模型请求包含的会话间消息。
func (s *Session) PendingAgentMessages() []tools.PendingMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]tools.PendingMessage, 0)
	for _, user := range s.pending {
		if user != nil && !user.IsMaintenanceHint && user.IsAgentMessage {
			list = append(list, tools.PendingMessage{ArrivalSeq: user.ArrivalSeq, Order: user.Order, Msg: user.Msg, TranscriptID: user.TranscriptID, Actionable: user.Wake})
		}
	}
	return list
}

func (s *Session) MarkRecalledByMessageID(messageID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range s.pending {
		if user.MessageID == messageID {
			s.notes = append(s.notes, systemNote("消息撤回", user.Ref+" 已撤回，不要再回应或引用其内容"))
			return
		}
	}
	for _, entry := range s.seen {
		if entry.user != nil && entry.user.MessageID == messageID {
			s.notes = append(s.notes, systemNote("消息撤回", entry.user.Ref+" 已撤回，不要再回应或引用其内容"))
			return
		}
	}
}

func (s *Session) NoteSelfSpoke(at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := at.Add(-gateWindowSize)
	kept := s.selfAt[:0]
	for _, existing := range s.selfAt {
		if existing.After(cutoff) {
			kept = append(kept, existing)
		}
	}
	s.selfAt = append(kept, at)
}

func (s *Session) RegisterMediaRefs(refs []media.Ref) {
	if len(refs) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ref := range refs {
		s.media.register(ref)
	}
}

func (s *Session) ResolveMediaByHash(hash string) (media.Ref, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.media.resolve(hash)
}

func (s *Session) RecentWindow(now time.Time, window time.Duration) (total int, self int, lastSelf time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	since := now.Add(-window)
	for _, entry := range s.seen {
		if entry.user != nil && !entry.user.IsAgentMessage && !entry.user.IsObservation && !entry.user.At.Before(since) {
			total++
		}
	}
	for _, user := range s.pending {
		if !user.IsAgentMessage && !user.IsObservation && !user.At.Before(since) {
			total++
		}
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

// Assemble 装配本轮上下文：本代头部（人格、群说明、摘要）已在 transcript 中，这里只按顺序展开
func (s *Session) Assemble(ctx context.Context, mem *memory.Manager, instruction, groupExtra string) ([]*schema.AgenticMessage, error) {
	if err := s.ensureGenerationHead(ctx, mem, instruction, groupExtra); err != nil {
		return nil, err
	}
	s.mu.Lock()
	entries := append([]sessionEntry(nil), s.seen...)
	pending := append([]*sessionUser(nil), s.pending...)
	s.mu.Unlock()

	messages := make([]*schema.AgenticMessage, 0, len(entries)+len(pending)+2)
	for _, entry := range orderEntries(entries) {
		if entry.msg == nil {
			continue
		}
		tagTranscriptEntryID(entry.msg, entry.transcriptID)
		messages = append(messages, entry.msg)
	}
	for _, user := range pending {
		if user.Msg == nil {
			continue
		}
		tagTranscriptEntryID(user.Msg, user.TranscriptID)
		messages = append(messages, user.Msg)
	}

	if tc := tools.GetToolContext(ctx); tc != nil {
		if seq := maxPendingSeq(pending); seq > tc.InjectedArrivalSeq {
			tc.InjectedArrivalSeq = seq
		}
	}
	return messages, nil
}

func maxPendingSeq(pending []*sessionUser) uint64 {
	var seq uint64
	for _, user := range pending {
		if user.ArrivalSeq > seq {
			seq = user.ArrivalSeq
		}
	}
	return seq
}
