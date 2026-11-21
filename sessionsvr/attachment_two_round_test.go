package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Self-hosted prepareUpload returns a relative URL to its own direct POST;
// prepareDownload returns a relative POST URL carrying attachmentId. The
// client sees the same URL-first shape as the official OSS server.
func TestRPCAttachmentTwoRoundSelfHosted(t *testing.T) {
	srv, err := New(Config{Data: t.TempDir(), Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	login := rpc(t, ts, "", "", "auth.login", map[string]string{"password": "pw", "deviceId": "d1"})
	access := login["data"].(map[string]any)["accessToken"].(string)
	_ = rpc(t, ts, "", access, "sessions.create", map[string]string{"id": "s1", "title": "t"})

	prep := rpc(t, ts, "", access, "attachments.getUploadUrl", map[string]any{
		"id": "a1", "sessionId": "s1", "name": "a.txt", "mime": "text/plain", "size": 4,
	})
	if prep["success"] != true {
		t.Fatalf("prepare upload = %+v", prep)
	}
	data := prep["data"].(map[string]any)
	if data["url"] != "/v1/attachments.put" || data["multipart"] != true {
		t.Fatalf("prepare data = %+v", data)
	}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("id", "a1")
	_ = w.WriteField("sessionId", "s1")
	_ = w.WriteField("name", "a.txt")
	_ = w.WriteField("mime", "text/plain")
	part, _ := w.CreateFormFile("file", "a.txt")
	_, _ = io.WriteString(part, "data")
	_ = w.Close()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/attachments.put", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("direct put status = %d", resp.StatusCode)
	}

	finish := rpc(t, ts, "", access, "attachments.finishUpload", map[string]string{"id": "a1"})
	if finish["success"] != true {
		t.Fatalf("finish = %+v", finish)
	}

	prepGet := rpc(t, ts, "", access, "attachments.getDownloadUrl", map[string]string{"attachmentId": "a1"})
	if prepGet["success"] != true {
		t.Fatalf("prepare download = %+v", prepGet)
	}
	gd := prepGet["data"].(map[string]any)
	if gd["url"] != "/v1/attachments.get" || gd["body"].(map[string]any)["attachmentId"] != "a1" {
		t.Fatalf("prepare download data = %+v", gd)
	}
	raw, _ := json.Marshal(map[string]string{"attachmentId": "a1"})
	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/v1/attachments.get", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if string(got) != "data" {
		t.Fatalf("download = %q", got)
	}
}
