package chatsvr

import (
	"context"
	"net"
	"testing"
)

func TestStartFailsWhenFixedPortIsBusy(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port

	app, err := New(Config{DataDir: t.TempDir(), Port: port})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if err := app.Start(context.Background()); err == nil {
		t.Fatalf("Start() succeeded on busy port %d", port)
	}
}
