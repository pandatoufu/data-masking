package desktop

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type workspaceSelectReport struct {
	Cases    map[string]map[string]any `json:"cases"`
	Failures []string                  `json:"failures"`
	Fatal    string                    `json:"fatal"`
}

// TestRealAppWorkspaceSelectors 加载真实 web/static/app.js（Node vm + fake DOM + fake Bridge），
// 驱动真实四类快捷选择：只列名称、显式读取、原 multipart/词卡解析链、快照与列表解耦、
// 过期响应与忙态保护。Node 不可用时跳过（不使 Go core/build 依赖 Node）。
func TestRealAppWorkspaceSelectors(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node 不可用：跳过真实 app.js 固定目录选择回归（不使 Go core/build 依赖 Node）")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	harness := filepath.Join(root, "internal", "desktop", "testdata", "workspace_selectors.mjs")
	appJs := filepath.Join(root, "web", "static", "app.js")
	reportPath := filepath.Join(t.TempDir(), "report.json")

	cmd := exec.Command("node", harness, appJs, reportPath)
	cmd.Dir = root
	out, runErr := cmd.CombinedOutput()
	raw, rerr := os.ReadFile(reportPath)
	if rerr != nil {
		t.Fatalf("node harness 未产出报告：runErr=%v readErr=%v\n%s", runErr, rerr, out)
	}
	var rep workspaceSelectReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatalf("报告解析失败：%v\n%s", err, raw)
	}
	if rep.Fatal != "" {
		t.Fatalf("harness fatal: %s", rep.Fatal)
	}
	if runErr != nil {
		t.Fatalf("node harness 失败：%v\n%s", runErr, out)
	}
	if len(rep.Failures) != 0 {
		t.Fatalf("harness 断言失败：%v", rep.Failures)
	}
	for _, name := range []string{
		"init-lists", "refresh-decoupled", "empty-unavailable", "stale-list", "no-binding",
		"original-read", "pending-read", "mapping-workspace", "cards-import",
		"busy-and-stale", "session-scoped", "language-switch", "conflict-guards",
		"read-failure-preserves",
	} {
		if _, ok := rep.Cases[name]; !ok {
			t.Fatalf("报告缺少用例 %q：%v", name, rep.Cases)
		}
	}

	// 3.1 只列名称、显式读取；无默认选择。
	if got := rep.Cases["init-lists"]["reads"].(float64); got != 0 {
		t.Fatalf("初始列举不得读取正文：%v", got)
	}
	// 3.2 original/pending 提交真实文件名与原始字节。
	if got, _ := rep.Cases["original-read"]["fileName"].(string); got != "合同 副本.txt" {
		t.Fatalf("original 提交应使用真实文件名：%q", got)
	}
	if got, _ := rep.Cases["original-read"]["size"].(float64); got != 4 {
		t.Fatalf("original 应提交原始 GBK 字节：%v", got)
	}
	if got, _ := rep.Cases["pending-read"]["textHasFile"].(bool); got {
		t.Fatal("粘贴模式不得提交隐藏的文件快照")
	}
	// 3.3 mapping 目录来源要求配对确认并保留真实名称。
	if got, _ := rep.Cases["mapping-workspace"]["source"].(string); got != "workspace" {
		t.Fatalf("mapping 应标记 workspace 来源：%q", got)
	}
	if got, _ := rep.Cases["mapping-workspace"]["confirms"].(float64); got < 1 {
		t.Fatalf("目录来源 mapping 必须要求配对确认：%v", got)
	}
	// 3.4 忙态与迟到响应保护。
	if got, _ := rep.Cases["busy-and-stale"]["busyDuring"].(bool); !got {
		t.Fatal("读取期间应进入全局忙态")
	}
	if got, _ := rep.Cases["busy-and-stale"]["busyAfter"].(bool); got {
		t.Fatal("读取完成后应释放忙态")
	}
	if got, _ := rep.Cases["session-scoped"]["file"].(string); got != "snap.txt" {
		t.Fatalf("切换视图/偏好不得丢失快照：%q", got)
	}
	if got, _ := rep.Cases["language-switch"]["file"].(string); got != "notes.txt" {
		t.Fatalf("切换语言不得丢失输入快照：%q", got)
	}
	if got, _ := rep.Cases["language-switch"]["origin"].(string); !strings.HasPrefix(got, "Source:") {
		t.Fatalf("来源应按当前语言渲染：%q", got)
	}
	if got, _ := rep.Cases["conflict-guards"]["fetchDelta"].(float64); got != 0 {
		t.Fatalf("读取中退出不得发起请求：%v", got)
	}
	if got, _ := rep.Cases["conflict-guards"]["generation"].(float64); got != 0 {
		t.Fatalf("读取不得推进未保存 generation：%v", got)
	}
}
