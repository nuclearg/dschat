//go:build darwin

package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installFakeOsascript 用一个记录 argv 的假脚本替换 osascript，
// 这样弹出窗口的行为（需要 GUI 会话）不影响对「传了什么」的验证。
func installFakeOsascript(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dump := filepath.Join(dir, "argv.bin")
	script := "#!/bin/sh\nprintf '%s\\0' \"$@\" > \"$DSCHAT_DIALOG_DUMP\"\n"
	path := filepath.Join(dir, "osascript")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DSCHAT_DIALOG_DUMP", dump)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	return dump
}

func readArgv(t *testing.T, dump string) []string {
	t.Helper()
	raw, err := os.ReadFile(dump)
	if err != nil {
		t.Fatalf("fake osascript was not called: %v", err)
	}
	parts := strings.Split(string(raw), "\x00")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func TestShowErrorDialogPassesTextAsArguments(t *testing.T) {
	dump := installFakeOsascript(t)

	// 故意带上引号、反斜杠与换行，确认不会被拼进脚本而破坏 AppleScript。
	title := "dschat 无法启动"
	message := "端口 5217 已被占用。\n占用者：\"Google Chrome\"（PID 1，C:\\tmp\\x）"

	showErrorDialog(title, message)
	argv := readArgv(t, dump)

	want := []string{
		"-e", "on run argv",
		"-e", `display alert (item 1 of argv) message (item 2 of argv) as critical buttons {"好"} default button 1`,
		"-e", "end run",
		title, message,
	}
	if len(argv) != len(want) {
		t.Fatalf("argv = %#v, want %d items", argv, len(want))
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Fatalf("argv[%d] = %q, want %q", i, argv[i], want[i])
		}
	}
}
