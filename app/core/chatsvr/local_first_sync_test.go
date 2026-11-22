package chatsvr

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A failed remote append must not roll back the local turn. The user can
// continue locally while the sync badge reports the server problem.
func TestSendKeepsLocalTurnWhenRemoteAppendFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/sessions.create":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{"session": map[string]any{
					"id": "s1", "title": "t", "ownerDeviceId": "", "createdAt": 1, "updatedAt": 1,
				}},
			})
		case "/v1/sessions.turn.append":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": false, "errCode": "RateLimited", "error": "触发限流",
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
	app.setSync(NewSync(server.URL, "access", "", time.Now().Add(time.Hour).UnixMilli(), app.deviceID, "zh-CN"))
	seedSession(t, app, "s1", SyncLocal)

	turn, err := app.Send("s1", SendRequest{Text: "hello"})
	if err != nil {
		t.Fatalf("Send() = %v, want local success", err)
	}
	if turn == nil {
		t.Fatal("Send() returned no turn")
	}
	turns, err := app.store.Turns("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || turns[0].ID != turn.ID {
		t.Fatalf("local turns = %+v, want the new turn", turns)
	}
}
