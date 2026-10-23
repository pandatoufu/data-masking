package desktop

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"testing"
)

type filePart struct {
	name string
	data []byte
}

func multipartFiles(t *testing.T, fields map[string]string, files map[string]filePart) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	for k, fp := range files {
		fw, err := w.CreateFormFile(k, fp.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(fp.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

func multipartFile(t *testing.T, filename string, data []byte, fields map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

func containsHan(s string) bool {
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}

func decodeB64(s string) ([]byte, error) { return base64.StdEncoding.DecodeString(s) }

func jsonStr(bodyJSON, key string, t *testing.T) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(bodyJSON), &m); err != nil {
		t.Fatalf("解析 JSON 失败：%v", err)
	}
	s, _ := m[key].(string)
	if s == "" {
		t.Fatalf("JSON 缺少 %q", key)
	}
	return s
}
