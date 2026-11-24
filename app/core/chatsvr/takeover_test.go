package chatsvr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dschat/app/core/model"
)

// Sending into a read-only session should claim it automatically when there
// is no open turn or unsynced local content.
func TestSendAutoClaimsReadOnlySessionWithoutConflict(t *testing.T) {
	app, err := New(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	claimed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/sessions.claim":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["force"] != false {
				t.Errorf("auto claim used force=%v, want false", body["force"])
			}
			claimed = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{"session": map[string]any{
					"id": "s1", "title": "t", "ownerDeviceId": app.deviceID, "createdAt": 1, "updatedAt": 1,
				}},
			})
		case "/v1/sessions.get":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{
					"session": map[string]any{
						"id": "s1", "title": "t", "ownerDeviceId": app.deviceID, "createdAt": 1, "updatedAt": 1,
					},
					"turns": []any{},
				},
			})
		case "/v1/sessions.turn.append":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	app.setSync(NewSync(server.URL, "access", "", time.Now().Add(time.Hour).UnixMilli(), app.deviceID, "zh-CN"))

	sess := model.Session{ID: "s1", Title: "t", OwnerID: "other-device", SyncState: SyncSynced, CreatedAt: 1, UpdatedAt: 1}
	if err := app.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Send("s1", SendRequest{Text: "continue"}); err != nil {
		t.Fatalf("Send() = %v, want auto-claim success", err)
	}
	if !claimed {
		t.Fatal("auto claim did not call sessions.claim")
	}
	got, err := app.store.Session("s1")
	if err != nil {
		t.Fatal(err)
	}
	if got.OwnerID != app.deviceID {
		t.Fatalf("owner = %q, want local device", got.OwnerID)
	}
}
