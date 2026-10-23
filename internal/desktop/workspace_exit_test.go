package desktop

import (
	"testing"

	"github.com/pandatoufu/data-masking/internal/i18n"
)

// 工作区在真实平台可用时使用统一未落盘文案（Linux 不再回退旧 Windows-only 分支）。
func TestWorkspaceCloseMessageOnCurrentPlatform(t *testing.T) {
	app := NewApp(nil, nil)
	w := NewWorkspace()
	w.SetRoot(t.TempDir())
	app.SetWorkspace(w)
	app.InitWorkspace(nil)
	if !w.Enabled() {
		t.Fatalf("当前平台工作区应可用：%v", w.DisabledReason())
	}
	got := app.workspaceCloseMessage()
	want := i18n.T(app.Language(), "native.confirm_close_workspace", nil)
	if got != want {
		t.Fatalf("工作区启用应使用统一未落盘文案：%q want %q", got, want)
	}
	if got == i18n.T(app.Language(), "native.confirm_close_legacy", nil) {
		t.Fatal("不应回退旧的平台差异文案")
	}
}

// 跨平台保存能力语义：自动成功静默退出、失败未保存确认、手动另存清除、忙态拒退出。
func TestExitSemanticsWithWorkspace(t *testing.T) {
	app := NewApp(nil, nil)
	w := NewWorkspace()
	w.SetRoot(t.TempDir())
	app.SetWorkspace(w)
	app.InitWorkspace(nil)
	if !w.Enabled() {
		t.Fatalf("当前平台工作区应可用：%v", w.DisabledReason())
	}

	confirmCalls := 0
	confirmResult := false
	app.SetConfirm(func(title, message string) bool {
		confirmCalls++
		return confirmResult
	})

	// 自动落盘成功视为已保存：无未保存 → 静默退出。
	gen := app.Life.MarkUnsaved()
	app.Life.SetUnsaved(gen, false)
	if action := app.Life.RequestClose(); action != CloseAllow {
		t.Fatalf("自动落盘成功应静默放行：%v", action)
	}
	if confirmCalls != 0 {
		t.Fatalf("不应弹未落盘确认：calls=%d", confirmCalls)
	}

	// 自动落盘失败且未另存：确认；取消保持窗口与结果。
	app.Life.ResetCloseReservation()
	_ = app.Life.MarkUnsaved()
	if action := app.Life.RequestClose(); action != CloseCancelledByUser {
		t.Fatalf("取消确认应保留结果：%v", action)
	}
	if confirmCalls != 1 {
		t.Fatalf("应弹一次未落盘确认：calls=%d", confirmCalls)
	}

	// 确认放弃则放行。
	confirmResult = true
	if action := app.Life.RequestClose(); action != CloseAllow {
		t.Fatalf("确认放弃应放行：%v", action)
	}

	// 手动另存成功清除对应未保存状态：再次关闭静默。
	app.Life.ResetCloseReservation()
	gen = app.Life.MarkUnsaved()
	app.Life.SetUnsaved(gen, false)
	before := confirmCalls
	if action := app.Life.RequestClose(); action != CloseAllow || confirmCalls != before {
		t.Fatalf("手动另存成功后应静默放行：action=%v calls=%d", action, confirmCalls)
	}

	// 处理中忙态仍阻止退出（保护继续适用）。
	app.Life.ResetCloseReservation()
	if !app.Saver.ReserveOperation() {
		t.Fatal("前置：应能保留一次业务处理")
	}
	if action := app.Life.RequestClose(); action != CloseBlockedSaving {
		t.Fatalf("处理中应阻止退出：%v", action)
	}
	app.Saver.ReleaseOperation()
}
