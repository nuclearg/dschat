//go:build linux

package desktop

import "os/exec"

func OpenBrowser(url string) { _ = exec.Command("xdg-open", url).Start() }
