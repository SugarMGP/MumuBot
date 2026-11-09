package agent

import (
	"context"
	"testing"
	"time"

	"mumu-bot/internal/config"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/testutil"
	"mumu-bot/internal/tools"
)

func TestLifeUsageHintOncePerThink(t *testing.T) {
	db, mgr := testutil.Database(t)
	if err := mgr.EnsureLife(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO model_call_hourly(bucket_start,task,model,request_count) VALUES(?,?,?,?)`, time.Now().UTC().Truncate(time.Hour), "life", "mock", 80).Error; err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultRuntime()
	cfg.Agent.LifeDailyRequestLimit = 100
	state := &runState{config: &cfg}
	session := &Session{kind: "life", targetID: 1, memory: mgr, media: newMediaIndex()}
	ctx := context.WithValue(context.Background(), runStateKey{}, state)
	ctx = tools.WithToolContext(ctx, &tools.ToolContext{MemoryMgr: mgr, Session: session, ConversationKind: "life"})
	message, err := injectLifeUsageHint(ctx, session)
	if err != nil || message == nil {
		t.Fatalf("first hint %v %v", message, err)
	}
	if message, err := injectLifeUsageHint(ctx, session); err != nil || message != nil {
		t.Fatalf("duplicate hint %v %v", message, err)
	}
	if session.HasActionablePending() {
		t.Fatal("hint created a wake input")
	}
	var count int64
	if err := db.Model(&memory.TranscriptEntry{}).Where("entry_kind='system_note'").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("hint transcript %d %v", count, err)
	}
}
