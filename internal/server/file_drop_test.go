package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/pandatoufu/data-masking/internal/config"
)

// 10.3：只读拖入能力由可注入纯函数按桌面模式与运行目标 GOOS 推导。
func TestFileDropEnabledPureFunction(t *testing.T) {
	cases := []struct {
		name    string
		desktop bool
		goos    string
		want    bool
	}{
		{"desktop windows", true, "windows", true},
		{"desktop linux", true, "linux", false},
		{"desktop darwin unknown", true, "darwin", false},
		{"browser windows", false, "windows", false},
		{"browser linux", false, "linux", false},
		{"empty goos", true, "", false},
	}
	for _, c := range cases {
		if got := fileDropEnabled(c.desktop, c.goos); got != c.want {
			t.Errorf("%s: fileDropEnabled(%v,%q)=%v want %v", c.name, c.desktop, c.goos, got, c.want)
		}
	}
}

type dropPageConfig struct {
	FileDropEnabled *bool `json:"file_drop_enabled"`
}

func readDropConfig(t *testing.T, body string) dropPageConfig {
	t.Helper()
	const marker = "window.__DM_CONFIG__ = "
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("页面未注入 pageConfig：%q", body)
	}
	rest := body[i+len(marker):]
	end := strings.Index(rest, ";</script>")
	if end < 0 {
		t.Fatalf("pageConfig 注入格式不符：%q", rest)
	}
	var pc dropPageConfig
	if err := json.Unmarshal([]byte(rest[:end]), &pc); err != nil {
		t.Fatalf("pageConfig 非法：%v", err)
	}
	return pc
}

// 10.3：两个页面路由均下发同一能力字段，且与运行时 GOOS 的纯函数结果一致。
func TestPageConfigCarriesFileDropCapabilityBothRoutes(t *testing.T) {
	s := New(config.Defaults(), nil, WithDesktopMode(), WithIdleTimeout(0))
	want := fileDropEnabled(true, runtime.GOOS)
	for _, path := range []string{"/", "/restore/"} {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s 应返回 200：%d", path, rec.Code)
		}
		pc := readDropConfig(t, rec.Body.String())
		if pc.FileDropEnabled == nil {
			t.Fatalf("%s 应始终下发布尔 file_drop_enabled", path)
		}
		if *pc.FileDropEnabled != want {
			t.Errorf("%s file_drop_enabled=%v want %v (GOOS=%s)", path, *pc.FileDropEnabled, want, runtime.GOOS)
		}
	}
}

// 非桌面/浏览器模式保守 false，且字段仍存在。
func TestPageConfigFileDropDisabledWhenNotDesktop(t *testing.T) {
	s := New(config.Defaults(), nil, WithIdleTimeout(0))
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	pc := readDropConfig(t, rec.Body.String())
	if pc.FileDropEnabled == nil {
		t.Fatal("非桌面路由仍应下发 file_drop_enabled=false")
	}
	if *pc.FileDropEnabled {
		t.Errorf("非桌面模式 file_drop_enabled 应为 false")
	}
}
