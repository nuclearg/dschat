//go:build !darwin && !windows

package desktop

func (autoStartManager) Enabled() (bool, error) { return false, nil }
func (autoStartManager) Set(bool) error         { return nil }
