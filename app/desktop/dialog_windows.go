//go:build windows

package desktop

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

var procMessageBoxW = syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")

// showErrorDialog 弹系统 MessageBox，阻塞到用户点掉为止。
func showErrorDialog(title, message string) {
	const (
		mbOK            = 0x00000000
		mbIconError     = 0x00000010
		mbSetForeground = 0x00010000
	)
	titlePtr, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %s\n", title, message)
		return
	}
	msgPtr, err := syscall.UTF16PtrFromString(message)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %s\n", title, message)
		return
	}
	_, _, _ = procMessageBoxW.Call(
		0,
		uintptr(unsafe.Pointer(msgPtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		uintptr(mbOK|mbIconError|mbSetForeground),
	)
}
