package agent

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestValidateSourceNameRejectionTable(t *testing.T) {
	reject := []struct {
		name string
		code Code
	}{
		{"", CodeInvalidSource},
		{".", CodeInvalidSource},
		{"..", CodeInvalidSource},
		{"../secret.txt", CodeInvalidSource},
		{"a/b.txt", CodeInvalidSource},
		{`a\b.txt`, CodeInvalidSource},
		{"/etc/passwd", CodeInvalidSource},
		{`C:\secret.txt`, CodeInvalidSource},
		{`\\server\share\secret.txt`, CodeInvalidSource},
		{"C:secret.txt", CodeInvalidSource},
		{"report.txt:ads", CodeInvalidSource},
		{"NUL", CodeInvalidSource},
		{"nul.txt", CodeInvalidSource},
		{"CON.log", CodeInvalidSource},
		{"com1.docx", CodeInvalidSource},
		{"LPT9.txt", CodeInvalidSource},
		{"report.txt.", CodeInvalidSource},
		{"report.txt ", CodeInvalidSource},
		{"report\x00.txt", CodeInvalidSource},
		{"report\t.txt", CodeInvalidSource},
		{"archive.zip", CodeUnsupportedInput},
		{"notes.rtf", CodeUnsupportedInput},
		{"report.odt", CodeUnsupportedInput},
		{"slides.pptx", CodeUnsupportedInput},
		{"data.pdf", CodeUnsupportedInput},
		{"image.png", CodeUnsupportedInput},
	}
	for _, c := range reject {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateSourceName(c.name)
			if err == nil {
				t.Fatalf("应拒绝 %q", c.name)
			}
			var ae *Error
			if !errors.As(err, &ae) || ae.Code != c.code {
				t.Fatalf("%q 应返回 %s，got %v", c.name, c.code, err)
			}
		})
	}
	accept := []string{
		"report.docx", "notes.txt", "合同.md", "my file.log", "a.b.py", "日志.js",
		"data.csv", "data.json", "config.yaml", "main.go", "notes.xyz",
		"README", "readme", ".env", ".gitignore", ".env.local",
	}
	for _, name := range accept {
		t.Run("accept "+name, func(t *testing.T) {
			if err := ValidateSourceName(name); err != nil {
				t.Fatalf("应接受 %q：%v", name, err)
			}
		})
	}
}

// original/ 直属范围：路径必须直接落在 original/ 下，且不因输入改变层级。
func TestOriginalPathStaysDirectChild(t *testing.T) {
	root := filepath.Join("tmp", "root")
	if err := ValidateSourceName("report.docx"); err != nil {
		t.Fatal(err)
	}
	got := OriginalPath(root, "report.docx")
	want := filepath.Join(root, "original", "report.docx")
	if got != want {
		t.Fatalf("OriginalPath=%q want %q", got, want)
	}
	if filepath.Dir(got) != filepath.Join(root, "original") {
		t.Fatalf("必须是 original/ 直属文件：%q", got)
	}
}
