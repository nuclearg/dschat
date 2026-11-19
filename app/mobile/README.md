# Mobile binding (Android + iOS)

This package is the `gomobile bind` façade over `app/core`. Both Android and
iOS use it:

- Android: AAR, started from a foreground service; the WebView loads the
  loopback URL returned by the façade.
- iOS: framework, started from the app delegate; WKWebView loads the same URL.

Nothing platform-specific belongs here — no tray, no browser launching, no
desktop paths. Keep the exported API to basic types (string/bool/[]byte) and
callbacks so `gomobile` can generate bindings.

## API

Only types `gomobile` can bind are exported, so the generated Android class is
`mobile.Mobile` with static methods:

```go
Start(dataDir string) (string, error) // boots the engine once, returns app.URL()
Stop() error                          // cancels the context, closes the DB; idempotent
Running() bool                        // lifecycle probe for shells/tests
```

`Start` is idempotent while the engine is up: a second call returns the same
URL, which lets the Android service call it on every `onStartCommand` without
special-casing process restarts. The package keeps a mutex around the single
instance, creates the engine with `chatsvr.Config{DataDir: dataDir, Port: 0}`,
and calls `app.Start(ctx)` with a cancellable context. `Stop` cancels that
context and then closes the app (which closes SQLite), and `Stop` on a stopped
engine is a no-op.

Build the AAR with the command in
[`../android/README.md`](../android/README.md). It embeds
`app/core/web/dist`, so rebind after any web build.
