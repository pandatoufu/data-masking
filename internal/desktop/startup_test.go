package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

// R2-C5：OnDomReady 早于预检完成时不得显示/ready；成功后才放行。
func TestStartupBarrierDomReadyBeforeCheck(t *testing.T) {
	w := &fakeWindow{}
	l := NewLifecycle(NewSaveCoordinator(nil, nil), w, nil)

	// DOM-ready 先到，但预检尚未完成：MarkReady 无效。
	l.MarkReady()
	if l.StartupSucceeded() {
		t.Fatal("预检未完成不应为成功")
	}
	if w.shows != 0 {
		t.Fatal("预检未完成不得显示窗口")
	}

	// 预检成功后才可 ready 并唤起排队的第二实例。
	l.HandleSecondInstance() // 排队
	if w.shows != 0 {
		t.Fatal("未 ready 不得立即唤起")
	}
	l.MarkStartupSuccess()
	l.MarkReady()
	if !l.StartupSucceeded() {
		t.Fatal("成功后应为成功")
	}
	if w.shows != 1 || w.unminimises != 1 {
		t.Fatalf("成功后应唤起：show=%d unmin=%d", w.shows, w.unminimises)
	}
}

// R2-C5：预检失败后不得显示、不得唤起；失败状态持久。
func TestStartupFailurePersistsAndBlocksWake(t *testing.T) {
	w := &fakeWindow{}
	l := NewLifecycle(NewSaveCoordinator(nil, nil), w, nil)
	l.MarkStartupFailure()

	l.MarkReady() // 失败后即使 ready 也不得显示/唤起
	if l.StartupSucceeded() {
		t.Fatal("失败后不应为成功")
	}
	if !l.StartupFailed() {
		t.Fatal("应持久失败")
	}
	l.HandleSecondInstance()
	if w.shows != 0 || w.unminimises != 0 {
		t.Fatal("失败后不得唤起窗口")
	}
}

// R2-C5：失败后到达的 DOM-ready 也不得显示。
func TestStartupFailureBlocksDomReady(t *testing.T) {
	w := &fakeWindow{}
	l := NewLifecycle(NewSaveCoordinator(nil, nil), w, nil)
	l.MarkStartupFailure()
	l.MarkReady()
	if w.shows != 0 {
		t.Fatal("失败后 DOM-ready 不得显示窗口")
	}
}

// 工作区降级只关闭自动落盘并提示一次，不影响启动成功与手动另存。
func TestWorkspaceDegradeDoesNotBlockStartupOrManualSave(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, workspaceDirMasked), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	app := NewApp(nil, nil)
	w := &Workspace{ops: OSFileOps{}}
	w.SetRoot(root)
	app.SetWorkspace(w)

	notices := 0
	app.InitWorkspace(func(string) { notices++ })
	if notices != 1 {
		t.Fatalf("降级应提示一次，实际 %d", notices)
	}
	// 降级不得影响手动另存。
	dest := filepath.Join(t.TempDir(), "out.txt")
	app.SetSaveDialog(&fakeDialog{path: dest})
	if res := app.Saver.Save("out.txt", b64("data")); res.Status != SaveStatusSaved {
		t.Fatalf("降级不得影响手动另存：%+v", res)
	}
	// 启动屏障不受影响。
	app.Life.MarkStartupSuccess()
	if !app.Life.StartupSucceeded() {
		t.Fatal("降级不得阻止启动成功")
	}
}
