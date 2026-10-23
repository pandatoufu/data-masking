package desktop

import (
	"net/http"
	"testing"
)

// CRITICAL-B：桌面 mask/restore 成功响应必须携带本次原子的 unsaved_generation，
// 前端据此上报，无需二次 RPC 读取未来 token。
func TestMaskResponseCarriesUnsavedGeneration(t *testing.T) {
	app := NewApp(nil, nil)
	body, ct := multipartFile(t, "notes.txt", []byte("电话 13812345678"), map[string]string{"profile": "balanced"})
	code, resp := post(t, app.Handler, "/api/mask", body, ct)
	if code != http.StatusOK {
		t.Fatalf("脱敏应成功：%d", code)
	}
	gen, ok := resp["unsaved_generation"].(float64)
	if !ok || gen < 1 {
		t.Fatalf("响应应含正数 unsaved_generation：%v", resp["unsaved_generation"])
	}
	// 后端已权威置未保存，且 token 与响应一致。
	if !app.Life.HasUnsaved() {
		t.Fatal("脱敏成功后应置未保存")
	}
	if uint64(gen) != app.Life.UnsavedGeneration() {
		t.Fatalf("响应 token (%v) 应与后端 generation (%d) 一致", gen, app.Life.UnsavedGeneration())
	}
}

func TestRestoreResponseCarriesUnsavedGeneration(t *testing.T) {
	app := NewApp(nil, nil)
	body, ct := multipartFile(t, "a.txt", []byte("电话 13813813813"), map[string]string{"profile": "balanced"})
	_, resp := post(t, app.Handler, "/api/mask", body, ct)
	masked, _ := decodeB64(resp["masked_b64"].(string))
	mapping, _ := decodeB64(resp["mapping_b64"].(string))
	maskedName, _ := resp["masked_name"].(string)

	rb, rct := multipartFiles(t, map[string]string{}, map[string]filePart{
		"file":    {maskedName, masked},
		"mapping": {"a.masked.mapping.json", mapping},
	})
	code, rresp := post(t, app.Handler, "/api/restore", rb, rct)
	if code != http.StatusOK {
		t.Fatalf("还原应成功：%d", code)
	}
	gen, ok := rresp["unsaved_generation"].(float64)
	if !ok || gen < 2 {
		t.Fatalf("还原响应应含递增的 unsaved_generation：%v", rresp["unsaved_generation"])
	}
}
