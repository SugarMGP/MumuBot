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
	return session.Assemble(ctx, a.memory, a.personaFor(ctx).Instruction(mode), extra)
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

// orderEntries 把代头提到最前，其余保持原顺序
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
	return s.appendHeadEntries(ctx, mem, specs)
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
	fixed := session.estimateTextTokens(systemNote("长期记忆", pinnedText)) + session.estimateTextTokens(systemNote("上下文摘要", memory.RenderContextDigest(digest.Overview, nil, 0)))
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
func (s *Session) appendHeadEntries(ctx context.Context, mem *memory.Manager, specs []headEntrySpec) error {
	if len(specs) == 0 {
		return nil
	}
	entries := make([]*memory.TranscriptEntry, 0, len(specs))
	for _, spec := range specs {
		payload, err := sonic.Marshal(spec.msg)
		if err != nil {
			return fmt.Errorf("编码%s失败: %w", spec.kind, err)
		}
		entries = append(entries, &memory.TranscriptEntry{
			ConversationKind: s.kind, TargetID: s.targetID, EntryKind: spec.kind, Payload: payload,
		})
	}
	if err := mem.AppendTranscripts(ctx, entries); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, spec := range specs {
		s.seen = append(s.seen, sessionEntry{msg: spec.msg, transcriptID: entries[i].ID})
	}
	return nil
}

// appendHeadEntry 追加单条非人格代头，例如群说明变更
func (s *Session) appendHeadEntry(ctx context.Context, mem *memory.Manager, kind string, msg *schema.AgenticMessage) error {
	return s.appendHeadEntries(ctx, mem, []headEntrySpec{{kind: kind, msg: msg}})
}

// noteHash 用来判断群说明是否变化
func noteHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:8])
}
