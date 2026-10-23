package mcp_test

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pandatoufu/data-masking/internal/workspace"
)

// buildAgentBinary 构建真实 headless 可执行文件（CGO 关闭，无 GUI tags）。
// 可通过 DM_AGENT_BIN 指定已构建产物，以便验收时驱动真实发布产物。
func buildAgentBinary(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("DM_AGENT_BIN"); p != "" {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("DM_AGENT_BIN 指向的产物不存在：%v", err)
		}
		t.Logf("使用已构建 headless 产物：%s", p)
		return p
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "data-masking-agent")
	if strings.EqualFold(filepath.Ext(os.Args[0]), ".exe") {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/data-masking-agent")
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("构建 headless 失败：%v\n%s", err, out)
	}
	return bin
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", "..", ".."))
}

// installAgent 把可执行文件放到临时“程序目录”并建立六目录，返回程序目录。
func installAgent(t *testing.T, bin string) string {
	t.Helper()
	root := t.TempDir()
	installed := filepath.Join(root, filepath.Base(bin))
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed, data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := workspace.EnsureHeadless(root); err != nil {
		t.Fatal(err)
	}
	return root
}

// 用 SDK 客户端驱动真实子进程：握手 → prepare → 高频确认 → restore → EOF 结束。
func TestSubprocessSessionViaSDKClient(t *testing.T) {
	bin := buildAgentBinary(t)
	root := installAgent(t, bin)
	installed := filepath.Join(root, filepath.Base(bin))

	if err := os.WriteFile(filepath.Join(root, workspace.DirOriginal, "notes.txt"), []byte("acme acme acme"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 阈值 1 + cards 中的 acme：同一原件命中 3 次触发高频确认。
	if err := os.WriteFile(filepath.Join(root, workspace.DirCards, "acme.txt"), []byte("acme\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(configHome, "data-masking"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configHome, "data-masking", "config.json"),
		[]byte(`{"high_freq_threshold":1}`), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	cmd := exec.Command(installed)
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+configHome)
	cmd.Stderr = os.Stderr
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	session, err := client.Connect(ctx, &sdk.CommandTransport{Command: cmd, TerminateDuration: 3 * time.Second}, nil)
	if err != nil {
		t.Fatalf("连接失败：%v", err)
	}

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("列举工具失败：%v", err)
	}
	if len(tools.Tools) != 3 {
		t.Fatalf("应只有三个工具：%d", len(tools.Tools))
	}

	project := t.TempDir()
	prep := callTool(t, session, "prepare_document", map[string]any{
		"source_name":  "notes.txt",
		"project_path": project,
		"profile":      "balanced",
	})
	if prep["status"] != "awaiting_frequency_confirmation" {
		t.Fatalf("应触发高频确认：%v", prep)
	}
	if pf, _ := prep["project_file"].(string); pf != "" {
		t.Fatalf("确认前不得有项目文件：%v", pf)
	}
	if _, err := os.Stat(filepath.Join(project, ".masked-input")); !os.IsNotExist(err) {
		t.Fatal("确认前不得创建项目交付目录")
	}

	confirmed := callTool(t, session, "confirm_operation", map[string]any{
		"job_id":          prep["job_id"],
		"confirmation_id": prep["confirmation_id"],
		"decision":        "continue_masking",
	})
	if confirmed["status"] != "delivered" {
		t.Fatalf("授权后应交付：%v", confirmed)
	}
	projectFile, _ := confirmed["project_file"].(string)
	if projectFile == "" {
		t.Fatalf("交付应返回项目文件：%v", confirmed)
	}
	masked, err := os.ReadFile(projectFile)
	if err != nil {
		t.Fatalf("项目副本应存在：%v", err)
	}
	if strings.Contains(string(masked), "acme") {
		t.Fatal("项目副本应已脱敏")
	}

	reply := filepath.Join(project, "reply.txt")
	if err := os.WriteFile(reply, masked, 0o600); err != nil {
		t.Fatal(err)
	}
	restored := callTool(t, session, "restore_document", map[string]any{
		"job_id":        prep["job_id"],
		"response_path": reply,
	})
	if restored["status"] != "restored" {
		t.Fatalf("应还原：%v", restored)
	}
	for _, banned := range []string{"restored_b64", "content", "mapping", "text"} {
		if _, ok := restored[banned]; ok {
			t.Fatalf("还原响应不得包含 %q：%v", banned, restored)
		}
	}

	// 关闭会话（EOF）：程序应在安全点结束并释放锁。
	if err := session.Close(); err != nil {
		t.Fatalf("关闭失败：%v", err)
	}
	waitForProcessExit(t, cmd, 5*time.Second)

	// 会话锁随进程退出释放：可再次启动同一 root。
	second := exec.Command(installed, "--version")
	second.Env = append(os.Environ(), "XDG_CONFIG_HOME="+configHome)
	out, err := second.CombinedOutput()
	if err != nil {
		t.Fatalf("重启应成功：%v %s", err, out)
	}
}

func callTool(t *testing.T, session *sdk.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s 调用失败：%v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s 返回工具错误：%+v", name, res.Content)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func waitForProcessExit(t *testing.T, cmd *exec.Cmd, timeout time.Duration) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		t.Fatal("stdin 关闭后子进程未在期限内退出（不应后台常驻）")
	}
}

// 原始 stdio 驱动：把测试秘密塞进参数与失败通道，完整捕获 stdout/stderr 断言无泄露。
func TestSubprocessRawStdioNoLeakAndNoTCP(t *testing.T) {
	bin := buildAgentBinary(t)
	root := installAgent(t, bin)
	installed := filepath.Join(root, filepath.Base(bin))
	if err := os.WriteFile(filepath.Join(root, workspace.DirOriginal, "notes.txt"), []byte("SECRET-VALUE 13800138000"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, installed)
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+t.TempDir())
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	enc := json.NewEncoder(stdin)
	send := func(v any) {
		if err := enc.Encode(v); err != nil {
			t.Fatalf("写入请求失败：%v", err)
		}
	}
	send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "raw", "version": "0"},
		},
	})
	send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	// 恶意路径 + 测试秘密：错误不得回显。
	send(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name": "prepare_document",
			"arguments": map[string]any{
				"source_name":  "../SECRET-VALUE.txt",
				"project_path": filepath.Join(t.TempDir(), "SECRET-PROJECT"),
				"profile":      "SECRET-PROFILE",
			},
		},
	})
	// 非法 schema 字段：SDK 校验错误也不得回显内容。
	send(map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{
			"name":      "prepare_document",
			"arguments": map[string]any{"force": "SECRET-VALUE", "source_name": 1},
		},
	})

	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	responses := map[float64]map[string]any{}
	var stdoutLines []string
	deadline := time.Now().Add(20 * time.Second)
	for len(responses) < 3 && time.Now().Before(deadline) {
		if !scanner.Scan() {
			break
		}
		line := scanner.Text()
		stdoutLines = append(stdoutLines, line)
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("stdout 必须是 JSON-RPC：%q", line)
		}
		if _, ok := msg["jsonrpc"]; !ok {
			t.Fatalf("stdout 含非协议输出：%q", line)
		}
		if id, ok := msg["id"].(float64); ok {
			responses[id] = msg
		}
	}
	_ = stdin.Close()
	_ = cmd.Wait()

	if _, ok := responses[1]["result"]; !ok {
		t.Fatalf("initialize 应有 result：%v", responses[1])
	}
	invalid := failureOf(t, responses[2])
	if invalid["code"] != "invalid_source" {
		t.Fatalf("越界原件名应 invalid_source：%v", invalid)
	}
	unknown := failureOf(t, responses[3])
	if unknown["code"] != "invalid_request" {
		t.Fatalf("未知字段应返回 failed/invalid_request：%v", unknown)
	}

	allOut := strings.Join(stdoutLines, "\n")
	for _, banned := range []string{"SECRET-VALUE", "13800138000", "SECRET-PROFILE", "SECRET-PROJECT"} {
		if strings.Contains(allOut, banned) {
			t.Fatalf("stdout 泄露 %q：%s", banned, allOut)
		}
		if strings.Contains(stderr.String(), banned) {
			t.Fatalf("stderr 泄露 %q：%s", banned, stderr.String())
		}
	}
	if strings.Contains(stderr.String(), "notes.txt") {
		t.Fatalf("stderr 不得输出业务参数：%s", stderr.String())
	}
}

// failureOf 从 JSON-RPC 响应中取出工具结构化失败信封。
// 真实子进程还原出现一致性告警：stdout 仅协议与固定匿名告警，stdout/stderr 均不泄露原文与指纹。
func TestSubprocessRestoreWarningNoLeak(t *testing.T) {
	bin := buildAgentBinary(t)
	root := installAgent(t, bin)
	installed := filepath.Join(root, filepath.Base(bin))

	orig := []byte("联系 13800138000")
	if err := os.WriteFile(filepath.Join(root, workspace.DirOriginal, "notes.txt"), orig, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, workspace.DirCards, "fixture.txt"), []byte("fixture-card-word\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(orig)
	fingerprint := hex.EncodeToString(sum[:])

	project := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, installed)
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+t.TempDir())
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	enc := json.NewEncoder(stdin)
	send := func(v any) {
		if err := enc.Encode(v); err != nil {
			t.Fatalf("写入请求失败：%v", err)
		}
	}
	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var stdoutLines []string

	readResponse := func(id float64) map[string]any {
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if !scanner.Scan() {
				t.Fatalf("stdout 提前关闭，未收到 id=%v 响应", id)
			}
			line := scanner.Text()
			stdoutLines = append(stdoutLines, line)
			var msg map[string]any
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				t.Fatalf("stdout 必须是 JSON-RPC：%q", line)
			}
			if _, ok := msg["jsonrpc"]; !ok {
				t.Fatalf("stdout 含非协议输出：%q", line)
			}
			if got, ok := msg["id"].(float64); ok && got == id {
				return msg
			}
		}
		t.Fatalf("等待 id=%v 响应超时", id)
		return nil
	}

	send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "raw", "version": "0"},
		},
	})
	send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	readResponse(1)

	send(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name": "prepare_document",
			"arguments": map[string]any{
				"source_name":  "notes.txt",
				"project_path": project,
				"profile":      "strict",
			},
		},
	})
	prep := toolPayload(t, readResponse(2))
	if prep["status"] != "delivered" {
		t.Fatalf("应交付：%v", prep)
	}
	jobID, _ := prep["job_id"].(string)
	projectFile, _ := prep["project_file"].(string)
	masked, err := os.ReadFile(projectFile)
	if err != nil {
		t.Fatalf("项目副本应存在：%v", err)
	}

	reply := filepath.Join(project, "reply.txt")
	if err := os.WriteFile(reply, append(append([]byte{}, masked...), []byte(" 补充")...), 0o600); err != nil {
		t.Fatal(err)
	}
	send(map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{
			"name":      "restore_document",
			"arguments": map[string]any{"job_id": jobID, "response_path": reply},
		},
	})
	restored := toolPayload(t, readResponse(3))
	if restored["status"] != "restored" {
		t.Fatalf("应还原：%v", restored)
	}
	warnings, _ := restored["warnings"].([]any)
	found := false
	for _, item := range warnings {
		w, _ := item.(map[string]any)
		if w["kind"] != "sha_mismatch" {
			continue
		}
		found = true
		if w["message"] != "还原结果与 mapping 记录的原文指纹不一致；仅供参考，不构成拿错 mapping 的断言。" {
			t.Fatalf("sha_mismatch 文案不符：%v", w)
		}
	}
	if !found {
		t.Fatalf("应返回 sha_mismatch 告警：%v", warnings)
	}

	_ = stdin.Close()
	_ = cmd.Wait()

	allOut := strings.Join(stdoutLines, "\n")
	for _, banned := range []string{"13800138000", fingerprint} {
		if strings.Contains(allOut, banned) {
			t.Fatalf("stdout 泄露 %q：%s", banned, allOut)
		}
		if strings.Contains(stderr.String(), banned) {
			t.Fatalf("stderr 泄露 %q：%s", banned, stderr.String())
		}
	}
}

// toolPayload 取出 JSON-RPC 响应中的结构化工具信封。
func toolPayload(t *testing.T, msg map[string]any) map[string]any {
	t.Helper()
	result, ok := msg["result"].(map[string]any)
	if !ok {
		t.Fatalf("应返回工具结果：%v", msg)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("应返回 structured content：%v", result)
	}
	return structured
}

func failureOf(t *testing.T, msg map[string]any) map[string]any {
	t.Helper()
	result, ok := msg["result"].(map[string]any)
	if !ok {
		t.Fatalf("应返回工具结果：%v", msg)
	}
	structured, ok := result["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("应返回 structured content：%v", result)
	}
	return structured
}
