package chatsvr

import (
	"net/http/httptest"
	"testing"
)

func TestAuthRedirectKeepsSettings(t *testing.T) {
	app, err := New(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/auth?t="+app.token+"&settings=1", nil)
	app.handleLogin(rec, req)
	if rec.Code != 302 {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/?settings=1" {
		t.Fatalf("location = %q", got)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/auth?t="+app.token, nil)
	app.handleLogin(rec, req)
	if got := rec.Header().Get("Location"); got != "/" {
		t.Fatalf("plain location = %q", got)
	}
}
