//go:build !windows

package desktop

import "syscall"

// mkfifoPlatform 在 POSIX 平台创建命名管道，用于验证特殊文件被列举跳过。
func mkfifoPlatform(path string) error { return syscall.Mkfifo(path, 0o600) }
