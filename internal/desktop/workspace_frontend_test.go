package desktop

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

type workspaceFrontendReport struct {
	Cases    map[string]map[string]any `json:"cases"`
	Failures []string                  `json:"failures"`
	Fatal    string                    `json:"fatal"`
}

// TestRealAppWorkspaceFrontend 加载真实 web/static/app.js（Node vm + fake DOM + fake Bridge），
// 驱动真实 maskSubmit / initRestore / confirmDiscard，断言自动落盘调用、产物状态、
// ReportUnsaved 上报与退出文案分支。Node 不可用时跳过。
func TestRealAppWorkspaceFrontend(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node 不可用：跳过真实 app.js 自动落盘回归（不使 Go core/build 依赖 Node）")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	harness := filepath.Join(root, "internal", "desktop", "testdata", "workspace_autosave.mjs")
	appJs := filepath.Join(root, "web", "static", "app.js")
	reportPath := filepath.Join(t.TempDir(), "report.json")

	cmd := exec.Command("node", harness, appJs, reportPath)
	cmd.Dir = root
	out, runErr := cmd.CombinedOutput()
	raw, rerr := os.ReadFile(reportPath)
	if rerr != nil {
		t.Fatalf("node harness 未产出报告：runErr=%v readErr=%v\n%s", runErr, rerr, out)
	}
	var rep workspaceFrontendReport
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

	for _, name := range []string{"mask-saved", "mask-failed", "mask-disabled", "restore-saved", "exit-text"} {
		if _, ok := rep.Cases[name]; !ok {
			t.Fatalf("报告缺少用例 %q：%v", name, rep.Cases)
		}
	}
	if got, _ := rep.Cases["mask-saved"]["maskedStatus"].(string); got != "saved" {
		t.Fatalf("mask-saved 状态应 saved，实际 %q", got)
	}
	if got, _ := rep.Cases["mask-disabled"]["maskedStatus"].(string); got != "unsaved" {
		t.Fatalf("mask-disabled 状态应 unsaved，实际 %q", got)
	}
	if got, _ := rep.Cases["exit-text"]["enabledMsg"].(string); got == "" {
		t.Fatal("exit-text 应记录启用文案")
	}
}
