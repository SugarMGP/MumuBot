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

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/schema"
)

type sessionUser struct {
	LogID          uint
	TranscriptID   uint
	Ref            string
	Text           string
	Content        string
	At             time.Time
	ReceivedAt     time.Time
	Mentioned      bool
	SenderID       int64
	Nickname       string
	ReferencedIDs  []int64
	ArrivalSeq     uint64
	Order          uint64
	MessageID      int64
	Msg            *schema.AgenticMessage
	IsAgentMessage bool
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

	mu                      sync.Mutex
	seen                    []sessionEntry
	pending                 []*sessionUser
	media                   *mediaIndex
	injected                uint64
	selfAt                  []time.Time
	notes                   []string
	pendingReferenceReceipt *memory.ReferenceReceipt
	nextOrder               uint64
	tokenScale              float64
	tokenSamples            int
	compactionRetryAfter    time.Time
}

type sessionStore struct {
	mu       sync.Mutex
	memory   *memory.Manager
	sessions map[string]*Session
}

func newSessionStore(mem *memory.Manager) *sessionStore {
	return &sessionStore{memory: mem, sessions: map[string]*Session{}}
}

func (s *sessionStore) get(kind string, targetID int64) *Session {
	key := sessionKey(kind, targetID)
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[key]
	if !ok {
		session = &Session{kind: kind, targetID: targetID, memory: s.memory, media: newMediaIndex(), tokenScale: 1}
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

func (s *Session) RestorePendingReference(payload *schema.AgenticMessage, transcriptID uint, receipt *memory.ReferenceReceipt) {
	if payload == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, sessionEntry{msg: payload, transcriptID: transcriptID})
	s.pendingReferenceReceipt = receipt
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
			s.seen = append(s.seen, sessionEntry{user: user, msg: user.Msg, transcriptID: user.TranscriptID, visibleSeq: seq})
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
		if user.ArrivalSeq <= seq || user.ArrivalSeq <= s.injected {
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
		if user != nil && user.IsAgentMessage {
			list = append(list, tools.PendingMessage{ArrivalSeq: user.ArrivalSeq, Order: user.Order, Msg: user.Msg, TranscriptID: user.TranscriptID})
		}
	}
	return list
}

func (s *Session) MessagesAfter(seq uint64) []*sessionUser {
	s.mu.Lock()
	defer s.mu.Unlock()
	var list []*sessionUser
	for _, entry := range s.seen {
		if entry.user != nil && entry.user.ArrivalSeq > seq {
			list = append(list, entry.user)
		}
	}
	for _, user := range s.pending {
		if user.ArrivalSeq > seq {
			list = append(list, user)
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].ArrivalSeq < list[j].ArrivalSeq })
	return list
}

func (s *Session) MarkRecalledByMessageID(messageID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range s.pending {
		if user.MessageID == messageID {
			s.notes = append(s.notes, fmt.Sprintf("（%s 已撤回，不要再回应或引用其内容）", user.Ref))
			return
		}
	}
	for _, entry := range s.seen {
		if entry.user != nil && entry.user.MessageID == messageID {
			s.notes = append(s.notes, fmt.Sprintf("（%s 已撤回，不要再回应或引用其内容）", entry.user.Ref))
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

func (s *Session) Assemble(ctx context.Context, mem *memory.Manager) ([]*schema.AgenticMessage, error) {
	s.mu.Lock()
	entries := append([]sessionEntry(nil), s.seen...)
	pending := append([]*sessionUser(nil), s.pending...)
	s.mu.Unlock()

	digestText := ""
	var err error
	if mem != nil {
		digestText, err = mem.RenderCheckpointDigest(ctx, s.kind, s.targetID)
		if err != nil {
			return nil, err
		}
	}
	messages := make([]*schema.AgenticMessage, 0, len(entries)+len(pending)+2)
	if digestText != "" {
		digestMessage := schema.UserAgenticMessage(digestText)
		digestMessage.Extra = map[string]any{"source": "internal_context", "kind": "checkpoint_digest"}
		messages = append(messages, digestMessage)
	}
	for _, entry := range entries {
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
	var referenceID uint
	if mem != nil {
		pendingTexts := make([]string, 0, len(pending))
		for _, user := range pending {
			if user != nil {
				pendingTexts = append(pendingTexts, user.Text)
			}
		}
		var pendingEntry *sessionEntry
		for i := range entries {
			if entries[i].transcriptID != 0 && entries[i].visibleSeq == 0 && entries[i].msg != nil && entries[i].msg.Extra["kind"] == "reference" {
				pendingEntry = &entries[i]
				break
			}
		}
		if pendingEntry != nil {
			referenceID = pendingEntry.transcriptID
		} else {
			snapshot, refErr := mem.BuildAutoReference(ctx, s.kind, s.targetID, s.autoReferenceUserIDs(), pendingTexts)
			if refErr != nil {
				return nil, refErr
			}
			seen, seenErr := mem.ReferenceSeenForSession(ctx, s.kind, s.targetID)
			if seenErr != nil {
				return nil, seenErr
			}
			delta, deltaErr := mem.BuildReferenceDelta(ctx, snapshot, seen)
			if deltaErr != nil {
				return nil, deltaErr
			}
			if delta.Text != "" {
				refMsg := schema.UserAgenticMessage(delta.Text)
				refMsg.Extra = map[string]any{"source": "internal_context", "kind": "reference", "reference_receipts": delta.Receipt}
				payload, _ := sonic.Marshal(refMsg)
				entry := &memory.TranscriptEntry{ConversationKind: s.kind, TargetID: s.targetID, EntryKind: "internal_reference", Payload: payload}
				if err := mem.AppendTranscript(ctx, entry); err != nil {
					return nil, err
				}
				referenceID = entry.ID
				messages = append(messages, refMsg)
				s.mu.Lock()
				s.seen = append(s.seen, sessionEntry{msg: refMsg, transcriptID: entry.ID})
				s.pendingReferenceReceipt = &delta.Receipt
				s.mu.Unlock()
			}
		}
	}

	if referenceID != 0 {
		visibleIDs = append(visibleIDs, referenceID)
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

func (s *Session) autoReferenceUserIDs() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.kind == memory.ConversationKindPrivate {
		return []int64{s.targetID}
	}
	seen := map[int64]bool{}
	ids := make([]int64, 0, 3)
	add := func(id int64) {
		if id > 0 && !seen[id] && len(ids) < 3 {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for i := len(s.pending) - 1; i >= 0; i-- {
		add(s.pending[i].SenderID)
	}
	for i := len(s.pending) - 1; i >= 0; i-- {
		for _, id := range s.pending[i].ReferencedIDs {
			add(id)
		}
	}
	return ids
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
