package tools

import (
	"context"
	"fmt"
	"testing"
	"time"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/testutil"
)

func TestSearchMembersGlobalAndRosterFailure(t *testing.T) {
	_, mgr := testutil.Database(t)
	ctx := context.Background()
	if err := mgr.SyncMemberProfiles(ctx, []memory.MemberProfile{{UserID: 1, Nickname: "小岚", UpdatedAt: time.Now()}, {UserID: 2, Nickname: "小岚", UpdatedAt: time.Now()}}, nil); err != nil {
		t.Fatal(err)
	}
	calls := 0
	tc := &ToolContext{ConversationKind: "group", TargetID: 88, MemoryMgr: mgr, MemberRosterCallback: func(ctx context.Context, id int64) (*MemberRoster, error) {
		calls++
		return &MemberRoster{GroupID: id, MemberIDs: []int64{1}}, nil
	}}
	groupCtx := WithToolContext(ctx, tc)
	page, err := searchMembers(groupCtx, &SearchMembersInput{Keyword: "小岚"})
	if err != nil || len(page.Members) != 2 || calls != 1 {
		t.Fatalf("not global: %+v %v", page, err)
	}
	for _, row := range page.Members {
		if row.CurrentGroupMember == nil || *row.CurrentGroupMember != (row.UserID == 1) {
			t.Fatalf("membership %+v", row)
		}
	}
	limited, err := searchMembers(groupCtx, &SearchMembersInput{GroupID: 88})
	if err != nil || len(limited.Members) != 1 || limited.Members[0].UserID != 1 {
		t.Fatalf("group filter %+v %v", limited, err)
	}
	tc.MemberRosterCallback = func(context.Context, int64) (*MemberRoster, error) { return nil, fmt.Errorf("offline") }
	page, err = searchMembers(groupCtx, nil)
	if err != nil || page.Warning == "" || len(page.Members) != 2 {
		t.Fatalf("lost local results %+v %v", page, err)
	}
	if _, err := searchMembers(groupCtx, &SearchMembersInput{GroupID: 88}); err == nil {
		t.Fatal("explicit group silently used historical roster")
	}
	tc.ConversationKind = "private"
	page, err = searchMembers(groupCtx, nil)
	if err != nil || len(page.Members) != 2 || page.Warning != "" {
		t.Fatalf("private tried roster: %+v %v", page, err)
	}
}
