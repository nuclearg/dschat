//go:build windows

package desktop

import "os/exec"

func OpenBrowser(url string) {
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

// OpenBrowserNewWindow uses the Windows shell's new-window hint.
func OpenBrowserNewWindow(url string) { _ = exec.Command("cmd", "/c", "start", "", url).Start() }
