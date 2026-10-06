package services

import (
	"context"

	"mumu-bot/internal/memory"
)

func (s *AdminService) ListConversationTargets(ctx context.Context, kind string) ([]memory.ConversationTarget, error) {
	return s.memory.ListConversationTargets(ctx, kind, false)
}

func (s *AdminService) SetConversationBlocked(ctx context.Context, kind string, targetID int64, blocked bool) error {
	return s.memory.SetConversationBlocked(ctx, kind, targetID, blocked)
}

func (s *AdminService) SetConversationExtraPrompt(ctx context.Context, targetID int64, prompt string) error {
	return s.memory.SetConversationExtraPrompt(ctx, targetID, prompt)
}

func (s *AdminService) ListFriendRequests(ctx context.Context, status string) ([]memory.FriendRequest, error) {
	var rows []memory.FriendRequest
	q := s.db.WithContext(ctx).Order("CASE WHEN status = 'pending' THEN 0 ELSE 1 END, received_at DESC")
	if status != "" {
		q = q.Where("status=?", status)
	}
	return rows, q.Find(&rows).Error
}
