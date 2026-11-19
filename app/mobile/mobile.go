// Package mobile is the gomobile-bind façade over app/core.
//
// It owns the single in-process chatsvr instance used by the Android (and,
// later, iOS) shells. The exported surface is deliberately tiny and uses only
// types gomobile can bind (string, error), so the generated Java/Kotlin class
// is simply `mobile.Mobile` with static Start/Stop methods.
//
// The host app is responsible for choosing a writable data directory (for
// Android that is the service's filesDir) and for keeping the process alive
// while the WebView is in use — on Android the foreground service does that.
package mobile

import (
	"context"
	"sync"

	"dschat/app/core/chatsvr"
)

var (
	mu     sync.Mutex
	app    *chatsvr.App
	cancel context.CancelFunc
	url    string
)

// Start boots the chatsvr engine once in-process and returns the authenticated
// loopback URL the WebView should load. Calling Start again while the engine is
// running returns the existing URL and does nothing, so a foreground service
// can call it from onCreate without special-casing process restarts.
func Start(dataDir string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	if app != nil {
		return url, nil
	}

	a, err := chatsvr.New(chatsvr.Config{DataDir: dataDir, Port: chatsvr.DefaultPort})
	if err != nil {
		return "", err
	}
	ctx, c := context.WithCancel(context.Background())
	if err := a.Start(ctx); err != nil {
		c()
		_ = a.Close()
		return "", err
	}

	app, cancel, url = a, c, a.URL()
	return url, nil
}

// Stop cancels the engine context and closes the database. It is idempotent and
// safe to call from any thread; after it returns a subsequent Start creates a
// fresh engine. Cancelling before Close lets the HTTP server's shutdown
// goroutine and any in-flight turn contexts unwind first.
func Stop() error {
	mu.Lock()
	defer mu.Unlock()
	if app == nil {
		return nil
	}

	a, c := app, cancel
	app, cancel, url = nil, nil, ""
	if c != nil {
		c()
	}
	return a.Close()
}

// Running reports whether the engine is currently up. It exists mainly so shell
// code and tests can assert lifecycle behaviour; gomobile binds it as a static
// boolean method.
func Running() bool {
	mu.Lock()
	defer mu.Unlock()
	return app != nil
}
