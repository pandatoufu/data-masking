//go:build windows

package desktop

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/pandatoufu/data-masking/internal/workspace"
)

// Windows：固定目录被 junction 占位时列表/读取都拒绝，且不跟随链接读写。
// 该用例仅在真实 Windows 环境运行；Linux 上不编译执行。
func TestWorkspaceInputWindowsRejectsJunctionDir(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, workspace.DirPending)
	cmd := exec.Command("cmd", "/c", "mklink", "/J", link, outside)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("无法创建 junction（需要相应环境）：%v %s", err, out)
	}
	in, _ := newTestInput(root, 0)
	if got := in.List(WorkspaceInputPending); got.Status != inputStatusUnavailable || got.MessageID != "input.dir_unavailable" {
		t.Fatalf("junction 目录不应被列举：%+v", got)
	}
	if err := os.WriteFile(filepath.Join(outside, "reply.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := in.Read(WorkspaceInputPending, "reply.txt"); got.Status != inputStatusFailed || got.DataB64 != "" {
		t.Fatalf("junction 目录不应被读取：%+v", got)
	}
}

// Windows：目录中的文件符号链接/重解析条目不被列举，显式读取也被拒绝。
func TestWorkspaceInputWindowsRejectsFileLink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(target, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, workspace.DirOriginal)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "link.txt")); err != nil {
		t.Skipf("无法创建符号链接（需要相应环境）：%v", err)
	}
	in, _ := newTestInput(root, 0)
	if got := in.List(WorkspaceInputOriginal); got.Status != inputStatusEmpty {
		t.Fatalf("文件链接不应被列举：%+v", got)
	}
	if got := in.Read(WorkspaceInputOriginal, "link.txt"); got.Status != inputStatusFailed || got.MessageID != "input.not_regular" {
		t.Fatalf("文件链接不应被读取：%+v", got)
	}
}
