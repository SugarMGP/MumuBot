package memory_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"mumu-bot/internal/media"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/testutil"
)

func TestMembersSearchAndSync(t *testing.T) {
	db, mgr := testutil.Database(t)
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Second)
	profiles := []memory.MemberProfile{{UserID: 10001, Nickname: "小岚", UpdatedAt: now}, {UserID: 10002, Nickname: "小岚", UpdatedAt: now}, {UserID: 9007199254740993, Nickname: "未发言的人", UpdatedAt: now}}
	names := []memory.MemberName{{UserID: 10001, GroupID: 88, Value: "阿岚", UpdatedAt: now}}
	if err := mgr.SyncMemberProfiles(ctx, profiles, names); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SyncMemberProfiles(ctx, []memory.MemberProfile{{UserID: 10001, UpdatedAt: now.Add(time.Millisecond)}}, nil); err != nil {
		t.Fatal(err)
	}
	profile, _ := mgr.GetMemberProfile(10001)
	if profile.Nickname != "小岚" {
		t.Fatal("empty nickname overwrote known name")
	}
	page, err := mgr.SearchMembers(ctx, memory.MemberSearchQuery{Keyword: "小岚", Limit: 1})
	if err != nil || len(page.Members) != 1 || !page.HasMore {
		t.Fatalf("page: %+v %v", page, err)
	}
	for i := range profiles {
		profiles[i].UpdatedAt = time.Now().UTC()
	}
	if err := mgr.SyncMemberProfiles(ctx, profiles, names); err != nil {
		t.Fatal(err)
	}
	next, err := mgr.SearchMembers(ctx, memory.MemberSearchQuery{Keyword: "小岚", Limit: 1, Cursor: page.NextCursor})
	if err != nil || len(next.Members) != 1 || next.Members[0].UserID == page.Members[0].UserID {
		t.Fatalf("cursor: %+v %v", next, err)
	}
	alias, err := mgr.SearchMembers(ctx, memory.MemberSearchQuery{Keyword: "阿岚"})
	if err != nil || len(alias.Members) != 1 || len(alias.Members[0].Names) != 1 {
		t.Fatalf("alias: %+v %v", alias, err)
	}
	large, err := mgr.SearchMembers(ctx, memory.MemberSearchQuery{Keyword: "9007199254740993"})
	if err != nil || len(large.Members) != 1 || large.Members[0].UserID != 9007199254740993 {
		t.Fatalf("id precision: %+v %v", large, err)
	}
	empty, err := mgr.SearchMembers(ctx, memory.MemberSearchQuery{GroupID: 88, MemberIDs: []int64{}})
	if err != nil || len(empty.Members) != 0 {
		t.Fatalf("empty roster leaked global people: %+v %v", empty, err)
	}
	var logs, memories int64
	db.Model(&memory.MessageLog{}).Count(&logs)
	db.Model(&memory.KnowledgeRecord{}).Count(&memories)
	if logs != 0 || memories != 0 {
		t.Fatal("roster created messages or memories")
	}
}

func TestForwardOriginalRetention(t *testing.T) {
	db, mgr := testutil.Database(t)
	ctx := context.Background()
	now := time.Now()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("original material")) }))
	defer server.Close()
	hash, _, err := media.CacheForwardFile(ctx, mgr.MediaDir(), server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	path, _ := media.ForwardFilePath(mgr.MediaDir(), hash)
	old := now.Add(-400 * 24 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	logs := []memory.MessageLog{{OneBotMessageID: 1, ConversationKind: "group", TargetID: 88, UserID: 9, MessageTime: old}, {OneBotMessageID: 2, ConversationKind: "group", TargetID: 99, UserID: 9, MessageTime: now}}
	if err := db.Create(&logs).Error; err != nil {
		t.Fatal(err)
	}
	for i, log := range logs {
		forward := memory.MessageForward{Handle: []string{"f-test-one", "f-test-two"}[i], MessageLogID: log.ID, Nodes: memory.ForwardNodes{{Content: "file", Files: []memory.ForwardFile{{CacheHash: hash}}}}}
		if err := db.Create(&forward).Error; err != nil {
			t.Fatal(err)
		}
	}
	if removed, err := mgr.CleanupOrphanForwardFiles(ctx, now); err != nil || removed != 0 {
		t.Fatalf("removed referenced original: %d %v", removed, err)
	}
	if candidates, count, err := mgr.CleanupExpiredMessages(ctx, now, 10); err != nil || count != 1 {
		t.Fatalf("cleanup: %d %v", count, err)
	} else {
		if n, err := mgr.ReleaseForwardFiles(ctx, candidates.ForwardFiles); err != nil || n != 0 {
			t.Fatalf("shared original released: %d %v", n, err)
		}
	}
	if err := db.Model(&logs[1]).Update("message_time", old).Error; err != nil {
		t.Fatal(err)
	}
	candidates, count, err := mgr.CleanupExpiredMessages(ctx, now, 10)
	if err != nil || count != 1 {
		t.Fatalf("last cleanup: %d %v", count, err)
	}
	if n, err := mgr.ReleaseForwardFiles(ctx, candidates.ForwardFiles); err != nil || n != 1 {
		t.Fatalf("last original: %d %v", n, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("unreferenced original retained")
	}
}
