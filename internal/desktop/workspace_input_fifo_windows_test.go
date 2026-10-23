//go:build windows

package desktop

import "errors"

// mkfifoPlatform 在 Windows 无 POSIX 命名管道语义：由调用方按平台如实记录。
func mkfifoPlatform(string) error { return errors.New("Windows 不支持 POSIX FIFO 用例") }
