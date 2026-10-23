package mcp_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	agentmcp "github.com/pandatoufu/data-masking/internal/agent/mcp"

	"github.com/pandatoufu/data-masking/internal/workspace"
)

// --version 不启动 MCP、不创建目录，只输出版本。
func TestVersionFlagHasNoSideEffects(t *testing.T) {
	bin := buildAgentBinary(t)
	root := t.TempDir()
	installed := filepath.Join(root, "data-masking-agent")
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed, data, 0o755); err != nil {
		t.Fatal(err)
	}
	// 不预先建六目录：--version 之后仍不应出现任何业务目录。
	out, err := runCommand(t, installed, "--version")
	if err != nil {
		t.Fatalf("--version 应成功：%v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("--version 应输出版本")
	}
	for _, name := range workspace.HeadlessDirs() {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("--version 不得创建 %s/", name)
		}
	}
	if _, err := os.Stat(filepath.Join(root, workspace.AgentStateDir)); !os.IsNotExist(err) {
		t.Fatal("--version 不得创建私有状态目录")
	}
	// 未知参数非零退出。
	if _, err := runCommand(t, installed, "--nope"); err == nil {
		t.Fatal("未知参数应非零退出")
	}
}

// MCP initialize 的 serverInfo.version 与 --version 一致，且来自同一构建注入值。
func TestVersionConsistentWithMCPHandshake(t *testing.T) {
	bin := buildAgentBinary(t)
	root := installAgent(t, bin)
	installed := filepath.Join(root, filepath.Base(bin))
	versionOut, err := runCommand(t, installed, "--version")
	if err != nil {
		t.Fatalf("--version 失败：%v", err)
	}
	version := strings.TrimSpace(versionOut)
	session := connect(t, installed, t.TempDir())
	defer func() { _ = session.Close() }()
	init := session.InitializeResult()
	if init == nil || init.ServerInfo == nil {
		t.Fatal("握手应返回 serverInfo")
	}
	if init.ServerInfo.Version != version {
		t.Fatalf("MCP 版本 %q 应与 --version %q 一致", init.ServerInfo.Version, version)
	}
	if init.ServerInfo.Name != agentmcp.ServerName {
		t.Fatalf("serverInfo.name=%q want %q", init.ServerInfo.Name, agentmcp.ServerName)
	}
}

// 无图形会话下可完成握手与工具发现，不初始化窗口或图形库。
func TestHandshakeWithoutGraphicalSession(t *testing.T) {
	session, _, _ := newPair(t)
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("无图形会话应可握手：%v", err)
	}
	if len(tools.Tools) != 3 {
		t.Fatalf("应发现三个工具：%d", len(tools.Tools))
	}
}

// 子进程协议测试：用真实 core 的残留分支验证「接受风险并交付」。
func TestSubprocessResidualConfirmationBranch(t *testing.T) {
	bin := buildAgentBinary(t)
	root := installAgent(t, bin)
	installed := filepath.Join(root, filepath.Base(bin))
	if err := os.WriteFile(filepath.Join(root, workspace.DirOriginal, "phone.txt"), []byte("13800138000"), 0o600); err != nil {
		t.Fatal(err)
	}
	// cards 自定义词 PHONE 会命中脱敏后的占位符 [PHONE_1] → 真实正向残留。
	if err := os.WriteFile(filepath.Join(root, workspace.DirCards, "residual.txt"), []byte("PHONE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(configHome, "data-masking"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configHome, "data-masking", "config.json"),
		[]byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	session := connect(t, installed, configHome)
	defer func() { _ = session.Close() }()
	project := t.TempDir()
	prep := callTool(t, session, "prepare_document", map[string]any{
		"source_name":  "phone.txt",
		"project_path": project,
		"profile":      "balanced",
	})
	if prep["status"] != "awaiting_residual_confirmation" {
		t.Fatalf("应触发残留确认：%v", prep)
	}
	if prep["allowed_decisions"].([]any)[0] != "deliver_with_residual_risk" {
		t.Fatalf("允许动作应仅为残留授权：%v", prep["allowed_decisions"])
	}
	if _, err := os.Stat(filepath.Join(project, ".masked-input")); !os.IsNotExist(err) {
		t.Fatal("残留确认前不得交付")
	}
	// 高频动作不能批准残留。
	rejected := callTool(t, session, "confirm_operation", map[string]any{
		"job_id":          prep["job_id"],
		"confirmation_id": prep["confirmation_id"],
		"decision":        "continue_masking",
	})
	if rejected["status"] != "failed" || rejected["code"] != "invalid_confirmation" {
		t.Fatalf("跨类型授权应拒绝：%v", rejected)
	}
	delivered := callTool(t, session, "confirm_operation", map[string]any{
		"job_id":          prep["job_id"],
		"confirmation_id": prep["confirmation_id"],
		"decision":        "deliver_with_residual_risk",
	})
	if delivered["status"] != "delivered" {
		t.Fatalf("接受风险后应交付：%v", delivered)
	}
	warnings, _ := delivered["warnings"].([]any)
	found := false
	for _, w := range warnings {
		entry := w.(map[string]any)
		if entry["kind"] == "residual_forward" {
			found = true
		}
	}
	if !found {
		t.Fatalf("交付应带匿名残留告警：%v", warnings)
	}
}

// runCommand 运行命令并返回合并输出。
func runCommand(t *testing.T, bin string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// connect 启动真实子进程并用 SDK 客户端连接。
func connect(t *testing.T, installed, configHome string) *sdk.ClientSession {
	t.Helper()
	cmd := exec.Command(installed)
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+configHome)
	cmd.Stderr = os.Stderr
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	session, err := client.Connect(context.Background(), &sdk.CommandTransport{Command: cmd, TerminateDuration: 3 * time.Second}, nil)
	if err != nil {
		t.Fatalf("连接失败：%v", err)
	}
	return session
}
