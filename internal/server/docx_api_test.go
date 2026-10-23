package server

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pandatoufu/data-masking/internal/docx"
	"github.com/pandatoufu/data-masking/internal/docxtest"
)

func docxBytes(t *testing.T, opts docxtest.FixtureOptions) []byte {
	t.Helper()
	b, err := docxtest.BuildFullFixture(opts)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// 8.1 DOCX 响应 format/encoding/cleanup 元数据，产物可解码为真实 DOCX。
func TestDocxMaskAPIFormatAndCleanup(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	data := docxBytes(t, docxtest.FixtureOptions{
		Body:         []string{"电话 13812345678"},
		InlineInsert: "新 13900000000",
		Comments:     &docxtest.CommentsFixture{Text: "批注值", Extended: true, People: true},
	})
	body, ct := makeMultipart(t, map[string][]string{"profile": {"strict"}},
		map[string]filePart{"file": {"合同.docx", data}})
	code, resp := doJSON(t, h, "POST", "/api/mask", body, ct)
	if code != http.StatusOK || resp["ok"] != true {
		t.Fatalf("DOCX 脱敏失败：%d %v", code, resp)
	}
	if resp["format"] != "docx" {
		t.Fatalf("format 应为 docx：%v", resp["format"])
	}
	if enc, ok := resp["encoding"].(string); !ok || enc != "" {
		t.Fatalf("DOCX encoding 键应保留且为空串：%v", resp["encoding"])
	}
	if name, _ := resp["masked_name"].(string); !strings.HasSuffix(name, ".masked.docx") {
		t.Fatalf("DOCX 脱敏名错误：%q", name)
	}
	cleanup, ok := resp["cleanup"].(map[string]any)
	if !ok {
		t.Fatalf("响应应含本次清理摘要：%v", resp["cleanup"])
	}
	if n, _ := cleanup["comments_deleted"].(float64); n < 1 {
		t.Fatalf("清理摘要应含删除批注数量：%v", cleanup)
	}
	masked, _ := base64.StdEncoding.DecodeString(resp["masked_b64"].(string))
	if _, err := docx.Open(masked, docx.DefaultLimits()); err != nil {
		t.Fatalf("masked_b64 应为有效 DOCX：%v", err)
	}
	mappingJSON, _ := base64.StdEncoding.DecodeString(resp["mapping_b64"].(string))
	var mp map[string]any
	if err := json.Unmarshal(mappingJSON, &mp); err != nil {
		t.Fatal(err)
	}
	if mp["version"].(float64) != 2 || mp["source_format"] != "docx" || mp["source_encoding"] != "" {
		t.Fatalf("DOCX mapping 字段错误：%v", mp)
	}
}

// 8.1/8.4 还原响应区分 DOCX 与跨格式文本；纯文本响应兼容。
func TestDocxRestoreAPIFormats(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	data := docxBytes(t, docxtest.FixtureOptions{Body: []string{"电话 13812345678"}})
	body, ct := makeMultipart(t, map[string][]string{"profile": {"strict"}},
		map[string]filePart{"file": {"合同.docx", data}})
	_, resp := doJSON(t, h, "POST", "/api/mask", body, ct)
	masked, _ := base64.StdEncoding.DecodeString(resp["masked_b64"].(string))
	mappingJSON, _ := base64.StdEncoding.DecodeString(resp["mapping_b64"].(string))

	// DOCX 回程。
	rb, rct := makeMultipart(t, nil, map[string]filePart{
		"file":    {"合同.masked.docx", masked},
		"mapping": {"合同.masked.mapping.json", mappingJSON},
	})
	code, rresp := doJSON(t, h, "POST", "/api/restore", rb, rct)
	if code != http.StatusOK || rresp["format"] != "docx" || rresp["encoding"] != "" {
		t.Fatalf("DOCX 回程响应错误：%d %v", code, rresp)
	}
	if name, _ := rresp["name"].(string); !strings.HasSuffix(name, ".restored.docx") {
		t.Fatalf("DOCX 回程命名错误：%q", name)
	}
	restored, _ := base64.StdEncoding.DecodeString(rresp["restored_b64"].(string))
	if _, err := docx.Open(restored, docx.DefaultLimits()); err != nil {
		t.Fatalf("还原产物应为有效 DOCX：%v", err)
	}

	// 同一 v2 mapping 还原粘贴文本 → text/UTF-8。
	pb, pct := makeMultipart(t, map[string][]string{"text": {"回复 [PHONE_1]"}},
		map[string]filePart{"mapping": {"合同.masked.mapping.json", mappingJSON}})
	code, presp := doJSON(t, h, "POST", "/api/restore", pb, pct)
	if code != http.StatusOK || presp["format"] != "text" || presp["encoding"] != "UTF-8" {
		t.Fatalf("v2+文本回程响应错误：%d %v", code, presp)
	}
	if name, _ := presp["name"].(string); !strings.HasSuffix(name, ".restored.txt") {
		t.Fatalf("v2+文本命名错误：%q", name)
	}

	// 纯文本响应兼容。
	tb, tct := makeMultipart(t, map[string][]string{"profile": {"balanced"}},
		map[string]filePart{"file": {"notes.txt", []byte("电话 13812345678")}})
	code, tresp := doJSON(t, h, "POST", "/api/mask", tb, tct)
	if code != http.StatusOK || tresp["format"] != "text" || tresp["encoding"] != "UTF-8" {
		t.Fatalf("纯文本响应兼容性破坏：%d %v", code, tresp)
	}
}

// 8.2 拒绝时无产物 base64；profile/覆盖门禁不变；preview_required 协议不变。
func TestDocxRejectAndGating(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()

	// 不支持修订 → 400 且无产物。
	bad := docxBytes(t, docxtest.FixtureOptions{Body: []string{"正文"}, ParagraphMarkDelete: true})
	body, ct := makeMultipart(t, map[string][]string{"profile": {"strict"}},
		map[string]filePart{"file": {"bad.docx", bad}})
	code, resp := doJSON(t, h, "POST", "/api/mask", body, ct)
	if code != http.StatusBadRequest {
		t.Fatalf("不支持修订应 400：%d %v", code, resp)
	}
	if _, has := resp["masked_b64"]; has {
		t.Fatalf("拒绝不应返回产物 base64：%v", resp)
	}
	if msg, _ := resp["message"].(string); !strings.Contains(msg, "修订") && !strings.Contains(msg, "批注") {
		t.Fatalf("拒绝应给中文指引：%q", msg)
	}

	// 非 custom 档携带覆盖字段仍 400。
	ok := docxBytes(t, docxtest.FixtureOptions{Body: []string{"正文"}})
	body2, ct2 := makeMultipart(t, map[string][]string{"profile": {"balanced"}, "rule_email": {"true"}},
		map[string]filePart{"file": {"a.docx", ok}})
	if code, resp := doJSON(t, h, "POST", "/api/mask", body2, ct2); code != http.StatusBadRequest {
		t.Fatalf("非 custom 覆盖字段应 400：%d %v", code, resp)
	}

	// 高频 DOCX 仍走 preview_required 协议。
	var bodyText []string
	for i := 0; i < 30; i++ {
		bodyText = append(bodyText, "内部词")
	}
	hf := docxBytes(t, docxtest.FixtureOptions{Body: bodyText})
	body3, ct3 := makeMultipart(t, map[string][]string{"profile": {"balanced"}, "custom": {"内部词"}},
		map[string]filePart{"file": {"hf.docx", hf}})
	code, resp = doJSON(t, h, "POST", "/api/mask", body3, ct3)
	if code != http.StatusConflict || resp["error"] != "preview_required" {
		t.Fatalf("DOCX 高频应要求预览：%d %v", code, resp)
	}
}

// 第二轮 8.6/8.8：执行前/还原页无常驻 DOCX 范围块；紧凑文件按钮与 .docx 选择保持；
// 完整边界改由 README 承载（见 desktop readme 契约测试）。
func TestDocxPageDisclosure(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	req := func(path string) string {
		rec := doRaw(t, h, "GET", path, nil, "")
		return rec
	}
	index := req("/")
	for _, want := range []string{"选择文件", "选择映射"} {
		if !strings.Contains(index, want) {
			t.Errorf("紧凑布局应含文件按钮 %q", want)
		}
	}
	if strings.Contains(index, "DOCX 处理范围") {
		t.Errorf("执行前不应常驻 DOCX 范围块")
	}
	if !strings.Contains(index, `accept=".txt"`) {
		t.Errorf("词文件选择器应仍仅 txt")
	}
	if strings.Contains(index, `accept=".txt,.md,.log,.py,.js,.docx"`) {
		t.Errorf("两个文档入口不应再带旧后缀 accept 过滤")
	}
	if !strings.Contains(index, `accept=".json"`) {
		t.Errorf("mapping 选择器应仍仅 json")
	}
	for _, want := range []string{"纯文本（不限后缀", "PDF", "PPTX", "语法有效"} {
		if !strings.Contains(index, want) {
			t.Errorf("文档入口应常显格式/语法说明 %q", want)
		}
	}
	restore := req("/restore")
	if strings.Contains(restore, "DOCX 回程边界") {
		t.Errorf("还原页不应常驻 DOCX 回程范围块")
	}
	if !strings.Contains(restore, ".docx") {
		t.Errorf("还原页应接受 docx")
	}
}

func doRaw(t *testing.T, h http.Handler, method, path string, body io.Reader, ct string) string {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Body.String()
}
