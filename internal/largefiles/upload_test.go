//go:build large

package largefiles

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/pandatoufu/data-masking/internal/config"
	"github.com/pandatoufu/data-masking/internal/server"
)

// isolateTemp 把进程临时目录指向独立目录，证明大于 32MiB 的请求不遗留 multipart 临时文件。
func isolateTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, dir)
	}
	return dir
}

func assertTempEmpty(t *testing.T, dir, ctx string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("%s: %v", ctx, err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("%s: 不应遗留 multipart 临时文件：%v", ctx, names)
	}
}

type uploadPart struct {
	field    string
	filename string
	data     []byte
}

func multipartBody(t *testing.T, parts ...uploadPart) (*bytes.Buffer, string) {
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

func postAPI(t *testing.T, h http.Handler, path string, body io.Reader, ct string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应不是 JSON：%.200s", rec.Body.String())
	}
	return rec.Code, out
}

// 4.4 GUI handler 路径的真实 >32MiB 上传（成功/失败/还原）不遗留 multipart 临时文件。
// 本条在 Linux 独立进程运行并由外部工具记录 OS 进程峰值；Go 堆采样由 TestLargeMeasure 记录。
func TestLargeUpload(t *testing.T) {
	dir := isolateTemp(t)
	cfg := config.Defaults()
	s := server.New(cfg, nil, server.WithOpenBrowser(func(string) error { return nil }))
	h := s.Handler()

	// 成功：40MiB 纯文本。
	const fortyMiB = 40 << 20
	text := largeText(fortyMiB)
	body, ct := multipartBody(t,
		uploadPart{field: "file", filename: "大.txt", data: text},
		uploadPart{field: "profile", data: []byte("strict")},
	)
	code, resp := postAPI(t, h, "/api/mask", body, ct)
	if code != http.StatusOK || resp["ok"] != true {
		t.Fatalf("40MiB 文本应成功：%d", code)
	}
	masked, err := base64.StdEncoding.DecodeString(resp["masked_b64"].(string))
	if err != nil || len(masked) != fortyMiB {
		t.Fatalf("40MiB 文本产物异常：len=%d err=%v", len(masked), err)
	}
	mapping, err := base64.StdEncoding.DecodeString(resp["mapping_b64"].(string))
	if err != nil || len(mapping) == 0 {
		t.Fatalf("mapping 异常：%v", err)
	}
	maskedName, _ := resp["masked_name"].(string)
	assertTempEmpty(t, dir, "40MiB 成功请求")

	// 成功：40MiB DOCX（媒体型）。
	docxData := sizedDocx(t, fortyMiB)
	body, ct = multipartBody(t,
		uploadPart{field: "file", filename: "大.docx", data: docxData},
		uploadPart{field: "profile", data: []byte("strict")},
	)
	if code, resp := postAPI(t, h, "/api/mask", body, ct); code != http.StatusOK || resp["ok"] != true {
		t.Fatalf("40MiB DOCX 应成功：%d", code)
	}
	assertTempEmpty(t, dir, "40MiB DOCX 成功请求")

	// 还原：40MiB 文本回程。
	body, ct = multipartBody(t,
		uploadPart{field: "file", filename: maskedName, data: masked},
		uploadPart{field: "mapping", filename: "大.mapping.json", data: mapping},
	)
	code, resp = postAPI(t, h, "/api/restore", body, ct)
	if code != http.StatusOK || resp["ok"] != true {
		t.Fatalf("40MiB 回程应成功：%d", code)
	}
	restored, err := base64.StdEncoding.DecodeString(resp["restored_b64"].(string))
	if err != nil || !bytes.Equal(restored, text) {
		t.Fatalf("40MiB 回程字节不一致：len=%d err=%v", len(restored), err)
	}
	assertTempEmpty(t, dir, "40MiB 回程请求")

	// 失败：默认上限 + 1 字节。
	over := largeText(size50MiB + 1)
	body, ct = multipartBody(t,
		uploadPart{field: "file", filename: "超限.txt", data: over},
		uploadPart{field: "profile", data: []byte("strict")},
	)
	code, resp = postAPI(t, h, "/api/mask", body, ct)
	if code != http.StatusBadRequest {
		t.Fatalf("超限请求应 400：%d", code)
	}
	pres, _ := resp["presentation"].(map[string]any)
	if id, _ := pres["message_id"].(string); id != "file.too_large_stream" {
		t.Fatalf("超限应有本地化大小反馈：%v", resp)
	}
	if _, ok := resp["masked_b64"]; ok {
		t.Fatal("超限不得返回产物")
	}
	assertTempEmpty(t, dir, "超限请求")
}
