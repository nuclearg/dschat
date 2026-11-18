# iOS shell

Swift + Xcode project. WKWebView host and app lifecycle. Depends on the
framework produced from `app/mobile` by `gomobile bind -target=ios`.

Planned, not implemented. iOS constraints already reflected in `app/core`:
the process can be suspended at any time, so the engine never assumes a
long-running daemon and closes orphaned open turns on next start.
