package desktop

import (
	"net/http"
	"testing"
)

// W3 回归：/api/mask 成功后，后端权威把未保存状态置 true（不依赖前端上报）。
func TestMaskMarksUnsavedAuthoritatively(t *testing.T) {
	app := NewApp(nil, nil)
	if app.Life.HasUnsaved() {
		t.Fatal("初始不应有未保存状态")
	}
	body, ct := multipartFile(t, "notes.txt", []byte("电话 13812345678"), map[string]string{"profile": "balanced"})
	code, _ := post(t, app.Handler, "/api/mask", body, ct)
	if code != http.StatusOK {
		t.Fatalf("脱敏应成功：%d", code)
	}
	if !app.Life.HasUnsaved() {
		t.Fatal("脱敏成功后后端应权威置未保存")
	}
}

// W3 回归：/api/restore 成功后同样权威置未保存。
func TestRestoreMarksUnsavedAuthoritatively(t *testing.T) {
	app := NewApp(nil, nil)
	body, ct := multipartFile(t, "a.txt", []byte("电话 13813813813"), map[string]string{"profile": "balanced"})
	_, resp := post(t, app.Handler, "/api/mask", body, ct)
	masked, _ := decodeB64(resp["masked_b64"].(string))
	mapping, _ := decodeB64(resp["mapping_b64"].(string))
	app.Life.SetUnsaved(app.Life.UnsavedGeneration(), false) // 模拟前端保存后清除

	rb, rct := multipartFiles(t, map[string]string{}, map[string]filePart{
		"file":    {resp["masked_name"].(string), masked},
		"mapping": {"a.masked.mapping.json", mapping},
	})
	code, _ := post(t, app.Handler, "/api/restore", rb, rct)
	if code != http.StatusOK {
		t.Fatalf("还原应成功：%d", code)
	}
	if !app.Life.HasUnsaved() {
		t.Fatal("还原成功后后端应权威置未保存")
	}
}

// W3/R2-C4：保存进行中，窗口关闭与 /api/exit 均被阻止；写完后放行（跨 goroutine 顺序）。
func TestExitGuardOrderingDuringSave(t *testing.T) {
	app := NewApp(nil, nil)
	gate := make(chan struct{})
	app.SetSaveDialog(&blockingDialog{path: "", release: gate})
	done := make(chan struct{})
	go func() { app.Saver.Save("x.txt", b64("x")); close(done) }()
	for !app.Saver.IsSaving() {
	}
	if app.Life.RequestClose() != CloseBlockedSaving {
		t.Fatal("保存中窗口关闭应被阻止")
	}
	if err := app.Life.BeforeExit(); err == nil {
		t.Fatal("保存中 /api/exit 应被阻止")
	}
	close(gate)
	<-done
	// 保存结束后，一次新的关闭/退出裁决应放行（用独立生命周期验证 /api/exit 路径）。
	if app.Life.RequestClose() != CloseAllow {
		t.Fatal("保存结束后应放行")
	}
	app2 := NewApp(nil, nil)
	if err := app2.Life.BeforeExit(); err != nil {
		t.Fatalf("无保存时退出守卫应放行：%v", err)
	}
}
