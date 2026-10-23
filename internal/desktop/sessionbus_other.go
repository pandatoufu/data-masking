//go:build desktop && !linux

package desktop

// checkSessionBus 在非 Linux 平台无需 session bus 检查。
func checkSessionBus() error { return nil }
