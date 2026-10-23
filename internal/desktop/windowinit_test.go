package desktop

import "testing"

// W2 回归：窗口初始化只执行一次，后续导航不重置。
func TestWindowInitRunsOnce(t *testing.T) {
	app := NewApp(nil, nil)
	calls := 0
	for i := 0; i < 5; i++ {
		app.initWindowOnce(func() { calls++ })
	}
	if calls != 1 {
		t.Fatalf("窗口初始化应只执行一次，实际 %d", calls)
	}
}

// recordingInit 记录窗口初始化动作的顺序与取值，供真实 maybeInitWindow 顺序回归使用。
type recordingInit struct {
	steps      []string
	minW, minH int
	setW, setH int
	posX, posY int
	shows      int
}

func (r *recordingInit) SetMinSize(w, h int) { r.steps = append(r.steps, "min"); r.minW, r.minH = w, h }
func (r *recordingInit) SetSize(w, h int)    { r.steps = append(r.steps, "size"); r.setW, r.setH = w, h }
func (r *recordingInit) SetPosition(x, y int) {
	r.steps = append(r.steps, "pos")
	r.posX, r.posY = x, y
}
func (r *recordingInit) Show() { r.steps = append(r.steps, "show"); r.shows++ }

// 真实 maybeInitWindow 的固定顺序回归：Min→Size→Position→Show，取值按非零 origin/小工作区钳制，
// 屏障（DOM-ready + 预检成功）未满足不得应用、应用后不因再次触发重放。
func TestMaybeInitWindowOrderGateAndOnce(t *testing.T) {
	app := NewApp(nil, nil)
	// 非零 origin + 小工作区（含 geometry/workarea 面板偏移）：坐标经注入换算器暴露，验证接线。
	area := WorkAreaRect{X: 1280, Y: 120, Width: 900, Height: 600, MonitorX: 1280, MonitorY: 100, DpiX: 144, DpiY: 144}
	app.SetWorkAreaProvider(func(x, y int) (WorkAreaRect, bool) { return area, true })
	app.SetPositionConverter(func(absX, absY int, a WorkAreaRect) (int, int) {
		return absX - a.MonitorX, absY - a.MonitorY
	})
	rec := &recordingInit{}
	app.SetWindowInitActions(rec)

	// 屏障未满足：不应用。
	app.maybeInitWindow()
	app.domReady.Store(true)
	app.maybeInitWindow() // 预检仍 pending
	if len(rec.steps) != 0 {
		t.Fatalf("屏障未满足不得应用窗口初始化，got %v", rec.steps)
	}

	// 预检成功 + DOM-ready：应用一次，顺序固定。
	app.Life.MarkStartupSuccess()
	app.maybeInitWindow()
	wantSteps := []string{"min", "size", "pos", "show"}
	if len(rec.steps) != len(wantSteps) {
		t.Fatalf("动作数应为 %d，got %v", len(wantSteps), rec.steps)
	}
	for i, s := range wantSteps {
		if rec.steps[i] != s {
			t.Fatalf("动作顺序应为 %v，got %v", wantSteps, rec.steps)
		}
	}
	initial, minSize := ClampWindowSize(area.Size())
	absX, absY := ClampWindowToWorkArea(area, initial)
	if rec.minW != minSize.Width || rec.minH != minSize.Height {
		t.Fatalf("最小尺寸动作应为 %+v，got (%d,%d)", minSize, rec.minW, rec.minH)
	}
	if rec.setW != initial.Width || rec.setH != initial.Height {
		t.Fatalf("尺寸动作应为 %+v，got (%d,%d)", initial, rec.setW, rec.setH)
	}
	if rec.posX != absX-area.MonitorX || rec.posY != absY-area.MonitorY {
		t.Fatalf("位置动作应为 (%d,%d)，got (%d,%d)", absX-area.MonitorX, absY-area.MonitorY, rec.posX, rec.posY)
	}
	if rec.shows != 1 {
		t.Fatalf("应 Show 一次，got %d", rec.shows)
	}

	// once：再次触发不重放（导航/重载保持已应用状态）。
	app.maybeInitWindow()
	app.maybeInitWindow()
	if len(rec.steps) != len(wantSteps) || rec.shows != 1 {
		t.Fatalf("初始化只应应用一次，got steps=%v shows=%d", rec.steps, rec.shows)
	}
}

// 预检失败持久阻断：即使 DOM-ready 也不得应用窗口初始化。
func TestMaybeInitWindowBlockedByStartupFailure(t *testing.T) {
	app := NewApp(nil, nil)
	rec := &recordingInit{}
	app.SetWindowInitActions(rec)
	app.domReady.Store(true)
	app.Life.MarkStartupFailure()
	app.maybeInitWindow()
	if len(rec.steps) != 0 {
		t.Fatalf("预检失败不得应用窗口初始化，got %v", rec.steps)
	}
}
