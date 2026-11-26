//go:build darwin

package desktop

import "os/exec"

func OpenBrowser(url string) { _ = exec.Command("open", url).Start() }
