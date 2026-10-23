//go:build windows

package workspace

import (
	"os"

	"golang.org/x/sys/windows"
)

// isLinkOrReparse 在 Windows 上识别符号链接、junction 等 reparse point：
// 既检查 Lstat 的 ModeSymlink，也直接读取文件属性中的 FILE_ATTRIBUTE_REPARSE_POINT。
func isLinkOrReparse(path string, info os.FileInfo) bool {
	if info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return true
	}
	attrs, err := windows.GetFileAttributes(p)
	if err != nil {
		return true
	}
	return attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
