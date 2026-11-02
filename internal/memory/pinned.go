package memory

import (
	"context"
	"fmt"
)

type pinnedSubject struct {
	Type   string
	Target string
}

// PinnedMemoriesForConversation 返回当前群聊或私聊主体以及自己的全部置顶记忆
func (m *Manager) PinnedMemoriesForConversation(ctx context.Context, kind string, targetID int64) ([]KnowledgeRecord, error) {
	var subject pinnedSubject
	switch kind {
	case ConversationKindGroup:
		subject = pinnedSubject{Type: SubjectGroup, Target: fmt.Sprintf("%d", targetID)}
	case ConversationKindPrivate:
		subject = pinnedSubject{Type: SubjectUser, Target: fmt.Sprintf("%d", targetID)}
	default:
		return nil, fmt.Errorf("未知会话类型 %q", kind)
	}
	return m.pinnedMemories(ctx, []pinnedSubject{subject, {Type: SubjectSelf, Target: SubjectSelf}})
}

// PinnedMemoriesForUser 返回指定 QQ 或自己的全部置顶记忆
func (m *Manager) PinnedMemoriesForUser(ctx context.Context, userID int64) ([]KnowledgeRecord, error) {
	if userID <= 0 {
		return nil, fmt.Errorf("用户 ID 无效")
	}
	return m.pinnedMemories(ctx, []pinnedSubject{{Type: SubjectUser, Target: fmt.Sprintf("%d", userID)}})
}

// PinnedMemoriesForSelf 返回自己的全部置顶记忆
func (m *Manager) PinnedMemoriesForSelf(ctx context.Context) ([]KnowledgeRecord, error) {
	return m.pinnedMemories(ctx, []pinnedSubject{{Type: SubjectSelf, Target: SubjectSelf}})
}

func (m *Manager) pinnedMemories(ctx context.Context, subjects []pinnedSubject) ([]KnowledgeRecord, error) {
	if len(subjects) == 0 {
		return nil, nil
	}
	q := m.db.WithContext(ctx).Where("pinned=true AND status='active'")
	condition := m.db.Where("1=0")
	for _, subject := range subjects {
		condition = condition.Or("(subject_type=? AND subject_target=?)", subject.Type, subject.Target)
	}
	var rows []KnowledgeRecord
	if err := q.Where(condition).Order("updated_at DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}
