//go:build desktop

package desktop

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	wailsassetserver "github.com/wailsapp/wails/v2/pkg/assetserver"
)

// stubRuntime 提供最小的 Wails RuntimeAssets，使 AssetServer 能注入脚本占位。
type stubRuntime struct{}

func (stubRuntime) DesktopIPC() []byte       { return []byte("/*ipc*/") }
func (stubRuntime) WebsocketIPC() []byte     { return []byte("/*ws*/") }
func (stubRuntime) RuntimeDesktopJS() []byte { return []byte("/*runtime*/") }

// 通过真实 Wails AssetServer 验证注入层：/ 与 /restore/ 注入 runtime/bindings，
// 未知 index 型路径不变成框架 defaultHTML，三个 API 原样进入业务 handler。
func TestWailsAssetServerInjection(t *testing.T) {
	app := NewApp(nil, nil)
	handler, err := wailsassetserver.NewAssetServerWithHandler(
		app.Handler,
		`{"desktop":{"App":{"SaveFile":"SaveFile"}}}`,
		false,
		nil,
		stubRuntime{},
	)
	if err != nil {
		t.Fatal(err)
	}

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	// 两个规范页面都应注入 wails runtime 与 bindings。
	for _, path := range []string{"/", "/restore/"} {
		rec := get(path)
		body := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Fatalf("%s 应 200，got %d", path, rec.Code)
		}
		if !strings.Contains(body, "/wails/runtime.js") {
			t.Fatalf("%s 应注入 runtime.js：%q", path, truncate(body))
		}
		if !strings.Contains(body, "/wails/ipc.js") {
			t.Fatalf("%s 应注入 ipc.js", path)
		}
	}

	// bindings 经 /wails/runtime.js 提供。
	runtimeRec := get("/wails/runtime.js")
	if runtimeRec.Code != http.StatusOK || !strings.Contains(runtimeRec.Body.String(), "wailsbindings") {
		t.Fatalf("runtime.js 应携带 bindings：%d %q", runtimeRec.Code, truncate(runtimeRec.Body.String()))
	}

	// 未知 index 型路径不得被替换为 Wails 默认页。
	rec := get("/unknown/")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未知 index 型路径应 400，got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "wails") && strings.Contains(rec.Body.String(), "Could not find") {
		t.Fatalf("不得返回框架默认页：%q", truncate(rec.Body.String()))
	}
	if !strings.Contains(rec.Body.String(), "本地桌面应用") {
		t.Fatalf("应返回本应用中文反馈：%q", truncate(rec.Body.String()))
	}

	// 旧 /restore 经 wrapper 重定向到 /restore/。
	rec = get("/restore")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/restore/" {
		t.Fatalf("旧 /restore 应重定向：%d %q", rec.Code, rec.Header().Get("Location"))
	}

	// 双页业务 multipart 均原样进入 handler：/api/mask 与 /api/restore。
	body, ct := multipartFile(t, "a.txt", []byte("电话 13812345678"), map[string]string{"profile": "balanced"})
	req := httptest.NewRequest("POST", "/api/mask", body)
	req.Header.Set("Content-Type", ct)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "masked_b64") {
		t.Fatalf("mask multipart 应原样成功：%d %s", rec.Code, truncate(rec.Body.String()))
	}
	masked := decodeB64Str(rec.Body.String(), "masked_b64", t)
	mapping := decodeB64Str(rec.Body.String(), "mapping_b64", t)
	maskedName := jsonStr(rec.Body.String(), "masked_name", t)

	rb, rct := multipartFiles(t, map[string]string{}, map[string]filePart{
		"file":    {maskedName, masked},
		"mapping": {"a.masked.mapping.json", mapping},
	})
	req = httptest.NewRequest("POST", "/api/restore", rb)
	req.Header.Set("Content-Type", rct)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "restored_b64") {
		t.Fatalf("restore multipart 应原样成功：%d %s", rec.Code, truncate(rec.Body.String()))
	}
}

func decodeB64Str(bodyJSON, key string, t *testing.T) []byte {
	t.Helper()
	v := jsonStr(bodyJSON, key, t)
	b, err := decodeB64(v)
	if err != nil {
		t.Fatalf("解码 %s 失败：%v", key, err)
	}
	return b
}

func truncate(s string) string {
	if len(s) > 200 {
		return s[:200]
	}
	return s
}
