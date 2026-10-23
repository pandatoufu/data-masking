// Package desktop 承载桌面宿主的无 Wails 逻辑：保存器、生命周期、屏幕策略与路由适配，
// 以及带 desktop build tag 的 Wails adapter。无 tag 时普通业务测试不依赖任何图形库。
package desktop

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/pandatoufu/data-masking/internal/audit"
	"github.com/pandatoufu/data-masking/internal/config"
	"github.com/pandatoufu/data-masking/internal/i18n"
	"github.com/pandatoufu/data-masking/internal/server"
	"github.com/pandatoufu/data-masking/internal/uiprefs"
	"github.com/pandatoufu/data-masking/internal/workspace"
)

// App 聚合桌面宿主状态，提供有限的绑定方法（保存、未保存状态、退出确认、偏好、
// 固定目录自动落盘与四类受限目录读取）。
// 它不复制业务处理器：三个业务 API 仍由同一 server.Handler 承载。
type App struct {
	Cfg     *config.Config
	Audit   *audit.Log
	Srv     *server.Server
	Handler http.Handler
	Saver   *SaveCoordinator
	Life    *Lifecycle

	// Workspace 是 exe 所在目录的跨平台自动落盘工作区（Windows/Linux 契约相同）。
	Workspace *Workspace

	// Input 是 GUI 专用固定目录输入服务（original/cards/pending/mapping 的直属
	// 列表与显式单文件读取）；不暴露任意路径，也不依赖 MCP 业务流程。
	Input *WorkspaceInput

	// workspaceNoticeOnce 保证工作区不可用提示在启动期只弹一次。
	workspaceNoticeOnce sync.Once

	ctxMu sync.RWMutex
	ctx   context.Context

	sessionBus func() error

	// workArea 返回包含给定坐标的显示器可用工作区（逻辑像素）。默认按平台实现，测试可注入。
	workArea WorkAreaProvider

	// windowPos 返回窗口当前的逻辑屏幕坐标，用于对齐“工作区显示器”与“窗口显示器”。
	// 默认 (0,0)；Wails 适配注入 runtime.WindowGetPosition。
	windowPos func() (x, y int)

	// setPos 把布局绝对逻辑坐标转换为当前平台 Wails WindowSetPosition 所需坐标。
	// 默认按平台（Windows 转相对 rcWork 的物理偏移；Linux 转相对 geometry 原点的逻辑偏移）。测试可注入。
	setPos func(absX, absY int, area WorkAreaRect) (x, y int)

	// winActions 抽象窗口初始化原生动作（Min→Size→Position→Show）；由 Wails 适配注入。
	// headless/无 GUI 时为 nil，maybeInitWindow 不执行原生动作。
	winActions WindowInitActions

	// windowInit 确保初次窗口尺寸/最小尺寸/Show 仅执行一次，后续页面导航不重置窗口。
	windowInit sync.Once

	// domReady 记录 WebView DOM 是否已就绪，用于与启动预检做顺序无关的屏障。
	domReady atomic.Bool

	// prefsStore 是独立 UI 偏好存储；prefs 是当前内存偏好。
	prefsStore *uiprefs.Store
	prefsMu    sync.RWMutex
	prefs      uiprefs.Prefs

	// titleSetter 把本地化标题应用到原生窗口（Wails 适配注入）。
	titleSetter func(string)
}

// Load 以与既有入口一致的路径加载用户 config、Audit 与独立 UI 偏好，再构造桌面 App。
// 偏好在此读取（早于 Wails options 构建），确保启动期标题/依赖提示语言正确。
func Load() *App {
	cfg := config.Defaults()
	if p, err := config.UserConfigPath(); err == nil {
		cfg = config.Load(p)
	}
	var auditLog *audit.Log
	if p, err := audit.DefaultPath(); err == nil {
		auditLog = audit.New(p)
	}
	sys := uiprefs.SystemLanguage()
	path, err := uiprefs.DefaultPath()
	if err != nil {
		path = ""
	}
	return NewAppWithPrefs(cfg, auditLog, uiprefs.NewStore(path, sys))
}

// NewApp 用给定 config/Audit 构造桌面 App；nil 配置回退默认。
// 使用默认路径与当前系统语言加载 UI 偏好（测试/兼容入口）。
func NewApp(cfg *config.Config, auditLog *audit.Log) *App {
	sys := uiprefs.SystemLanguage()
	path, err := uiprefs.DefaultPath()
	if err != nil {
		path = ""
	}
	return NewAppWithPrefs(cfg, auditLog, uiprefs.NewStore(path, sys))
}

// NewAppWithPrefs 用给定 config/Audit/偏好存储构造桌面 App；nil 配置回退默认。
func NewAppWithPrefs(cfg *config.Config, auditLog *audit.Log, store *uiprefs.Store) *App {
	if cfg == nil {
		cfg = config.Defaults()
	}
	if store == nil {
		store = uiprefs.NewStore("", i18n.ZH)
	}
	prefs := store.Load()
	saver := NewSaveCoordinator(nil, nil)
	life := NewLifecycle(saver, nil, nil)
	srv := server.New(cfg, auditLog,
		server.WithDesktopMode(),
		server.WithExitGuard(life.BeforeExit),
		server.WithArtifactsCallback(life.MarkUnsaved),
		server.WithOperationGuard(saver.ReserveOperation, saver.ReleaseOperation),
		server.WithUIPrefs(prefs.Theme, prefs.Language),
	)
	app := &App{
		Cfg:        cfg,
		Audit:      auditLog,
		Srv:        srv,
		Saver:      saver,
		Life:       life,
		Workspace:  NewWorkspace(),
		prefsStore: store,
		prefs:      prefs,
		sessionBus: func() error { return nil },
		workArea:   platformWorkArea,
		windowPos:  func() (int, int) { return 0, 0 },
		setPos:     platformSetPos,
	}
	app.Input = NewWorkspaceInput(app.workspaceRootForInput, app.ensurePendingInputDir, cfg.MaxFileSize)
	// 关闭确认/另存标题在展示时读取当前语言；系统是/否判定不依赖界面文本。
	life.SetUnsavedCloseMessage(app.workspaceCloseMessage)
	life.SetConfirmTitle(app.localizedConfirmTitle)
	saver.SetDialogTitle(app.localizedSaveDialogTitle)
	app.Handler = DesktopHandler(srv.Handler())
	return app
}

// SetWorkspace 注入工作区实现（测试/适配用）。
func (a *App) SetWorkspace(w *Workspace) { a.Workspace = w }

// SetWorkspaceRoot 注入工作区根目录（测试/适配用，非用户可配置项）。空串回退 exe 所在目录。
func (a *App) SetWorkspaceRoot(root string) {
	if a.Workspace != nil {
		a.Workspace.SetRoot(root)
	}
}

// WorkspaceRoot 返回当前工作区根目录。
func (a *App) WorkspaceRoot() string {
	if a.Workspace == nil {
		return ""
	}
	return a.Workspace.Root()
}

// InitWorkspace 初始化 GUI 五目录工作区，并在同一真实 root 下独立重试 pending/；
// 五目录失败时经 notify 弹一次本地化提示后继续。pending 失败只影响快捷输入入口，
// 不触发第二次自动落盘提示。.agent-state 仍只由 headless 管理。
func (a *App) InitWorkspace(notify func(string)) {
	if a.Workspace == nil {
		a.Workspace = NewWorkspace()
	}
	a.Workspace.Init()
	if a.Workspace.DisabledReasonID() != "" {
		a.workspaceNoticeOnce.Do(func() {
			if notify != nil {
				notify(a.localizedWorkspaceNotice())
			}
		})
	}
}

// workspaceRootForInput 返回当前工作区根目录；未初始化时为空（不回退 cwd）。
func (a *App) workspaceRootForInput() string {
	if a.Workspace == nil {
		return ""
	}
	return a.Workspace.Root()
}

// ensurePendingInputDir 触发 pending/ 的安全补建重试，仅影响快捷输入入口。
func (a *App) ensurePendingInputDir() error {
	if a.Workspace == nil {
		return workspace.ErrUnresolvedRoot
	}
	return a.Workspace.EnsurePending()
}

func (a *App) workspaceCloseMessage() string {
	if a.Workspace != nil && a.Workspace.Enabled() {
		return i18n.T(a.Language(), "native.confirm_close_workspace", nil)
	}
	return i18n.T(a.Language(), "native.confirm_close_legacy", nil)
}

// initWindowOnce 只执行一次窗口初始化逻辑；后续页面导航/重载与之无关。
func (a *App) initWindowOnce(fn func()) { a.windowInit.Do(fn) }

// SetWorkAreaProvider 注入工作区提供者（测试或平台适配用）。nil 时恢复平台默认。
func (a *App) SetWorkAreaProvider(p WorkAreaProvider) {
	if p == nil {
		p = platformWorkArea
	}
	a.workArea = p
}

// SetWindowPositionProvider 注入窗口坐标提供者（Wails 适配用）。
func (a *App) SetWindowPositionProvider(fn func() (int, int)) {
	if fn == nil {
		fn = func() (int, int) { return 0, 0 }
	}
	a.windowPos = fn
}

// SetPositionConverter 注入布局绝对逻辑坐标→平台设置坐标的换算器（测试/平台适配用）。
func (a *App) SetPositionConverter(fn func(absX, absY int, area WorkAreaRect) (int, int)) {
	if fn == nil {
		fn = platformSetPos
	}
	a.setPos = fn
}

// SetWindowInitActions 注入窗口初始化原生动作（Wails 适配用）。nil 表示 headless 不执行原生动作。
func (a *App) SetWindowInitActions(w WindowInitActions) { a.winActions = w }

// PositionForSet 返回调用平台 WindowSetPosition 应使用的坐标。
func (a *App) PositionForSet(absX, absY int, area WorkAreaRect) (int, int) {
	if a.setPos == nil {
		return platformSetPos(absX, absY, area)
	}
	return a.setPos(absX, absY, area)
}

// WorkAreaRect 返回窗口所在显示器的可用工作区矩形；失败时回退初始目标尺寸、原点 (0,0)。
func (a *App) WorkAreaRect() WorkAreaRect {
	if a.workArea != nil {
		x, y := 0, 0
		if a.windowPos != nil {
			x, y = a.windowPos()
		}
		if r, ok := a.workArea(x, y); ok && r.Width > 0 && r.Height > 0 {
			return r
		}
	}
	return WorkAreaRect{Width: InitialWidth, Height: InitialHeight}
}

// WorkAreaSize 返回窗口所在显示器可用工作区的逻辑尺寸；失败时回退初始目标尺寸。
func (a *App) WorkAreaSize() WindowSize { return a.WorkAreaRect().Size() }

// SetContext 记录 Wails 启动上下文，供保存对话框等 runtime 调用使用。
func (a *App) SetContext(ctx context.Context) {
	a.ctxMu.Lock()
	a.ctx = ctx
	a.ctxMu.Unlock()
}

// Context 返回 Wails 启动上下文。
func (a *App) Context() context.Context {
	a.ctxMu.RLock()
	defer a.ctxMu.RUnlock()
	return a.ctx
}

// SetSaveDialog 注入平台另存对话框实现。
func (a *App) SetSaveDialog(d SaveDialog) { a.Saver.SetDialog(d) }

// SetWindow 注入窗口操作实现。
func (a *App) SetWindow(w WindowActions) { a.Life.window = w }

// SetConfirm 注入原生放弃确认实现。
func (a *App) SetConfirm(fn func(title, message string) bool) { a.Life.confirm = fn }

// SetSessionBusCheck 注入 session bus 可用性检查（Linux 桌面用）。
func (a *App) SetSessionBusCheck(fn func() error) {
	if fn == nil {
		fn = func() error { return nil }
	}
	a.sessionBus = fn
}

// RunStartupPreflight 执行启动前依赖检查。检查失败时调用 notify 显示中文原因并调用 quit，
// 返回 false 表示不应展示业务窗口。成功返回 true。
func (a *App) RunStartupPreflight(notify func(string), quit func()) bool {
	if a.sessionBus == nil {
		return true
	}
	if err := a.sessionBus(); err != nil {
		if notify != nil {
			notify(i18n.T(a.Language(), "native.session_bus_unavailable", nil))
		}
		if quit != nil {
			quit()
		}
		return false
	}
	return true
}

// --- Wails 绑定面 ---

// Bridge 返回暴露给前端的窄绑定对象（仅保存/未保存 generation 上报/退出确认）。
// 不绑定整个 App，避免内部注入方法与字段经反射进入 JS。
func (a *App) Bridge() *Bridge { return NewBridge(a) }
