package chatsvr

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Every chatsvr -> sessionsvr call is a POST. A canonical-host / HTTPS
// redirect in front of sessionsvr must not turn it into a GET, and 307/308
// must replay the JSON body. Table covers all standard redirect statuses Go
// recognises.
func TestSyncHTTPClient_followsRedirectsWithPostAndBody(t *testing.T) {
	statuses := []int{
		http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusSeeOther,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect,
	}

	for _, status := range statuses {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var gotMethod, gotBody, gotAuth string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/start" {
					w.Header().Set("Location", "/final")
					w.WriteHeader(status)
					return
				}
				gotMethod = r.Method
				gotAuth = r.Header.Get("Authorization")
				body, _ := io.ReadAll(r.Body)
				gotBody = string(body)
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			req, err := http.NewRequest(http.MethodPost, server.URL+"/start", bytes.NewReader([]byte(`{"x":1}`)))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer token")

			resp, err := newSyncHTTPClient(5 * time.Second).Do(req)
			if err != nil {
				t.Fatalf("status %d: %v", status, err)
			}
			resp.Body.Close()

			if gotMethod != http.MethodPost {
				t.Fatalf("status %d: final method = %s, want POST", status, gotMethod)
			}
			if gotBody != `{"x":1}` {
				t.Fatalf("status %d: final body = %q", status, gotBody)
			}
			if gotAuth != "Bearer token" {
				t.Fatalf("status %d: final Authorization = %q", status, gotAuth)
			}
		})
	}
}
