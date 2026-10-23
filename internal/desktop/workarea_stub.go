//go:build (!desktop || !windows) && (!desktop || !linux || !cgo || (!production && !dev))

package desktop

// platformWorkArea 在无可用平台工作区 API 时回退（headless 测试、非桌面构建、
// Linux 无 CGo / 无 GTK 开发库、Windows 非 cgo 构建）。
// 返回 false 让 WorkAreaSize 回退到初始目标尺寸，绝不假装读取到真实工作区。
func platformWorkArea(x, y int) (WorkAreaRect, bool) { return WorkAreaRect{}, false }
