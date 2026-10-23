package desktop

import (
	"sync"
	"testing"
)

// R2/GLM-CRITICAL-A：/api/exit 接受（BeforeExit==nil）后，OnBeforeClose 的 RequestClose
// 必须直接 CloseAllow，不能因“已保留关闭”再次 ReserveClose 失败而死锁。
// 同时确认：接受前新保存被禁止；确认取消可释放并重试。
func TestAcceptedExitThenRequestCloseAllows(t *testing.T) {
	saver := NewSaveCoordinator(&fakeDialog{path: ""}, nil)
	l := NewLifecycle(saver, nil, func(string, string) bool { return false /* 不应被调用 */ })
	l.MarkUnsaved()

	// 前端退出：后端接受，保留关闭并提交确认。
	if err := l.BeforeExit(); err != nil {
		t.Fatalf("接受退出应返回 nil：%v", err)
	}
	if !saver.IsCloseReserved() {
		t.Fatal("接受退出应保留关闭")
	}
	// 接受后新保存不得开始。
	if res := saver.Save("x.txt", b64("x")); res.Status != SaveStatusError {
		t.Fatalf("接受退出后不得开始保存：%+v", res)
	}
	// SDK Quit 会调用 OnBeforeClose -> RequestClose：必须 CloseAllow（不死锁）。
	if got := l.RequestClose(); got != CloseAllow {
		t.Fatalf("已接受退出后 RequestClose 应 CloseAllow，got %v", got)
	}
	// 再次调用也应稳定放行。
	if got := l.RequestClose(); got != CloseAllow {
		t.Fatalf("重复 RequestClose 应稳定 CloseAllow，got %v", got)
	}
}

// 接受前不得误放行：保存进行中 BeforeExit 必须拒绝。
func TestBeforeExitRejectedDuringSave(t *testing.T) {
	saver := NewSaveCoordinator(&fakeDialog{path: ""}, nil)
	l := NewLifecycle(saver, nil, nil)
	gate := make(chan struct{})
	saver.SetDialog(&blockingDialog{path: "", release: gate})
	done := make(chan struct{})
	go func() { saver.Save("x.txt", b64("x")); close(done) }()
	for !saver.IsSaving() {
	}
	if err := l.BeforeExit(); err == nil {
		t.Fatal("保存中 BeforeExit 应拒绝")
	}
	if l.confirmedExit.Load() {
		t.Fatal("被拒绝的退出不得置 confirmedExit")
	}
	close(gate)
	<-done
	// 保存结束后可接受退出。
	if err := l.BeforeExit(); err != nil {
		t.Fatalf("保存结束后应可退出：%v", err)
	}
	if l.RequestClose() != CloseAllow {
		t.Fatal("接受后 RequestClose 应放行")
	}
}

// 关闭确认取消后可重试关闭；未保存结果保留。
func TestExitConfirmCancelThenRetry(t *testing.T) {
	saver := NewSaveCoordinator(nil, nil)
	calls := 0
	l := NewLifecycle(saver, nil, func(string, string) bool {
		calls++
		return calls > 1 // 第一次取消，第二次确认
	})
	l.MarkUnsaved()

	if got := l.RequestClose(); got != CloseCancelledByUser {
		t.Fatalf("首次应取消，got %v", got)
	}
	if saver.IsCloseReserved() {
		t.Fatal("取消应释放保留")
	}
	if !l.HasUnsaved() {
		t.Fatal("取消应保留未保存结果")
	}
	if got := l.RequestClose(); got != CloseAllow {
		t.Fatalf("重试确认后应放行，got %v", got)
	}
}

// W1：generation 与 unsaved 标志在同一临界区，旧 token 的 false 不得覆盖新 true。
// 用并发压力显式验证不因 load/store 分离而误清。
func TestUnsavedTokenConcurrentStaleFalseRejected(t *testing.T) {
	l := NewLifecycle(NewSaveCoordinator(nil, nil), nil, nil)
	oldGen := l.UnsavedGeneration() // 旧页面持有的 token（0）

	// 后端产出新结果，拿到新 token。
	newGen := l.MarkUnsaved()
	if newGen == oldGen {
		t.Fatal("MarkUnsaved 应推进 token")
	}
	if !l.HasUnsaved() {
		t.Fatal("新结果应置未保存")
	}

	// 旧页面迟到的 false（旧 token）与新页面正确上报并发；结果必须始终为 true。
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.SetUnsaved(oldGen, false) // 过期，应被拒绝
		}()
	}
	wg.Wait()
	if !l.HasUnsaved() {
		t.Fatal("并发旧 token false 不得清除新结果 true")
	}

	// 当前 token 的 false 才可清除。
	l.SetUnsaved(newGen, false)
	if l.HasUnsaved() {
		t.Fatal("当前 token 的 false 应被接受")
	}
}

// Warning A 回归：Bridge.SetExitConfirmed（客户端 flag）在保存进行中不得生效。
// MarkConfirmedExit 仅在已持有“已保留关闭”（权威接受）时允许；保存中（stateSaving、
// 未 reserve）调用必须被忽略，随后窗口关闭 RequestClose 仍被阻止，不能绕过保存守卫。
func TestSetExitConfirmedRejectedWhileSaving(t *testing.T) {
	app := NewApp(nil, nil)
	l := app.Life
	gate := make(chan struct{})
	app.SetSaveDialog(&blockingDialog{path: "", release: gate})
	done := make(chan struct{})
	go func() { app.Saver.Save("x.txt", b64("x")); close(done) }()
	for !app.Saver.IsSaving() {
	}

	// 保存中：任意客户端经 Bridge 提交“已确认退出”——必须被忽略。
	app.Bridge().SetExitConfirmed()
	if l.confirmedExit.Load() {
		t.Fatal("保存中 SetExitConfirmed 不得置 confirmedExit")
	}
	if got := l.RequestClose(); got != CloseBlockedSaving {
		t.Fatalf("保存中窗口关闭应被阻止（不被客户端 flag 绕过），got %v", got)
	}
	// 保存中的 /api/exit 守卫同样拒绝。
	if err := l.BeforeExit(); err == nil {
		t.Fatal("保存中 BeforeExit 应拒绝")
	}

	// 保存结束后：权威接受退出 → SetExitConfirmed 生效 → Quit 的 RequestClose 放行。
	close(gate)
	<-done
	if err := l.BeforeExit(); err != nil {
		t.Fatalf("保存结束后应可接受退出：%v", err)
	}
	app.Bridge().SetExitConfirmed()
	if !l.confirmedExit.Load() {
		t.Fatal("已 reserve（权威接受）后 SetExitConfirmed 应生效")
	}
	if got := l.RequestClose(); got != CloseAllow {
		t.Fatalf("接受后 RequestClose 应放行，got %v", got)
	}
}

// Warning A 回归：未 reserve（空闲、无退出请求）时 SetExitConfirmed 不得提前放行关闭。
// 后续 RequestClose 仍走正常未保存确认流程（不被未授权 flag 跳过）。
func TestSetExitConfirmedIgnoredWithoutReserve(t *testing.T) {
	app := NewApp(nil, nil)
	l := app.Life
	confirmCalled := false
	l.confirm = func(string, string) bool { confirmCalled = true; return false }

	l.MarkUnsaved()
	app.Bridge().SetExitConfirmed() // 客户端在未发起/未被接受退出时误置
	if l.confirmedExit.Load() {
		t.Fatal("未 reserve 时 SetExitConfirmed 必须被忽略")
	}
	if got := l.RequestClose(); got != CloseCancelledByUser {
		t.Fatalf("未授权 flag 不得跳过未保存确认，got %v", got)
	}
	if !confirmCalled {
		t.Fatal("未保存确认对话框仍应被咨询")
	}
}

// Warning A 回归：完整前端退出序列 —— 前端确认放弃 → /api/exit（BeforeExit 权威接受并 reserve）
// → Done 通知 MarkConfirmedExit → SDK Quit 触发 OnBeforeClose(RequestClose) 必须放行（不二次确认、不死锁）。
func TestCompleteExitSequenceViaBridge(t *testing.T) {
	app := NewApp(nil, nil)
	l := app.Life
	confirmCalls := 0
	l.confirm = func(string, string) bool { confirmCalls++; return true } // 用户确认放弃
	l.MarkUnsaved()

	// 1) 前端确认放弃（confirmDiscard）后请求 /api/exit → BeforeExit 接受。
	if err := l.BeforeExit(); err != nil {
		t.Fatalf("BeforeExit 应接受：%v", err)
	}
	// 接受后不可再开始保存（写入期间退出路径不产生并行保存）。
	if res := app.Saver.Save("x.txt", b64("x")); res.Status != SaveStatusError {
		t.Fatalf("接受退出后不得开始保存：%+v", res)
	}
	// 2) Done 通知（wails_desktop onStartup goroutine）→ MarkConfirmedExit。
	l.MarkConfirmedExit()
	if !l.confirmedExit.Load() {
		t.Fatal("权威接受后 MarkConfirmedExit 应提交确认")
	}
	// 3) runtime.Quit → OnBeforeClose → RequestClose：直接放行，且不再弹确认。
	if got := l.RequestClose(); got != CloseAllow {
		t.Fatalf("完整退出序列末尾 RequestClose 应放行，got %v", got)
	}
	if confirmCalls != 0 {
		t.Fatalf("已确认退出后不得再次弹放弃确认（called=%d）", confirmCalls)
	}
}
