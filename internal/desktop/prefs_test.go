package desktop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pandatoufu/data-masking/internal/i18n"
	"github.com/pandatoufu/data-masking/internal/uiprefs"
)

func newPrefsApp(t *testing.T, path string) *App {
	t.Helper()
	return NewAppWithPrefs(nil, nil, uiprefs.NewStore(path, i18n.ZH))
}

func TestSetUIPreferencesPersistsValidValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ui-prefs.json")
	app := newPrefsApp(t, path)
	res := app.SetUIPreferences(uiprefs.ThemeDark, "en")
	if !res.Persisted || res.MessageID != "" {
		t.Fatalf("合法更新应持久化：%+v", res)
	}
	if res.Theme != uiprefs.ThemeDark || res.Language != "en" {
		t.Fatalf("effective 应为新值：%+v", res)
	}
	if app.Language() != i18n.EN {
		t.Fatalf("语言应即时生效：%s", app.Language())
	}
	// 重新加载文件确认落盘。
	got := uiprefs.Load(path, i18n.ZH)
	if got.Theme != uiprefs.ThemeDark || got.Language != "en" {
		t.Fatalf("偏好应落盘：%+v", got)
	}
}

func TestSetUIPreferencesRejectsInvalidAndPaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ui-prefs.json")
	app := newPrefsApp(t, path)
	res := app.SetUIPreferences(uiprefs.ThemeDark, "en")
	if !res.Persisted {
		t.Fatal("初始合法更新应成功")
	}
	// 非法主题、非法语言、任意路径/配置键：全部拒绝且不写入。
	for _, bad := range [][2]string{
		{"/etc/passwd", "en"},
		{uiprefs.ThemeDark, "../../secret"},
		{"neon", "en"},
		{uiprefs.ThemeDark, "zh;rm -rf"},
	} {
		r := app.SetUIPreferences(bad[0], bad[1])
		if r.MessageID != "prefs.invalid" || r.Persisted {
			t.Fatalf("非法输入应拒绝：%v -> %+v", bad, r)
		}
		if r.Theme != uiprefs.ThemeDark || r.Language != "en" {
			t.Fatalf("拒绝后应保持原有效值：%+v", r)
		}
	}
	if uiprefs.Load(path, i18n.ZH).Theme != uiprefs.ThemeDark {
		t.Fatal("非法请求不得写入")
	}
}

// 持久化失败不阻断业务：本次选择仍生效，返回可见失败标识。
func TestSetUIPreferencesPersistFailureKeepsSelection(t *testing.T) {
	// 以目录作为偏好路径，使完整替换失败。
	dir := t.TempDir()
	app := newPrefsApp(t, dir)
	res := app.SetUIPreferences(uiprefs.ThemeLight, "zh")
	if res.Persisted || res.MessageID != "prefs.save_failed" {
		t.Fatalf("写失败应返回可见标识：%+v", res)
	}
	if app.CurrentPrefs().Theme != uiprefs.ThemeLight || app.Language() != i18n.ZH {
		t.Fatalf("写失败时本次选择仍应生效：%+v", app.CurrentPrefs())
	}
}

// 快速切换按最后选择收敛。
func TestSetUIPreferencesRapidToggleConverges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ui-prefs.json")
	app := newPrefsApp(t, path)
	for _, th := range []string{uiprefs.ThemeLight, uiprefs.ThemeDark, uiprefs.ThemeSystem, uiprefs.ThemeDark} {
		_ = app.SetUIPreferences(th, "en")
	}
	if app.CurrentPrefs().Theme != uiprefs.ThemeDark {
		t.Fatalf("应收敛到最后选择：%+v", app.CurrentPrefs())
	}
	if uiprefs.Load(path, i18n.ZH).Theme != uiprefs.ThemeDark {
		t.Fatalf("落盘应收敛到最后选择：%+v", uiprefs.Load(path, i18n.ZH))
	}
}

// 启动期已存偏好随页面下发前被 App 读取；SetUIPreferences 更新原生窗口标题。
func TestSetUIPreferencesUpdatesTitle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ui-prefs.json")
	app := newPrefsApp(t, path)
	var title string
	app.SetTitleSetter(func(t string) { title = t })
	app.SetUIPreferences(uiprefs.ThemeDark, "en")
	want := i18n.T(i18n.EN, "native.app_title", nil)
	if title != want {
		t.Fatalf("标题应按新语言更新：got %q want %q", title, want)
	}
}

// 偏好文件绝不写入敏感内容。
func TestPrefsFileHasNoSensitiveKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ui-prefs.json")
	app := newPrefsApp(t, path)
	app.SetUIPreferences(uiprefs.ThemeDark, "en")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"words", "mapping", "secret", "path", "placeholder"} {
		if contains(string(raw), bad) {
			t.Fatalf("偏好不得含 %q：%s", bad, raw)
		}
	}
}
