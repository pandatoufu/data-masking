//go:build !desktop || !windows

package desktop

// platformSetPos 在非 Windows 平台把布局绝对逻辑坐标转换为 Wails Linux `SetPosition`
// 所需的“相对当前显示器 geometry 原点的逻辑偏移”（gtk_window_move 会再加 geometry 原点）。
// 保持 !desktop 分层：headless/无平台适配时 area.MonitorX/Y 缺省 0，退化为恒等。
func platformSetPos(absX, absY int, area WorkAreaRect) (int, int) {
	return LinuxSetPos(absX, absY, area)
}
