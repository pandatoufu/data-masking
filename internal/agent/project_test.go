package agent

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestValidateProjectPathRejectsWorkspaceOverlap(t *testing.T) {
	root := t.TempDir()
	project := t.TempDir()
	outside := t.TempDir()

	if _, err := ValidateProjectPath(root, project); err != nil {
		t.Fatalf("独立项目应可用：%v", err)
	}
	cases := []string{
		root,
		filepath.Join(root, "sub"),
		filepath.Dir(root),
	}
	for _, p := range cases {
		if _, err := ValidateProjectPath(root, p); err == nil {
			t.Fatalf("应与工作区互斥：%q", p)
		} else {
			var ae *Error
			if !errors.As(err, &ae) || ae.Code != CodeInvalidProject {
				t.Fatalf("应返回 invalid_project，got %v", err)
			}
		}
	}
	_ = outside
}

func TestValidateProjectPathRequiresExistingAbsoluteDir(t *testing.T) {
	root := t.TempDir()
	// 相对路径不得用 cwd 猜测。
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(prev) }()
	if _, err := ValidateProjectPath(root, "."); err == nil {
		t.Fatal("相对路径应拒绝，不得取 cwd")
	}
	if _, err := ValidateProjectPath(root, filepath.Join(work, "missing")); err == nil {
		t.Fatal("不存在的目录应拒绝")
	}
	// 普通文件当项目路径应拒绝。
	file := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateProjectPath(root, file); err == nil {
		t.Fatal("文件应拒绝")
	}
}

func TestValidateProjectPathRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "project-link")
	if err := os.Symlink(real, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("Windows 无创建符号链接权限：平台特有用例按平台运行")
		}
		t.Fatal(err)
	}
	if _, err := ValidateProjectPath(root, link); err == nil {
		t.Fatal("链接项目路径应拒绝")
	}
}

func TestValidateResponsePathBoundToProjectSubtree(t *testing.T) {
	root := t.TempDir()
	project := t.TempDir()
	inside := filepath.Join(project, "replies", "answer.txt")
	if err := os.MkdirAll(filepath.Dir(inside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inside, []byte("reply"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateResponsePath(project, inside); err != nil {
		t.Fatalf("项目子树内回复应可用：%v", err)
	}

	other := t.TempDir()
	crossProject := filepath.Join(other, "answer.txt")
	if err := os.WriteFile(crossProject, []byte("cross"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateResponsePath(project, crossProject); err == nil {
		t.Fatal("跨项目回复应拒绝")
	}

	badExt := filepath.Join(project, "answer.zip")
	if err := os.WriteFile(badExt, []byte("zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateResponsePath(project, badExt); err == nil {
		t.Fatal("非法格式应先拒绝")
	} else {
		var ae *Error
		if !errors.As(err, &ae) || ae.Code != CodeUnsupportedInput {
			t.Fatalf("应返回 unsupported_input，got %v", err)
		}
	}

	dir := filepath.Join(project, "subdir.txt")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateResponsePath(project, dir); err == nil {
		t.Fatal("目录应拒绝")
	}
	_ = root
}

func TestValidateResponsePathRejectsSymlinkEscape(t *testing.T) {
	project := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(project, "escape.txt")
	if err := os.Symlink(outside, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("Windows 无创建符号链接权限：平台特有用例按平台运行")
		}
		t.Fatal(err)
	}
	if _, err := ValidateResponsePath(project, link); err == nil {
		t.Fatal("链接越界应拒绝")
	}
}
