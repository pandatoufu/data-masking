package desktop

import "testing"

// C2 回归：工作区提供者注入、坐标透传、回退与钳制语义。
func TestWorkAreaSizeUsesProvider(t *testing.T) {
	app := NewApp(nil, nil)
	app.SetWorkAreaProvider(func(x, y int) (WorkAreaRect, bool) {
		return WorkAreaRect{Width: 1280, Height: 1000}, true
	})
	if got := app.WorkAreaSize(); got.Width != 1280 || got.Height != 1000 {
		t.Fatalf("应采用注入工作区：%+v", got)
	}
	// 初始不超过工作区；常规最小不超过初始。
	initial, min := ClampWindowSize(app.WorkAreaSize())
	if initial.Width != 960 || initial.Height != 720 {
		t.Fatalf("1280x1000 工作区应使用完整初始尺寸：%+v", initial)
	}
	if min.Width != RegularMinWidth || min.Height != RegularMinHeight {
		t.Fatalf("应使用常规最小：%+v", min)
	}
}

// 工作区查询必须用窗口实际坐标，保证显示器一致。
func TestWorkAreaUsesWindowPosition(t *testing.T) {
	app := NewApp(nil, nil)
	var gotX, gotY = -999, -999
	app.SetWindowPositionProvider(func() (int, int) { return 2560, -120 })
	app.SetWorkAreaProvider(func(x, y int) (WorkAreaRect, bool) {
		gotX, gotY = x, y
		return WorkAreaRect{Width: 1920, Height: 1080}, true
	})
	app.WorkAreaSize()
	if gotX != 2560 || gotY != -120 {
		t.Fatalf("应以窗口坐标查询工作区（含负坐标）：got=(%d,%d)", gotX, gotY)
	}
}

func TestWorkAreaSizeFallsBackOnFailure(t *testing.T) {
	app := NewApp(nil, nil)
	app.SetWorkAreaProvider(func(x, y int) (WorkAreaRect, bool) { return WorkAreaRect{}, false })
	got := app.WorkAreaSize()
	if got.Width != InitialWidth || got.Height != InitialHeight {
		t.Fatalf("失败应回退初始尺寸：%+v", got)
	}
}

func TestWorkAreaSmallScreenClamps(t *testing.T) {
	app := NewApp(nil, nil)
	app.SetWorkAreaProvider(func(x, y int) (WorkAreaRect, bool) {
		return WorkAreaRect{Width: 520, Height: 360}, true
	})
	initial, min := ClampWindowSize(app.WorkAreaSize())
	if initial.Width != 520 || initial.Height != 360 {
		t.Fatalf("初始应贴合小工作区：%+v", initial)
	}
	if min.Width > initial.Width || min.Height > initial.Height {
		t.Fatalf("有效最小不得超过初始：min=%+v initial=%+v", min, initial)
	}
	if min.Width != 520 || min.Height != 360 {
		t.Fatalf("小工作区有效最小应适配：%+v", min)
	}
}

// 窗口位置按工作区矩形钳制（不使用整屏 Center），保证不被任务栏遮挡。
func TestClampWindowToWorkArea(t *testing.T) {
	// 带左下任务栏：工作区原点非 (0,0)，尺寸小于整屏。
	area := WorkAreaRect{X: 0, Y: 0, Width: 1920, Height: 1040}
	x, y := ClampWindowToWorkArea(area, WindowSize{Width: 960, Height: 720})
	if x != (1920-960)/2 || y != (1040-720)/2 {
		t.Fatalf("应在工作区内居中：x=%d y=%d", x, y)
	}
	// 窗口大于工作区时对齐左上，保证标题栏可达。
	x2, y2 := ClampWindowToWorkArea(WorkAreaRect{X: 100, Y: 50, Width: 400, Height: 300}, WindowSize{Width: 600, Height: 400})
	if x2 != 100 || y2 != 50 {
		t.Fatalf("超大窗口应对齐工作区左上：x=%d y=%d", x2, y2)
	}
	// 次级显示器负坐标工作区。
	x3, y3 := ClampWindowToWorkArea(WorkAreaRect{X: -1920, Y: 0, Width: 1920, Height: 1040}, WindowSize{Width: 960, Height: 720})
	if x3 != -1920+(1920-960)/2 {
		t.Fatalf("负坐标显示器应正确居中：x=%d", x3)
	}
	_ = y3
}

// 默认平台提供者在 headless 下返回 false，WorkAreaSize 安全回退（不 panic）。
func TestWorkAreaSizeDefaultSafe(t *testing.T) {
	app := NewApp(nil, nil)
	got := app.WorkAreaSize()
	if got.Width <= 0 || got.Height <= 0 {
		t.Fatalf("默认回退应为正尺寸：%+v", got)
	}
}
