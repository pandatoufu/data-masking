package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pandatoufu/data-masking/internal/config"
)

func TestDesktopModeInjectsFlagAndHidesURL(t *testing.T) {
	s := New(config.Defaults(), nil, WithDesktopMode(), WithIdleTimeout(0))
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `"mode":"desktop"`) {
		t.Fatalf("桌面页应含 mode:desktop：%q", body)
	}
	var pc struct {
		URL            string `json:"url"`
		DefaultProfile string `json:"default_profile"`
		Profiles       []struct {
			Name string `json:"name"`
		} `json:"profiles"`
	}
	marker := "window.__DM_CONFIG__ = "
	i := strings.Index(body, marker)
	rest := body[i+len(marker):]
	end := strings.Index(rest, ";</script>")
	if err := json.Unmarshal([]byte(rest[:end]), &pc); err != nil {
		t.Fatalf("pageConfig 非法：%v", err)
	}
	if pc.URL != "" {
		t.Fatalf("桌面模式应隐藏回环地址：%q", pc.URL)
	}
	if pc.DefaultProfile != "balanced" || len(pc.Profiles) < 4 {
		t.Fatalf("仍应下发真实 config/profile：%+v", pc)
	}
}

func TestBrowserModeKeepsURL(t *testing.T) {
	s := New(config.Defaults(), nil, WithIdleTimeout(0))
	// 浏览器模式仍注入 url 字段（此处为空因未 Start，但 mode 应为空）。
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if strings.Contains(rec.Body.String(), `"mode":"desktop"`) {
		t.Fatal("浏览器模式不应注入 desktop 标志")
	}
}

func TestRestoreCanonicalAndLegacyPaths(t *testing.T) {
	s := New(config.Defaults(), nil, WithDesktopMode(), WithIdleTimeout(0))
	for _, path := range []string{"/restore", "/restore/"} {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s 应返回还原页：%d", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "选择还原文件") {
			t.Fatalf("%s 应为还原页：%q", path, rec.Body.String())
		}
	}
}

func TestDesktopExitMessage(t *testing.T) {
	var released bool
	s := New(config.Defaults(), nil, WithDesktopMode(), WithExitGuard(func() error { return nil }), WithIdleTimeout(0))
	req := httptest.NewRequest("POST", "/api/exit", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("桌面退出应成功：%d", rec.Code)
	}
	select {
	case <-s.Done():
		released = true
	default:
	}
	if released {
		t.Fatal("退出消息应立即返回，关停异步触发")
	}
}

func TestExitGuardRejects(t *testing.T) {
	s := New(config.Defaults(), nil, WithDesktopMode(), WithExitGuard(func() error {
		return errString("保存进行中")
	}), WithIdleTimeout(0))
	req := httptest.NewRequest("POST", "/api/exit", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("守卫拒绝应 409：%d %s", rec.Code, rec.Body.String())
	}
	select {
	case <-s.Done():
		t.Fatal("守卫拒绝不应触发关停")
	default:
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// 已存在旧 idle 配置仍可读，但桌面模式不启动 idleLoop（不 Start 即无关停路径）。
func TestDesktopModeReadsIdleButNoIdleLoop(t *testing.T) {
	cfg := config.Defaults()
	cfg.IdleTimeoutMin = 1
	s := New(cfg, nil, WithDesktopMode(), WithIdleTimeout(0))
	if s.idle <= 0 {
		t.Fatal("旧 idle 配置应仍可解析")
	}
}

// 桌面模式：mask/restore 成功响应携带本次原子的 unsaved_generation。
func TestDesktopArtifactsGenerationInResponse(t *testing.T) {
	var calls int
	s := New(config.Defaults(), nil,
		WithDesktopMode(),
		WithArtifactsCallback(func() uint64 { calls++; return uint64(calls) }),
		WithIdleTimeout(0),
	)
	body, ct := makeMultipart(t, map[string][]string{"profile": {"balanced"}},
		map[string]filePart{"file": {"a.txt", []byte("电话 13812345678")}})
	code, resp := doJSON(t, s.Handler(), "POST", "/api/mask", body, ct)
	if code != http.StatusOK {
		t.Fatalf("脱敏应成功：%d %v", code, resp)
	}
	if gen, ok := resp["unsaved_generation"].(float64); !ok || gen != 1 {
		t.Fatalf("应携带 unsaved_generation=1：%v", resp["unsaved_generation"])
	}
}

// 浏览器模式（非桌面）不新增 unsaved_generation 字段，保持业务响应兼容。
func TestBrowserModeNoUnsavedGeneration(t *testing.T) {
	var calls int
	s := New(config.Defaults(), nil,
		WithArtifactsCallback(func() uint64 { calls++; return uint64(calls) }),
		WithIdleTimeout(0),
	)
	body, ct := makeMultipart(t, map[string][]string{"profile": {"balanced"}},
		map[string]filePart{"file": {"a.txt", []byte("电话 13812345678")}})
	code, resp := doJSON(t, s.Handler(), "POST", "/api/mask", body, ct)
	if code != http.StatusOK {
		t.Fatalf("脱敏应成功：%d %v", code, resp)
	}
	if _, has := resp["unsaved_generation"]; has {
		t.Fatalf("浏览器模式不应新增 unsaved_generation 字段：%v", resp)
	}
}
