package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mumu-bot/internal/config"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"
	"mumu-bot/internal/testutil"
	"mumu-bot/internal/tools"

	"github.com/bytedance/sonic"
	"github.com/coder/websocket"
)

func TestRosterRefreshCacheAndSync(t *testing.T) {
	db, mgr := testutil.Database(t)
	if err := db.Create(&memory.ConversationTarget{ConversationKind: "group", TargetID: 88, Active: true}).Error; err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	var fail atomic.Bool
	var mu sync.Mutex
	rows := []onebot.GroupMember{{UserID: 9007199254740993, Nickname: "未发言", Card: "名片"}, {UserID: 123, Nickname: "无名片"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		for {
			_, raw, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			var request struct {
				Action string          `json:"action"`
				Echo   json.RawMessage `json:"echo"`
			}
			if sonic.Unmarshal(raw, &request) != nil {
				return
			}
			var data any = map[string]any{}
			retcode := 0
			status := "ok"
			switch request.Action {
			case "get_version_info":
				data = map[string]any{"app_name": "NapCat.OneBot", "app_version": "4.0", "protocol_version": "v11"}
			case "get_login_info":
				data = map[string]any{"user_id": 10000, "nickname": "mock"}
			case "get_group_member_list":
				requests.Add(1)
				mu.Lock()
				data = append([]onebot.GroupMember{}, rows...)
				mu.Unlock()
				if fail.Load() {
					retcode = 1
					status = "failed"
				}
			}
			response := struct {
				Status  string          `json:"status"`
				Retcode int             `json:"retcode"`
				Data    any             `json:"data"`
				Echo    json.RawMessage `json:"echo"`
			}{status, retcode, data, request.Echo}
			encoded, _ := sonic.Marshal(response)
			if conn.Write(r.Context(), websocket.MessageText, encoded) != nil {
				return
			}
		}
	}))
	defer server.Close()
	bot := onebot.NewClient(config.Bootstrap{OneBotWSURL: "ws" + strings.TrimPrefix(server.URL, "http")})
	defer bot.Close()
	bot.Connect()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := bot.WaitSelfID(ctx); err != nil {
		t.Fatal(err)
	}
	a := &Agent{memory: mgr, bot: bot, memberRosters: newAgentTTLCache[int64, *tools.MemberRoster](128, 10*time.Minute)}
	first, err := a.memberRoster(ctx, 88)
	if err != nil || len(first.MemberIDs) != 2 || first.MemberIDs[0] != 9007199254740993 {
		t.Fatalf("roster %+v %v", first, err)
	}
	deadline := a.memberRosters.Get(88).ExpiresAt()
	if _, err := a.memberRoster(ctx, 88); err != nil || requests.Load() != 1 {
		t.Fatalf("cache repeated fetch: %d %v", requests.Load(), err)
	}
	if !deadline.Equal(a.memberRosters.Get(88).ExpiresAt()) {
		t.Fatal("cache hit extended expiration")
	}
	profile, err := mgr.GetMemberProfile(9007199254740993)
	if err != nil || profile.Nickname != "未发言" {
		t.Fatalf("not synced: %+v %v", profile, err)
	}
	mu.Lock()
	rows = nil
	mu.Unlock()
	a.memberRosters.Delete(88)
	empty, err := a.memberRoster(ctx, 88)
	if err != nil || len(empty.MemberIDs) != 0 || requests.Load() != 2 {
		t.Fatalf("empty: %+v %v", empty, err)
	}
	if _, err := mgr.GetMemberProfile(9007199254740993); err != nil {
		t.Fatal("empty roster erased known person")
	}
	fail.Store(true)
	a.memberRosters.Delete(88)
	if _, err := a.memberRoster(ctx, 88); err == nil || a.memberRosters.Get(88) != nil {
		t.Fatal("failed roster published to cache")
	}
	fail.Store(false)
	mu.Lock()
	rows = []onebot.GroupMember{{UserID: 456, Nickname: "未提交", Card: "同步失败"}}
	mu.Unlock()
	if err := db.Migrator().RenameTable("member_names", "member_names_unavailable"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.memberRoster(ctx, 88); err == nil || a.memberRosters.Get(88) != nil {
		t.Fatal("database failure published roster to cache")
	}
	if _, err := mgr.GetMemberProfile(456); err == nil {
		t.Fatal("failed roster transaction partially committed")
	}
}
