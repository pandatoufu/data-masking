package desktop

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

type uiDualViewReport struct {
	Cases    map[string]map[string]any `json:"cases"`
	Failures []string                  `json:"failures"`
	Fatal    string                    `json:"fatal"`
}

// TestRealAppDualViewThemeLanguage 加载真实 web/static/app.js（Node vm + fake DOM + fake Bridge），
// 驱动真实 setView/setPreferences/maskSubmit/runRestore/useSessionMapping，断言：
// 双视图状态保留、主题/语言即时生效、会话映射快照不被新脱敏覆盖、两视图未保存聚合与最大 generation。
// Node 不可用时跳过。
func TestRealAppDualViewThemeLanguage(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node 不可用：跳过真实 app.js 双视图回归（不使 Go core/build 依赖 Node）")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	harness := filepath.Join(root, "internal", "desktop", "testdata", "ui_dual_view.mjs")
	appJs := filepath.Join(root, "web", "static", "app.js")
	reportPath := filepath.Join(t.TempDir(), "report.json")

	cmd := exec.Command("node", harness, appJs, reportPath)
	cmd.Dir = root
	out, runErr := cmd.CombinedOutput()
	raw, rerr := os.ReadFile(reportPath)
	if rerr != nil {
		t.Fatalf("node harness 未产出报告：runErr=%v readErr=%v\n%s", runErr, rerr, out)
	}
	var rep uiDualViewReport
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
	for _, name := range []string{"dual-view-preserve", "busy-reject", "prefs-live", "mapping-snapshot", "generation-aggregate"} {
		if _, ok := rep.Cases[name]; !ok {
			t.Fatalf("报告缺少用例 %q：%v", name, rep.Cases)
		}
	}
	if got, _ := rep.Cases["dual-view-preserve"]["preserved"].(bool); !got {
		t.Fatal("dual-view-preserve 应为 true")
	}
	if got, _ := rep.Cases["mapping-snapshot"]["afterNewMask"].(string); got == "" || got[:1] != "A" {
		t.Fatalf("mapping-snapshot 新脱敏后应仍为 A：%q", got)
	}
}
