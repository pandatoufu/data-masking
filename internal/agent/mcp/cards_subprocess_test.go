package mcp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pandatoufu/data-masking/internal/workspace"
)

const (
	msgCardsMissing = "自定义词规则已启用，请先在程序目录的 cards 文件夹放入有效 TXT 词库文件。"
	msgCardsInvalid = "cards 词库无效或无法读取，请在本机检查 TXT 文件的编码、内容及输入限制后重试。"
)

// buildFreshAgentBinary 返回用于 cards 闭环的真实 headless 子进程：
// 默认临时编译当前工作树；设置 DM_AGENT_BIN 时改用指定已构建产物，
// 以便验收时由真实 MCP 客户端驱动本次发布的二进制（不默认依赖 dist 旧产物）。
func buildFreshAgentBinary(t *testing.T) string {
	t.Helper()
	return buildAgentBinary(t)
}

func writeAgentConfig(t *testing.T, configHome, body string) {
	t.Helper()
	dir := filepath.Join(configHome, "data-masking")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// 真实临时子进程 + cards 驱动：握手 → 高频确认 → 确认失效 → 高频/残留确认 → 交付 → 还原 → EOF。
func TestSubprocessCardsClosedLoopAndStale(t *testing.T) {
	bin := buildFreshAgentBinary(t)
	root := installAgent(t, bin)
	installed := filepath.Join(root, filepath.Base(bin))
	configHome := t.TempDir()
	writeAgentConfig(t, configHome, `{"high_freq_threshold":1,"custom_words":["LEGACY-CONFIG-WORD"]}`)
	cardPath := filepath.Join(root, workspace.DirCards, "words.txt")
	if err := os.WriteFile(cardPath, []byte("acme\nPHONE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	original := []byte("acme acme LEGACY-CONFIG-WORD 13800138000")
	if err := os.WriteFile(filepath.Join(root, workspace.DirOriginal, "notes.txt"),
		original, 0o600); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()

	ctx := context.Background()
	cmd := exec.Command(installed)
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+configHome)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	client := sdk.NewClient(&sdk.Implementation{Name: "cards-client", Version: "0.0.0"}, nil)
	session, err := client.Connect(ctx, &sdk.CommandTransport{Command: cmd, TerminateDuration: 3 * time.Second}, nil)
	if err != nil {
		t.Fatalf("连接失败：%v", err)
	}

	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 3 {
		t.Fatalf("应发现三个工具：%v %v", tools, err)
	}

	// 1) 高频确认后改变词文件字节（词集合不变）→ confirmation_stale，且无项目副本。
	first := callTool(t, session, "prepare_document", map[string]any{
		"source_name":  "notes.txt",
		"project_path": project,
		"profile":      "balanced",
	})
	if first["status"] != "awaiting_frequency_confirmation" {
		t.Fatalf("cards 应驱动高频确认：%v", first)
	}
	// 真实子进程创建的私有任务元数据：Linux 新目录 0700、事件文件 0600。
	assertPrivateStatePermissions(t, root, first["job_id"].(string))
	if err := os.WriteFile(cardPath, []byte("acme\nPHONE\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := callTool(t, session, "confirm_operation", map[string]any{
		"job_id":          first["job_id"],
		"confirmation_id": first["confirmation_id"],
		"decision":        "continue_masking",
	})
	if stale["status"] != "failed" || stale["code"] != "confirmation_stale" {
		t.Fatalf("词库字节变化应 confirmation_stale：%v", stale)
	}
	if _, err := os.Stat(filepath.Join(project, ".masked-input", first["job_id"].(string))); !os.IsNotExist(err) {
		t.Fatal("失效确认不得交付")
	}

	// 2) 新任务：高频授权后进入真实正向残留确认，再交付、还原。
	second := callTool(t, session, "prepare_document", map[string]any{
		"source_name":  "notes.txt",
		"project_path": project,
		"profile":      "balanced",
	})
	if second["status"] != "awaiting_frequency_confirmation" {
		t.Fatalf("应再次高频确认：%v", second)
	}
	continued := callTool(t, session, "confirm_operation", map[string]any{
		"job_id":          second["job_id"],
		"confirmation_id": second["confirmation_id"],
		"decision":        "continue_masking",
	})
	if continued["status"] != "awaiting_residual_confirmation" {
		t.Fatalf("cards 中的 PHONE 应命中占位符形成残留确认：%v", continued)
	}
	delivered := callTool(t, session, "confirm_operation", map[string]any{
		"job_id":          continued["job_id"],
		"confirmation_id": continued["confirmation_id"],
		"decision":        "deliver_with_residual_risk",
	})
	if delivered["status"] != "delivered" {
		t.Fatalf("接受残留风险后应交付：%v", delivered)
	}
	projectFile, _ := delivered["project_file"].(string)
	if filepath.Base(projectFile) != "input.masked.txt" {
		t.Fatalf("项目副本应使用固定脱敏名，got %q", projectFile)
	}
	masked, err := os.ReadFile(projectFile)
	if err != nil {
		t.Fatalf("项目副本应存在：%v", err)
	}
	for _, banned := range []string{"acme", "13800138000"} {
		if strings.Contains(string(masked), banned) {
			t.Fatalf("项目副本仍含 %q：%s", banned, masked)
		}
	}
	// 旧 custom_words 被完全忽略：该词不在 cards 中，必须原样保留而非参与替换。
	if !strings.Contains(string(masked), "LEGACY-CONFIG-WORD") {
		t.Fatalf("旧 custom_words 不应生效：%s", masked)
	}
	// 无覆盖：原件在 prepare/交付后保持原字节。
	if got, err := os.ReadFile(filepath.Join(root, workspace.DirOriginal, "notes.txt")); err != nil || !reflect.DeepEqual(got, original) {
		t.Fatalf("原件不得被覆写：%v %q", err, got)
	}

	// 失败重试：先从项目内一个不支持扩展名的回复失败，任务仍 delivered；再用正确回复成功还原。
	badReply := filepath.Join(project, "reply.zip")
	if err := os.WriteFile(badReply, []byte("not supported"), 0o600); err != nil {
		t.Fatal(err)
	}
	failed := callTool(t, session, "restore_document", map[string]any{
		"job_id":        delivered["job_id"],
		"response_path": badReply,
	})
	if failed["status"] != "failed" {
		t.Fatalf("不支持格式的回复应失败：%v", failed)
	}

	reply := filepath.Join(project, "reply.txt")
	if err := os.WriteFile(reply, masked, 0o600); err != nil {
		t.Fatal(err)
	}
	restored := callTool(t, session, "restore_document", map[string]any{
		"job_id":        delivered["job_id"],
		"response_path": reply,
	})
	if restored["status"] != "restored" {
		t.Fatalf("失败后正确回复应仍可还原：%v", restored)
	}
	if _, ok := restored["content"]; ok {
		t.Fatalf("不得返回还原正文：%v", restored)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("关闭失败：%v", err)
	}
	waitForProcessExit(t, cmd, 5*time.Second)
	if strings.Contains(stderr.String(), "acme") || strings.Contains(stderr.String(), "13800138000") || strings.Contains(stderr.String(), "LEGACY-CONFIG-WORD") {
		t.Fatalf("stderr 不得包含业务参数/词值：%s", stderr.String())
	}
}

// rawAgent 用原始 stdio 驱动真实子进程，完整捕获 stdout/stderr。
type rawAgent struct {
	t      *testing.T
	cmd    *exec.Cmd
	stdin  *os.File
	scan   *bufio.Scanner
	stderr strings.Builder
	lines  []string
}

func startRawAgent(t *testing.T, installed, configHome string) *rawAgent {
	t.Helper()
	cmd := exec.Command(installed)
	env := []string{}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "XDG_CONFIG_HOME=") {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = append(env, "XDG_CONFIG_HOME="+configHome)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	r := &rawAgent{t: t, cmd: cmd, stdin: stdin.(*os.File)}
	cmd.Stderr = &r.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	r.scan = bufio.NewScanner(stdout)
	r.scan.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	return r
}

func (r *rawAgent) send(v any) {
	r.t.Helper()
	if err := json.NewEncoder(r.stdin).Encode(v); err != nil {
		r.t.Fatalf("写入请求失败：%v", err)
	}
}

func (r *rawAgent) read(id float64) map[string]any {
	r.t.Helper()
	for {
		if !r.scan.Scan() {
			r.t.Fatalf("stdout 提前关闭，未收到 id=%v 响应；stderr=%s", id, r.stderr.String())
		}
		line := r.scan.Text()
		r.lines = append(r.lines, line)
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			r.t.Fatalf("stdout 必须是 JSON-RPC：%q", line)
		}
		if _, ok := msg["jsonrpc"]; !ok {
			r.t.Fatalf("stdout 含非协议输出：%q", line)
		}
		if got, ok := msg["id"].(float64); ok && got == id {
			return msg
		}
	}
}

func (r *rawAgent) stdout() string { return strings.Join(r.lines, "\n") }

func (r *rawAgent) close() {
	_ = r.stdin.Close()
	_ = r.cmd.Wait()
}

// 真实子进程的 cards 固定错误、custom 关闭不读与完整输出无泄露。
func TestSubprocessCardsFixedErrorsAndNoLeak(t *testing.T) {
	bin := buildFreshAgentBinary(t)
	root := installAgent(t, bin)
	installed := filepath.Join(root, filepath.Base(bin))
	configHome := t.TempDir()
	writeAgentConfig(t, configHome, `{}`)
	if err := os.WriteFile(filepath.Join(root, workspace.DirOriginal, "notes.txt"),
		[]byte("sk-abcdefghij0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()

	r := startRawAgent(t, installed, configHome)
	defer r.close()
	r.send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "raw", "version": "0"},
		},
	})
	r.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	r.read(1)

	prepareReq := func(id float64, profile string) map[string]any {
		r.send(map[string]any{
			"jsonrpc": "2.0", "id": id, "method": "tools/call",
			"params": map[string]any{
				"name": "prepare_document",
				"arguments": map[string]any{
					"source_name":  "notes.txt",
					"project_path": project,
					"profile":      profile,
				},
			},
		})
		msg := r.read(id)
		result, _ := msg["result"].(map[string]any)
		assertTextStructuredAgree(t, result)
		return toolPayload(t, msg)
	}

	// 两条固定 message：无 TXT / 无效 TXT，都不含候选名/词/路径/底层错误。
	missing := prepareReq(2, "balanced")
	if missing["status"] != "failed" || missing["code"] != "invalid_config" || missing["message"] != msgCardsMissing {
		t.Fatalf("无 TXT 应返回固定缺失错误：%v", missing)
	}

	secretName := "SECRET-CARD-NAME.txt"
	secretWord := "SECRET-CARD-WORD"
	secretPath := filepath.Join(root, workspace.DirCards, secretName)
	if err := os.WriteFile(secretPath, []byte(secretWord+"\x00\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	invalid := prepareReq(3, "balanced")
	if invalid["status"] != "failed" || invalid["code"] != "invalid_config" || invalid["message"] != msgCardsInvalid {
		t.Fatalf("坏 TXT 应返回固定无效错误：%v", invalid)
	}

	// 关闭 custom：同一坏 TXT 存在也不读取，仍按启用规则交付。
	delivered := prepareReq(4, "minimal")
	if delivered["status"] != "delivered" {
		t.Fatalf("custom 关闭不应读取 cards：%v", delivered)
	}

	// text 与 structured 同源已在上方每次响应中校验。

	allOut := r.stdout()
	for _, banned := range []string{secretName, secretWord, secretPath, root, "0x00"} {
		if strings.Contains(allOut, banned) {
			t.Fatalf("stdout 泄露 %q：%s", banned, allOut)
		}
		if strings.Contains(r.stderr.String(), banned) {
			t.Fatalf("stderr 泄露 %q：%s", banned, r.stderr.String())
		}
	}
}

// assertPrivateStatePermissions 校验真实子进程新建的私有任务目录/事件文件权限（仅 Unix）。
func assertPrivateStatePermissions(t *testing.T, root, jobID string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	jobDir := filepath.Join(root, workspace.AgentStateDir, jobID)
	info, err := os.Stat(jobDir)
	if err != nil {
		t.Fatalf("私有任务目录应存在：%v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("私有任务目录权限应为 0700，got %04o", perm)
	}
	entries, err := os.ReadDir(jobDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("私有任务目录应有事件文件：%v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			t.Fatalf("私有任务目录不应含子目录：%s", entry.Name())
		}
		fileInfo, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		if perm := fileInfo.Mode().Perm(); perm != 0o600 {
			t.Fatalf("事件文件权限应为 0600，got %04o", perm)
		}
	}
}

// assertTextStructuredAgree 校验同一响应的 text content 与 structured content 完全一致。
func assertTextStructuredAgree(t *testing.T, result map[string]any) {
	t.Helper()
	content, _ := result["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("应有一个 text content：%v", result)
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	var fromText map[string]any
	if err := json.Unmarshal([]byte(text), &fromText); err != nil {
		t.Fatalf("text content 应为 JSON：%v", err)
	}
	structured, _ := result["structuredContent"].(map[string]any)
	if !reflect.DeepEqual(fromText, structured) {
		t.Fatalf("text/structured 必须同源：text=%v structured=%v", fromText, structured)
	}
}
