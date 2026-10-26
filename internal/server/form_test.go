package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pandatoufu/data-masking/internal/config"
	"github.com/pandatoufu/data-masking/internal/core"
)

// orderedPart 按给定顺序写入 multipart：filename 为空表示普通值字段。
type orderedPart struct {
	field    string
	filename string
	data     []byte
}

func makeOrderedMultipart(t *testing.T, parts ...orderedPart) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, p := range parts {
		if p.filename == "" {
			if err := w.WriteField(p.field, string(p.data)); err != nil {
				t.Fatal(err)
			}
			continue
		}
		fw, err := w.CreateFormFile(p.field, p.filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(p.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

func newTestServerWithLimit(t *testing.T, limit int64) *Server {
	t.Helper()
	cfg := config.Defaults()
	cfg.MaxFileSize = limit
	return New(cfg, nil,
		WithOpenBrowser(func(string) error { return nil }),
		WithLockPath(filepath.Join(t.TempDir(), "instance.lock")),
		WithIdleTimeout(1e9),
		WithFallbackWriter(&bytes.Buffer{}),
	)
}

func postRaw(t *testing.T, h http.Handler, body io.Reader, contentType string) (int, map[string]any) {
	t.Helper()
	return postRawPath(t, h, "/api/mask", body, contentType)
}

func postRawPath(t *testing.T, h http.Handler, path string, body io.Reader, contentType string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", path, body)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应不是 JSON：%s", rec.Body.String())
	}
	return rec.Code, out
}

func presID(resp map[string]any) string {
	pres, _ := resp["presentation"].(map[string]any)
	id, _ := pres["message_id"].(string)
	return id
}

// 1) readBounded：最多 limit+1、无整数溢出、负数按 0 处理。
func TestReadBoundedLimitPlusOne(t *testing.T) {
	data, over, err := readBounded(strings.NewReader("0123456789"), 4)
	if err != nil || !over || string(data) != "01234" {
		t.Fatalf("上限 4 应保留 5 字节并标记超限：%q over=%v err=%v", data, over, err)
	}
	data, over, err = readBounded(strings.NewReader("0123"), 4)
	if err != nil || over || string(data) != "0123" {
		t.Fatalf("等于上限不应超限：%q over=%v err=%v", data, over, err)
	}
	data, over, err = readBounded(strings.NewReader("ab"), -1)
	if err != nil || !over || string(data) != "a" {
		t.Fatalf("负数上限按 0 处理并多读 1 字节：%q over=%v err=%v", data, over, err)
	}
	data, over, err = readBounded(strings.NewReader("hello"), math.MaxInt64)
	if err != nil || over || string(data) != "hello" {
		t.Fatalf("MaxInt64 上限不得溢出：%q over=%v err=%v", data, over, err)
	}
}

// 2) 逐 part 解析：任意顺序下文档快照最多 limit+1，无长度声明也判定超限；
// 词文件独立 1MiB 上限；未知文件不累积。
func TestParseFormBoundedSnapshots(t *testing.T) {
	limit := int64(16)
	doc := bytes.Repeat([]byte("a"), 100)
	word := bytes.Repeat([]byte("w"), int(core.MaxWordFileSize)+10)
	body, ct := makeOrderedMultipart(t,
		orderedPart{field: "file", filename: "notes.txt", data: doc},
		orderedPart{field: "custom_file", filename: "words.txt", data: word},
		orderedPart{field: "extra", filename: "unknown.bin", data: bytes.Repeat([]byte("x"), 1024)},
		orderedPart{field: "profile", data: []byte("strict")},
	)
	// 去掉 ContentLength，模拟没有可信长度声明的输入。
	req := httptest.NewRequest("POST", "/api/mask", io.NopCloser(bytes.NewReader(body.Bytes())))
	req.Header.Set("Content-Type", ct)
	if req.ContentLength != -1 {
		t.Fatalf("测试应使用未知长度请求，got %d", req.ContentLength)
	}
	fd, err := parseForm(req, limit, formForMask)
	if err != nil {
		t.Fatal(err)
	}
	if !fd.doc.over || int64(len(fd.doc.data)) != limit+1 {
		t.Fatalf("文档快照应为 limit+1 并标记超限：len=%d over=%v", len(fd.doc.data), fd.doc.over)
	}
	if !fd.word.over || int64(len(fd.word.data)) != core.MaxWordFileSize+1 {
		t.Fatalf("词文件快照应为 1MiB+1 并标记超限：len=%d over=%v", len(fd.word.data), fd.word.over)
	}
	if got := fd.first("profile"); got != "strict" {
		t.Fatalf("文件在前、profile 在后仍应读到 profile：%q", got)
	}
	if len(fd.values["extra"]) != 0 {
		t.Fatalf("未知文件字段不得进入值表：%v", fd.values["extra"])
	}
}

// 3) 协议元数据保护未放宽：part 数超限拒绝；malformed multipart 拒绝。
func TestParseFormProtocolLimits(t *testing.T) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for i := 0; i < formMaxParts+1; i++ {
		if err := w.WriteField("confirmed", fmt.Sprintf("w%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	_ = w.Close()
	req := httptest.NewRequest("POST", "/api/mask", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if _, err := parseForm(req, 1024, formForMask); !errors.Is(err, errFormTooLarge) {
		t.Fatalf("part 数超过协议上限应拒绝，got %v", err)
	}

	req = httptest.NewRequest("POST", "/api/mask", strings.NewReader("not-multipart"))
	req.Header.Set("Content-Type", "multipart/form-data")
	if _, err := parseForm(req, 1024, formForMask); err == nil {
		t.Fatal("缺少 boundary 应拒绝")
	}

	// 合法边界但正文在 part 中途截断：应返回读取错误而非静默成功。
	var trunc bytes.Buffer
	tw := multipart.NewWriter(&trunc)
	fw, err := tw.CreateFormFile("file", "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(bytes.Repeat([]byte("a"), 64))
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	full := trunc.Bytes()
	cut := bytes.LastIndex(full, []byte("\r\n--"))
	if cut < 0 {
		t.Fatal("找不到边界")
	}
	req = httptest.NewRequest("POST", "/api/mask", bytes.NewReader(full[:cut]))
	req.Header.Set("Content-Type", tw.FormDataContentType())
	if _, err := parseForm(req, 1024, formForMask); err == nil {
		t.Fatal("截断 multipart 应拒绝")
	}
}

// 4) 文档超限：返回既有大小错误语义的本地化反馈，不交付任何产物；错误优先级保持。
func TestMaskOverLimitResponseHasNoArtifact(t *testing.T) {
	s := newTestServerWithLimit(t, 16)
	h := s.Handler()
	big := []byte(strings.Repeat("a", 17))
	body, ct := makeOrderedMultipart(t,
		orderedPart{field: "profile", data: []byte("strict")},
		orderedPart{field: "file", filename: "notes.txt", data: big},
	)
	code, resp := postRaw(t, h, bytes.NewReader(body.Bytes()), ct)
	if code != http.StatusBadRequest || presID(resp) != "file.too_large_stream" {
		t.Fatalf("超限应返回 400 + file.too_large_stream：%d %v", code, resp)
	}
	for _, key := range []string{"masked_b64", "mapping_b64"} {
		if _, ok := resp[key]; ok {
			t.Fatalf("超限不得返回产物 %s", key)
		}
	}
	if strings.Contains(fmt.Sprint(resp["message"]), "a") && !strings.Contains(fmt.Sprint(resp["message"]), "上限") {
		t.Fatalf("错误文案不得回显原文：%v", resp["message"])
	}

	// 未知 profile 与超限同时出现：保持旧顺序，先报 profile。
	body, ct = makeOrderedMultipart(t,
		orderedPart{field: "profile", data: []byte("nope")},
		orderedPart{field: "file", filename: "notes.txt", data: big},
	)
	code, resp = postRaw(t, h, bytes.NewReader(body.Bytes()), ct)
	if code != http.StatusBadRequest || presID(resp) != "profile.unknown" {
		t.Fatalf("profile 错误应优先于大小错误：%d %v", code, resp)
	}
}

// 5) mask 表单：文件在前、profile 在后、重复 confirmed、覆盖字段错误、
// custom 关闭忽略超大词文件、custom 开启按 1MiB 拒绝。
func TestMaskFormOrderAndWordRules(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	doc := []byte("内部词 内部词 内部词\n")

	// 文件在前 + profile/覆盖在后 + custom 关闭：超大词文件静默忽略。
	oversizedWord := bytes.Repeat([]byte("w\n"), 600000) // > 1MiB
	body, ct := makeOrderedMultipart(t,
		orderedPart{field: "file", filename: "a.txt", data: doc},
		orderedPart{field: "custom_file", filename: "words.txt", data: oversizedWord},
		orderedPart{field: "profile", data: []byte("__custom")},
		orderedPart{field: "rule_custom", data: []byte("false")},
	)
	code, resp := postRaw(t, h, bytes.NewReader(body.Bytes()), ct)
	if code != http.StatusOK || resp["ok"] != true {
		t.Fatalf("custom 关闭时应忽略超大词文件：%d %v", code, resp)
	}

	// custom 开启：同一词文件按 1MiB 拒绝。
	body, ct = makeOrderedMultipart(t,
		orderedPart{field: "file", filename: "a.txt", data: doc},
		orderedPart{field: "custom_file", filename: "words.txt", data: oversizedWord},
		orderedPart{field: "profile", data: []byte("strict")},
	)
	code, resp = postRaw(t, h, bytes.NewReader(body.Bytes()), ct)
	if code != http.StatusBadRequest || presID(resp) != "words.file_too_large" {
		t.Fatalf("custom 开启时超大词文件应拒绝：%d %v", code, resp)
	}

	// 重复 confirmed 保留：高频词两次确认后直接成功（无 409）。
	high := bytes.Repeat([]byte("内部词\n"), 30)
	body, ct = makeOrderedMultipart(t,
		orderedPart{field: "file", filename: "a.txt", data: high},
		orderedPart{field: "profile", data: []byte("strict")},
		orderedPart{field: "custom", data: []byte("内部词")},
		orderedPart{field: "confirmed", data: []byte("内部词")},
		orderedPart{field: "confirmed", data: []byte("内部词")},
	)
	code, resp = postRaw(t, h, bytes.NewReader(body.Bytes()), ct)
	if code != http.StatusOK || resp["ok"] != true {
		t.Fatalf("重复 confirmed 应保留并避免 409：%d %v", code, resp)
	}

	// 覆盖字段错误：文件在前仍按既有规则报错。
	body, ct = makeOrderedMultipart(t,
		orderedPart{field: "file", filename: "a.txt", data: doc},
		orderedPart{field: "profile", data: []byte("__custom")},
		orderedPart{field: "rule_email", data: []byte("abc")},
	)
	code, resp = postRaw(t, h, bytes.NewReader(body.Bytes()), ct)
	if code != http.StatusBadRequest || presID(resp) != "rule.bad_bool" {
		t.Fatalf("覆盖字段错误应保持：%d %v", code, resp)
	}
}

// 6) restore 表单：文件优先于粘贴、未使用 text 不影响 file、首个文件生效、
// 无文件粘贴按文档上限拒绝、mapping 缺失优先。
func TestRestoreFormSemantics(t *testing.T) {
	s := newTestServerWithLimit(t, 64)
	h := s.Handler()

	// 先用默认服务生成配对 mapping 与密文。
	full := newTestServer(t)
	body, ct := makeOrderedMultipart(t,
		orderedPart{field: "file", filename: "a.txt", data: []byte("电话 13812345678\n")},
		orderedPart{field: "profile", data: []byte("strict")},
	)
	_, masResp := postRaw(t, full.Handler(), bytes.NewReader(body.Bytes()), ct)
	masked, _ := base64.StdEncoding.DecodeString(masResp["masked_b64"].(string))
	mapping, _ := base64.StdEncoding.DecodeString(masResp["mapping_b64"].(string))

	// 文件在前、mapping 在后；同时带超限 text，仍应成功且使用文件。
	bigText := strings.Repeat("x", 200)
	body, ct = makeOrderedMultipart(t,
		orderedPart{field: "file", filename: "a.masked.txt", data: masked},
		orderedPart{field: "text", data: []byte(bigText)},
		orderedPart{field: "mapping", filename: "a.mapping.json", data: mapping},
	)
	code, resp := postRawPath(t, h, "/api/restore", bytes.NewReader(body.Bytes()), ct)
	if code != http.StatusOK || resp["ok"] != true {
		t.Fatalf("文件优先且未使用 text 超限不应影响结果：%d %v", code, resp)
	}
	if restored, _ := base64.StdEncoding.DecodeString(resp["restored_b64"].(string)); !bytes.Contains(restored, []byte("13812345678")) {
		t.Fatalf("还原内容不符：%q", restored)
	}

	// 无文件、粘贴超限：按文档上限拒绝。
	body, ct = makeOrderedMultipart(t,
		orderedPart{field: "mapping", filename: "a.mapping.json", data: mapping},
		orderedPart{field: "text", data: []byte(bigText)},
	)
	code, resp = postRawPath(t, h, "/api/restore", bytes.NewReader(body.Bytes()), ct)
	if code != http.StatusBadRequest || presID(resp) != "file.too_large_stream" {
		t.Fatalf("粘贴超限应拒绝：%d %v", code, resp)
	}
	for _, key := range []string{"restored_b64"} {
		if _, ok := resp[key]; ok {
			t.Fatalf("超限不得返回 %s", key)
		}
	}

	// 首个同名文件生效：第二个文件即使不同也不参与。
	body, ct = makeOrderedMultipart(t,
		orderedPart{field: "file", filename: "first.masked.txt", data: masked},
		orderedPart{field: "file", filename: "second.masked.txt", data: []byte("other")},
		orderedPart{field: "mapping", filename: "a.mapping.json", data: mapping},
	)
	code, resp = postRawPath(t, h, "/api/restore", bytes.NewReader(body.Bytes()), ct)
	if code != http.StatusOK {
		t.Fatalf("首个文件应生效：%d %v", code, resp)
	}
	// 命名沿用 mapping 记录的原件名；成功本身证明首个 file 部件参与处理。
	if name, _ := resp["name"].(string); name != "a.restored.txt" {
		t.Fatalf("还原命名应来自配对 mapping：%q", name)
	}

	// mapping 缺失优先于输入缺失。
	body, ct = makeOrderedMultipart(t, orderedPart{field: "text", data: []byte("x")})
	code, resp = postRawPath(t, h, "/api/restore", bytes.NewReader(body.Bytes()), ct)
	if code != http.StatusBadRequest || presID(resp) != "http.mapping_missing" {
		t.Fatalf("mapping 缺失应保持：%d %v", code, resp)
	}
}
