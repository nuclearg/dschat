package chatsvr

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func writeResult(w http.ResponseWriter, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func TestLoginAndListRPC(t *testing.T) {
	const access = "access-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		switch r.URL.Path {
		case "/v1/auth.login":
			if r.Header.Get("X-Language") != "zh-CN" {
				t.Errorf("language = %q", r.Header.Get("X-Language"))
			}
			if r.Header.Get("X-Trace-Id") == "" {
				t.Error("auth.login missing X-Trace-Id")
			}
			writeResult(w, map[string]any{
				"success": true,
				"data": map[string]any{
					"accessToken": access, "refreshToken": "refresh", "expiresIn": 900,
					"deviceId": "device", "serverId": "server",
				},
			})
		case "/v1/sessions.list":
			if got := r.Header.Get("Authorization"); got != "Bearer "+access {
				t.Errorf("authorization = %q", got)
			}
			if r.Header.Get("X-Trace-Id") == "" {
				t.Error("sessions.list missing X-Trace-Id")
			}
			writeResult(w, map[string]any{
				"success": true,
				"data": map[string]any{"sessions": []map[string]any{{
					"id": "s1", "title": "hello", "ownerDeviceId": "device",
					"createdAt": 1, "updatedAt": 2,
				}}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	res, err := Login(t.Context(), server.URL, "pw", "name", "test", "device", "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if res.AccessToken != access || res.ServerID != "server" || res.DeviceID != "device" {
		t.Fatalf("login = %+v", res)
	}
	s := NewSync(server.URL, res.AccessToken, res.RefreshToken, res.AccessExpiresAt, res.DeviceID, "zh-CN")
	list, err := s.ListSessions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "s1" || list[0].Title != "hello" {
		t.Fatalf("list = %+v", list)
	}
}

func TestLoginFailureCarriesResultData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeResult(w, map[string]any{
			"success": false,
			"error":   "密码错误",
			"errCode": "InvalidPassword",
			"data":    map[string]any{"reason": "bad-password"},
		})
	}))
	defer server.Close()

	_, err := Login(t.Context(), server.URL, "bad", "name", "test", "device", "zh-CN")
	var remote *RemoteError
	if !errors.As(err, &remote) {
		t.Fatalf("error = %v", err)
	}
	if remote.Code != "InvalidPassword" || remote.Message != "密码错误" {
		t.Fatalf("remote = %+v", remote)
	}
	if remote.Data["reason"] != "bad-password" {
		t.Fatalf("data = %+v", remote.Data)
	}
}

func TestSyncExpiredAccessDoesNotHang(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeResult(w, map[string]any{
			"success": false,
			"error":   "expired",
			"errCode": "TokenExpired",
		})
	}))
	defer server.Close()

	s := NewSync(server.URL, "old", "", time.Now().Add(-time.Minute).UnixMilli(), "device", "en-US")
	_, err := s.ListSessions(t.Context())
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Code != "TokenExpired" {
		t.Fatalf("error = %v", err)
	}
}
