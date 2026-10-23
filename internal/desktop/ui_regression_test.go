package desktop

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

type uiRegressionReport struct {
	Cases    map[string]map[string]any `json:"cases"`
	Failures []string                  `json:"failures"`
	Fatal    string                    `json:"fatal"`
}

// TestRealAppUIRegressions 覆盖 verifier 指出的前端缺陷回归：
// W1 重执行确认语义、W2 还原范围情境、W4 未知 ID、S1 搜索批量、S2 非法导入保留、
// S3 已保存另存取消仍 saved、S6 多文件 drop/非目标、S7 会话映射 metadata。
func TestRealAppUIRegressions(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node 不可用：跳过真实 app.js UI 回归（不使 Go core/build 依赖 Node）")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	harness := filepath.Join(root, "internal", "desktop", "testdata", "ui_regression.mjs")
	appJs := filepath.Join(root, "web", "static", "app.js")
	reportPath := filepath.Join(t.TempDir(), "report.json")

	cmd := exec.Command("node", harness, appJs, reportPath)
	cmd.Dir = root
	out, runErr := cmd.CombinedOutput()
	raw, rerr := os.ReadFile(reportPath)
	if rerr != nil {
		t.Fatalf("node harness 未产出报告：runErr=%v readErr=%v\n%s", runErr, rerr, out)
	}
	var rep uiRegressionReport
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
		"reexecute-confirm", "unknown-id-en", "search-bulk", "illegal-import-preserve",
		"multi-file-drop", "save-cancel-keeps-saved", "busy-save-disabled", "restore-processing-feedback",
		"adjust-details-state", "collapsed-import", "result-row-left", "mapping-and-docx-summary", "inline-feedback",
		"session-mapping-metadata",
		// 第四轮：live FileList 模型、真实 change/drop 监听器、平台能力与提交内容。
		"filelist-reset-model", "change-mask-bytes", "cancel-repeat-zero", "multi-change-keeps-old",
		"disabled-busy-keeps-old", "txt-change-gbk-utf8", "mask-listener-multipart-composite", "restore-listener-multipart",
		"drop-capability-missing", "drop-capability-linux", "drop-capability-windows",
		// 第五轮：三产物真实保存路径单一成功句、全态回退、双语重绘与部分保存。
		"saved-sentence-single", "saved-sentence-bilingual", "restored-saved-sentence",
		"manual-save-rename", "manual-save-cancel", "manual-save-failed-old-path", "manual-save-retry",
		"saved-state-fallbacks", "partial-and-cleanup",
		// verifier C1/W1：已保存态无独立状态 chip、成功 cleanup_warning 可见且双语、重试清旧失败。
		"saved-no-status-chip", "native-save-cleanup-warning", "native-save-warning-bilingual",
		"native-save-retry-clears-failure", "native-save-cancel-keeps-warning",
		// 第六轮：无 I/O 纯词法显示名提取、独立显示名快照，真实保存回调→按钮监听器→DOM→multipart。
		"display-name-lexical", "session-display-name-autosave", "session-display-name-manual-rename",
		"session-display-name-fallback-busy", "session-display-name-snapshot-frozen",
	} {
		if _, ok := rep.Cases[name]; !ok {
			t.Fatalf("报告缺少用例 %q：%v", name, rep.Cases)
		}
	}
}
