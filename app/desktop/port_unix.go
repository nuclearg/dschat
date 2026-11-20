//go:build darwin || linux

package desktop

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// findListener 用 lsof 找出监听该端口的进程。
// 同一用户的进程总能列出；权限不足时返回零值，由健康探测兜底。
func findListener(port int) occupant {
	out, err := exec.Command(
		"lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-t",
	).Output()
	if err != nil {
		return occupant{}
	}
	for _, line := range strings.Split(string(out), "\n") {
		if pid, err := strconv.Atoi(strings.TrimSpace(line)); err == nil && pid > 0 {
			return occupant{PID: pid}
		}
	}
	return occupant{}
}

// processPath 用 ps 取可执行文件路径；macOS 的 comm 即完整路径。
func processPath(pid int) string {
	if pid <= 0 {
		return ""
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// platformProcessName 是 processPath 失败时的兜底：只用进程名。
func platformProcessName(pid int) string {
	if path := processPath(pid); path != "" {
		return filepath.Base(path)
	}
	return ""
}
