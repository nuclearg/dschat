# Mobile binding (Android + iOS)

This package is the `gomobile bind` façade over `app/core`. Both Android and
iOS use it:

- Android: AAR, started from a foreground service; the WebView loads the
  loopback URL returned by the façade.
- iOS: framework, started from the app delegate; WKWebView loads the same URL.

Nothing platform-specific belongs here — no tray, no browser launching, no
desktop paths. Keep the exported API to basic types (string/bool/[]byte) and
callbacks so `gomobile` can generate bindings.

Not implemented yet.
