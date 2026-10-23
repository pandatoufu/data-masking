package desktop

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pandatoufu/data-masking/internal/docxtest"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

func post(t *testing.T, h http.Handler, path string, body *bytes.Buffer, ct string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", path, body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s 响应不是 JSON：%s", path, rec.Body.String())
	}
	return rec.Code, out
}

// 2.4 mask→restore 往返经桌面 handler 与既有 server 一致。
func TestDesktopMaskRestoreRoundTrip(t *testing.T) {
	app := NewApp(nil, nil)
	orig := []byte("会议纪要\r\n联系人 13812345678\r\n")
	body, ct := multipartFile(t, "notes.txt", orig, map[string]string{"profile": "balanced"})
	code, resp := post(t, app.Handler, "/api/mask", body, ct)
	if code != http.StatusOK || resp["ok"] != true {
		t.Fatalf("脱敏失败：%d %v", code, resp)
	}
	masked, _ := base64.StdEncoding.DecodeString(resp["masked_b64"].(string))
	mappingJSON, _ := base64.StdEncoding.DecodeString(resp["mapping_b64"].(string))

	body2, ct2 := multipartFiles(t, map[string]string{}, map[string]filePart{
		"file":    {resp["masked_name"].(string), masked},
		"mapping": {"notes.masked.mapping.json", mappingJSON},
	})
	code, rresp := post(t, app.Handler, "/api/restore", body2, ct2)
	if code != http.StatusOK || rresp["ok"] != true {
		t.Fatalf("还原失败：%d %v", code, rresp)
	}
	restored, _ := base64.StdEncoding.DecodeString(rresp["restored_b64"].(string))
	if !bytes.Equal(restored, orig) {
		t.Fatalf("往返字节应一致：%q vs %q", restored, orig)
	}
}

// 2.4 GBK 纯文本经桌面 handler 检出编码并正确往返。
func TestDesktopGBKRoundTrip(t *testing.T) {
	app := NewApp(nil, nil)
	utf8Text := "会议纪要\n联系人 13812345678\n"
	gbkBytes, _, err := transform.Bytes(simplifiedchinese.GBK.NewEncoder(), []byte(utf8Text))
	if err != nil {
		t.Fatal(err)
	}
	body, ct := multipartFile(t, "notes.txt", gbkBytes, map[string]string{"profile": "balanced"})
	code, resp := post(t, app.Handler, "/api/mask", body, ct)
	if code != http.StatusOK || resp["ok"] != true {
		t.Fatalf("GBK 脱敏失败：%d %v", code, resp)
	}
	if enc, _ := resp["encoding"].(string); enc != "GBK" {
		t.Fatalf("应检出 GBK 编码：%v", resp["encoding"])
	}
}

// 2.4 custom 高频 409 确认协议不变。
func TestDesktop409PreviewProtocol(t *testing.T) {
	app := NewApp(nil, nil)
	text := bytes.Repeat([]byte("内部词\n"), 30)
	body, ct := multipartFile(t, "a.txt", text, map[string]string{"profile": "balanced", "custom": "内部词"})
	code, resp := post(t, app.Handler, "/api/mask", body, ct)
	if code != http.StatusConflict || resp["error"] != "preview_required" {
		t.Fatalf("应返回 409 preview_required：%d %v", code, resp)
	}
	if _, ok := resp["hits"]; !ok {
		t.Fatal("409 应含 hits")
	}
}

// 2.4 非 custom 携带覆盖字段 400；未知 profile 400。
func TestDesktopOverrideAndProfileGating(t *testing.T) {
	app := NewApp(nil, nil)
	body, ct := multipartFile(t, "a.txt", []byte("x"), map[string]string{"profile": "balanced", "rule_email": "true"})
	code, resp := post(t, app.Handler, "/api/mask", body, ct)
	if code != http.StatusBadRequest {
		t.Fatalf("非 custom 覆盖应 400：%d %v", code, resp)
	}
	if msg, _ := resp["message"].(string); !strings.Contains(msg, "仅 custom") {
		t.Fatalf("应提示仅 custom：%q", msg)
	}

	body, ct = multipartFile(t, "a.txt", []byte("x"), map[string]string{"profile": "nope"})
	code, resp = post(t, app.Handler, "/api/mask", body, ct)
	if code != http.StatusBadRequest {
		t.Fatalf("未知 profile 应 400：%d %v", code, resp)
	}
}

// 2.4 DOCX 成功与拒绝经桌面 handler 语义一致。
func TestDesktopDOCXSuccessAndReject(t *testing.T) {
	app := NewApp(nil, nil)
	ok, err := docxtest.BuildFullFixture(docxtest.FixtureOptions{Body: []string{"电话 13812345678"}})
	if err != nil {
		t.Fatal(err)
	}
	body, ct := multipartFile(t, "合同.docx", ok, map[string]string{"profile": "strict"})
	code, resp := post(t, app.Handler, "/api/mask", body, ct)
	if code != http.StatusOK || resp["format"] != "docx" {
		t.Fatalf("DOCX 应成功且 format=docx：%d %v", code, resp)
	}
	if enc, _ := resp["encoding"].(string); enc != "" {
		t.Fatalf("DOCX encoding 应为空串：%v", resp["encoding"])
	}

	bad, err := docxtest.BuildFullFixture(docxtest.FixtureOptions{Body: []string{"正文"}, ParagraphMarkDelete: true})
	if err != nil {
		t.Fatal(err)
	}
	body, ct = multipartFile(t, "bad.docx", bad, map[string]string{"profile": "strict"})
	code, resp = post(t, app.Handler, "/api/mask", body, ct)
	if code != http.StatusBadRequest {
		t.Fatalf("不支持修订应 400：%d %v", code, resp)
	}
	if _, has := resp["masked_b64"]; has {
		t.Fatal("拒绝不应返回产物")
	}
}

// 2.4 未知业务路径仍 404，不新增 endpoint。
func TestDesktopUnknownBusinessPaths404(t *testing.T) {
	app := NewApp(nil, nil)
	for _, p := range []string{"/api/unknown", "/api/save", "/api/exit2"} {
		req := httptest.NewRequest("POST", p, nil)
		rec := httptest.NewRecorder()
		app.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s 应 404（got %d）", p, rec.Code)
		}
	}
}
