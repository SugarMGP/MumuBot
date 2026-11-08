package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/persona"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/schema"
	"go.uber.org/zap"
)

const (
	kindSystemPrompt  = "system_prompt"
	kindContextDigest = "context_digest"
	kindPinnedMemory  = "pinned_memory"
	kindGroupNote     = "group_note"
	kindSystemNote    = "system_note"
	kindLifeSummary   = "life_summary"
	kindWorkerSummary = "worker_summary"

	systemMessagePrefix = "[系统消息："
)

// assembleMessages 按会话类型取当前预设，并补齐本代头部
func (a *Agent) assembleMessages(ctx context.Context, session *Session) ([]*schema.AgenticMessage, error) {
	mode := persona.ModeGroup
	extra := ""
	if session.kind == memory.ConversationKindPrivate {
		mode = persona.ModePrivate
	}
	if session.kind == memory.ConversationKindGroup {
		note, err := a.memory.WithContext(ctx).GetGroupExtraPrompt(ctx, session.targetID)
		if err != nil {
			zap.L().Warn("读取群说明失败", zap.Int64("target_id", session.targetID), zap.Error(err))
		} else {
			extra = note
		}
	}
	return session.Assemble(ctx, a.memory, a.personaFor(ctx).SessionInstruction(mode, session.targetID), extra)
}

// headPriority 决定代头行在上下文里的固定位置，非代头返回 -1
func headPriority(kind string) int {
	switch kind {
	case kindSystemPrompt:
		return 0
	case kindGroupNote:
		return 1
	case kindContextDigest:
		return 2
	case kindPinnedMemory:
		return 3
	}
	return -1
}

// messageKind 读取消息上挂载的条目类型
func messageKind(msg *schema.AgenticMessage) string {
	if msg == nil || msg.Extra == nil {
		return ""
	}
	kind, _ := msg.Extra["kind"].(string)
	return kind
}

// isHeadEntry 判断是否为本代头部行
func isHeadEntry(msg *schema.AgenticMessage) bool {
	return headPriority(messageKind(msg)) >= 0
}

// orderEntries 代头优先，其余按真实可见顺序排列，尚未可见的轨迹保持追加顺序放在末尾
func orderEntries(entries []sessionEntry) []sessionEntry {
	heads := make([]sessionEntry, 0, 4)
	rest := make([]sessionEntry, 0, len(entries))
	for _, entry := range entries {
		if isHeadEntry(entry.msg) {
			heads = append(heads, entry)
			continue
		}
		rest = append(rest, entry)
	}
	sort.SliceStable(heads, func(i, j int) bool {
		left, right := headPriority(messageKind(heads[i].msg)), headPriority(messageKind(heads[j].msg))
		if left != right {
			return left < right
		}
		return heads[i].transcriptID < heads[j].transcriptID
	})
	sort.SliceStable(rest, func(i, j int) bool {
		left, right := rest[i].visibleSeq, rest[j].visibleSeq
		if left == 0 || right == 0 {
			return left != 0 && right == 0
		}
		return left < right
	})
	return append(heads, rest...)
}

// systemNote 统一拼装系统注入行的前缀
func systemNote(label, text string) string {
	line := systemMessagePrefix + label + "]"
	if text = strings.TrimSpace(text); text != "" {
		line += " " + text
	}
	return line
}

type headEntrySpec struct {
	kind string
	msg  *schema.AgenticMessage
}

// ensureGenerationHead 在本代第一条消息前原子写入人格、群说明、摘要和置顶记忆
func (s *Session) ensureGenerationHead(ctx context.Context, mem *memory.Manager, instruction, groupExtra string) error {
	if mem == nil {
		return fmt.Errorf("记忆系统未初始化")
	}
	system, note := s.generationHead()
	if system != nil {
		return s.syncGroupNote(ctx, mem, note, groupExtra)
	}
	instruction = strings.TrimSpace(instruction)
	if instruction == "" {
		return fmt.Errorf("人格预设为空")
	}
	specs := make([]headEntrySpec, 0, 4)
	systemMsg := schema.SystemAgenticMessage(instruction)
	systemMsg.Extra = map[string]any{"kind": kindSystemPrompt}
	specs = append(specs, headEntrySpec{kind: kindSystemPrompt, msg: systemMsg})
	if s.kind == memory.ConversationKindWorker {
		_, err := s.appendHeadEntries(ctx, mem, specs)
		return err
	}
	if s.kind == memory.ConversationKindLife {
		// life 没有群说明和话题摘要，只带自己的置顶记忆
		messages, err := buildLifeHead(ctx, mem, instruction)
		if err != nil {
			return err
		}
		_, err = s.appendHeadMessages(ctx, mem, messages)
		return err
	}
	if text := strings.TrimSpace(groupExtra); text != "" {
		noteMsg := schema.UserAgenticMessage(systemNote("群说明", text))
		noteMsg.Extra = map[string]any{"kind": kindGroupNote, "hash": noteHash(text)}
		specs = append(specs, headEntrySpec{kind: kindGroupNote, msg: noteMsg})
	}
	digest, err := mem.LoadContextDigest(ctx, s.kind, s.targetID)
	if err != nil {
		return err
	}
	pinned, err := mem.PinnedMemoriesForConversation(ctx, s.kind, s.targetID)
	if err != nil {
		return err
	}
	pinnedText := renderPinnedMemories(pinned)
	digestText, omitted := renderBoundedDigest(ctx, s, digest, pinnedText)
	if omitted > 0 {
		zap.L().Warn("摘要话题超出注入预算", zap.String("kind", s.kind), zap.Int64("target_id", s.targetID), zap.Int("omitted", omitted))
	}
	if digestText = strings.TrimSpace(digestText); digestText != "" {
		digestMsg := schema.UserAgenticMessage(systemNote("上下文摘要", digestText))
		digestMsg.Extra = map[string]any{"kind": kindContextDigest}
		specs = append(specs, headEntrySpec{kind: kindContextDigest, msg: digestMsg})
	}
	if pinnedText = strings.TrimSpace(pinnedText); pinnedText != "" {
		pinnedMsg := schema.UserAgenticMessage(systemNote("长期记忆", pinnedText))
		pinnedMsg.Extra = map[string]any{"kind": kindPinnedMemory}
		specs = append(specs, headEntrySpec{kind: kindPinnedMemory, msg: pinnedMsg})
	}
	_, err = s.appendHeadEntries(ctx, mem, specs)
	return err
}

// buildLifeHead 构造本代生活代头：人格 + 自己的置顶记忆，供首次装配和摘要换代共用
func buildLifeHead(ctx context.Context, mem *memory.Manager, instruction string) ([]*schema.AgenticMessage, error) {
	instruction = strings.TrimSpace(instruction)
	if instruction == "" {
		return nil, fmt.Errorf("人格预设为空")
	}
	system := schema.SystemAgenticMessage(instruction)
	system.Extra = map[string]any{"kind": kindSystemPrompt}
	messages := []*schema.AgenticMessage{system}
	pinned, err := mem.PinnedMemoriesForSelf(ctx)
	if err != nil {
		return nil, err
	}
	if text := strings.TrimSpace(renderPinnedMemories(pinned)); text != "" {
		pinnedMsg := schema.UserAgenticMessage(systemNote("长期记忆", text))
		pinnedMsg.Extra = map[string]any{"kind": kindPinnedMemory}
		messages = append(messages, pinnedMsg)
	}
	return messages, nil
}

// appendHeadMessages 把已有的代头消息原样写成 transcript 行，返回可标记 visible 的行号
func (s *Session) appendHeadMessages(ctx context.Context, mem *memory.Manager, messages []*schema.AgenticMessage) ([]uint, error) {
	specs := make([]headEntrySpec, 0, len(messages))
	for _, msg := range messages {
		kind := messageKind(msg)
		if headPriority(kind) < 0 {
			return nil, fmt.Errorf("无效的代头类型")
		}
		specs = append(specs, headEntrySpec{kind: kind, msg: msg})
	}
	return s.appendHeadEntries(ctx, mem, specs)
}

// headEntries 把代头消息编码成待写入的 transcript 行
func headEntries(messages []*schema.AgenticMessage) ([]memory.HeadEntry, error) {
	entries := make([]memory.HeadEntry, 0, len(messages))
	for _, msg := range messages {
		kind := messageKind(msg)
		if headPriority(kind) < 0 {
			return nil, fmt.Errorf("无效的代头类型")
		}
		payload, err := sonic.Marshal(msg)
		if err != nil {
			return nil, err
		}
		entries = append(entries, memory.HeadEntry{Kind: kind, Payload: payload})
	}
	return entries, nil
}

// attachHeadMessages 给事务里已写入的代头补上行号并挂到内存
func (s *Session) attachHeadMessages(messages []*schema.AgenticMessage, ids []uint) {
	if len(messages) == 0 || len(messages) != len(ids) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, msg := range messages {
		tagTranscriptEntryID(msg, ids[i])
		s.seen = append(s.seen, sessionEntry{msg: msg, transcriptID: ids[i]})
	}
}

// stripHeadEntries 去掉代头，供摘要检查点只保留摘要正文
func stripHeadEntries(messages []*schema.AgenticMessage) []*schema.AgenticMessage {
	kept := make([]*schema.AgenticMessage, 0, len(messages))
	for _, message := range messages {
		if isHeadEntry(message) {
			continue
		}
		kept = append(kept, message)
	}
	return kept
}

// renderPinnedMemories 渲染当前会话主体和自己的全部 active 置顶记忆
func renderPinnedMemories(records []memory.KnowledgeRecord) string {
	lines := make([]string, 0, len(records))
	for _, record := range records {
		line := fmt.Sprintf("- #%d [%s] %s", record.ID, record.SubjectType, strings.TrimSpace(record.Label))
		if content := strings.TrimSpace(record.Content); content != "" {
			if strings.TrimSpace(record.Label) != "" {
				line += "：" + content
			} else {
				line += " " + content
			}
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// renderBoundedDigest 在 30% 摘要预算内保留话题，pinned 和 overview 固定
func renderBoundedDigest(ctx context.Context, session *Session, digest *memory.ContextDigest, pinnedText string) (string, int) {
	if digest == nil {
		return "", 0
	}
	window := 0
	if generation := generationFrom(ctx); generation != nil {
		window = generation.contextWindow
	}
	cfg := configFor(ctx)
	if window <= 0 || cfg == nil {
		return memory.RenderContextDigest(digest.Overview, digest.Topics, 0), 0
	}
	budget := int(float64(window) * cfg.Compactor.DigestBudgetRatio)
	fixed := session.estimateTextTokens(systemNote("长期记忆", pinnedText))
	if fixed+session.estimateTextTokens(systemNote("上下文摘要", memory.RenderContextDigest(digest.Overview, nil, len(digest.Topics)))) > budget {
		zap.L().Warn("固定摘要内容超过注入预算，保留概览与置顶记忆", zap.Int("budget", budget))
	}
	selected := make([]memory.ContextTopic, 0, len(digest.Topics))
	for _, topic := range digest.Topics {
		candidate := append(selected, topic)
		text := memory.RenderContextDigest(digest.Overview, candidate, len(digest.Topics)-len(candidate))
		if fixed+session.estimateTextTokens(systemNote("上下文摘要", text)) > budget {
			break
		}
		selected = candidate
	}
	omitted := len(digest.Topics) - len(selected)
	if omitted > 0 {
		return memory.RenderContextDigest(digest.Overview, selected, omitted), omitted
	}
	return memory.RenderContextDigest(digest.Overview, selected, 0), 0
}

// syncGroupNote 在群说明变化时追加一条系统消息，头部保持不动
func (s *Session) syncGroupNote(ctx context.Context, mem *memory.Manager, note *sessionEntry, groupExtra string) error {
	text := strings.TrimSpace(groupExtra)
	previous := ""
	if note != nil && note.msg != nil && note.msg.Extra != nil {
		if value, ok := note.msg.Extra["hash"].(string); ok {
			previous = value
		}
	}
	current := ""
	if text != "" {
		current = noteHash(text)
	}
	if current == previous {
		return nil
	}
	content := systemNote("群说明取消", "")
	if current != "" {
		content = systemNote("群说明变更", text)
	}
	msg := schema.UserAgenticMessage(content)
	msg.Extra = map[string]any{"kind": kindSystemNote, "hash": current}
	return s.appendHeadEntry(ctx, mem, kindSystemNote, msg)
}

// generationHead 返回本代的人格行与最后一条群说明行
func (s *Session) generationHead() (system *sessionEntry, note *sessionEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.seen {
		entry := &s.seen[i]
		if entry.msg == nil || entry.msg.Extra == nil {
			continue
		}
		switch entry.msg.Extra["kind"] {
		case kindSystemPrompt:
			system = entry
		case kindGroupNote, kindSystemNote:
			note = entry
		}
	}
	return system, note
}

// appendHeadEntries 批量写入代头，事务成功后才挂到内存，避免半代头
func (s *Session) appendHeadEntries(ctx context.Context, mem *memory.Manager, specs []headEntrySpec) ([]uint, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	entries := make([]*memory.TranscriptEntry, 0, len(specs))
	for _, spec := range specs {
		payload, err := sonic.Marshal(spec.msg)
		if err != nil {
			return nil, fmt.Errorf("编码%s失败: %w", spec.kind, err)
		}
		entries = append(entries, &memory.TranscriptEntry{
			ConversationKind: s.kind, TargetID: s.targetID, EntryKind: spec.kind, Payload: payload,
		})
	}
	if err := mem.AppendTranscripts(ctx, entries); err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(entries))
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, spec := range specs {
		tagTranscriptEntryID(spec.msg, entries[i].ID)
		s.seen = append(s.seen, sessionEntry{msg: spec.msg, transcriptID: entries[i].ID})
		ids = append(ids, entries[i].ID)
	}
	return ids, nil
}

// appendHeadEntry 追加单条非人格代头，例如群说明变更
func (s *Session) appendHeadEntry(ctx context.Context, mem *memory.Manager, kind string, msg *schema.AgenticMessage) error {
	_, err := s.appendHeadEntries(ctx, mem, []headEntrySpec{{kind: kind, msg: msg}})
	return err
}

// noteHash 用来判断群说明是否变化
func noteHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:8])
}
