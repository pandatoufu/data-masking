package workspace

import (
	"errors"
	"os"
	"sync"
)

// ErrBusy 表示目标锁已被其他进程（或另一处代码）持有；调用方应报告可重试忙态。
var ErrBusy = errors.New("workspace: 资源正被其他进程使用")

// ErrUnsafeLock 表示锁文件路径被符号链接或 Windows reparse point 占位。
var ErrUnsafeLock = errors.New("workspace: 锁文件被链接或 reparse point 占位")

// Lock 是固定锁文件上的跨进程 OS advisory/exclusive 锁。
// 锁随进程退出由 OS 释放；调用方 MUST NOT 以“锁文件存在”判断忙，也不删除锁文件。
type Lock struct {
	path     string
	file     *os.File
	mu       sync.Mutex
	released bool
}

// AcquireLock 以非阻塞方式获取 path 上的排他锁。
// 文件不存在时创建（权限 0600）；路径是符号链接/reparse point 返回 ErrUnsafeLock；
// 已被他处持有时返回 ErrBusy。失败不残留打开的句柄。
func AcquireLock(path string) (*Lock, error) {
	f, err := openLockFile(path)
	if err != nil {
		return nil, err
	}
	if err := tryLockFile(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Lock{path: path, file: f}, nil
}

// Path 返回锁文件路径。
func (l *Lock) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// Release 释放锁并关闭句柄；重复调用安全。锁文件保留在磁盘上。
func (l *Lock) Release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return nil
	}
	l.released = true
	err := unlockFile(l.file)
	if cerr := l.file.Close(); err == nil {
		err = cerr
	}
	return err
}
