package pptx

import (
	"archive/zip"
	"bytes"
	"io"
	"testing"
)

func readZip(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip: %v", err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		out[f.Name] = b
	}
	return out
}

func writeZip(t *testing.T, parts map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range parts {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func mutate(t *testing.T, data []byte, fn func(map[string][]byte)) []byte {
	t.Helper()
	parts := readZip(t, data)
	fn(parts)
	return writeZip(t, parts)
}

func replaceInPart(t *testing.T, parts map[string][]byte, name, old, new string) {
	t.Helper()
	body, ok := parts[name]
	if !ok {
		t.Fatalf("missing part %s", name)
	}
	if !bytes.Contains(body, []byte(old)) {
		t.Fatalf("part %s does not contain %q", name, old)
	}
	parts[name] = bytes.ReplaceAll(body, []byte(old), []byte(new))
}
