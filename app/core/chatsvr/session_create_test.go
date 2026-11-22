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

// A local session can be reattached to a server where its row still exists
// (disconnect/reconnect, or create succeeded before the local synced flag was
// persisted). The second create must reuse the remote session, not surface
// SessionExists to the user on every turn.
func TestEnsureRemoteSessionReusesExistingSession(t *testing.T) {
	creates := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/sessions.create":
			creates++
			if creates == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"success": true,
					"data": map[string]any{"session": map[string]any{
						"id": "s1", "title": "t", "ownerDeviceId": "",
						"createdAt": 1, "updatedAt": 1,
					}},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": false, "errCode": "SessionExists", "error": "会话已存在",
			})
		case "/v1/sessions.get":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{
					"session": map[string]any{
						"id": "s1", "title": "t", "ownerDeviceId": "",
						"createdAt": 1, "updatedAt": 1,
					},
					"turns": []any{},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	app, err := New(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	s := NewSync(server.URL, "access", "", time.Now().Add(time.Hour).UnixMilli(), app.deviceID, "zh-CN")
	app.setSync(s)

	sess := model.Session{ID: "s1", Title: "t", OwnerID: app.deviceID, SyncState: SyncLocal}
	if err := app.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	if err := app.ensureRemoteSession(context.Background(), sess); err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	if err := app.ensureRemoteSession(context.Background(), sess); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if creates != 2 {
		t.Fatalf("creates = %d, want 2", creates)
	}
}
