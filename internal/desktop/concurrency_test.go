package desktop

import "testing"

// R2-C3：新结果置位后，旧 generation 的 false 迟到不得清除 true。
func TestStaleUnsavedFalseRejected(t *testing.T) {
	l := NewLifecycle(NewSaveCoordinator(nil, nil), nil, nil)

	// 旧页面读取的 generation（初始 0）。
	oldGen := l.UnsavedGeneration()
	// 后端产出新结果：generation 推进并置 true。
	l.MarkUnsaved()
	if !l.HasUnsaved() {
		t.Fatal("新结果应置未保存")
	}
	// 旧页面迟到的 false 上报（携带旧 generation）必须被拒绝。
	l.SetUnsaved(oldGen, false)
	if !l.HasUnsaved() {
		t.Fatal("旧 generation 的 false 不得清除新结果 true")
	}
	// 当前 generation 的 false（保存完成后清空）才被接受。
	l.SetUnsaved(l.UnsavedGeneration(), false)
	if l.HasUnsaved() {
		t.Fatal("当前 generation 的 false 应被接受")
	}
}

// R2-C3：旧 generation 的 true 也应被拒绝，避免旧页面错误置位。
func TestStaleUnsavedTrueRejected(t *testing.T) {
	l := NewLifecycle(NewSaveCoordinator(nil, nil), nil, nil)
	gen := l.UnsavedGeneration()
	l.MarkUnsaved()
	l.SetUnsaved(l.UnsavedGeneration(), false)
	// 已清空后，旧 generation 的 true 不应复活状态。
	l.SetUnsaved(gen, true)
	if l.HasUnsaved() {
		t.Fatal("旧 generation 的 true 不应复活未保存状态")
	}
}

// R2-C4：关闭检查保留后，新的保存不得开始。
func TestCloseReservationBlocksNewSave(t *testing.T) {
	saver := NewSaveCoordinator(&fakeDialog{path: ""}, nil)
	l := NewLifecycle(saver, nil, nil)
	if l.RequestClose() != CloseAllow {
		t.Fatal("无未保存应允许关闭并保留")
	}
	if !saver.IsCloseReserved() {
		t.Fatal("接受关闭后应保留关闭状态")
	}
	res := saver.Save("x.txt", b64("x"))
	if res.Status != SaveStatusError {
		t.Fatalf("保留关闭后不得开始保存：%+v", res)
	}
}

// R2-C4：取消关闭应释放保留，之后可继续保存。
func TestCancelledCloseReleasesReservation(t *testing.T) {
	saver := NewSaveCoordinator(&fakeDialog{path: ""}, nil)
	l := NewLifecycle(saver, nil, func(string, string) bool { return false })
	l.MarkUnsaved()
	if l.RequestClose() != CloseCancelledByUser {
		t.Fatal("取消确认应返回 CloseCancelledByUser")
	}
	if saver.IsCloseReserved() {
		t.Fatal("取消关闭应释放保留")
	}
	if !l.HasUnsaved() {
		t.Fatal("取消关闭应保留未保存状态与结果")
	}
	// 释放后保存可进行。
	if res := saver.Save("x.txt", "!!!bad"); res.Status != SaveStatusError {
		if saver.IsCloseReserved() {
			t.Fatal("不应仍保留关闭")
		}
	}
}

// R2-C4：确认期间（保留关闭）不得开始保存。
func TestSaveRejectedDuringCloseConfirm(t *testing.T) {
	saver := NewSaveCoordinator(&fakeDialog{path: ""}, nil)
	// confirm 回调期间尝试保存，应被拒绝（已保留关闭）。
	var duringSaveErr bool
	l := NewLifecycle(saver, nil, func(string, string) bool {
		res := saver.Save("y.txt", b64("y"))
		duringSaveErr = res.Status == SaveStatusError
		return true
	})
	l.MarkUnsaved()
	if l.RequestClose() != CloseAllow {
		t.Fatal("确认后应允许关闭")
	}
	if !duringSaveErr {
		t.Fatal("关闭确认期间新保存应被拒绝")
	}
}

// R2-C4：退出被后端拒绝后释放保留，可再次退出与操作。
func TestRejectedExitReservationReset(t *testing.T) {
	saver := NewSaveCoordinator(nil, nil)
	l := NewLifecycle(saver, nil, nil)
	if err := l.BeforeExit(); err != nil {
		t.Fatal("首次退出应放行")
	}
	if !saver.IsCloseReserved() {
		t.Fatal("接受退出应保留关闭")
	}
	// 模拟后端拒绝：释放保留、撤销确认标志。
	l.ResetCloseReservation()
	if saver.IsCloseReserved() {
		t.Fatal("拒绝退出后应释放保留")
	}
	if l.confirmedExit.Load() {
		t.Fatal("拒绝退出不得留下已确认退出标志")
	}
	// 再次退出应可重新保留。
	if err := l.BeforeExit(); err != nil {
		t.Fatalf("释放后再次退出应放行：%v", err)
	}
}
