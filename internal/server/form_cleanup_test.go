package server

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/pandatoufu/data-masking/internal/docxtest"
)

// isolateTempDir 把进程临时目录指向独立目录，用于证明应用请求不会遗留 multipart 临时文件。
func isolateTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, dir)
	}
	return dir
}

func assertNoTempFiles(t *testing.T, dir, ctx string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("%s: 读取临时目录失败：%v", ctx, err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("%s: 不应遗留 multipart 临时文件，got %v", ctx, names)
	}
}

type failingReader struct {
	data []byte
	pos  int
	fail int
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.pos >= r.fail {
		return 0, errors.New("simulated body failure")
	}
	remain := r.fail - r.pos
	if remain > len(p) {
		remain = len(p)
	}
	n := copy(p, r.data[r.pos:r.pos+remain])
	r.pos += n
	return n, nil
}

// 成功、超限、畸形与读取失败的请求都不留下应用创建的 multipart 临时文件。
func TestNoMultipartTempFilesAcrossPaths(t *testing.T) {
	dir := isolateTempDir(t)
	s := newTestServerWithLimit(t, 16)
	h := s.Handler()

	// 成功路径。
	body, ct := makeOrderedMultipart(t,
		orderedPart{field: "file", filename: "a.txt", data: []byte("hello")},
		orderedPart{field: "profile", data: []byte("strict")},
	)
	if code, resp := postRaw(t, h, bytes.NewReader(body.Bytes()), ct); code != http.StatusOK {
		t.Fatalf("成功路径应 200：%d %v", code, resp)
	}
	assertNoTempFiles(t, dir, "成功")

	// 超限路径。
	body, ct = makeOrderedMultipart(t,
		orderedPart{field: "file", filename: "big.txt", data: bytes.Repeat([]byte("a"), 64)},
		orderedPart{field: "profile", data: []byte("strict")},
	)
	if code, _ := postRaw(t, h, bytes.NewReader(body.Bytes()), ct); code != http.StatusBadRequest {
		t.Fatalf("超限应 400，got %d", code)
	}
	assertNoTempFiles(t, dir, "超限")

	// 缺 boundary 的畸形请求。
	req := httptest.NewRequest("POST", "/api/mask", strings.NewReader("nope"))
	req.Header.Set("Content-Type", "multipart/form-data")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("畸形请求应 400，got %d", rec.Code)
	}
	assertNoTempFiles(t, dir, "畸形")

	// 中途读取失败。
	good, ct := makeOrderedMultipart(t,
		orderedPart{field: "file", filename: "a.txt", data: bytes.Repeat([]byte("a"), 256)},
		orderedPart{field: "profile", data: []byte("strict")},
	)
	req = httptest.NewRequest("POST", "/api/mask", &failingReader{data: good.Bytes(), fail: 200})
	req.Header.Set("Content-Type", ct)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("读取失败应 400，got %d %s", rec.Code, rec.Body.String())
	}
	assertNoTempFiles(t, dir, "读取失败")
}

// 预解析 MultipartForm 的临时文件由请求最外层清理：成功与业务拒绝都不遗留。
func TestPreparsedMultipartTempResourceCleaned(t *testing.T) {
	dir := isolateTempDir(t)
	s := newTestServer(t)
	h := s.Handler()

	// 超过 ReadForm 的 10MiB 保留额，强制落盘；用随机媒体部件避免文本脱敏占满回归时间。
	media := make([]byte, (10<<20)+(512<<10))
	for i := range media {
		media[i] = byte(i*131 + 17)
	}
	doc, err := docxtest.BuildFullFixture(docxtest.FixtureOptions{Body: []string{"电话 13812345678"}, Image: media})
	if err != nil {
		t.Fatal(err)
	}
	body, ct := makeOrderedMultipart(t,
		orderedPart{field: "file", filename: "big.docx", data: doc},
		orderedPart{field: "profile", data: []byte("strict")},
	)
	req := httptest.NewRequest("POST", "/api/mask", bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", ct)
	if err := req.ParseMultipartForm(0); err != nil {
		t.Fatalf("预解析应成功：%v", err)
	}
	if req.MultipartForm == nil {
		t.Fatal("应持有预解析 MultipartForm")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("预解析应产生临时文件以验证清理：entries=%d err=%v", len(entries), err)
	}

	// Body 已被预解析消费；handler 从 MultipartForm 读取并在响应前清理。
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("预解析请求应成功：%d %s", rec.Code, rec.Body.String())
	}
	assertNoTempFiles(t, dir, "预解析成功后")

	// 业务拒绝路径：同一预解析资源也必须清理。
	body, ct = makeOrderedMultipart(t,
		orderedPart{field: "file", filename: "bad.rtf", data: []byte("{\\rtf1 x}")},
		orderedPart{field: "profile", data: []byte("strict")},
	)
	req = httptest.NewRequest("POST", "/api/mask", bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", ct)
	if err := req.ParseMultipartForm(0); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("不支持格式应 400：%d %s", rec.Code, rec.Body.String())
	}
	assertNoTempFiles(t, dir, "预解析拒绝后")
}

// 清理失败必须给出当前语言的本地反馈、不带原文/词值，并不能假装成功。
func TestCleanupFailureFeedbackLocalized(t *testing.T) {
	isolateTempDir(t)
	s := newTestServer(t)
	s.formCleanup = func(*http.Request) error { return errors.New("injected cleanup failure") }
	h := s.Handler()
	secret := "绝密 13812345678"
	body, ct := makeOrderedMultipart(t,
		orderedPart{field: "file", filename: "a.txt", data: []byte(secret)},
		orderedPart{field: "profile", data: []byte("strict")},
	)
	code, resp := postRaw(t, h, bytes.NewReader(body.Bytes()), ct)
	if code != http.StatusInternalServerError {
		t.Fatalf("清理失败应 500，got %d %v", code, resp)
	}
	if id := presID(resp); id != "http.form_cleanup_failed" {
		t.Fatalf("应返回 http.form_cleanup_failed，got %q (%v)", id, resp)
	}
	text := strings.ToLower(strings.Join([]string{str(resp["error"]), str(resp["message"])}, " "))
	if strings.Contains(text, "绝密") || strings.Contains(text, "13812345678") {
		t.Fatalf("清理失败文案不得包含原文：%v", resp)
	}
	for _, key := range []string{"masked_b64", "mapping_b64"} {
		if _, ok := resp[key]; ok {
			t.Fatalf("清理失败不得交付产物 %s", key)
		}
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
