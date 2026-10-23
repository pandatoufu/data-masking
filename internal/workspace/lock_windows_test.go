//go:build windows

package workspace

import (
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
)

// Windows 原生下锁文件被 junction（reparse point）占位必须判为 ErrUnsafeLock。
// 该用例仅在真实 Windows 环境运行；Linux 上不编译执行。
func TestLockRejectsJunctionOnWindows(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, ".workspace.lock")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, outside).CombinedOutput(); err != nil {
		t.Skipf("无法创建 junction：%v %s", err, out)
	}
	if _, err := AcquireLock(link); !errors.Is(err, ErrUnsafeLock) {
		t.Fatalf("junction 占位应 ErrUnsafeLock，got %v", err)
	}
}
