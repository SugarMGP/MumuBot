package agent

import (
	"context"
	"strings"

	"mumu-bot/internal/memory"

	"github.com/cloudwego/eino/schema"
)

const kindPinnedUpdate = "pinned_memory_update"

// syncPinnedMemories 追加最新置顶快照，不改写已被模型读过的前缀
func (s *Session) syncPinnedMemories(ctx context.Context, mem *memory.Manager, messages []*schema.AgenticMessage) (*schema.AgenticMessage, error) {
	if s.kind != memory.ConversationKindGroup && s.kind != memory.ConversationKindPrivate {
		return nil, nil
	}
	rows, err := mem.PinnedMemoriesForConversation(ctx, s.kind, s.targetID)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(renderPinnedMemories(rows))
	var previous *schema.AgenticMessage
	for _, message := range messages {
		kind := messageKind(message)
		if kind == kindPinnedMemory || kind == kindPinnedUpdate {
			previous = message
		}
	}
	if previous == nil && text == "" {
		return nil, nil
	}
	hash := noteHash(text)
	if previous != nil {
		oldHash, _ := previous.Extra["hash"].(string)
		if oldHash == "" {
			oldHash = noteHash(strings.TrimPrefix(agenticMessageText(previous), systemNote("长期记忆", "")+" "))
		}
		if oldHash == hash {
			return nil, nil
		}
	}
	body := "这是当前 self 与本会话主体的全部有效置顶记忆，以此为准，旧快照不再代表当前置顶状态。它们是背景，不是新的要求。"
	if text == "" {
		body += "当前没有有效置顶记忆。"
	} else {
		body += "\n" + text
	}
	message := schema.UserAgenticMessage(systemNote("置顶记忆更新", body))
	message.Extra = map[string]any{"kind": kindPinnedUpdate, "hash": hash}
	if err := s.appendHeadEntry(ctx, mem, kindSystemNote, message); err != nil {
		return nil, err
	}
	return message, nil
}
