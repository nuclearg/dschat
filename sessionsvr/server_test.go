package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func rpc(t *testing.T, ts *httptest.Server, lang, token, action string, body any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/"+action, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if lang != "" {
		req.Header.Set("X-Language", lang)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s status = %d", action, resp.StatusCode)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRPCLoginAndSession(t *testing.T) {
	srv, err := New(Config{Data: t.TempDir(), Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	meta := rpc(t, ts, "", "", "meta.get", map[string]any{})
	if meta["success"] != true || meta["data"].(map[string]any)["protocol"].(float64) != ProtocolVersion {
		t.Fatalf("meta = %+v", meta)
	}
	wrong := rpc(t, ts, "zh-CN", "", "auth.login", map[string]string{"password": "bad"})
	if wrong["success"] != false || wrong["errCode"] != "InvalidPassword" || wrong["error"] != "密码错误" {
		t.Fatalf("wrong login = %+v", wrong)
	}
	login := rpc(t, ts, "en-US", "", "auth.login", map[string]string{"password": "pw", "deviceId": "d1"})
	if login["success"] != true {
		t.Fatalf("login = %+v", login)
	}
	access := login["data"].(map[string]any)["accessToken"].(string)
	created := rpc(t, ts, "", access, "sessions.create", map[string]string{"id": "s1", "title": "t"})
	if created["success"] != true {
		t.Fatalf("create = %+v", created)
	}
	appended := rpc(t, ts, "", access, "sessions.turn.append", map[string]any{
		"sessionId": "s1",
		"turn":      map[string]any{"id": "t1", "parentTurnId": "0", "q": map[string]any{"text": "hi"}},
	})
	if appended["success"] != true {
		t.Fatalf("append = %+v", appended)
	}
	got := rpc(t, ts, "", access, "sessions.get", map[string]string{"sessionId": "s1"})
	turns := got["data"].(map[string]any)["turns"].([]any)
	if len(turns) != 1 {
		t.Fatalf("turns = %+v", turns)
	}
}
