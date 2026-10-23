package desktop

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

type uiFailureReport struct {
	Cases    map[string]map[string]any `json:"cases"`
	Failures []string                  `json:"failures"`
	Fatal    string                    `json:"fatal"`
}

// TestRealAppFailureRendering 覆盖 S8：失败/传输 reject/409 不得残留“处理中”，
// 旧结果保留可见可保存、无旧结果隐藏占位、busy 解除且下一执行可用。
func TestRealAppFailureRendering(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node 不可用：跳过真实 app.js 失败展示回归（不使 Go core/build 依赖 Node）")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	harness := filepath.Join(root, "internal", "desktop", "testdata", "ui_failure.mjs")
	appJs := filepath.Join(root, "web", "static", "app.js")
	reportPath := filepath.Join(t.TempDir(), "report.json")

	cmd := exec.Command("node", harness, appJs, reportPath)
	cmd.Dir = root
	out, runErr := cmd.CombinedOutput()
	raw, rerr := os.ReadFile(reportPath)
	if rerr != nil {
		t.Fatalf("node harness 未产出报告：runErr=%v readErr=%v\n%s", runErr, rerr, out)
	}
	var rep uiFailureReport
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
		"fail-keeps-old-mask", "fail-no-old-mask", "transport-reject-mask", "409-keeps-old", "fail-keeps-old-restore",
	} {
		if _, ok := rep.Cases[name]; !ok {
			t.Fatalf("报告缺少用例 %q：%v", name, rep.Cases)
		}
	}
}
