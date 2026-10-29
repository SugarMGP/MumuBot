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
	LogID         uint
	TranscriptID  uint
	Ref           string
	Text          string
	Content       string
	At            time.Time
	ReceivedAt    time.Time
	Mentioned     bool
	SenderID      int64
	Nickname      string
	ReferencedIDs []int64
	ArrivalSeq    uint64
	MessageID     int64
	Msg           *schema.AgenticMessage
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
	refs                 map[string]int64
	refsLog              map[string]uint
	nextRef              int
	injected             uint64
	selfAt               []time.Time
	notes                []string
	autoRefVersions      []string
	tokenScale           float64
	tokenSamples         int
	compactionRetryAfter time.Time
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

func (s *Session) AppendUser(msg *onebot.ConversationMessage, logID uint, mediaRefs []media.Ref, ref string) *sessionUser {
	if msg == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user := s.newUserLocked(msg, logID, mediaRefs, ref)
	s.pending = append(s.pending, user)
	return user
}

// RestoreUser 用 transcript 中保存的原始消息体恢复用户条目
func (s *Session) RestoreUser(msg *onebot.ConversationMessage, logID uint, mediaRefs []media.Ref, ref string, transcriptID uint, visibleSeq uint64, visible bool, payload *schema.AgenticMessage) *sessionUser {
	if msg == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user := s.newUserLocked(msg, logID, mediaRefs, ref)
	user.TranscriptID = transcriptID
	if payload != nil {
		user.Msg = payload
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
	s.pending = append(s.pending, &sessionUser{Msg: payload, TranscriptID: transcriptID})
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

func (s *Session) newUserLocked(msg *onebot.ConversationMessage, logID uint, mediaRefs []media.Ref, ref string) *sessionUser {
	if msg.MessageID == 0 {
		ref = ""
	} else if ref == "" {
		s.nextRef++
		ref = "m" + strconv.Itoa(s.nextRef)
	} else if n, err := strconv.Atoi(strings.TrimPrefix(ref, "m")); err == nil && n > s.nextRef {
		s.nextRef = n
	}
	referenced := append([]int64(nil), msg.AtList...)
	if msg.Reply != nil && msg.Reply.SenderID > 0 {
		referenced = append(referenced, msg.Reply.SenderID)
	}
	user := &sessionUser{
		LogID: logID, Ref: ref, Content: strings.TrimSpace(msg.Content), At: msg.Time,
		ReceivedAt: msg.ReceivedAt, Mentioned: msg.IsMentioned, SenderID: msg.UserID,
		Nickname: msg.Nickname, ReferencedIDs: referenced, ArrivalSeq: msg.ArrivalSeq,
		MessageID: msg.MessageID,
	}
	user.Text = s.renderUserText(msg, ref, mediaRefs)
	user.Msg = schema.UserAgenticMessage(user.Text)
	if s.refs == nil {
		s.refs = map[string]int64{}
	}
	if s.refsLog == nil {
		s.refsLog = map[string]uint{}
	}
	if ref != "" {
		s.refs[ref] = msg.MessageID
		if logID != 0 {
			s.refsLog[ref] = logID
		}
	}
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
		list = append(list, tools.PendingMessage{ArrivalSeq: user.ArrivalSeq, Msg: user.Msg, TranscriptID: user.TranscriptID})
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

func (s *Session) ResolveMessageRef(ref string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	messageID, ok := s.refs[strings.TrimSpace(ref)]
	return messageID, ok
}

func (s *Session) ResolveMessageLogRef(ref string) (uint, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	logID, ok := s.refsLog[strings.TrimSpace(ref)]
	return logID, ok
}

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

func (s *Session) LatestLogID() uint {
	s.mu.Lock()
	defer s.mu.Unlock()
	var latest uint
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
		if entry.user != nil && !entry.user.At.Before(since) {
			total++
		}
	}
	for _, user := range s.pending {
		if !user.At.Before(since) {
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
	refVersions := append([]string(nil), s.autoRefVersions...)
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
		refText, versions, refErr := mem.BuildAutoReference(ctx, s.kind, s.autoReferenceUserIDs())
		if refErr != nil {
			return nil, refErr
		}
		if !equalVersionList(versions, refVersions) {
			content := refText
			if content == "" {
				content = "【内部参考更新】先前的内部参考已不再有效。"
			} else {
				content = "【内部参考更新】以下为当前有效内部参考；若与压缩基线冲突，以本条为准。\n" + content
			}
			refMsg := schema.UserAgenticMessage(content)
			refMsg.Extra = map[string]any{"source": "internal_context", "reference_versions": versions}
			payload, _ := sonic.Marshal(refMsg)
			entry := &memory.TranscriptEntry{ConversationKind: s.kind, TargetID: s.targetID, EntryKind: "internal_reference", Payload: payload}
			if err := mem.AppendTranscript(ctx, entry); err != nil {
				return nil, err
			}
			referenceID = entry.ID
			messages = append(messages, refMsg)
			s.mu.Lock()
			s.seen = append(s.seen, sessionEntry{msg: refMsg, transcriptID: entry.ID})
			s.autoRefVersions = append([]string(nil), versions...)
			s.mu.Unlock()
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

// SetAutoReferenceVersions 恢复已经注入过的内部参考版本，避免重启后重复追加
func (s *Session) SetAutoReferenceVersions(versions []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.autoRefVersions = append([]string(nil), versions...)
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

func equalVersionList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
