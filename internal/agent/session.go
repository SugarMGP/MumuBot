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

	"github.com/bytedance/sonic"

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
	IsAgentMessage    bool
	IsMaintenanceHint bool
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

	mu                   sync.Mutex
	seen                 []sessionEntry
	pending              []*sessionUser
	media                *mediaIndex
	injected             uint64
	selfAt               []time.Time
	notes                []string
	nextOrder            uint64
	fullScale            float64
	fullSamples          int
	tokenBase            *tokenBase
	compactionRetryAfter time.Time
	turnSeq              uint64
	lastMemoryTurn       uint64
	lastTopicTurn        uint64
	lastMemoryHintTurn   uint64
	lastTopicHintTurn    uint64
	lastActivityAt       time.Time
	maintenanceMu        sync.Mutex
	idleDoneForActivity  bool
	idleCancel           context.CancelFunc
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

func (s *sessionStore) get(kind string, targetID int64) *Session {
	key := sessionKey(kind, targetID)
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[key]
	if !ok {
		session = &Session{kind: kind, targetID: targetID, memory: s.memory, media: newMediaIndex(), fullScale: 1}
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
	if msg.ReceivedAt.After(s.lastActivityAt) {
		s.lastActivityAt = msg.ReceivedAt
	}
	s.idleDoneForActivity = false
	if s.idleCancel != nil {
		s.idleCancel()
		s.idleCancel = nil
	}
	return user
}

// AppendAgentMessage 把另一个会话投递的内部消息加入待处理工作集
func (s *Session) AppendAgentMessage(message *schema.AgenticMessage, transcriptID uint, at time.Time) *sessionUser {
	if message == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if at.IsZero() {
		at = time.Now()
	}
	user := &sessionUser{
		Text: agenticMessageText(message), Msg: message, IsAgentMessage: true,
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
	s.pending = append(s.pending, &sessionUser{Msg: payload, TranscriptID: transcriptID, Order: s.nextOrderLocked()})
}

func (s *Session) RestorePendingAgentMessage(payload *schema.AgenticMessage, transcriptID uint) {
	if payload == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, &sessionUser{Msg: payload, Text: agenticMessageText(payload), IsAgentMessage: true, TranscriptID: transcriptID, Order: s.nextOrderLocked()})
}

// RestorePendingMaintenanceHint 恢复尚未进入模型输入的维护提醒
func (s *Session) RestorePendingMaintenanceHint(payload *schema.AgenticMessage, transcriptID uint) {
	if payload == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, &sessionUser{Msg: payload, Text: agenticMessageText(payload), IsMaintenanceHint: true, TranscriptID: transcriptID, Order: s.nextOrderLocked()})
}

func agenticMessageText(message *schema.AgenticMessage) string {
	if message == nil {
		return ""
	}
	var parts []string
	for _, block := range message.ContentBlocks {
		if block != nil && block.UserInputText != nil {
			parts = append(parts, block.UserInputText.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func (s *Session) AppendSeenMessage(msg *schema.AgenticMessage, transcriptID uint, visibleSeq uint64) {
	if msg == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, sessionEntry{msg: msg, transcriptID: transcriptID, visibleSeq: visibleSeq})
}

func (s *Session) ApplyVisibleSeqs(seqs map[uint]uint64) {
	if len(seqs) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.seen {
		if seq, ok := seqs[s.seen[i].transcriptID]; ok {
			s.seen[i].visibleSeq = seq
		}
	}
	kept := s.pending[:0]
	for _, user := range s.pending {
		if seq, ok := seqs[user.TranscriptID]; ok {
			entry := sessionEntry{msg: user.Msg, transcriptID: user.TranscriptID, visibleSeq: seq}
			if !user.IsMaintenanceHint {
				entry.user = user
			}
			s.seen = append(s.seen, entry)
			continue
		}
		kept = append(kept, user)
	}
	s.pending = kept
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
		MessageID: msg.MessageID, Order: s.nextOrderLocked(),
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

// AllPendingMessages 返回包含维护提醒在内的待处理项
func (s *Session) AllPendingMessages() []*sessionUser {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*sessionUser(nil), s.pending...)
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
		list = append(list, tools.PendingMessage{ArrivalSeq: user.ArrivalSeq, Order: user.Order, Msg: user.Msg, TranscriptID: user.TranscriptID})
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
			list = append(list, tools.PendingMessage{ArrivalSeq: user.ArrivalSeq, Order: user.Order, Msg: user.Msg, TranscriptID: user.TranscriptID})
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
	s.selfAt = append(s.selfAt, at)
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

// LatestLogID 返回当前会话已经进入工作集的最新消息日志编号，用作工具读取快照
func (s *Session) LatestLogID() uint {
	s.mu.Lock()
	defer s.mu.Unlock()
	var latest uint
	for _, entry := range s.seen {
		if entry.user != nil && entry.user.LogID > latest {
			latest = entry.user.LogID
		}
	}
	for _, user := range s.pending {
		if user.LogID > latest {
			latest = user.LogID
		}
	}
	return latest
}

func (s *Session) RecentWindow(now time.Time, window time.Duration) (total int, self int, lastSelf time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	since := now.Add(-window)
	for _, entry := range s.seen {
		if entry.user != nil && !entry.user.IsAgentMessage && !entry.user.At.Before(since) {
			total++
		}
	}
	for _, user := range s.pending {
		if !user.IsAgentMessage && !user.At.Before(since) {
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
		if entry.msg != nil {
			messages = append(messages, entry.msg)
		}
	}
	visibleIDs := make([]uint, 0)
	for _, entry := range entries {
		if entry.transcriptID != 0 && entry.visibleSeq == 0 {
			visibleIDs = append(visibleIDs, entry.transcriptID)
		}
	}
	for _, user := range pending {
		if user.Msg != nil {
			messages = append(messages, user.Msg)
		}
		if user.TranscriptID != 0 {
			visibleIDs = append(visibleIDs, user.TranscriptID)
		}
	}

	if tc := tools.GetToolContext(ctx); tc != nil {
		tc.VisibleTranscriptIDs = append(tc.VisibleTranscriptIDs, visibleIDs...)
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

// CompleteTurn 在成功完成一次思考后推进维护计数
func (s *Session) CompleteTurn(memoryMaintained, topicMaintained bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turnSeq++
	if memoryMaintained {
		s.lastMemoryTurn = s.turnSeq
	}
	if topicMaintained {
		s.lastTopicTurn = s.turnSeq
	}
}

// MaintenanceDue 返回本轮需要追加的维护提醒和对应计数
func (s *Session) MaintenanceDue(memoryTurns, topicTurns int) (string, uint64, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var labels []string
	nextMemory, nextTopic := s.lastMemoryHintTurn, s.lastTopicHintTurn
	if memoryTurns > 0 && s.turnSeq-s.lastMemoryTurn >= uint64(memoryTurns) && s.turnSeq-s.lastMemoryHintTurn >= uint64(max(1, memoryTurns/2)) {
		labels = append(labels, fmt.Sprintf("你已经连续 %d 轮没有更新长期记忆。如果有稳定偏好、身份、约定、重要经历或需要纠正的旧记忆，先用 searchMemory 查重，再用 manageMemory 新建或更新；没有就继续，不要为了清空提醒而硬写。", memoryTurns))
		nextMemory = s.turnSeq
	}
	if topicTurns > 0 && s.turnSeq-s.lastTopicTurn >= uint64(topicTurns) && s.turnSeq-s.lastTopicHintTurn >= uint64(max(1, topicTurns/2)) {
		labels = append(labels, fmt.Sprintf("你已经连续 %d 轮没有更新话题。如果刚才的对话有实质进展、结论、未完事项或收尾，用 updateTopic 更新摘要和关键消息；没有就继续，不要硬造话题。", topicTurns))
		nextTopic = s.turnSeq
	}
	if len(labels) == 0 {
		return "", nextMemory, nextTopic
	}
	return strings.Join(labels, "\n"), nextMemory, nextTopic
}

// AppendMaintenanceHint 写入持久化系统提醒并加入待处理输入
func (s *Session) AppendMaintenanceHint(ctx context.Context, mem *memory.Manager, text string, memoryTurn, topicTurn uint64) error {
	if mem == nil || strings.TrimSpace(text) == "" {
		return nil
	}
	msg := schema.UserAgenticMessage(systemNote("维护提醒", text))
	msg.Extra = map[string]any{"kind": "maintenance_hint"}
	payload, err := sonic.Marshal(msg)
	if err != nil {
		return err
	}
	entry := &memory.TranscriptEntry{ConversationKind: s.kind, TargetID: s.targetID, EntryKind: "system_note", Payload: payload}
	if err := mem.AppendTranscript(ctx, entry); err != nil {
		return err
	}
	s.mu.Lock()
	s.pending = append(s.pending, &sessionUser{Msg: msg, Text: text, TranscriptID: entry.ID, IsMaintenanceHint: true, Order: s.nextOrderLocked()})
	s.lastMemoryHintTurn = memoryTurn
	s.lastTopicHintTurn = topicTurn
	s.mu.Unlock()
	return nil
}

// IdleAvailable 判断当前空闲周期是否已经尝试过压缩
func (s *Session) IdleAvailable() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.idleDoneForActivity
}

// MarkIdleStart 登记空闲压缩取消函数
func (s *Session) MarkIdleStart(cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idleCancel = cancel
}

// ClearIdleStart 清除空闲压缩取消函数
func (s *Session) ClearIdleStart() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idleCancel = nil
}

// MarkIdleDone 标记当前空闲周期已经处理过
func (s *Session) MarkIdleDone() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idleDoneForActivity = true
}

// MarkTopicMaintained 记录话题维护
func (s *Session) MarkTopicMaintained() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastTopicTurn = s.turnSeq
}

// LastActivityAt 返回最近真实消息时间
func (s *Session) LastActivityAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastActivityAt
}

// SetLastActivity 更新最近真实消息时间
func (s *Session) SetLastActivity(at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if at.After(s.lastActivityAt) {
		s.lastActivityAt = at
	}
}
