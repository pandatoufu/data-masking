//go:build desktop && windows

package desktop

// platformSetPos 在 Windows 上把布局绝对逻辑坐标转换为 Wails SetPos 所需的
// “相对当前显示器工作区原点的物理偏移”（SDK 会再加 workRect.Left/Top）。
func platformSetPos(absX, absY int, area WorkAreaRect) (int, int) {
	return WindowsSetPosOffset(absX, absY, area)
}
