package chatsvr

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dschat/app/core/model"
)

// The two-round attachment protocol must not depend on whether the server is
// official (presigned OSS) or self-hosted (relative URL back to the server).
// Both URL responses are ordinary success results; the client then sends bytes
// directly and finishes the metadata write.
func TestSyncAttachment_twoRound(t *testing.T) {
	var uploaded, finished, downloaded bool
	var putBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/attachments.getUploadUrl":
			writeResult(w, map[string]any{
				"success": true,
				"data": map[string]any{
					"url": "/direct/upload", "method": http.MethodPut,
					"headers":   map[string]string{"Content-Type": "text/plain"},
					"objectKey": "data/a/s/a.txt", "multipart": false,
				},
			})
		case "/direct/upload":
			b, _ := io.ReadAll(r.Body)
			putBody = string(b)
			uploaded = r.Method == http.MethodPut
			w.WriteHeader(http.StatusOK)
		case "/v1/attachments.finishUpload":
			finished = true
			writeResult(w, map[string]any{"success": true, "data": map[string]any{"attachment": map[string]any{"id": "att-1"}}})
		case "/v1/attachments.getDownloadUrl":
			writeResult(w, map[string]any{
				"success": true,
				"data": map[string]any{
					"url": "/direct/download", "method": http.MethodGet,
					"name": "a.txt", "mime": "text/plain",
				},
			})
		case "/direct/download":
			downloaded = true
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("downloaded"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	s := NewSync(server.URL, "token", "refresh", 0, "dev", "zh-CN")
	a := model.Attachment{ID: "att-1", Name: "a.txt", MIME: "text/plain", Size: 4}
	if err := s.PutAttachment(context.Background(), "s1", a, strings.NewReader("data"), 4); err != nil {
		t.Fatal(err)
	}
	if !uploaded || !finished || putBody != "data" {
		t.Fatalf("uploaded=%v finished=%v body=%q", uploaded, finished, putBody)
	}
	body, name, mime, err := s.GetAttachment(context.Background(), "att-1")
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	data, _ := io.ReadAll(body)
	if !downloaded || string(data) != "downloaded" || name != "a.txt" || mime != "text/plain" {
		t.Fatalf("downloaded=%v body=%q name=%q mime=%q", downloaded, data, name, mime)
	}
}
