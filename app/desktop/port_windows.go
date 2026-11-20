//go:build windows

package desktop

import (
	"os/exec"
	"strconv"
	"strings"
)

// findListener 解析 netstat 的 LISTENING 行，取出占用该 IPv4 端口的 PID。
func findListener(port int) occupant {
	out, err := exec.Command("netstat", "-ano", "-p", "tcp").Output()
	if err != nil {
		return occupant{}
	}
	suffix := ":" + strconv.Itoa(port)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		// 依次是 协议、本地地址、外部地址、状态、PID
		if !strings.EqualFold(fields[3], "LISTENING") {
			continue
		}
		if !strings.HasSuffix(fields[1], suffix) {
			continue
		}
		if pid, err := strconv.Atoi(fields[4]); err == nil && pid > 0 {
			return occupant{PID: pid}
		}
	}
	return occupant{}
}

// processPath 在 Windows 上需要额外权限，缺省留空，用进程名说明即可。
func processPath(int) string { return "" }

// platformProcessName 用 tasklist 取映像名（如 dschat.exe）。
func platformProcessName(pid int) string {
	if pid <= 0 {
		return ""
	}
	out, err := exec.Command(
		"tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/FO", "CSV", "/NH",
	).Output()
	if err != nil {
		return ""
	}
	// CSV 首列即映像名："dschat.exe","1234",...
	line := strings.TrimSpace(string(out))
	if !strings.HasPrefix(line, `"`) {
		return ""
	}
	rest := line[1:]
	if end := strings.Index(rest, `"`); end >= 0 {
		return rest[:end]
	}
	return ""
}
