package services

import (
	"context"

	"mumu-bot/internal/memory"
)

func (s *AdminService) ListConversationTargets(ctx context.Context, kind string) ([]memory.ConversationTarget, error) {
	return s.memory.ListConversationTargets(ctx, kind, false)
}

func (s *AdminService) SetConversationsBlocked(ctx context.Context, kind string, targetIDs []int64, blocked bool) error {
	return s.memory.SetConversationsBlocked(ctx, kind, targetIDs, blocked)
}

func (s *AdminService) SetConversationExtraPrompt(ctx context.Context, targetID int64, prompt string) error {
	return s.memory.SetConversationExtraPrompt(ctx, targetID, prompt)
}
