package agent

import (
	"context"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"mumu-bot/internal/media"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/refstore"
	"mumu-bot/internal/testutil"
	"mumu-bot/internal/tools"

	"github.com/bytedance/sonic"
	"github.com/zjutjh/onebot-sdk/message"
)

func TestForwardFileSourcesAndSnapshots(t *testing.T) {
	_, mgr := testutil.Database(t)
	a := &Agent{memory: mgr}
	ctx := context.Background()
	var downloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { downloads.Add(1); w.Write([]byte("material")) }))
	defer server.Close()
	data := message.FileData{FileID: "file-source", Name: "same-name.txt", URL: server.URL, Size: 8}
	pending := a.prepareForwardFile(ctx, 88, data)
	if pending.CacheHash != "" || downloads.Load() != 0 {
		t.Fatal("known source downloaded eagerly")
	}
	ref, err := a.forwardFileRef(ctx, 88, pending)
	if err != nil || ref.State != refstore.StatePending || ref.Locator.TargetID != 88 {
		t.Fatalf("wrong source: %+v %v", ref, err)
	}
	cached := a.prepareForwardFile(ctx, 0, data)
	if cached.CacheHash == "" || downloads.Load() != 1 {
		t.Fatalf("URL-only not cached: %+v", cached)
	}
	raw, err := sonic.Marshal(cached)
	if err != nil || strings.Contains(string(raw), server.URL) {
		t.Fatal("temporary URL persisted")
	}
	ref, err = a.forwardFileRef(ctx, 0, cached)
	if err != nil || ref.State != refstore.StateReady {
		t.Fatalf("snapshot: %+v %v", ref, err)
	}
	_, body, _, _, _, binary, err := mgr.RefStore().ReadText(ref.FileRef(), 0, 100)
	if err != nil || binary || body != "material" {
		t.Fatalf("wrong snapshot bytes: %s %v", body, err)
	}
	missing := a.prepareForwardFile(ctx, 0, message.FileData{Name: "missing"})
	if _, err := a.forwardFileRef(ctx, 0, missing); err == nil {
		t.Fatal("guessed source for unavailable file")
	}
	server.Close()
	unavailable := a.prepareForwardFile(ctx, 0, data)
	raw, _ = sonic.Marshal(unavailable)
	if unavailable.Unavailable == "" || strings.Contains(string(raw), server.URL) {
		t.Fatalf("URL leaked on download failure: %s", raw)
	}
}

func TestForwardImagesKeepAttachmentTypes(t *testing.T) {
	db, mgr := testutil.Database(t)
	path := filepath.Join(t.TempDir(), "same.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	a := &Agent{memory: mgr}
	log := memory.MessageLog{OneBotMessageID: 10, ConversationKind: "group", TargetID: 88, UserID: 9}
	if err := db.Create(&log).Error; err != nil {
		t.Fatal(err)
	}
	for index, content := range []string{"[图片附件:0][图片附件:1]", "[图片:same][图片:same]"} {
		handle := []string{"f-image-position", "f-image-legacy"}[index]
		node := memory.ForwardNode{Content: content, Images: media.Refs{{Hash: "same", Path: path, Mime: "image/png"}, {Hash: "same", Path: path, Mime: "image/png", Sticker: true}}}
		if err := db.Create(&memory.MessageForward{Handle: handle, MessageLogID: log.ID, Nodes: memory.ForwardNodes{node}}).Error; err != nil {
			t.Fatal(err)
		}
		result, err := a.readForward(context.Background(), handle, 0)
		if err != nil {
			t.Fatal(err)
		}
		body := result.(*tools.ReadForwardOutput).Content
		if strings.Count(body, "[图片:image_") != 1 || strings.Count(body, "[表情包:image_") != 1 {
			t.Fatal(body)
		}
	}
}
