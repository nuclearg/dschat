//go:build darwin

package desktop

import "os/exec"

func OpenBrowser(url string) { _ = exec.Command("open", url).Start() }

// OpenBrowserNewWindow asks macOS to launch a fresh browser instance/window.
func OpenBrowserNewWindow(url string) { _ = exec.Command("open", "-n", url).Start() }
