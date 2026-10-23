package desktop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pandatoufu/data-masking/internal/server"
)

func newDesktopServer(t *testing.T) *App {
	t.Helper()
	return NewApp(nil, nil)
}

func TestDesktopHandlerServesBothPages(t *testing.T) {
	app := newDesktopServer(t)
	h := app.Handler

	for _, path := range []string{"/", "/restore/"} {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s 应返回 200，got %d", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
			t.Fatalf("%s 应为 HTML：%q", path, ct)
		}
	}
}

func TestDesktopHandlerRedirectsLegacyRestore(t *testing.T) {
	app := newDesktopServer(t)
	req := httptest.NewRequest("GET", "/restore", nil)
	rec := httptest.NewRecorder()
	app.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("旧 /restore 应重定向：%d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/restore/" {
		t.Fatalf("重定向目标应为 /restore/：%q", loc)
	}
}

func TestDesktopHandlerUnknownIndexLikeReturns400(t *testing.T) {
	app := newDesktopServer(t)
	for _, path := range []string{"/nope/", "/index.html", "/foo/index.html", "/missing/"} {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		app.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s 应为 400，got %d", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "本地桌面应用") {
			t.Fatalf("%s 应给本应用中文提示：%q", path, rec.Body.String())
		}
	}
}

func TestDesktopHandlerUnknownNonIndexStays404(t *testing.T) {
	app := newDesktopServer(t)
	req := httptest.NewRequest("GET", "/totally-unknown", nil)
	rec := httptest.NewRecorder()
	app.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("非 index 型未知路径应保持 404：%d", rec.Code)
	}
}

func TestDesktopHandlerKeepsThreeAPIsAndNoSaveEndpoint(t *testing.T) {
	app := newDesktopServer(t)
	for _, p := range []string{"/api/save", "/api/config", "/api/ping"} {
		req := httptest.NewRequest("POST", p, strings.NewReader(""))
		rec := httptest.NewRecorder()
		app.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s 不应存在（got %d）", p, rec.Code)
		}
	}
}

func TestDesktopPageCarriesModeFlag(t *testing.T) {
	app := newDesktopServer(t)
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	app.Handler.ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, `"mode":"desktop"`) {
		t.Fatalf("桌面页应注入 mode:desktop：%q", body)
	}
	// 页面配置仍含真实 profile 与六个规则。
	if !strings.Contains(body, "balanced") || !strings.Contains(body, "__custom") {
		t.Fatalf("桌面页应保留真实 profile：%q", body)
	}
	// 隐藏回环地址。
	var pc struct {
		URL  string `json:"url"`
		Mode string `json:"mode"`
	}
	marker := "window.__DM_CONFIG__ = "
	i := strings.Index(body, marker)
	rest := body[i+len(marker):]
	end := strings.Index(rest, ";</script>")
	if err := json.Unmarshal([]byte(rest[:end]), &pc); err != nil {
		t.Fatalf("配置不是合法 JSON：%v", err)
	}
	if pc.URL != "" {
		t.Fatalf("桌面模式应隐藏回环地址：%q", pc.URL)
	}
	if pc.Mode != "desktop" {
		t.Fatalf("mode 应为 desktop：%q", pc.Mode)
	}
}

// 桌面 handler 的 multipart 业务语义与 server 一致（不复制业务校验）。
func TestDesktopHandlerMaskUnchanged(t *testing.T) {
	app := newDesktopServer(t)
	orig := []byte("电话 13812345678")
	body, ct := multipartFile(t, "notes.txt", orig, map[string]string{"profile": "balanced"})
	req := httptest.NewRequest("POST", "/api/mask", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	app.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("桌面 multipart 脱敏应成功：%d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "masked_b64") {
		t.Fatalf("应返回产物：%s", rec.Body.String())
	}
}

// server 自身在桌面模式下的退出守卫：保存进行中返回 409。
func TestServerExitGuardBlocksDuringSave(t *testing.T) {
	app := newDesktopServer(t)
	gate := make(chan struct{})
	app.SetSaveDialog(&blockingDialog{path: "", release: gate})
	done := make(chan struct{})
	go func() { app.Saver.Save("x.txt", b64("x")); close(done) }()
	for !app.Saver.IsSaving() {
	}

	req := httptest.NewRequest("POST", "/api/exit", nil)
	rec := httptest.NewRecorder()
	app.Srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("保存中退出应 409：%d %s", rec.Code, rec.Body.String())
	}
	close(gate)
	<-done
}

var _ = server.WithDesktopMode
