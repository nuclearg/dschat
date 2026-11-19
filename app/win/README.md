# Windows shell

`main.go` + the shared `app/desktop` tray. Built with systray (cgo) and
mingw-w64:

```sh
make app-windows
# dist/dschat-windows-amd64.exe
```

Default mode starts the engine and shows a notification-area icon with
打开 / 配置 / 退出. `-serve` runs headless, `-no-open` skips the browser,
`-port` / `-data` override the defaults. The executable is unsigned; expect
SmartScreen on first run.
