package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/pandatoufu/data-masking/internal/docx"
	"github.com/pandatoufu/data-masking/internal/docxtest"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// capturedPost 是 Go 侧捕获的一次真实到达 handler 的 POST（用于校验原始 multipart 字节）。
type capturedPost struct {
	path        string
	contentType string
	body        []byte
}

type capturedFile struct {
	filename string
	data     []byte
}

type harnessCase struct {
	Name     string         `json:"name"`
	Path     string         `json:"path"`
	Status   int            `json:"status"`
	Response map[string]any `json:"response"`
	JS       map[string]any `json:"js"`
}

type harnessReport struct {
	Cases []harnessCase `json:"cases"`
	Fatal string        `json:"fatal"`
}

// TestRealAppMultipartTransport 加载真实 web/static/app.js（Node vm + 轻量 DOM fake +
// 测试钩子），以真实 maskSubmit / initRestore / postMultipart 把含文件的 FormData 发给
// 真实 Go handler，校验：body 为 ArrayBuffer、Content-Type/boundary 配套、DOCX 二进制与
// UTF-8/GBK 词字节、中文文件名、部分勾选、重复 confirmed、409 重试、mapping 上传与粘贴
// 还原、以及序列化失败不发出请求。Node 不可用时跳过（Go core/build 不依赖 Node）。
func TestRealAppMultipartTransport(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node 不可用：跳过真实 app.js 上传回归（不使 Go core/build 依赖 Node）")
	}
	root := repoRoot(t)

	tmp := t.TempDir()
	fixtures := filepath.Join(tmp, "fixtures")
	if err := os.MkdirAll(fixtures, 0o755); err != nil {
		t.Fatal(err)
	}

	docxData := docxBytes(t, docxtest.FixtureOptions{Body: []string{"电话 13812345678", "绝密词 手输词 内容"}})
	writeFixture(t, fixtures, "合同.docx", docxData)
	writeFixture(t, fixtures, "notes.txt", []byte("绝密词 A\n绝密词 B\n"))
	writeFixture(t, fixtures, "highfreq.txt", []byte(strings.Repeat("内部词 \n", 30)))
	utf8Words := []byte("绝密词\n")
	writeFixture(t, fixtures, "words-utf8.txt", utf8Words)
	gbkWords, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("绝密词\n"))
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, fixtures, "words-gbk.txt", gbkWords)

	s := newTestServer(t)
	var mu sync.Mutex
	var posts []capturedPost
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/") {
			mu.Lock()
			posts = append(posts, capturedPost{path: r.URL.Path, contentType: r.Header.Get("Content-Type"), body: body})
			mu.Unlock()
		}
		r2 := r.Clone(r.Context())
		r2.Body = io.NopCloser(bytes.NewReader(body))
		s.Handler().ServeHTTP(w, r2)
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	reportPath := filepath.Join(tmp, "report.json")
	harness := filepath.Join(root, "internal", "server", "testdata", "multipart_transport.mjs")
	appJs := filepath.Join(root, "web", "static", "app.js")
	cmd := exec.Command(node, harness, appJs, srv.URL, fixtures, reportPath)
	cmd.Dir = root
	out, runErr := cmd.CombinedOutput()
	raw, rerr := os.ReadFile(reportPath)
	if rerr != nil {
		t.Fatalf("node harness 未产出报告：runErr=%v readErr=%v\n%s", runErr, rerr, out)
	}
	var rep harnessReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatalf("报告解析失败：%v\n%s", err, raw)
	}
	if rep.Fatal != "" {
		t.Fatalf("harness fatal: %s", rep.Fatal)
	}
	if runErr != nil {
		t.Fatalf("node harness 失败：%v\n%s", runErr, out)
	}

	// JS 侧事实：每个 fetch 用例 body 必须是完整 ArrayBuffer，Content-Type/boundary 配套。
	for _, c := range rep.Cases {
		if c.Name == "serialization-failure" {
			if c.JS["fetchCalled"] != false {
				t.Errorf("%s: 序列化失败不得发出请求", c.Name)
			}
			if c.JS["rejected"] != true {
				t.Errorf("%s: 序列化失败应 reject", c.Name)
			}
			continue
		}
		if c.JS["fetchCalled"] != true {
			t.Errorf("%s: 应发出请求", c.Name)
		}
		if c.JS["bodyIsArrayBuffer"] != true {
			t.Errorf("%s: body 必须为 ArrayBuffer（旧 direct FormData 会失败），实际 %v", c.Name, c.JS["bodyIsArrayBuffer"])
		}
		ct, _ := c.JS["contentType"].(string)
		if !strings.HasPrefix(ct, "multipart/form-data; boundary=") {
			t.Errorf("%s: Content-Type 应为带 boundary 的 multipart，实际 %q", c.Name, ct)
		}
		if c.JS["boundaryPresent"] != true {
			t.Errorf("%s: Content-Type 声明的 boundary 必须出现在 body 中", c.Name)
		}
	}

	// 捕获到的 POST 应与 report 中的 fetch 用例一一对应（顺序一致）。
	idxByName := map[string]int{}
	postIdx := 0
	for _, c := range rep.Cases {
		if c.JS["fetchCalled"] != true {
			continue
		}
		if postIdx >= len(posts) {
			t.Fatalf("捕获 POST 数少于 report 记录数：%d", len(posts))
		}
		pc := posts[postIdx]
		if pc.path != c.Path {
			t.Fatalf("%s: 捕获路径 %q != report %q", c.Name, pc.path, c.Path)
		}
		if ct, _ := c.JS["contentType"].(string); pc.contentType != ct {
			t.Fatalf("%s: 捕获 Content-Type %q != JS %q", c.Name, pc.contentType, ct)
		}
		idxByName[c.Name] = postIdx
		postIdx++
	}
	if postIdx != len(posts) {
		t.Fatalf("捕获 POST 数 %d 与 fetch 用例数 %d 不一致", len(posts), postIdx)
	}

	byName := map[string]harnessCase{}
	for _, c := range rep.Cases {
		byName[c.Name] = c
	}

	// 1) 二进制 DOCX + 中文文件名 + 手输词 + 词文件 + 重复 confirmed。
	{
		c := byName["docx-binary-mask"]
		vals, files := parseCapturedPost(t, posts[idxByName["docx-binary-mask"]])
		if got := vals["profile"]; len(got) != 1 || got[0] != "balanced" {
			t.Errorf("docx-binary-mask profile=%v", got)
		}
		if got := vals["custom"]; len(got) != 1 || got[0] != "手输词" {
			t.Errorf("docx-binary-mask 手输 custom=%v", got)
		}
		if got := vals["confirmed"]; len(got) != 2 || got[0] != "绝密词" || got[1] != "绝密词" {
			t.Errorf("docx-binary-mask 重复 confirmed 未保持：%v", got)
		}
		assertFile(t, files, "file", "合同.docx", docxData, "docx-binary-mask")
		assertFile(t, files, "custom_file", "词表.txt", utf8Words, "docx-binary-mask")
		assertResponseOK(t, c, "docx-binary-mask")
		if c.Response["format"] != "docx" {
			t.Errorf("docx-binary-mask format=%v", c.Response["format"])
		}
		masked := decodeB64(t, c.Response["masked_b64"], "docx-binary-mask")
		doc, err := docx.Open(masked, docx.DefaultLimits())
		if err != nil {
			t.Fatalf("docx-binary-mask 产物应为有效 DOCX：%v", err)
		}
		main, _ := doc.PartBytes(doc.MainPart())
		if !bytes.Contains(main, []byte("CUSTOM_")) {
			t.Errorf("docx-binary-mask 正文应含自定义词占位符")
		}
		if bytes.Contains(main, []byte("绝密词")) || bytes.Contains(main, []byte("手输词")) {
			t.Errorf("docx-binary-mask 正文不应残留自定义词明文")
		}
		mapping := decodeB64(t, c.Response["mapping_b64"], "docx-binary-mask")
		var mp map[string]any
		if err := json.Unmarshal(mapping, &mp); err != nil {
			t.Fatalf("docx-binary-mask mapping 非法：%v", err)
		}
		if mp["version"].(float64) != 2 {
			t.Errorf("docx-binary-mask mapping version=%v", mp["version"])
		}
	}

	// 2) GBK 词文件原始字节保真 + 生效。
	{
		c := byName["wordfile-gbk"]
		_, files := parseCapturedPost(t, posts[idxByName["wordfile-gbk"]])
		assertFile(t, files, "custom_file", "词表.txt", gbkWords, "wordfile-gbk")
		assertResponseOK(t, c, "wordfile-gbk")
		masked := decodeB64(t, c.Response["masked_b64"], "wordfile-gbk")
		if bytes.Contains(masked, []byte("绝密词")) {
			t.Errorf("wordfile-gbk 不应残留 GBK 词明文")
		}
		if !bytes.Contains(masked, []byte("[CUSTOM_1]")) {
			t.Errorf("wordfile-gbk 应出现自定义词占位符")
		}
	}

	// 3) 词的部分勾选 → words-selected.txt。
	{
		c := byName["partial-checked-words"]
		_, files := parseCapturedPost(t, posts[idxByName["partial-checked-words"]])
		assertFile(t, files, "custom_file", "words-selected.txt", []byte("绝密词\n"), "partial-checked-words")
		assertResponseOK(t, c, "partial-checked-words")
	}

	// 4) 409 preview_required 与重试。
	{
		first := byName["mask-409-first"]
		if first.Status != http.StatusConflict || first.Response["error"] != "preview_required" {
			t.Errorf("mask-409-first 应为 409 preview_required：%d %v", first.Status, first.Response)
		}
		retry := byName["mask-409-retry"]
		assertResponseOK(t, retry, "mask-409-retry")
		_, files := parseCapturedPost(t, posts[idxByName["mask-409-retry"]])
		if got := files["file"]; len(got) != 1 || got[0].filename != "高频.txt" {
			t.Errorf("mask-409-retry 应保留原始文件名：%v", got)
		}
	}

	// 5) 还原 DOCX。
	{
		c := byName["restore-docx"]
		vals, files := parseCapturedPost(t, posts[idxByName["restore-docx"]])
		if len(files["file"]) != 1 || len(files["mapping"]) != 1 {
			t.Fatalf("restore-docx 应含 file+mapping：%v", files)
		}
		// 文件模式：非激活的 text 字段必须缺席。
		if got := vals["text"]; len(got) != 0 {
			t.Errorf("restore-docx 文件模式不应提交 text 字段：%v", got)
		}
		assertResponseOK(t, c, "restore-docx")
		if c.Response["format"] != "docx" {
			t.Errorf("restore-docx format=%v", c.Response["format"])
		}
		restored := decodeB64(t, c.Response["restored_b64"], "restore-docx")
		if _, err := docx.Open(restored, docx.DefaultLimits()); err != nil {
			t.Fatalf("restore-docx 产物应为有效 DOCX：%v", err)
		}
		if n, _ := c.Response["restored_count"].(float64); n < 1 {
			t.Errorf("restore-docx 应还原至少 1 处：%v", c.Response["restored_count"])
		}
	}

	// 6) 粘贴文本还原。
	{
		c := byName["restore-paste"]
		vals, files := parseCapturedPost(t, posts[idxByName["restore-paste"]])
		if got := vals["text"]; len(got) != 1 || !strings.Contains(got[0], "[PHONE_1]") {
			t.Errorf("restore-paste text=%v", got)
		}
		if len(files["mapping"]) != 1 {
			t.Errorf("restore-paste 应含 mapping")
		}
		// 文本模式：非激活的 file 字段必须缺席。
		if len(files["file"]) != 0 {
			t.Errorf("restore-paste 文本模式不应提交 file 字段：%v", files["file"])
		}
		assertResponseOK(t, c, "restore-paste")
		if c.Response["format"] != "text" || c.Response["encoding"] != "UTF-8" {
			t.Errorf("restore-paste format/encoding=%v/%v", c.Response["format"], c.Response["encoding"])
		}
		restored := decodeB64(t, c.Response["restored_b64"], "restore-paste")
		if !bytes.Contains(restored, []byte("13812345678")) {
			t.Errorf("restore-paste 应找回原文：%q", restored)
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

func writeFixture(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func parseCapturedPost(t *testing.T, pc capturedPost) (map[string][]string, map[string][]capturedFile) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, pc.path, bytes.NewReader(pc.body))
	req.Header.Set("Content-Type", pc.contentType)
	if err := req.ParseMultipartForm(32 << 20); err != nil {
		t.Fatalf("multipart 解析失败（%s）：%v", pc.path, err)
	}
	files := map[string][]capturedFile{}
	if req.MultipartForm != nil {
		for field, fhs := range req.MultipartForm.File {
			for _, fh := range fhs {
				f, err := fh.Open()
				if err != nil {
					t.Fatal(err)
				}
				data, _ := io.ReadAll(f)
				_ = f.Close()
				files[field] = append(files[field], capturedFile{filename: fh.Filename, data: data})
			}
		}
	}
	return req.MultipartForm.Value, files
}

func assertFile(t *testing.T, files map[string][]capturedFile, field, name string, want []byte, ctx string) {
	t.Helper()
	got := files[field]
	if len(got) != 1 {
		t.Fatalf("%s：字段 %s 应有 1 个文件，实际 %d", ctx, field, len(got))
	}
	if got[0].filename != name {
		t.Errorf("%s：字段 %s 文件名 %q != %q", ctx, field, got[0].filename, name)
	}
	if !bytes.Equal(got[0].data, want) {
		t.Errorf("%s：字段 %s 字节不保真（len %d != %d）", ctx, field, len(got[0].data), len(want))
	}
}

func assertResponseOK(t *testing.T, c harnessCase, ctx string) {
	t.Helper()
	if c.Status != http.StatusOK || c.Response["ok"] != true {
		t.Fatalf("%s：应为 200 ok，实际 %d %v", ctx, c.Status, c.Response)
	}
}

func decodeB64(t *testing.T, v any, ctx string) []byte {
	t.Helper()
	s, _ := v.(string)
	if s == "" {
		t.Fatalf("%s：缺少 base64 产物", ctx)
	}
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("%s：base64 解码失败：%v", ctx, err)
	}
	return data
}
