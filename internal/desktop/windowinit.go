package desktop

// WindowInitActions 抽象初次窗口初始化所需的原生动作，顺序固定为 Min → Size → Position → Show。
// 生产由 Wails 适配实现；测试可注入记录实现，使顺序/取值回归走真实 maybeInitWindow 路径，
// 而不是只测试一个与应用无关的 mock 函数。
type WindowInitActions interface {
	SetMinSize(width, height int)
	SetSize(width, height int)
	SetPosition(x, y int)
	Show()
}

// ApplyWindowInit 执行唯一一次窗口初始化的固定顺序动作：
//  1. 依据工作区钳制初始尺寸与有效最小尺寸，先设最小再设尺寸（小工作区不会被旧常规最小钳住）；
//  2. 按工作区矩形（非整屏）钳制窗口绝对逻辑位置，经平台换算后设置位置；
//  3. 最后 Show。
//
// 生产 maybeInitWindow 与顺序回归测试共用本函数。
func ApplyWindowInit(actions WindowInitActions, area WorkAreaRect, positionForSet func(absX, absY int, area WorkAreaRect) (int, int)) {
	initial, minSize := ClampWindowSize(area.Size())
	actions.SetMinSize(minSize.Width, minSize.Height)
	actions.SetSize(initial.Width, initial.Height)
	absX, absY := ClampWindowToWorkArea(area, initial)
	setX, setY := positionForSet(absX, absY, area)
	actions.SetPosition(setX, setY)
	actions.Show()
}

// maybeInitWindow 在“DOM-ready 且启动预检成功”时执行一次窗口初始化/显示/唤起。
// 顺序无关：无论 OnStartup 与 OnDomReady 谁先完成，都只在此处统一放行。
// 屏障（DOM-ready + 预检成功）未满足不得应用；预检失败持久阻断；
// 已应用后不因后续导航/重载重放（sync.Once），状态保持。
func (a *App) maybeInitWindow() {
	if !a.domReady.Load() || !a.Life.StartupSucceeded() {
		return
	}
	a.initWindowOnce(func() {
		if a.winActions == nil {
			return
		}
		ApplyWindowInit(a.winActions, a.WorkAreaRect(), a.PositionForSet)
	})
	a.Life.MarkReady()
}
