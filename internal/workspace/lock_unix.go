//go:build !windows

package workspace

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// openLockFile 以 O_NOFOLLOW 打开/创建锁文件：符号链接直接返回 ErrUnsafeLock。
func openLockFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, ErrUnsafeLock
		}
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func tryLockFile(f *os.File) error {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return ErrBusy
	}
	return err
}

func unlockFile(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
