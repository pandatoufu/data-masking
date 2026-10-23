package server

import (
	"net/http"
	"testing"

	"github.com/pandatoufu/data-masking/internal/i18n"
)

// 校验服务器各反馈路径都给出稳定 presentation.message_id，且该 id 双语已注册；
// 同时确认旧 error/message 字段保留（旧调用者兼容）。
func TestFeedbackCoverageHasPresentationIDs(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()

	assertPres := func(name, method, path string, fields map[string][]string, files map[string]filePart, wantStatus int, wantID string) {
		t.Helper()
		body, ct := makeMultipart(t, fields, files)
		code, resp := doJSON(t, h, method, path, body, ct)
		if code != wantStatus {
			t.Fatalf("%s: 期望 %d，实际 %d %v", name, wantStatus, code, resp)
		}
		if _, ok := resp["message"]; !ok {
			t.Errorf("%s: 应保留旧 message 字段", name)
		}
		pres, ok := resp["presentation"].(map[string]any)
		if !ok {
			t.Fatalf("%s: 缺少 presentation：%v", name, resp)
		}
		id, _ := pres["message_id"].(string)
		if id != wantID {
			t.Errorf("%s: message_id=%q want %q", name, id, wantID)
		}
		if !i18n.Has(id, i18n.ZH) || !i18n.Has(id, i18n.EN) {
			t.Errorf("%s: id %q 未双语注册", name, id)
		}
	}

	assertPres("method", "GET", "/api/mask", nil, nil, http.StatusMethodNotAllowed, "http.method_post_only")
	assertPres("missing-file", "POST", "/api/mask", nil, nil, http.StatusBadRequest, "http.mask_file_missing")
	assertPres("bad-ext", "POST", "/api/mask", nil,
		map[string]filePart{"file": {"data.docm", []byte("x")}}, http.StatusBadRequest, "file.unsupported_format")
	assertPres("unknown-profile", "POST", "/api/mask", map[string][]string{"profile": {"nope"}},
		map[string]filePart{"file": {"a.txt", []byte("x")}}, http.StatusBadRequest, "profile.unknown")
	assertPres("bad-rule", "POST", "/api/mask",
		map[string][]string{"profile": {"__custom"}, "rule_email": {"abc"}},
		map[string]filePart{"file": {"a.txt", []byte("x")}}, http.StatusBadRequest, "rule.bad_bool")
	assertPres("restore-missing-mapping", "POST", "/api/restore", map[string][]string{"text": {"x"}},
		nil, http.StatusBadRequest, "http.mapping_missing")
	assertPres("restore-bad-mapping", "POST", "/api/restore", map[string][]string{"text": {"x"}},
		map[string]filePart{"mapping": {"bad.json", []byte("{not json")}}, http.StatusBadRequest, "mapping.json_invalid")
	assertPres("exit", "POST", "/api/exit", nil, nil, http.StatusOK, "exit.ok")
}

// 高频 409 确认态携带 preview.required 展示标识，且不产出产物。
func TestPreviewRequiredCarriesPresentation(t *testing.T) {
	s := newTestServer(t)
	body, ct := makeMultipart(t,
		map[string][]string{"profile": {"balanced"}, "custom": {"内部词"}},
		map[string]filePart{"file": {"a.txt", repeatBytes("内部词\n", 30)}})
	code, resp := doJSON(t, s.Handler(), "POST", "/api/mask", body, ct)
	if code != http.StatusConflict || resp["error"] != "preview_required" {
		t.Fatalf("应 409 preview_required：%d %v", code, resp)
	}
	pres, ok := resp["presentation"].(map[string]any)
	if !ok || pres["message_id"] != "preview.required" {
		t.Fatalf("应携带 preview.required：%v", resp["presentation"])
	}
	if _, has := resp["masked_b64"]; has {
		t.Fatal("确认态不得产出产物")
	}
}

func repeatBytes(s string, n int) []byte {
	var out []byte
	for i := 0; i < n; i++ {
		out = append(out, []byte(s)...)
	}
	return out
}
