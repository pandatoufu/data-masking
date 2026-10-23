//go:build windows

package workspace

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Windows 原生 reparse point（junction）占位的固定目录必须判为不可用，且不跟随写入。
// 该用例仅在真实 Windows 环境运行；Linux 上不编译执行。
func TestEnsureRejectsJunctionOnWindows(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, DirMasked)
	// 用 mklink /J 创建 junction，避免依赖符号链接权限。
	cmd := exec.Command("cmd", "/c", "mklink", "/J", link, outside)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("无法创建 junction（需要相应环境）：%v %s", err, out)
	}
	err := EnsureHeadless(root)
	if err == nil || !errors.Is(err, ErrUnsafeDir) {
		t.Fatalf("reparse 占位应返回 ErrUnsafeDir，got %v", err)
	}
	if names, _ := os.ReadDir(outside); len(names) != 0 {
		t.Fatalf("不得跟随 reparse point 写入：%v", names)
	}
}

// Windows：GUI 五目录之一被 junction 占位时同样拒绝，且不跟随 reparse point 写入。
// 仅在真实 Windows 环境运行。
func TestEnsureGUIRejectsRequiredJunctionOnWindows(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, DirMapping)
	cmd := exec.Command("cmd", "/c", "mklink", "/J", link, outside)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("无法创建 junction（需要相应环境）：%v %s", err, out)
	}
	err := EnsureGUI(root)
	if err == nil || !errors.Is(err, ErrUnsafeDir) {
		t.Fatalf("GUI reparse 占位应返回 ErrUnsafeDir，got %v", err)
	}
	if names, _ := os.ReadDir(outside); len(names) != 0 {
		t.Fatalf("GUI 不得跟随 reparse point 写入：%v", names)
	}
}

// Windows：pending/ 是 junction 时 EnsureGUI 自身不检查、不触碰且五目录可用；
// EnsureDir 独立初始化则拒绝该占位；headless 因六目录要求拒绝启动。
// 仅在真实 Windows 环境运行。
func TestEnsureGUIIgnoresPendingJunctionOnWindows(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, DirPending)
	cmd := exec.Command("cmd", "/c", "mklink", "/J", link, outside)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("无法创建 junction（需要相应环境）：%v %s", err, out)
	}
	if err := EnsureGUI(root); err != nil {
		t.Fatalf("pending junction 不得影响 GUI：%v", err)
	}
	for _, name := range GUIDirs() {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil || !info.IsDir() {
			t.Fatalf("%s 应为目录：err=%v", name, err)
		}
	}
	if names, _ := os.ReadDir(outside); len(names) != 0 {
		t.Fatalf("GUI 不得跟随 pending junction 写入：%v", names)
	}
	if err := EnsureHeadless(root); err == nil || !errors.Is(err, ErrUnsafeDir) {
		t.Fatalf("headless 应拒绝 pending junction，got %v", err)
	}
}

// Windows：GUI 以 EnsureDir 独立初始化 pending/ 时必须拒绝 junction 占位，
// 不跟随写入，也不替换该路径。仅在真实 Windows 环境运行。
func TestEnsureDirRejectsJunctionOnWindows(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, DirPending)
	cmd := exec.Command("cmd", "/c", "mklink", "/J", link, outside)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("无法创建 junction（需要相应环境）：%v %s", err, out)
	}
	err := EnsureDir(root, DirPending)
	if err == nil || !errors.Is(err, ErrUnsafeDir) {
		t.Fatalf("pending junction 应返回 ErrUnsafeDir，got %v", err)
	}
	if names, _ := os.ReadDir(outside); len(names) != 0 {
		t.Fatalf("EnsureDir 不得跟随 reparse point 写入：%v", names)
	}
	if _, statErr := os.Lstat(link); statErr != nil {
		t.Fatalf("占位不得被删除：%v", statErr)
	}
}
