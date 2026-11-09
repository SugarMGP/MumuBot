package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/testutil"
	"mumu-bot/internal/web/services"

	"github.com/bytedance/sonic"
	"github.com/go-chi/chi/v5"
)

func TestMemoryWriteAPI(t *testing.T) {
	_, mgr := testutil.Database(t)
	ctx := context.Background()
	created, err := mgr.ManageMemories(ctx, []memory.MemoryChange{{Action: "create", SubjectType: "self", MemoryType: "profile", Content: "original"}})
	if err != nil {
		t.Fatal(err)
	}
	a := &App{admin: services.NewAdminService(mgr, t.TempDir())}
	id := created[0].ItemID
	router := chi.NewRouter()
	router.Patch("/memories/{id}", a.handleMemoryUpdate)
	router.Get("/memories/{id}", a.handleMemoryDetail)
	router.Delete("/memories/{id}", a.handleMemoryDelete)
	path := fmt.Sprintf("/memories/%d", id)
	for _, test := range []struct {
		method, body string
		want         int
		code         string
	}{
		{"PATCH", `{"content":"missing version"}`, 400, "validation_failed"},
		{"PATCH", `{"version":1,"content":"   "}`, 400, "validation_failed"},
		{"PATCH", `{"version":1,"status":"archived","pinned":true}`, 400, "validation_failed"},
		{"DELETE", "", 409, "memory_delete_active"},
		{"PATCH", `{"version":1,"content":"corrected","pinned":true}`, 200, ""},
		{"PATCH", `{"version":1,"content":"stale"}`, 409, "version_conflict"},
		{"PATCH", `{"version":2,"status":"archived"}`, 200, ""},
		{"PATCH", `{"version":3,"content":"archived edit"}`, 409, "memory_not_editable"},
		{"DELETE", "", 200, ""},
		{"GET", "", 404, "not_found"},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, path, strings.NewReader(test.body))
		router.ServeHTTP(response, request)
		if response.Code != test.want {
			t.Fatalf("%s %s: %d %s", test.method, test.body, response.Code, response.Body.String())
		}
		var payload struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
			Data struct {
				Version uint64 `json:"version"`
			} `json:"data"`
		}
		if err := sonic.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Error.Code != test.code {
			t.Fatalf("wrong error: %+v", payload)
		}
		if test.method == "PATCH" && test.want == http.StatusOK && payload.Data.Version == 0 {
			t.Fatal("missing version in API response")
		}
	}
}
