package desktop

import "testing"

// CRITICAL-C 回归：Windows 上 Wails v2.11 runtime.WindowSetPosition 经 winc/controlbase.SetPos
// 把 x/y 加到当前显示器 rcWork.Left/Top 物理坐标上（SetWindowPos(workRect.Left+x, workRect.Top+y)，
// x/y 不再做 DPI 缩放）。因此传入的是“相对工作区原点的物理偏移”，不是绝对逻辑坐标。
// 本文件按真实链路验证：物理 rcWork → 逻辑工作区 → 钳制 → WindowsSetPosOffset → SDK 终值
// 必须仍落在同一 rcWork 内（多屏 origin/负坐标/高 DPI 不飞屏）。

// physRect 模拟 Windows GetMonitorInfoW 的 rcWork（物理像素，屏幕坐标，可为负）。
type physRect struct{ left, top, right, bottom int }

// logicalFromPhys 复刻 workarea_windows.go scalePhysicalToLogical 的换算（96 基准，同样截断）。
func logicalFromPhys(p, dpi int) int {
	if dpi == 0 {
		return p
	}
	return int(float64(p) * 96.0 / float64(dpi))
}

// areaFromPhys 由物理 rcWork 与 DPI 得到逻辑 WorkAreaRect（同 workarea_windows.go 行为，
// 含 DpiX/DpiY 保留）。
func areaFromPhys(w physRect, dpiX, dpiY int) WorkAreaRect {
	return WorkAreaRect{
		X:      logicalFromPhys(w.left, dpiX),
		Y:      logicalFromPhys(w.top, dpiY),
		Width:  logicalFromPhys(w.right-w.left, dpiX),
		Height: logicalFromPhys(w.bottom-w.top, dpiY),
		DpiX:   dpiX,
		DpiY:   dpiY,
	}
}

// sdkFinalPhysical 模拟 winc/controlbase.SetPos 的最终 SetWindowPos 参数：
// 终值 = 当前显示器工作区物理原点 + Wails 收到的 x/y（不再缩放）。
func sdkFinalPhysical(w physRect, x, y int) (int, int) { return w.left + x, w.top + y }

// assertFinalWithinWorkArea 走完整链路（钳制尺寸→钳制位置→偏移换算→SDK 终值），
// 断言窗口外框落在同一物理 rcWork 内。tol 容忍整数截断误差（≤2 物理像素），
// 远小于“把绝对逻辑坐标当偏移传入”造成的数百/数千像素飞屏。
func assertFinalWithinWorkArea(t *testing.T, name string, w physRect, dpiX, dpiY int) {
	t.Helper()
	area := areaFromPhys(w, dpiX, dpiY)
	initial, minSize := ClampWindowSize(area.Size())
	absX, absY := ClampWindowToWorkArea(area, initial)
	offX, offY := WindowsSetPosOffset(absX, absY, area)
	finX, finY := sdkFinalPhysical(w, offX, offY)

	const tol = 2
	// 窗口物理外框尺寸（逻辑 × dpi/96；向上取整保守估计右/下边界）。
	physW := (initial.Width*dpiX + 95) / 96
	physH := (initial.Height*dpiY + 95) / 96
	if physW > w.right-w.left {
		physW = w.right - w.left // 超大窗口被钳到工作区宽
	}
	if physH > w.bottom-w.top {
		physH = w.bottom - w.top
	}
	if minSize.Width > initial.Width || minSize.Height > initial.Height {
		t.Fatalf("%s：有效最小不得超过初始 min=%+v initial=%+v", name, minSize, initial)
	}
	if finX < w.left-tol || finX+physW > w.right+tol {
		t.Fatalf("%s：最终 X=%d + 宽 %d 越出 rcWork [%d,%d]（abs=(%d,%d) off=(%d,%d) area=%+v）",
			name, finX, physW, w.left, w.right, absX, absY, offX, offY, area)
	}
	if finY < w.top-tol || finY+physH > w.bottom+tol {
		t.Fatalf("%s：最终 Y=%d + 高 %d 越出 rcWork [%d,%d]（abs=(%d,%d) off=(%d,%d) area=%+v）",
			name, finY, physH, w.top, w.bottom, absX, absY, offX, offY, area)
	}
}

// 覆盖矩阵：100/125/150/200 DPI × 主屏/右非零 origin/左负 origin/负 Y/小工作区。
func TestWindowsSetPosOffsetStaysInWorkArea(t *testing.T) {
	cases := []struct {
		name       string
		work       physRect
		dpiX, dpiY int
	}{
		{"主屏100%", physRect{0, 0, 1920, 1040}, 96, 96},
		{"主屏125%", physRect{0, 0, 1920, 1040}, 120, 120},
		{"主屏150%", physRect{0, 0, 1920, 1040}, 144, 144},
		{"主屏200%", physRect{0, 0, 1920, 1040}, 192, 192},
		{"右侧副屏非零origin100%", physRect{1920, 0, 3840, 1040}, 96, 96},
		{"右侧副屏非零origin150%", physRect{1920, 0, 3840, 1040}, 144, 144},
		{"右侧1440宽150%非整除", physRect{1920, 120, 3200, 1160}, 144, 144},
		{"左侧副屏负origin100%", physRect{-1920, 0, 0, 1040}, 96, 96},
		{"左侧副屏负origin200%", physRect{-1920, 0, 0, 1040}, 192, 192},
		{"上方副屏负Y100%", physRect{0, -1040, 1920, 0}, 96, 96},
		{"左上副屏负origin负Y150%", physRect{-2560, -1440, -640, -400}, 144, 144},
		{"小工作区800x560", physRect{0, 0, 800, 560}, 96, 96},
		{"小工作区高DPI", physRect{0, 0, 1200, 800}, 192, 192},
	}
	for _, c := range cases {
		assertFinalWithinWorkArea(t, c.name, c.work, c.dpiX, c.dpiY)
	}
}

// 精确数值回归：偏移必须是“相对工作区原点的物理偏移”，而非绝对逻辑坐标。
func TestWindowsSetPosOffsetExactValues(t *testing.T) {
	// 主屏 100%：居中 (480,160)，偏移=绝对值（origin 0、DPI 96）。
	area := areaFromPhys(physRect{0, 0, 1920, 1040}, 96, 96)
	initial, _ := ClampWindowSize(area.Size())
	absX, absY := ClampWindowToWorkArea(area, initial)
	if absX != 480 || absY != 160 {
		t.Fatalf("100%% 居中逻辑坐标应为 (480,160)，got (%d,%d)", absX, absY)
	}
	offX, offY := WindowsSetPosOffset(absX, absY, area)
	if offX != 480 || offY != 160 {
		t.Fatalf("100%% 偏移应等于居中坐标，got (%d,%d)", offX, offY)
	}

	// 右侧副屏 150%（rcWork 左 1920、宽 1920）：逻辑区 X=1280、W=1280；
	// 960 宽居中 → 逻辑绝对 X=1440；偏移 = (1440-1280)*144/96 = 240（相对物理），
	// SDK 终值 = 1920+240 = 2160；若错误传绝对逻辑 1440，终值=3360（偏右 1200 物理像素）。
	areaR := areaFromPhys(physRect{1920, 0, 3840, 1040}, 144, 144)
	initR, _ := ClampWindowSize(areaR.Size())
	absRX, absRY := ClampWindowToWorkArea(areaR, initR)
	offRX, offRY := WindowsSetPosOffset(absRX, absRY, areaR)
	if offRX != 240 {
		t.Fatalf("右副屏150%% 偏移 X 应为 240，got %d（absX=%d area.X=%d）", offRX, absRX, areaR.X)
	}
	finX, _ := sdkFinalPhysical(physRect{1920, 0, 3840, 1040}, offRX, offRY)
	if finX != 2160 {
		t.Fatalf("SDK 终值 X 应为 2160，got %d", finX)
	}

	// 左侧副屏 200%（rcWork 左 -1920、宽 1920）：逻辑 X=-960、W=960，窗口占满 → absX=-960、偏移 0。
	areaL := areaFromPhys(physRect{-1920, 0, 0, 1040}, 192, 192)
	initL, _ := ClampWindowSize(areaL.Size())
	absLX, _ := ClampWindowToWorkArea(areaL, initL)
	offLX, _ := WindowsSetPosOffset(absLX, 0, areaL)
	if absLX != -960 || offLX != 0 {
		t.Fatalf("左副屏200%% 应 absX=-960、偏移 0，got abs=%d off=%d", absLX, offLX)
	}
	if finX, _ := sdkFinalPhysical(physRect{-1920, 0, 0, 1040}, offLX, 0); finX != -1920 {
		t.Fatalf("SDK 终值 X 应为 -1920（工作区左原点），got %d", finX)
	}

	// 上方副屏负 Y：逻辑 Y=-1040、H=1040，720 高居中 → absY=-880；偏移 Y=160；终值 Y=-880。
	areaU := areaFromPhys(physRect{0, -1040, 1920, 0}, 96, 96)
	_, minU := ClampWindowSize(areaU.Size())
	if minU.Height != RegularMinHeight || minU.Width != RegularMinWidth {
		t.Fatalf("上方副屏常规工作区最小尺寸应为常规值：%+v", minU)
	}
	absUX, absUY := ClampWindowToWorkArea(areaU, WindowSize{Width: 960, Height: 720})
	if absUY != -880 {
		t.Fatalf("负 Y 居中应为 -880，got %d", absUY)
	}
	_, offUY := WindowsSetPosOffset(absUX, absUY, areaU)
	if offUY != 160 {
		t.Fatalf("负 Y 偏移应为 160，got %d", offUY)
	}
}

// 未知 DPI（0）按 96 处理：偏移退化为逻辑差值，不除 0、不异常。
func TestWindowsSetPosOffsetUnknownDPI(t *testing.T) {
	area := WorkAreaRect{X: 100, Y: 50, Width: 1000, Height: 800} // DpiX/DpiY 缺省 0
	offX, offY := WindowsSetPosOffset(300, 250, area)
	if offX != 200 || offY != 200 {
		t.Fatalf("未知 DPI 应按 96 处理，got (%d,%d)", offX, offY)
	}
}

// Windows 偏移必须相对工作区原点（传绝对坐标即错）；直接验证换算公式。
func TestWindowsSetPosOffsetRelativeNotAbsolute(t *testing.T) {
	area := WorkAreaRect{X: 1280, Y: 0, Width: 1280, Height: 693, DpiX: 144, DpiY: 144}
	offX, offY := WindowsSetPosOffset(1440, 0, area)
	if offX <= 0 || offX >= 1280 {
		t.Fatalf("偏移应为工作区内相对值，got %d", offX)
	}
	if offX == 1440 {
		t.Fatal("不得把绝对逻辑坐标原样作为偏移传入（多屏会飞屏）")
	}
	if offY != 0 {
		t.Fatalf("Y 偏移应为 0，got %d", offY)
	}
}

// sdkFinalLinux 模拟 Wails v2.11 internal/frontend/desktop/linux/window.c SetPosition：
// 终值 = gdk_monitor_get_geometry 原点 + Wails 收到的 x/y（不做缩放）。
func sdkFinalLinux(monX, monY, offX, offY int) (int, int) { return monX + offX, monY + offY }

// assertLinuxFinalWithinWorkArea 走完整链路（钳制尺寸→钳制位置→LinuxSetPos→SDK 终值），
// 断言 SDK 终值精确回到布局绝对坐标且位于工作区内。覆盖非零/负 origin、负 Y、
// 顶/左侧面板（geometry 原点 != workarea 原点，差一个 panelOffset）与小工作区。
func assertLinuxFinalWithinWorkArea(t *testing.T, name string, area WorkAreaRect) {
	t.Helper()
	initial, _ := ClampWindowSize(area.Size())
	absX, absY := ClampWindowToWorkArea(area, initial)
	offX, offY := LinuxSetPos(absX, absY, area)
	finX, finY := sdkFinalLinux(area.MonitorX, area.MonitorY, offX, offY)
	if finX != absX || finY != absY {
		t.Fatalf("%s：SDK 终值应为 (%d,%d)，got (%d,%d)（off=(%d,%d) mon=(%d,%d)）",
			name, absX, absY, finX, finY, offX, offY, area.MonitorX, area.MonitorY)
	}
	if finX < area.X || finY < area.Y || finX > area.X+area.Width || finY > area.Y+area.Height {
		t.Fatalf("%s：终值 (%d,%d) 越出工作区 [%d,%d]x[%d,%d]", name, finX, finY,
			area.X, area.X+area.Width, area.Y, area.Y+area.Height)
	}
}

// Linux 偏移必须相对“显示器 geometry 原点”，而非 workarea 原点；不能固化为恒等。
func TestLinuxSetPosRelativeToMonitorGeometry(t *testing.T) {
	cases := []struct {
		name string
		area WorkAreaRect
	}{
		{"单屏0origin", WorkAreaRect{X: 0, Y: 0, Width: 1920, Height: 1040, MonitorX: 0, MonitorY: 0}},
		{"右侧副屏非零origin", WorkAreaRect{X: 1920, Y: 0, Width: 1920, Height: 1040, MonitorX: 1920, MonitorY: 0}},
		{"左侧副屏负origin", WorkAreaRect{X: -1920, Y: 0, Width: 1920, Height: 1040, MonitorX: -1920, MonitorY: 0}},
		{"上方副屏负Y", WorkAreaRect{X: 0, Y: -1040, Width: 1920, Height: 1040, MonitorX: 0, MonitorY: -1040}},
		{"顶部面板", WorkAreaRect{X: 0, Y: 32, Width: 1920, Height: 1008, MonitorX: 0, MonitorY: 0}},
		{"左侧面板", WorkAreaRect{X: 64, Y: 0, Width: 1856, Height: 1040, MonitorX: 0, MonitorY: 0}},
		{"左上双面板", WorkAreaRect{X: 64, Y: 32, Width: 1856, Height: 1008, MonitorX: 0, MonitorY: 0}},
		{"小工作区顶面板", WorkAreaRect{X: 0, Y: 24, Width: 800, Height: 536, MonitorX: 0, MonitorY: 0}},
	}
	for _, c := range cases {
		assertLinuxFinalWithinWorkArea(t, c.name, c.area)
	}
}

// 顶部面板精确值：窗口贴工作区顶 (absY=32) 时，偏移必须为 32（相对 geometry 原点 0），
// SDK 终值 32；若错误复用 workarea 原点 area.Y=32 会得偏移 0、终值 0（落到顶部面板之下）。
func TestLinuxSetPosPreservesPanelOffset(t *testing.T) {
	area := WorkAreaRect{X: 0, Y: 32, Width: 1920, Height: 1008, MonitorX: 0, MonitorY: 0}
	offX, offY := LinuxSetPos(480, 32, area)
	if offY != 32 {
		t.Fatalf("顶部面板下偏移 Y 应为 32（相对 geometry 原点），got %d", offY)
	}
	if finX, finY := sdkFinalLinux(area.MonitorX, area.MonitorY, offX, offY); finX != 480 || finY != 32 {
		t.Fatalf("SDK 终值应为 (480,32)，got (%d,%d)", finX, finY)
	}
}

// PositionForSet 默认接平台换算；注入后必须使用注入实现（可测试的 placement adapter）。
// 本测试只验证注入/恢复的接线语义：先记录当前构建实际选中的平台默认输出，
// 注入后必须改用注入实现，nil 恢复必须精确回到记录值。
// 平台换算的数学语义（Windows 相对 rcWork 物理偏移 / Linux 相对 geometry 原点逻辑偏移）由
// TestWindowsSetPosOffset* 与 TestLinuxSetPosRelativeToMonitorGeometry 专项覆盖，此处不硬编码平台默认。
func TestPositionForSetInjectable(t *testing.T) {
	app := NewApp(nil, nil)
	area := WorkAreaRect{X: 1280, Y: 0, Width: 1280, Height: 693, DpiX: 144, DpiY: 144}
	// 注入前先记录当前构建实际选中的默认（有/无 desktop 标签、各 OS 均自洽）。
	defX, defY := app.PositionForSet(1400, 10, area)
	app.SetPositionConverter(func(ax, ay int, _ WorkAreaRect) (int, int) { return ax - 1280, ay })
	gotX, gotY := app.PositionForSet(1400, 10, area)
	if gotX != 120 || gotY != 10 {
		t.Fatalf("应使用注入换算器，got (%d,%d)", gotX, gotY)
	}
	// nil 恢复平台默认：必须回到注入前记录的实际默认值。
	app.SetPositionConverter(nil)
	if x, y := app.PositionForSet(1400, 10, area); x != defX || y != defY {
		t.Fatalf("nil 应回复平台默认 (%d,%d)，got (%d,%d)", defX, defY, x, y)
	}
}
