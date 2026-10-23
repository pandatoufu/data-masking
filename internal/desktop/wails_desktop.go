//go:build desktop

package desktop

import (
	"context"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/pandatoufu/data-masking/internal/i18n"
)

// singleInstanceID 固定且独立于 exe 路径，保证同一用户会话只保留一个可交互业务窗口。
const singleInstanceID = "github.com.pandatoufu.data-masking"

// Run 启动 Wails 桌面宿主。仅使用进程内 handler，不创建业务 TCP 监听、不打开浏览器。
func Run() error {
	app := Load()
	// 启动快照语言：Wails Options（标题/Windows Messages/Linux Messages）在 options 构建时固定。
	startupLang := app.Language()
	app.SetSaveDialog(&wailsSaveDialog{app: app})
	app.SetWindow(&wailsWindowActions{app: app})
	app.SetTitleSetter(func(title string) { runtime.WindowSetTitle(app.Context(), title) })
	app.SetConfirm(func(title, message string) bool {
		return wailsConfirm(app.Context(), title, message)
	})
	app.SetSessionBusCheck(checkSessionBus)
	// 窗口坐标提供者：让工作区显示器与窗口实际所在显示器一致（多屏/负坐标）。
	app.SetWindowPositionProvider(func() (int, int) {
		return runtime.WindowGetPosition(app.Context())
	})
	// 窗口初始化原生动作：顺序由 ApplyWindowInit 统一为 Min→Size→Position→Show。
	app.SetWindowInitActions(&wailsWindowInit{app: app})

	return wails.Run(&options.App{
		Title:         i18n.T(startupLang, "native.app_title", nil),
		Width:         InitialWidth,
		Height:        InitialHeight,
		MinWidth:      RegularMinWidth,
		MinHeight:     RegularMinHeight,
		DisableResize: false,
		Frameless:     false,
		StartHidden:   true,
		AlwaysOnTop:   false,
		AssetServer:   &assetserver.Options{Handler: app.Handler},
		OnStartup:     app.onStartup,
		OnDomReady:    app.onDomReady,
		OnBeforeClose: app.onBeforeClose,
		Bind:          []interface{}{app.Bridge()},
		Windows:       &windows.Options{Messages: localizedWindowsMessages(startupLang)},
		Linux:         &linux.Options{ProgramName: "data-masking", Messages: localizedLinuxMessages(startupLang)},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: singleInstanceID,
			OnSecondInstanceLaunch: func(options.SecondInstanceData) {
				app.Life.HandleSecondInstance()
			},
		},
	})
}

func (a *App) onStartup(ctx context.Context) {
	a.SetContext(ctx)
	lang := a.Language()
	if !a.RunStartupPreflight(
		func(msg string) {
			_, _ = runtime.MessageDialog(ctx, runtime.MessageDialogOptions{
				Type:    runtime.ErrorDialog,
				Title:   i18n.T(lang, "native.startup_failed_title", nil),
				Message: msg,
			})
		},
		func() { runtime.Quit(ctx) },
	) {
		// 预检失败：持久失败状态，阻止显示与后续唤起。
		a.Life.MarkStartupFailure()
		return
	}
	a.Life.MarkStartupSuccess()
	// 工作区初始化：两平台都建 GUI 五目录；失败弹一次本地化提示后继续启动（保留手动另存）。
	// pending/ 与 .agent-state 只由 headless 初始化，GUI 不检查或触碰。
	a.InitWorkspace(func(msg string) {
		_, _ = runtime.MessageDialog(ctx, runtime.MessageDialogOptions{
			Type:    runtime.WarningDialog,
			Title:   i18n.T(a.Language(), "native.workspace_unavailable_title", nil),
			Message: msg,
		})
	})
	// 预检成功可能晚于 DOM-ready：此时补做一次窗口初始化（sync.Once 保证仅一次）。
	a.maybeInitWindow()
	// /api/exit 的前端确认已完成：Done 通知到达时避免二次确认并结束进程。
	go func() {
		<-a.Srv.Done()
		a.Life.MarkConfirmedExit()
		runtime.Quit(ctx)
	}()
}

func (a *App) onDomReady(ctx context.Context) {
	a.domReady.Store(true)
	a.maybeInitWindow()
}

// wailsWindowInit 实现 WindowInitActions：由 ApplyWindowInit 固定顺序调用，
// 底层直通 Wails runtime，不复制窗口策略逻辑。
type wailsWindowInit struct{ app *App }

func (w *wailsWindowInit) SetMinSize(width, height int) {
	runtime.WindowSetMinSize(w.app.Context(), width, height)
}

func (w *wailsWindowInit) SetSize(width, height int) {
	runtime.WindowSetSize(w.app.Context(), width, height)
}

func (w *wailsWindowInit) SetPosition(x, y int) {
	runtime.WindowSetPosition(w.app.Context(), x, y)
}

func (w *wailsWindowInit) Show() { runtime.WindowShow(w.app.Context()) }

func (a *App) onBeforeClose(ctx context.Context) bool {
	switch a.Life.RequestClose() {
	case CloseAllow:
		return false
	case CloseBlockedSaving:
		lang := a.Language()
		_, _ = runtime.MessageDialog(ctx, runtime.MessageDialogOptions{
			Type:    runtime.WarningDialog,
			Title:   i18n.T(lang, "native.close_blocked_title", nil),
			Message: i18n.T(lang, "native.close_blocked_body", nil),
		})
		return true
	default:
		return true
	}
}

// wailsSaveDialog 使用 Wails 原生另存对话框，仅接受建议文件名。
type wailsSaveDialog struct{ app *App }

func (d *wailsSaveDialog) Choose(title, suggestedName string) (string, bool, error) {
	ctx := d.app.Context()
	path, err := runtime.SaveFileDialog(ctx, runtime.SaveDialogOptions{
		Title:           title,
		DefaultFilename: suggestedName,
	})
	if err != nil {
		return "", false, err
	}
	if path == "" {
		return "", true, nil
	}
	return path, false, nil
}

// wailsWindowActions 实现 WindowActions。
type wailsWindowActions struct{ app *App }

func (w *wailsWindowActions) Show()       { runtime.WindowShow(w.app.Context()) }
func (w *wailsWindowActions) Unminimise() { runtime.WindowUnminimise(w.app.Context()) }

// wailsConfirm 使用 Wails 原生 Question 对话框请求放弃确认。
// v2.11 的 Windows 映射为 MB_YESNO、Linux 映射为 GTK_BUTTONS_YES_NO，
// 只实现“是/否”，自定义 Buttons/CancelButton 不生效；因此只把真实的 Yes 视为确认，
// No/取消/任何异常一律不放行（返回 false 表示用户未确认放弃）。
func wailsConfirm(ctx context.Context, title, message string) bool {
	result, err := runtime.MessageDialog(ctx, runtime.MessageDialogOptions{
		Type:          runtime.QuestionDialog,
		Title:         title,
		Message:       message,
		DefaultButton: "No",
	})
	if err != nil {
		return false
	}
	return isAffirmativeDialogResult(result)
}

// localizedWindowsMessages 提供 WebView2 缺失/过旧的原生提示（按启动语言快照）。
// 官方安装入口由 Wails browser 策略在用户确认后打开，不静默安装、不捆绑运行时。
func localizedWindowsMessages(lang i18n.Lang) *windows.Messages {
	t := func(id string) string { return i18n.T(lang, id, nil) }
	return &windows.Messages{
		InstallationRequired: t("native.webview2_installation_required"),
		UpdateRequired:       t("native.webview2_update_required"),
		MissingRequirements:  t("native.webview2_missing_requirements"),
		Webview2NotInstalled: t("native.webview2_not_installed"),
		Error:                t("native.webview2_error"),
		FailedToInstall:      t("native.webview2_failed_install"),
		DownloadPage:         t("native.webview2_download_page"),
		PressOKToInstall:     t("native.webview2_press_ok"),
		ContactAdmin:         t("native.webview2_contact_admin"),
		InvalidFixedWebview2: t("native.webview2_invalid_fixed"),
		WebView2ProcessCrash: t("native.webview2_process_crash"),
	}
}

// localizedLinuxMessages 提供 Linux WebKitGTK 依赖不足的原生提示（按启动语言快照）。
func localizedLinuxMessages(lang i18n.Lang) *linux.Messages {
	return &linux.Messages{
		WebKit2GTKMinRequired: i18n.T(lang, "native.linux_webkit_required", map[string]any{"version": "%s"}),
	}
}
