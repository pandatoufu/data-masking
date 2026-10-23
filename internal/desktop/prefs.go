package desktop

import (
	"strings"

	"github.com/pandatoufu/data-masking/internal/i18n"
	"github.com/pandatoufu/data-masking/internal/uiprefs"
)

// UIPrefResult 是 SetUIPreferences binding 的返回：effective 偏好与持久化状态。
type UIPrefResult struct {
	Theme     string `json:"theme"`
	Language  string `json:"language"`
	Persisted bool   `json:"persisted"`
	// MessageID 在持久化失败或非法输入时非空，前端按当前语言提示。
	MessageID string `json:"message_id,omitempty"`
}

// Language 返回当前 UI 语言。
func (a *App) Language() i18n.Lang {
	a.prefsMu.RLock()
	defer a.prefsMu.RUnlock()
	return i18n.Lang(a.prefs.Language)
}

// CurrentPrefs 返回当前内存偏好副本。
func (a *App) CurrentPrefs() uiprefs.Prefs {
	a.prefsMu.RLock()
	defer a.prefsMu.RUnlock()
	return a.prefs
}

// SetTitleSetter 注入原生窗口标题设置器（Wails 适配用）。
func (a *App) SetTitleSetter(fn func(string)) { a.titleSetter = fn }

// localizedConfirmTitle 是原生放弃确认对话框的标题（展示时读取当前语言）。
func (a *App) localizedConfirmTitle() string {
	return i18n.T(a.Language(), "native.confirm_exit_title", nil)
}

// localizedSaveDialogTitle 是原生另存对话框标题（展示时读取当前语言）。
func (a *App) localizedSaveDialogTitle() string {
	return i18n.T(a.Language(), "native.save_dialog_title", nil)
}

// localizedWorkspaceNotice 返回工作区不可用的一次性本地化提示。
func (a *App) localizedWorkspaceNotice() string {
	id := ""
	if a.Workspace != nil {
		id = a.Workspace.DisabledReasonID()
	}
	if id == "" {
		id = "workspace.unavailable"
	}
	return i18n.T(a.Language(), id, nil)
}

// SetUIPreferences 是受限偏好更新：仅接受主题/语言合法值，不接受路径或任意配置键。
// 同步更新内存偏好与原生窗口标题；持久化失败不阻断，本次选择仍生效。
func (a *App) SetUIPreferences(theme, language string) UIPrefResult {
	theme = strings.TrimSpace(theme)
	language = strings.TrimSpace(language)
	if !uiprefs.ValidTheme(theme) || !uiprefs.ValidLanguage(language) {
		cur := a.CurrentPrefs()
		return UIPrefResult{Theme: cur.Theme, Language: cur.Language, Persisted: false, MessageID: "prefs.invalid"}
	}
	a.prefsMu.Lock()
	a.prefs = uiprefs.Prefs{Version: uiprefs.Version, Theme: theme, Language: language}
	effective := a.prefs
	a.prefsMu.Unlock()

	// 原生窗口标题即时更新（若已注入）。
	if a.titleSetter != nil {
		a.titleSetter(i18n.T(i18n.Lang(language), "native.app_title", nil))
	}
	err := error(nil)
	if a.prefsStore != nil && a.prefsStore.Path() != "" {
		err = a.prefsStore.Save(effective)
	}
	res := UIPrefResult{Theme: effective.Theme, Language: effective.Language, Persisted: err == nil}
	if err != nil {
		res.MessageID = "prefs.save_failed"
	}
	return res
}
