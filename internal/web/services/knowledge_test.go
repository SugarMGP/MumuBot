package services

import (
	"context"
	"errors"
	"testing"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/testutil"

	"gorm.io/gorm"
)

func TestMemoryAdminLifecycle(t *testing.T) {
	db, mgr := testutil.Database(t)
	ctx := context.Background()
	created, err := mgr.ManageMemories(ctx, []memory.MemoryChange{{Action: "create", SubjectType: "self", SubjectTarget: "self", MemoryType: "profile", Content: "first"}})
	if err != nil {
		t.Fatal(err)
	}
	id := created[0].ItemID
	admin := NewAdminService(mgr, t.TempDir())
	pinned := true
	if err := admin.UpdateKnowledge(ctx, id, KnowledgeUpdate{Version: 1, Pinned: &pinned}); err != nil {
		t.Fatal(err)
	}
	item, _ := admin.GetKnowledge(id)
	if !item.Pinned || item.ForgetAt != nil || item.Version != 2 {
		t.Fatalf("pin: %+v", item)
	}
	content := "corrected"
	if err := admin.UpdateKnowledge(ctx, id, KnowledgeUpdate{Version: 1, Content: &content}); !errors.Is(err, memory.ErrMemoryVersion) {
		t.Fatalf("stale edit: %v", err)
	}
	if err := admin.UpdateKnowledge(ctx, id, KnowledgeUpdate{Version: 2, Content: &content}); err != nil {
		t.Fatal(err)
	}
	pinned = false
	if err := admin.UpdateKnowledge(ctx, id, KnowledgeUpdate{Version: 3, Pinned: &pinned}); err != nil {
		t.Fatal(err)
	}
	item, _ = admin.GetKnowledge(id)
	if item.Pinned || item.ForgetAt == nil || item.Version != 4 {
		t.Fatalf("unpin: %+v", item)
	}
	if err := admin.DeleteKnowledge(ctx, id); !errors.Is(err, memory.ErrMemoryDeleteActive) {
		t.Fatalf("deleted active: %v", err)
	}
	if err := admin.UpdateKnowledge(ctx, id, KnowledgeUpdate{Version: 4, Status: "archived"}); err != nil {
		t.Fatal(err)
	}
	if err := admin.UpdateKnowledge(ctx, id, KnowledgeUpdate{Version: 5, Content: &content}); !errors.Is(err, memory.ErrMemoryNotEditable) {
		t.Fatalf("edited archived: %v", err)
	}
	detail, err := admin.KnowledgeDetail(ctx, id)
	if err != nil || len(detail.Revisions) != 5 {
		t.Fatalf("revisions %d: %v", len(detail.Revisions), err)
	}
	if err := admin.DeleteKnowledge(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.GetKnowledge(id); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("not deleted: %v", err)
	}
	var revisions int64
	if err := db.Model(&memory.KnowledgeRevision{}).Where("item_id=?", id).Count(&revisions).Error; err != nil || revisions != 0 {
		t.Fatalf("cascade %d: %v", revisions, err)
	}
}
