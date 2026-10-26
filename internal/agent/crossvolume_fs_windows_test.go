//go:build windows

package agent

import (
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// crossVolumeCandidates 返回 Windows 上可能的第二个卷根路径（盘符）。
// 只接受固定/可移动的 NTFS/ReFS 卷：产品的无覆盖发布依赖硬链接，
// FAT/exFAT 与网络卷不适用，也不作为本用例候选。
func crossVolumeCandidates() []string {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil
	}
	var roots []string
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		p, err := windows.UTF16PtrFromString(root)
		if err != nil {
			continue
		}
		switch windows.GetDriveType(p) {
		case windows.DRIVE_FIXED, windows.DRIVE_REMOVABLE:
		default:
			continue
		}
		if !supportsHardLinks(p) {
			continue
		}
		roots = append(roots, root)
	}
	return roots
}

// supportsHardLinks 报告卷是否使用支持硬链接的文件系统（NTFS/ReFS）。
func supportsHardLinks(root *uint16) bool {
	name := make([]uint16, windows.MAX_PATH+1)
	var serial, maxComponentLength, flags uint32
	if err := windows.GetVolumeInformation(root, nil, 0, &serial, &maxComponentLength, &flags, &name[0], uint32(len(name))); err != nil {
		return false
	}
	switch strings.ToLower(windows.UTF16ToString(name)) {
	case "ntfs", "refs":
		return true
	}
	return false
}

// fsDeviceID 用卷序列号标识文件系统，用于判断两个路径是否同卷。
func fsDeviceID(t *testing.T, path string) uint64 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("打开 %s 读取卷序列号失败：%v", path, err)
	}
	defer f.Close()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		t.Fatalf("读取 %s 的卷序列号失败：%v", path, err)
	}
	return uint64(info.VolumeSerialNumber)
}

// assertNotHardLink 断言发布的产物是独立文件（链接数为 1）。
// Windows 的 os.FileInfo 不提供链接数，需按句柄读取文件信息。
func assertNotHardLink(t *testing.T, path string, _ os.FileInfo) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("打开产物读取链接数失败：%v", err)
	}
	defer f.Close()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		t.Fatalf("读取产物链接数失败：%v", err)
	}
	if info.NumberOfLinks != 1 {
		t.Fatalf("跨卷交付不得使用硬链接（nlink=%d）", info.NumberOfLinks)
	}
}
