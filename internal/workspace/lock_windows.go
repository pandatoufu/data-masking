//go:build windows

package workspace

import (
	"errors"
	"io/fs"
	"os"

	"golang.org/x/sys/windows"
)

// openLockFile 在 Windows 上以 OPEN_ALWAYS 打开/创建锁文件，并以
// FILE_FLAG_OPEN_REPARSE_POINT 打开 reparse point 本身，再用句柄属性拒绝它。
func openLockFile(path string) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil {
		if isLinkOrReparse(path, info) {
			return nil, ErrUnsafeLock
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(
		p,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		_ = windows.CloseHandle(h)
		return nil, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = windows.CloseHandle(h)
		return nil, ErrUnsafeLock
	}
	return os.NewFile(uintptr(h), path), nil
}

func tryLockFile(f *os.File) error {
	err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, new(windows.Overlapped),
	)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ErrBusy
	}
	return err
}

func unlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, new(windows.Overlapped))
}
