//go:build linux

package desktop

import "os/exec"

func OpenBrowser(url string) { _ = exec.Command("xdg-open", url).Start() }

// OpenBrowserNewWindow has no portable Linux new-window request; use the default opener.
func OpenBrowserNewWindow(url string) { OpenBrowser(url) }
