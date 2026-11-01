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

// orderEntries 把代头三类提到最前，其余保持原顺序
func orderEntries(entries []sessionEntry) []sessionEntry {
	heads := make([]sessionEntry, 0, 3)
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

// ensureGenerationHead 在本代第一条消息前写入人格、群说明与上下文摘要
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
	systemMsg := schema.SystemAgenticMessage(instruction)
	systemMsg.Extra = map[string]any{"kind": kindSystemPrompt}
	if err := s.appendHeadEntry(ctx, mem, kindSystemPrompt, systemMsg); err != nil {
		return err
	}
	if text := strings.TrimSpace(groupExtra); text != "" {
		noteMsg := schema.UserAgenticMessage(systemNote("群说明", text))
		noteMsg.Extra = map[string]any{"kind": kindGroupNote, "hash": noteHash(text)}
		if err := s.appendHeadEntry(ctx, mem, kindGroupNote, noteMsg); err != nil {
			return err
		}
	}
	digest, err := mem.RenderCheckpointDigest(ctx, s.kind, s.targetID)
	if err != nil {
		return err
	}
	if digest = strings.TrimSpace(digest); digest != "" {
		digestMsg := schema.UserAgenticMessage(systemNote("上下文摘要", digest))
		digestMsg.Extra = map[string]any{"kind": kindContextDigest}
		if err := s.appendHeadEntry(ctx, mem, kindContextDigest, digestMsg); err != nil {
			return err
		}
	}
	return nil
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

// appendHeadEntry 把头部行写入 transcript 并挂到工作集
func (s *Session) appendHeadEntry(ctx context.Context, mem *memory.Manager, kind string, msg *schema.AgenticMessage) error {
	payload, err := sonic.Marshal(msg)
	if err != nil {
		return fmt.Errorf("编码%s失败: %w", kind, err)
	}
	entry := &memory.TranscriptEntry{
		ConversationKind: s.kind,
		TargetID:         s.targetID,
		EntryKind:        kind,
		Payload:          payload,
	}
	if err := mem.AppendTranscript(ctx, entry); err != nil {
		return err
	}
	s.mu.Lock()
	s.seen = append(s.seen, sessionEntry{msg: msg, transcriptID: entry.ID})
	s.mu.Unlock()
	return nil
}

// noteHash 用来判断群说明是否变化
func noteHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:8])
}
