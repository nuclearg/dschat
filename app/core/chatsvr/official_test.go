package chatsvr

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 官方模式下用户只输 11 位手机号，云端按 E.164 存。
// 这个转换只发生一次，放在客户端边界上；错了就是"手机号格式不合法"，且很难从 UI 看出来。
func TestNormalizePhone(t *testing.T) {
	cases := map[string]string{
		"13800138000":    "+8613800138000",
		" 13800138000 ":  "+8613800138000",
		"+8613800138000": "+8613800138000",
		"+85212345678":   "+85212345678",
		"12345":          "12345",
		"":               "",
	}
	for in, want := range cases {
		if got := normalizePhone(in); got != want {
			t.Errorf("normalizePhone(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoginOfficialParsesTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth.login" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["phone"] != "+8613800138000" || body["password"] != "passw0rd!" {
			t.Errorf("payload = %v", body)
		}
		writeResult(w, map[string]any{
			"success": true,
			"data": map[string]any{
				"accessToken": "a", "refreshToken": "r", "expiresIn": 7200,
				"deviceId": "dev-1", "serverId": "srv-1",
			},
		})
	}))
	defer server.Close()

	res, err := LoginOfficial(context.Background(), server.URL, "+8613800138000", "passw0rd!",
		"mac", "darwin", "dev-1", "zh-CN")
	if err != nil {
		t.Fatalf("LoginOfficial: %v", err)
	}
	if res.AccessToken != "a" || res.RefreshToken != "r" || res.ServerID != "srv-1" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if res.AccessExpiresAt == 0 {
		t.Fatal("AccessExpiresAt should be derived from expiresIn")
	}
}

// 云端返回的业务错误必须带 errCode 透上来：前端靠它决定提示什么。
func TestLoginOfficialPropagatesErrCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeResult(w, map[string]any{
			"success": false, "error": "手机号或密码错误", "errCode": "InvalidPassword",
		})
	}))
	defer server.Close()

	_, err := LoginOfficial(context.Background(), server.URL, "+8613800138000", "bad",
		"mac", "darwin", "dev-1", "zh-CN")
	if err == nil {
		t.Fatal("expected error")
	}
	remote, ok := err.(*RemoteError)
	if !ok {
		t.Fatalf("error type = %T", err)
	}
	if remote.Code != "InvalidPassword" || remote.Message != "手机号或密码错误" {
		t.Fatalf("remote = %+v", remote)
	}
}
