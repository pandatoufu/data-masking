package desktop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pandatoufu/data-masking/internal/i18n"
	"github.com/pandatoufu/data-masking/internal/uiprefs"
)

// 启动前读取偏好并在首帧前随页面下发 theme/language 与双语 catalog。
func TestInitialPrefsInjectedBeforeFirstFrame(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ui-prefs.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"theme":"dark","language":"en"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	app := NewAppWithPrefs(nil, nil, uiprefs.NewStore(path, i18n.ZH))
	if app.Language() != i18n.EN {
		t.Fatalf("启动语言应为已存 en，实际 %s", app.Language())
	}

	for _, pagePath := range []string{"/", "/restore/"} {
		req := httptest.NewRequest(http.MethodGet, pagePath, nil)
		rec := httptest.NewRecorder()
		app.Handler.ServeHTTP(rec, req)
		body := rec.Body.String()
		const marker = "window.__DM_CONFIG__ = "
		i := strings.Index(body, marker)
		if i < 0 {
			t.Fatalf("%s 未注入 pageConfig", pagePath)
		}
		rest := body[i+len(marker):]
		end := strings.Index(rest, ";</script>")
		var pc struct {
			Theme    string                       `json:"theme"`
			Language string                       `json:"language"`
			I18n     map[string]map[string]string `json:"i18n"`
		}
		if err := json.Unmarshal([]byte(rest[:end]), &pc); err != nil {
			t.Fatalf("%s pageConfig 非法：%v", pagePath, err)
		}
		if pc.Theme != "dark" || pc.Language != "en" {
			t.Fatalf("%s 首帧偏好应为 dark/en：%+v", pagePath, pc)
		}
		if len(pc.I18n["zh"]) == 0 || len(pc.I18n["en"]) == 0 {
			t.Fatalf("%s 应下发双语 catalog", pagePath)
		}
		if _, ok := pc.I18n["en"]["ui.view_mask"]; !ok {
			t.Fatalf("%s 英文 catalog 应含 UI 文案", pagePath)
		}
	}
}

// restore 规范路径注入初始 restore 视图；根路径默认 mask。
func TestInitialViewInjection(t *testing.T) {
	app := NewApp(nil, nil)
	read := func(p string) string {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		app.Handler.ServeHTTP(rec, req)
		return rec.Body.String()
	}
	if !strings.Contains(read("/restore/"), `"initial_view":"restore"`) {
		t.Fatal("/restore/ 应注入 initial_view=restore")
	}
	if strings.Contains(read("/"), `"initial_view":"restore"`) {
		t.Fatal("/ 不应注入 restore 初始视图")
	}
}
