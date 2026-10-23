package desktop

// 逻辑尺寸（Wails 使用逻辑像素）。初始目标 960×720，常规最小 600×400。
const (
	InitialWidth      = 960
	InitialHeight     = 720
	RegularMinWidth   = 600
	RegularMinHeight  = 400
	absoluteMinWidth  = 320
	absoluteMinHeight = 240
)

// WindowSize 是逻辑像素宽高。
type WindowSize struct {
	Width  int
	Height int
}

// WorkAreaRect 是逻辑像素的可用工作区矩形（排除任务栏/面板）。
// X/Y 为工作区原点，Width/Height 为工作区逻辑尺寸。
// DpiX/DpiY 为该显示器 DPI（96 基准）；0 视为 96（未知），用于 Windows 位置换算。
// MonitorX/MonitorY 为该显示器 geometry 原点（Linux gdk_monitor_get_geometry；Windows 不使用，缺省 0）。
// 注意：有顶栏/侧栏面板时 geometry 原点与工作区原点不同（差一个 panelOffset），
// Wails Linux SetPosition 是相对 geometry 原点，故必须与 X/Y 分开记录，不能复用。
type WorkAreaRect struct {
	X        int
	Y        int
	Width    int
	Height   int
	DpiX     int
	DpiY     int
	MonitorX int
	MonitorY int
}

// Size 返回工作区逻辑尺寸。
func (r WorkAreaRect) Size() WindowSize { return WindowSize{Width: r.Width, Height: r.Height} }

// effectiveDPI 返回有效 DPI（0 视为 96）。
func effectiveDPI(dpi int) int {
	if dpi <= 0 {
		return 96
	}
	return dpi
}

// WindowsSetPosOffset 把“布局绝对逻辑坐标”转换为 Wails Windows `SetPos` 所需的
// “相对当前显示器工作区原点的物理偏移”。
//
// 依据 Wails v2.11 winc/controlbase.go SetPos：SetWindowPos(workRect.Left+x, workRect.Top+y)，
// 其中 workRect 为物理像素、x/y 不做 DPI 缩放；而我们的 area.X/Y 是 rcWork 原点的逻辑换算。
// 因此相对偏移 = (目标绝对逻辑 - area.X) * dpi / 96，得到相对该显示器工作区原点的物理偏移；
// 与 SDK 的 workRect.Left 相加后落回正确的绝对物理位置（含多屏 origin/负坐标/高 DPI）。
func WindowsSetPosOffset(absX, absY int, area WorkAreaRect) (int, int) {
	dpiX := effectiveDPI(area.DpiX)
	dpiY := effectiveDPI(area.DpiY)
	relX := (absX - area.X) * dpiX / 96
	relY := (absY - area.Y) * dpiY / 96
	return relX, relY
}

// LinuxSetPos 把布局绝对逻辑坐标转换为 Wails Linux `SetPosition` 所需的
// “相对当前显示器 geometry 原点的逻辑偏移”。
//
// 依据 Wails v2.11 internal/frontend/desktop/linux/window.c SetPosition（~252）：
// monitorDimensions = gdk_monitor_get_geometry(当前显示器)，随后
// gtk_window_move(monitorDimensions.x + x, monitorDimensions.y + y)，输入 x/y 不做缩放。
// 因此偏移 = (absX-area.MonitorX, absY-area.MonitorY)；直接用工作区原点 area.X/Y 会在
// 有顶栏/侧栏面板时少/多一个 panelOffset（geometry 原点 != workarea 原点）。
//
// 限制：Wayland 下 Wails/SDL 的 gtk_window_move 不保证生效，位置断言仅在 X11 有效；
// 真机 GUI（Wayland）尚未验证。
func LinuxSetPos(absX, absY int, area WorkAreaRect) (int, int) {
	return absX - area.MonitorX, absY - area.MonitorY
}

// WorkAreaProvider 返回包含给定逻辑屏幕坐标 (x,y) 的显示器可用工作区矩形与是否成功。
// 工作区应排除任务栏/面板等不可用区域；失败时返回 false，由调用方回退。
// 以窗口实际所在坐标查询，保证“工作区显示器”与“窗口显示器”一致。
type WorkAreaProvider func(x, y int) (WorkAreaRect, bool)

// ClampWindowToWorkArea 计算窗口逻辑外框位置，使其落入工作区矩形内（居中且不越界）。
// 尺寸大于工作区时对齐工作区左上，保证标题栏可达。
func ClampWindowToWorkArea(area WorkAreaRect, size WindowSize) (x, y int) {
	x = area.X + (area.Width-size.Width)/2
	y = area.Y + (area.Height-size.Height)/2
	if x < area.X {
		x = area.X
	}
	if y < area.Y {
		y = area.Y
	}
	// 仅当窗口能容纳于工作区时才做右上/右下钳制；超大窗口对齐左上以保证标题栏可达。
	if size.Width <= area.Width {
		if maxX := area.X + area.Width - size.Width; x > maxX {
			x = maxX
		}
	}
	if size.Height <= area.Height {
		if maxY := area.Y + area.Height - size.Height; y > maxY {
			y = maxY
		}
	}
	return x, y
}

// ClampWindowSize 依据当前可用工作区计算初始与有效最小尺寸：
// 初始不超过工作区；工作区小于常规最小时下调有效最小，避免把窗口控件放到屏幕外。
// 同时保证最小不低于可用的绝对下限，且不超过初始尺寸。
func ClampWindowSize(area WindowSize) (initial WindowSize, min WindowSize) {
	w, h := area.Width, area.Height
	if w <= 0 {
		w = InitialWidth
	}
	if h <= 0 {
		h = InitialHeight
	}
	initial = WindowSize{Width: minInt(InitialWidth, w), Height: minInt(InitialHeight, h)}
	min = WindowSize{Width: minInt(RegularMinWidth, initial.Width), Height: minInt(RegularMinHeight, initial.Height)}
	if min.Width > initial.Width {
		min.Width = initial.Width
	}
	if min.Height > initial.Height {
		min.Height = initial.Height
	}
	return initial, min
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
