package desktop

import (
	"testing"
	"time"
)

type fakeWindow struct {
	shows       int
	unminimises int
}

func (w *fakeWindow) Show()       { w.shows++ }
func (w *fakeWindow) Unminimise() { w.unminimises++ }

func TestRequestCloseNoUnsavedAllows(t *testing.T) {
	saver := NewSaveCoordinator(nil, nil)
	l := NewLifecycle(saver, &fakeWindow{}, nil)
	if l.RequestClose() != CloseAllow {
		t.Fatal("无未保存结果应允许关闭")
	}
}

func TestRequestCloseUnsavedConfirmCancelKeeps(t *testing.T) {
	saver := NewSaveCoordinator(nil, nil)
	l := NewLifecycle(saver, &fakeWindow{}, func(string, string) bool { return false })
	l.MarkUnsaved()
	if l.RequestClose() != CloseCancelledByUser {
		t.Fatal("取消确认应保持窗口")
	}
	if !l.HasUnsaved() {
		t.Fatal("取消后未保存状态应保持")
	}
}

func TestRequestCloseUnsavedConfirmProceeds(t *testing.T) {
	saver := NewSaveCoordinator(nil, nil)
	l := NewLifecycle(saver, &fakeWindow{}, func(string, string) bool { return true })
	l.MarkUnsaved()
	if l.RequestClose() != CloseAllow {
		t.Fatal("确认放弃应允许关闭")
	}
}

func TestDuringSaveAllClosePathsBlocked(t *testing.T) {
	gate := make(chan struct{})
	d := &blockingDialog{path: "", release: gate}
	saver := NewSaveCoordinator(d, nil)
	l := NewLifecycle(saver, &fakeWindow{}, func(string, string) bool { return true })
	l.MarkUnsaved()

	done := make(chan struct{})
	go func() { saver.Save("x.txt", b64("x")); close(done) }()
	for !saver.IsSaving() {
	}

	if l.RequestClose() != CloseBlockedSaving {
		t.Fatal("保存进行中窗口关闭应被阻止")
	}
	if err := l.BeforeExit(); err == nil {
		t.Fatal("保存进行中 /api/exit 应被阻止")
	}
	close(gate)
	<-done
	if l.RequestClose() != CloseAllow {
		t.Fatal("保存结束后应可退出")
	}
}

func TestBeforeExitNoSaveAllows(t *testing.T) {
	l := NewLifecycle(NewSaveCoordinator(nil, nil), nil, nil)
	if err := l.BeforeExit(); err != nil {
		t.Fatalf("无保存时退出守卫应放行：%v", err)
	}
}

func TestConfirmExitAvoidsSecondConfirm(t *testing.T) {
	// Warning A：MarkConfirmedExit 仅在已 reserve（权威接受，如 /api/exit 的 BeforeExit
	// 成功后 Done 通知）时生效；真实运行序列是 BeforeExit → MarkConfirmedExit → Quit→RequestClose。
	l := NewLifecycle(NewSaveCoordinator(nil, nil), &fakeWindow{}, func(string, string) bool {
		t.Fatal("已确认退出后不应再次弹确认")
		return false
	})
	l.MarkUnsaved()
	if err := l.BeforeExit(); err != nil {
		t.Fatalf("BeforeExit 应接受：%v", err)
	}
	l.MarkConfirmedExit()
	if l.RequestClose() != CloseAllow {
		t.Fatal("已确认退出应直接放行")
	}
}

func TestSecondInstanceWakeBeforeReadyQueues(t *testing.T) {
	w := &fakeWindow{}
	l := NewLifecycle(NewSaveCoordinator(nil, nil), w, nil)
	l.MarkStartupSuccess()
	l.HandleSecondInstance() // 未 ready
	if w.shows != 0 {
		t.Fatal("未 ready 时不应立即唤起")
	}
	l.MarkReady()
	if w.shows != 1 || w.unminimises != 1 {
		t.Fatalf("ready 后应补一次唤起：show=%d unmin=%d", w.shows, w.unminimises)
	}
}

func TestSecondInstanceWakeAfterReadyImmediate(t *testing.T) {
	w := &fakeWindow{}
	l := NewLifecycle(NewSaveCoordinator(nil, nil), w, nil)
	l.MarkStartupSuccess()
	l.MarkReady()
	l.HandleSecondInstance()
	if w.shows != 1 || w.unminimises != 1 {
		t.Fatalf("ready 后应立即唤起：show=%d unmin=%d", w.shows, w.unminimises)
	}
}

func TestNoDesktopIdle(t *testing.T) {
	// 桌面生命周期不提供 idle 关停：构造后即使超过配置时长也不会自行关停。
	l := NewLifecycle(NewSaveCoordinator(nil, nil), &fakeWindow{}, nil)
	time.Sleep(20 * time.Millisecond)
	if l.RequestClose() != CloseAllow {
		t.Fatal("不应存在空闲自动关停路径")
	}
}
