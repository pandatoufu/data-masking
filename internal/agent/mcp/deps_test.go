package mcp_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 依赖图不得包含 GUI/桌面/服务层，保证 headless 不初始化 WebView/GTK。
func TestDependencyGraphHasNoGUI(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "github.com/pandatoufu/data-masking/cmd/data-masking-agent")
	cmd.Dir = filepath.Join("..", "..", "..")
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("go list 不可用：%v", err)
	}
	for _, banned := range []string{"wailsapp/wails", "internal/desktop", "internal/server"} {
		if strings.Contains(string(out), banned) {
			t.Fatalf("headless 依赖图不得包含 %s", banned)
		}
	}
}

// 根 main.go 仍是开发提示 stub：不启动服务、不打开浏览器。
func TestRootMainStaysHintStub(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", ".")
	cmd.Dir = filepath.Join("..", "..", "..")
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("go list 不可用：%v", err)
	}
	for _, banned := range []string{"internal/agent", "internal/server", "wailsapp/wails"} {
		if strings.Contains(string(out), banned) {
			t.Fatalf("无 tag 的根入口不得依赖 %s", banned)
		}
	}
}
