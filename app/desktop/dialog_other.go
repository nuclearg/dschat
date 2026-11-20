//go:build !darwin && !windows

package desktop

import (
	"fmt"
	"os"
)

// showErrorDialog 在没有原生弹窗的平台上只写 stderr。
func showErrorDialog(title, message string) {
	fmt.Fprintf(os.Stderr, "%s: %s\n", title, message)
}
