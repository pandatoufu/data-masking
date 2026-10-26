//go:build !windows

package agent

import (
	"os"
	"syscall"
	"testing"
)

// crossVolumeCandidates 返回 POSIX 上常见的独立临时文件系统挂载点；
// 不存在或与工作区同卷的候选会在用例中跳过。
func crossVolumeCandidates() []string {
	return []string{"/dev/shm", "/tmp", "/var/tmp"}
}

// fsDeviceID 读取路径所在文件系统的设备号；无法读取时返回 0，按同卷处理。
func fsDeviceID(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("读取 %s 的设备号失败：%v", path, err)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev)
	}
	return 0
}

// assertNotHardLink 断言发布的产物是独立文件（nlink 为 1），
// 而非仍与发布用临时文件共享的硬链接。
func assertNotHardLink(t *testing.T, _ string, info os.FileInfo) {
	t.Helper()
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("无法读取产物链接数：不是 syscall.Stat_t")
	}
	if int64(st.Nlink) != 1 {
		t.Fatalf("跨卷交付不得使用硬链接（nlink=%d）", st.Nlink)
	}
}
