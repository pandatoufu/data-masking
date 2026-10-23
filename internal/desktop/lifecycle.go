package desktop

import (
	"errors"
	"sync"
	"sync/atomic"
)

// CloseAction 是关闭/退出请求的裁决结果。
type CloseAction int

const (
	// CloseAllow 允许关停（提交器已确认或没有未保存结果）。
	CloseAllow CloseAction = iota
	// CloseBlockedSaving 保存进行中或已有关闭/退出保留，必须阻止关停。
	CloseBlockedSaving
	// CloseCancelledByUser 用户在放弃确认中取消，必须保持窗口与结果。
	CloseCancelledByUser
)

// WindowActions 抽象窗口操作，便于无 GUI 测试注入。
type WindowActions interface {
	Show()
	Unminimise()
}

// StartupState 是启动预检状态。
type StartupState int

const (
	startupPending StartupState = iota
	startupSuccess
	startupFailure
)

// legacyUnsavedCloseMessage 是工作区未启用时“存在未保存结果”关闭确认的既有文案。
const legacyUnsavedCloseMessage = "存在尚未保存的结果。选择“是”将放弃这些结果并退出；选择“否”将返回应用，结果仍可保存。"

// Lifecycle 统一关闭/退出、未保存状态、启动屏障与单实例唤起协调。
// OnBeforeClose 与 /api/exit 共用同一裁决，且与 SaveCoordinator 共享原子保留，
// 保证“关闭检查后不可再开始保存、确认期间不可开始保存”。
type Lifecycle struct {
	saver  *SaveCoordinator
	window WindowActions
	// confirm 返回 true 表示用户确认放弃未保存结果。无 GUI 或测试可注入。
	confirm func(title, message string) bool

	closeMsgMu sync.RWMutex
	// closeMsg 返回存在未落盘结果时关闭确认的文案；nil 时用既有文案。
	// 工作区启用时（Windows/Linux 契约相同）由 App 注入统一未落盘文案。
	closeMsg func() string
	// closeTitle 返回关闭确认对话框标题；nil 时用既有中文标题。
	closeTitle func() string

	confirmedExit atomic.Bool
	ready         atomic.Bool
	pendingWake   atomic.Bool
	startup       atomic.Int32 // StartupState

	// unsavedMu 保护 unsavedGen 与 unsaved，使 generation 与标志的检查/更新原子，
	// 消除“检查 gen 与 store 分离”的 TOCTOU（旧 false 覆盖新 true）。
	unsavedMu  sync.Mutex
	unsavedGen uint64
	unsaved    bool

	closeMu sync.Mutex
	wakeMu  sync.Mutex
}

// NewLifecycle 构造生命周期协调器。
func NewLifecycle(saver *SaveCoordinator, window WindowActions, confirm func(title, message string) bool) *Lifecycle {
	if confirm == nil {
		confirm = func(string, string) bool { return false }
	}
	l := &Lifecycle{saver: saver, window: window, confirm: confirm}
	l.startup.Store(int32(startupPending))
	return l
}

// SetUnsavedCloseMessage 注入存在未落盘结果时关闭确认的文案提供者；
// nil 回退既有文案。工作区启用时由 App 注入统一未落盘文案（两平台一致）。
func (l *Lifecycle) SetUnsavedCloseMessage(fn func() string) {
	l.closeMsgMu.Lock()
	l.closeMsg = fn
	l.closeMsgMu.Unlock()
}

func (l *Lifecycle) closeMessage() string {
	l.closeMsgMu.RLock()
	fn := l.closeMsg
	l.closeMsgMu.RUnlock()
	if fn != nil {
		return fn()
	}
	return legacyUnsavedCloseMessage
}

// SetConfirmTitle 注入关闭确认对话框标题提供者；nil 回退既有中文标题。
func (l *Lifecycle) SetConfirmTitle(fn func() string) {
	l.closeMsgMu.Lock()
	l.closeTitle = fn
	l.closeMsgMu.Unlock()
}

func (l *Lifecycle) confirmDialogTitle() string {
	l.closeMsgMu.RLock()
	fn := l.closeTitle
	l.closeMsgMu.RUnlock()
	if fn != nil {
		return fn()
	}
	return "退出应用"
}

// --- 启动屏障（C5）---

// MarkStartupSuccess 标记预检成功，仅此后允许窗口初始化/显示/唤起。
func (l *Lifecycle) MarkStartupSuccess() {
	l.startup.Store(int32(startupSuccess))
	if l.pendingWake.CompareAndSwap(true, false) {
		l.wake()
	}
}

// MarkStartupFailure 标记预检失败并持久保留；失败后不得显示窗口，后续唤起被忽略。
func (l *Lifecycle) MarkStartupFailure() { l.startup.Store(int32(startupFailure)) }

// StartupSucceeded 报告预检是否已成功；pending/failure 均返回 false。
func (l *Lifecycle) StartupSucceeded() bool { return StartupState(l.startup.Load()) == startupSuccess }

// StartupFailed 报告预检是否已失败。
func (l *Lifecycle) StartupFailed() bool { return StartupState(l.startup.Load()) == startupFailure }

// --- 未保存状态（C3）---

// SetUnsaved 由前端在结果/保存状态变化时上报是否存在未保存产物。
// gen 为前端结果/页面 generation；若早于当前已记录 generation，则忽略该过期上报，
// 避免旧 false 异步到达覆盖新结果的 true。检查与更新在同一临界区内完成（无 TOCTOU）。
func (l *Lifecycle) SetUnsaved(gen uint64, v bool) {
	l.unsavedMu.Lock()
	defer l.unsavedMu.Unlock()
	if gen < l.unsavedGen {
		return // 过期上报，拒绝清除/覆盖
	}
	if gen > l.unsavedGen {
		l.unsavedGen = gen
	}
	l.unsaved = v
}

// MarkUnsaved 由后端在成功产出待保存产物时权威置位：推进 generation 并置 true。
// 返回本次原子 token（新 generation），随业务成功响应下发前端携带上报；
// 旧结果页面持有的旧 token 其 false 上报将被拒绝。
func (l *Lifecycle) MarkUnsaved() uint64 {
	l.unsavedMu.Lock()
	defer l.unsavedMu.Unlock()
	l.unsavedGen++
	l.unsaved = true
	return l.unsavedGen
}

// HasUnsaved 报告当前是否存在未保存产物。
func (l *Lifecycle) HasUnsaved() bool {
	l.unsavedMu.Lock()
	defer l.unsavedMu.Unlock()
	return l.unsaved
}

// UnsavedGeneration 返回当前未保存 generation（测试/诊断用）。
func (l *Lifecycle) UnsavedGeneration() uint64 {
	l.unsavedMu.Lock()
	defer l.unsavedMu.Unlock()
	return l.unsavedGen
}

// --- 关闭/退出（C4）---

// RequestClose 裁决一次窗口关闭/退出：
//  1. 若退出已被接受（confirmedExit，如 /api/exit 成功或此前已确认关闭），直接放行；
//     此时关闭保留仍由该已接受路径持有，不再二次保留，避免“已保留→再保留失败”死锁。
//  2. 否则原子保留关闭；保存中或已保留则阻止（此后不可再开始保存）。
//  3. 有未保存时弹确认；取消则释放保留、保留结果。
//  4. 接受时保留关闭并仅在此时提交 confirmedExit。
func (l *Lifecycle) RequestClose() CloseAction {
	// 已接受退出：仅当确实持有“已保留关闭”时才放行（避免客户端误置 confirmed 绕过保存守卫）。
	if l.confirmedExit.Load() && l.closeReserved() {
		return CloseAllow
	}
	if !l.reserveClose() {
		return CloseBlockedSaving
	}
	if l.HasUnsaved() {
		// 原生 Question 对话框只有“是/否”：是=放弃并退出，否=保留结果。
		if !l.confirm(l.confirmDialogTitle(), l.closeMessage()) {
			l.releaseClose()
			return CloseCancelledByUser
		}
	}
	l.confirmedExit.Store(true)
	return CloseAllow
}

// BeforeExit 是 /api/exit 的退出守卫：保存中或已保留关闭时拒绝，否则原子保留关闭并接受退出。
// 注意：接受后 confirmedExit=true，后续 OnBeforeClose 经 RequestClose 直接放行（不重复保留）。
func (l *Lifecycle) BeforeExit() error {
	if !l.reserveClose() {
		return errors.New("保存进行中，暂时无法退出")
	}
	l.confirmedExit.Store(true)
	return nil
}

func (l *Lifecycle) reserveClose() bool {
	l.closeMu.Lock()
	defer l.closeMu.Unlock()
	if l.saver == nil {
		return true
	}
	return l.saver.ReserveClose()
}

func (l *Lifecycle) releaseClose() {
	l.closeMu.Lock()
	defer l.closeMu.Unlock()
	if l.saver != nil {
		l.saver.ReleaseClose()
	}
}

// ResetCloseReservation 在退出被后端拒绝后释放保留，使应用可继续操作并允许后续退出。
func (l *Lifecycle) ResetCloseReservation() {
	l.releaseClose()
	l.confirmedExit.Store(false)
}

// MarkConfirmedExit 标记本次退出已获得用户确认，避免窗口二次确认。
// 仅在已持有“已保留关闭”（即退出已被接受）时生效；否则忽略，
// 防止客户端在保存进行中误置 confirmed 从而绕过保存守卫。
func (l *Lifecycle) MarkConfirmedExit() {
	if !l.closeReserved() {
		return
	}
	l.confirmedExit.Store(true)
}

func (l *Lifecycle) closeReserved() bool {
	if l.saver == nil {
		return true
	}
	return l.saver.IsCloseReserved()
}

// --- 单实例唤起 ---

// MarkReady 标记窗口已可交互；若启动尚未 ready 时收到过唤起，则此时补一次唤起。
// 仅在启动预检成功后有效；失败时不得唤起。
func (l *Lifecycle) MarkReady() {
	if !l.StartupSucceeded() {
		return
	}
	l.ready.Store(true)
	if l.pendingWake.CompareAndSwap(true, false) {
		l.wake()
	}
}

// HandleSecondInstance 处理重复启动：已 ready 时立即唤起，未 ready 时排队一次；
// 启动失败时忽略（不展示业务窗口）。
func (l *Lifecycle) HandleSecondInstance() {
	if l.StartupFailed() {
		return
	}
	if !l.ready.Load() {
		l.pendingWake.Store(true)
		return
	}
	l.wake()
}

func (l *Lifecycle) wake() {
	l.wakeMu.Lock()
	defer l.wakeMu.Unlock()
	if l.window == nil {
		return
	}
	l.window.Unminimise()
	l.window.Show()
}
