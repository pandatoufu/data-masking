package agent

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pandatoufu/data-masking/internal/workspace"
)

func seedOriginal(t *testing.T, root, name string, data []byte) string {
	t.Helper()
	dir := filepath.Join(root, workspace.DirOriginal)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadSourceReturnsBytesWithoutOverwriting(t *testing.T) {
	root := t.TempDir()
	path := seedOriginal(t, root, "report.txt", []byte("hello"))
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadSource(root, "report.txt", 0)
	if err != nil {
		t.Fatalf("读取失败：%v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("字节应完整：%q", got)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.ModTime() != before.ModTime() || after.Size() != before.Size() {
		t.Fatal("读取不得改写原件")
	}
}

func TestReadSourceOverLimitRejected(t *testing.T) {
	root := t.TempDir()
	seedOriginal(t, root, "big.txt", make([]byte, 64))
	_, err := ReadSource(root, "big.txt", 16)
	var ae *Error
	if !errors.As(err, &ae) || ae.Code != CodeInputTooLarge {
		t.Fatalf("超限应返回 input_too_large，got %v", err)
	}
}

func TestReadSourceRejectsSymlinkWithoutReadingTarget(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("SECRET-OUTSIDE"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, workspace.DirOriginal)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(outside, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("Windows 无创建符号链接权限：平台特有 reparse 用例按平台运行")
		}
		t.Fatal(err)
	}
	_, err := ReadSource(root, "link.txt", 0)
	var ae *Error
	if !errors.As(err, &ae) || ae.Code != CodeInvalidSource {
		t.Fatalf("链接应拒绝为 invalid_source，got %v", err)
	}
	if got, _ := os.ReadFile(outside); string(got) != "SECRET-OUTSIDE" {
		t.Fatalf("越界目标不得被读取或改动：%q", got)
	}
}

func TestReadSourceRejectsDirectoryAndMissing(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, workspace.DirOriginal, "sub.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sub.txt", "missing.txt"} {
		_, err := ReadSource(root, name, 0)
		var ae *Error
		if !errors.As(err, &ae) || ae.Code != CodeInvalidSource {
			t.Fatalf("%s 应拒绝为 invalid_source，got %v", name, err)
		}
	}
}
