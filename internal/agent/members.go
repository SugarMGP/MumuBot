package agent

import (
	"context"
	"time"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/tools"

	"github.com/jellydator/ttlcache/v3"
)

func (a *Agent) memberRoster(ctx context.Context, groupID int64) (*tools.MemberRoster, error) {
	allowed, err := a.memory.ConversationAllowed(ctx, memory.ConversationKindGroup, groupID)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, memory.ErrConversationUnavailable
	}
	if cached := a.memberRosters.Get(groupID); cached != nil {
		return cached.Value(), nil
	}
	fetchedAt := time.Now().UTC()
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	rows, err := a.bot.GetGroupMembers(callCtx, groupID)
	if err != nil {
		return nil, err
	}
	profiles := make([]memory.MemberProfile, 0, len(rows))
	names := make([]memory.MemberName, 0, len(rows))
	roster := &tools.MemberRoster{GroupID: groupID, FetchedAt: fetchedAt, MemberIDs: make([]int64, 0, len(rows))}
	for _, row := range rows {
		id := int64(row.UserID)
		roster.MemberIDs = append(roster.MemberIDs, id)
		profiles = append(profiles, memory.MemberProfile{UserID: id, Nickname: row.Nickname, UpdatedAt: fetchedAt})
		if row.Card != "" {
			names = append(names, memory.MemberName{UserID: id, GroupID: groupID, Value: row.Card, UpdatedAt: fetchedAt})
		}
	}
	if err := a.memory.SyncMemberProfiles(ctx, profiles, names); err != nil {
		return nil, err
	}
	a.memberRosters.Set(groupID, roster, ttlcache.DefaultTTL)
	return roster, nil
}
