//go:build !darwin && !linux && !windows

package desktop

// findListener 在不支持的平台上直接放弃，弹窗里会提示手动查看。
func findListener(int) occupant { return occupant{} }

func processPath(int) string { return "" }

func platformProcessName(int) string { return "" }
