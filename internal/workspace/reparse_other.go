//go:build !windows

package workspace

import "os"

// isLinkOrReparse 在非 Windows 平台上仅需识别符号链接；
// info 必须来自 Lstat（不跟随链接）。
func isLinkOrReparse(_ string, info os.FileInfo) bool {
	return info.Mode()&os.ModeSymlink != 0
}
