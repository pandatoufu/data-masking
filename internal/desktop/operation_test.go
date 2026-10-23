package desktop

import "testing"

// 处理保留与保存/关闭共享同一原子状态：处理中不能保存/关闭，关闭保留后不能处理。
func TestOperationReservationCoordinatesWithSaveAndClose(t *testing.T) {
	saver := NewSaveCoordinator(nil, nil)

	if !saver.ReserveOperation() {
		t.Fatal("空闲时应可开始处理")
	}
	if saver.ReserveOperation() {
		t.Fatal("处理中不应再次开始处理")
	}
	if saver.ReserveClose() {
		t.Fatal("处理中不应能保留关闭")
	}
	if !saver.IsProcessing() {
		t.Fatal("应处于处理中")
	}
	// 处理中保存被拒绝。
	res := saver.Save("a.txt", b64("x"))
	if res.Status != SaveStatusError {
		t.Fatalf("处理中保存应被拒绝：%+v", res)
	}

	// 处理完成释放后，保存与关闭恢复可用。
	saver.ReleaseOperation()
	if saver.IsProcessing() {
		t.Fatal("释放后不应仍处理中")
	}
	if !saver.ReserveClose() {
		t.Fatal("空闲后应可保留关闭")
	}
	if saver.ReserveOperation() {
		t.Fatal("关闭保留期间不应能开始处理")
	}
	saver.ReleaseClose()
	if !saver.ReserveOperation() {
		t.Fatal("释放关闭保留后应可处理")
	}
	saver.ReleaseOperation()
}

// 错误释放：处理保留必须可由完整响应路径释放一次，且幂等不破坏后续。
func TestOperationReleaseIsForgiving(t *testing.T) {
	saver := NewSaveCoordinator(nil, nil)
	if !saver.ReserveOperation() {
		t.Fatal("应可开始处理")
	}
	saver.ReleaseOperation()
	saver.ReleaseOperation() // 重复释放不应 panic；状态已 idle
	if !saver.ReserveOperation() {
		t.Fatal("重复释放后仍应可处理")
	}
	saver.ReleaseOperation()
}

// Lifecycle 在处理的处理中状态下拒绝关闭与退出，且不误提交 confirmedExit。
func TestLifecycleBlocksCloseWhileProcessing(t *testing.T) {
	saver := NewSaveCoordinator(nil, nil)
	life := NewLifecycle(saver, nil, func(string, string) bool { return true })
	if !saver.ReserveOperation() {
		t.Fatal("应可开始处理")
	}
	if action := life.RequestClose(); action != CloseBlockedSaving {
		t.Fatalf("处理中关闭应被阻止：%v", action)
	}
	if err := life.BeforeExit(); err == nil {
		t.Fatal("处理中 /api/exit 应被拒绝")
	}
	saver.ReleaseOperation()
	if err := life.BeforeExit(); err != nil {
		t.Fatalf("处理完成后退出应被接受：%v", err)
	}
}
