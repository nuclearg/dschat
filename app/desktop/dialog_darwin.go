//go:build darwin

package desktop

import (
	"fmt"
	"os"
	"os/exec"
)

// showErrorDialog 用 AppleScript 弹系统告警框，阻塞到用户点掉为止。
// 文案通过 argv 传入而不是拼进脚本，避免引号、换行把脚本弄坏。
func showErrorDialog(title, message string) {
	script := []string{
		"on run argv",
		`display alert (item 1 of argv) message (item 2 of argv) as critical buttons {"好"} default button 1`,
		"end run",
	}
	args := []string{"-e", script[0], "-e", script[1], "-e", script[2], title, message}
	if err := exec.Command("osascript", args...).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %s\n", title, message)
	}
}
