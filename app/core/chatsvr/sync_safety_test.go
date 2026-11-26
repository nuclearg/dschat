package chatsvr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dschat/app/core/model"
)

func TestPullSessionKeepsPendingLocalAnswer(t *testing.T) {
	app, err := New(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v1/sessions.get" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data": map[string]any{
				"session": map[string]any{
					"id": "s1", "title": "remote title", "ownerDeviceId": app.deviceID,
					"createdAt": 1, "updatedAt": 1,
				},
				"turns": []any{map[string]any{
					"id": "t1", "parentTurnId": "0",
					"q":         map[string]any{"text": "q", "attachments": []any{}, "createdAt": 1},
					"createdAt": 1,
					"answer": map[string]any{
						"status": "done", "createdAt": 2,
						"blocks": []any{map[string]any{"type": "text", "text": "remote answer"}},
					},
				}},
			},
		})
	}))
	defer server.Close()
	app.setSync(NewSync(server.URL, "access", "", time.Now().Add(time.Hour).UnixMilli(), app.deviceID, "zh-CN"))

	sess := model.Session{ID: "s1", Title: "local title", OwnerID: app.deviceID, SyncState: SyncSynced, ServerID: "server"}
	if err := app.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	turn := model.Turn{
		ID: "t1", SessionID: "s1", ParentTurnID: "0",
		Q: model.Q{Text: "q", CreatedAt: 1}, CreatedAt: 1,
	}
	localAnswer := model.Answer{Status: model.StatusDone, Blocks: []map[string]any{{"type": "text", "text": "local answer"}}}
	if err := app.store.UpsertTurn(turn); err != nil {
		t.Fatal(err)
	}
	if err := app.store.PutAnswer("t1", localAnswer, SyncPending); err != nil {
		t.Fatal(err)
	}
	_ = app.store.SetKV(pendingTitleKey("s1"), "local title")

	if err := app.pullSession(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
	got, err := app.store.Turn("t1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Answer == nil || got.Answer.Blocks[0]["text"] != "local answer" {
		t.Fatalf("pending local answer was overwritten: %+v", got.Answer)
	}
	saved, _ := app.store.Session("s1")
	if saved.Title != "local title" {
		t.Fatalf("pending local title was overwritten: %q", saved.Title)
	}
}

func TestSyncAllDoesNotDeleteOnEmptyRemote(t *testing.T) {
	app, err := New(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v1/sessions.list" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"sessions": []any{}}})
	}))
	defer server.Close()
	app.setSync(NewSync(server.URL, "access", "", time.Now().Add(time.Hour).UnixMilli(), app.deviceID, "zh-CN"))
	_ = app.store.SetKV("server_id", "server-a")
	if err := app.store.CreateSession(model.Session{
		ID: "s1", Title: "t", OwnerID: app.deviceID, SyncState: SyncSynced, ServerID: "server-a", CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}

	app.syncAll(context.Background())
	if _, err := app.store.Session("s1"); err != nil {
		t.Fatalf("empty remote list deleted local session: %v", err)
	}
}

func TestRenameIsLocalFirst(t *testing.T) {
	app, err := New(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	seedSession(t, app, "s1", SyncSynced)
	if err := app.Rename("s1", "new title"); err != nil {
		t.Fatalf("Rename() = %v", err)
	}
	got, err := app.store.Session("s1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "new title" {
		t.Fatalf("title = %q", got.Title)
	}
}

func TestDeleteKeepsLocalDeleteWhenRemoteFails(t *testing.T) {
	app, err := New(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer server.Close()
	app.setSync(NewSync(server.URL, "access", "", time.Now().Add(time.Hour).UnixMilli(), app.deviceID, "zh-CN"))
	seedSession(t, app, "s1", SyncSynced)

	if err := app.Delete("s1"); err != nil {
		t.Fatalf("Delete() = %v", err)
	}
	if _, err := app.store.Session("s1"); err == nil {
		t.Fatal("local session still exists after delete")
	}
	if pending, _ := app.store.KV(pendingDeleteKey("s1")); pending == "" {
		t.Fatal("remote delete was not added to the outbox")
	}
}

func TestSendDoesNotWaitForSlowRemoteAppend(t *testing.T) {
	app, err := New(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/sessions.create":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{"session": map[string]any{
					"id": "s1", "title": "", "ownerDeviceId": "", "createdAt": 1, "updatedAt": 1,
				}},
			})
		case "/v1/sessions.turn.append":
			time.Sleep(2 * time.Second)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	app.setSync(NewSync(server.URL, "access", "", time.Now().Add(time.Hour).UnixMilli(), app.deviceID, "zh-CN"))
	seedSession(t, app, "s1", SyncLocal)

	start := time.Now()
	turn, err := app.Send("s1", SendRequest{Text: "hello"})
	elapsed := time.Since(start)
	if err != nil || turn == nil {
		t.Fatalf("Send() = %v, %v", turn, err)
	}
	if elapsed > time.Second {
		t.Fatalf("Send() waited %s for remote append", elapsed)
	}
}
