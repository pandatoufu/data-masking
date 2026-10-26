package agent

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pandatoufu/data-masking/internal/pptxtest"
)

func TestPreparePPTXDeliversMaskedCopy(t *testing.T) {
	app, root := newTestApp(t, fakeMasker{})
	project := t.TempDir()
	delivered := deliverForTest(t, app, root, project, "报告.pptx", pptxtest.Rich())
	if filepath.Base(delivered.ProjectFile) != "input.masked.pptx" {
		t.Fatalf("PPTX 交付名错误：%q", delivered.ProjectFile)
	}
	if _, err := os.Stat(delivered.ProjectFile); err != nil {
		t.Fatal(err)
	}
	// 项目内不得出现 mapping。
	entries, _ := os.ReadDir(filepath.Dir(delivered.ProjectFile))
	for _, e := range entries {
		if strings.Contains(e.Name(), "mapping") {
			t.Fatalf("项目副本不得包含 mapping：%s", e.Name())
		}
	}
	cleanup := findWarning(delivered.Warnings, "pptx_cleanup")
	if cleanup == nil || cleanup.Count != 4 {
		t.Fatalf("pptx_cleanup 计数错误：%+v", delivered.Warnings)
	}
	if unchecked := findWarning(delivered.Warnings, "unchecked_content"); unchecked == nil || unchecked.Count != 1 {
		t.Fatalf("缺少未检查告警：%+v", delivered.Warnings)
	}
	if findWarning(delivered.Warnings, "docx_cleanup") != nil {
		t.Fatalf("PPTX 不得出现 DOCX 清理告警：%+v", delivered.Warnings)
	}
	// 私有 mapping 为 version 3。
	job := delivered.JobID
	mappingDirs, _ := os.ReadDir(filepath.Join(root, "mapping", job))
	if len(mappingDirs) != 1 {
		t.Fatalf("私有 mapping 缺失：%v", mappingDirs)
	}
}

func findWarning(ws []Warning, kind string) *Warning {
	for i := range ws {
		if ws[i].Kind == kind {
			return &ws[i]
		}
	}
	return nil
}

func TestRestorePPTXPathAAndWarnings(t *testing.T) {
	app, root := newTestApp(t, fakeMasker{})
	project := t.TempDir()
	delivered := deliverForTest(t, app, root, project, "报告.pptx", pptxtest.Rich())
	masked, err := os.ReadFile(delivered.ProjectFile)
	if err != nil {
		t.Fatal(err)
	}
	reply := filepath.Join(project, "reply.pptx")
	if err := os.WriteFile(reply, masked, 0o600); err != nil {
		t.Fatal(err)
	}
	resp := app.Restore(RestoreRequest{JobID: delivered.JobID, ResponsePath: reply})
	if resp.Status != StatusRestored {
		t.Fatalf("PPTX 回程失败：%+v", resp)
	}
	if findWarning(resp.Warnings, "pptx_history") == nil {
		t.Fatalf("缺少 pptx_history：%+v", resp.Warnings)
	}
	c := findWarning(resp.Warnings, "pptx_cleanup")
	if c == nil || c.Count != 0 {
		t.Fatalf("回程 pptx_cleanup 应为 0：%+v", resp.Warnings)
	}
	// 隐私：公开响应不含部件名、源名、mapping 或还原路径。
	blob, _ := json.Marshal(resp)
	for _, secret := range []string{"ppt/slides", "报告.pptx", "mapping", "restored/", "13800138000"} {
		if strings.Contains(string(blob), secret) {
			t.Fatalf("公开响应泄露 %q：%s", secret, blob)
		}
	}
	for _, w := range resp.Warnings {
		if w.Count < 0 {
			t.Fatalf("非法计数：%+v", w)
		}
	}
}

func TestRestorePPTXRejectsCrossFormatAndRetries(t *testing.T) {
	app, root := newTestApp(t, fakeMasker{})
	project := t.TempDir()
	delivered := deliverForTest(t, app, root, project, "报告.pptx", pptxtest.Minimal("13800138000"))
	// DOCX 回复用于 PPTX 任务：明确 unsupported_input，任务仍可重试。
	replyDocx := filepath.Join(project, "reply.docx")
	if err := os.WriteFile(replyDocx, []byte("not-a-docx"), 0o600); err != nil {
		t.Fatal(err)
	}
	failed := app.Restore(RestoreRequest{JobID: delivered.JobID, ResponsePath: replyDocx})
	if failed.Status != StatusFailed || failed.Code != string(CodeUnsupportedInput) {
		t.Fatalf("跨格式应 unsupported_input：%+v", failed)
	}
	masked, _ := os.ReadFile(delivered.ProjectFile)
	reply := filepath.Join(project, "reply.pptx")
	if err := os.WriteFile(reply, masked, 0o600); err != nil {
		t.Fatal(err)
	}
	ok := app.Restore(RestoreRequest{JobID: delivered.JobID, ResponsePath: reply})
	if ok.Status != StatusRestored {
		t.Fatalf("修正后应可重试：%+v", ok)
	}
}

func TestRestorePPTXRejectedNameWritesNoPending(t *testing.T) {
	app, root := newTestApp(t, fakeMasker{})
	project := t.TempDir()
	delivered := deliverForTest(t, app, root, project, "报告.pptx", pptxtest.Minimal("13800138000"))
	bad := filepath.Join(project, "reply.pptm")
	if err := os.WriteFile(bad, []byte("macro"), 0o600); err != nil {
		t.Fatal(err)
	}
	resp := app.Restore(RestoreRequest{JobID: delivered.JobID, ResponsePath: bad})
	if resp.Status != StatusFailed || resp.Code != string(CodeUnsupportedInput) {
		t.Fatalf("非法名称应 unsupported_input：%+v", resp)
	}
	if entries, _ := os.ReadDir(filepath.Join(root, "pending", delivered.JobID)); len(entries) != 0 {
		t.Fatalf("名称校验失败不得写 pending：%v", entries)
	}
}

func TestRestorePPTXSurvivesRestartAndIsIdempotent(t *testing.T) {
	app, root := newTestApp(t, fakeMasker{})
	project := t.TempDir()
	delivered := deliverForTest(t, app, root, project, "报告.pptx", pptxtest.Minimal("13800138000"))
	masked, _ := os.ReadFile(delivered.ProjectFile)
	reply := filepath.Join(project, "reply.pptx")
	if err := os.WriteFile(reply, masked, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := NewApp(root, filepath.Join(t.TempDir(), "absent-config.json"))
	restarted.Core = fakeMasker{}
	if err := restarted.Open(); err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	first := restarted.Restore(RestoreRequest{JobID: delivered.JobID, ResponsePath: reply})
	if first.Status != StatusRestored {
		t.Fatalf("重启后回程失败：%+v", first)
	}
	restoredDir := filepath.Join(root, "restored", delivered.JobID)
	before, _ := os.ReadDir(restoredDir)
	again := restarted.Restore(RestoreRequest{JobID: delivered.JobID, ResponsePath: reply})
	if again.Status != StatusRestored {
		t.Fatalf("同字节重试应幂等：%+v", again)
	}
	after, _ := os.ReadDir(restoredDir)
	if len(after) != len(before) {
		t.Fatalf("同字节重试新增了 restored 目录：%d → %d", len(before), len(after))
	}
	if w := findWarning(again.Warnings, "pptx_cleanup"); w == nil || w.Count != 0 {
		t.Fatalf("幂等重试应保留 pptx_cleanup=0：%+v", again.Warnings)
	}
	other := filepath.Join(project, "other.pptx")
	if err := os.WriteFile(other, []byte("different"), 0o600); err != nil {
		t.Fatal(err)
	}
	third := restarted.Restore(RestoreRequest{JobID: delivered.JobID, ResponsePath: other})
	if third.Status != StatusFailed || third.Code != string(CodeInvalidState) {
		t.Fatalf("异字节重试应 invalid_state：%+v", third)
	}
}

func TestRestorePPTXTextMismatchWarning(t *testing.T) {
	app, root := newTestApp(t, fakeMasker{})
	project := t.TempDir()
	delivered := deliverForTest(t, app, root, project, "报告.pptx", pptxtest.Minimal("13800138000"))
	masked, _ := os.ReadFile(delivered.ProjectFile)
	// 保留占位符但改写其他文字，触发文字参考差异（非阻断）。
	edited := mutatePPTX(t, masked, func(parts map[string][]byte) {
		body := string(parts["ppt/slides/slide1.xml"])
		body = strings.ReplaceAll(body, "<a:t>", "<a:t>模型补注 ")
		parts["ppt/slides/slide1.xml"] = []byte(body)
	})
	reply := filepath.Join(project, "edited.pptx")
	if err := os.WriteFile(reply, edited, 0o600); err != nil {
		t.Fatal(err)
	}
	resp := app.Restore(RestoreRequest{JobID: delivered.JobID, ResponsePath: reply})
	if resp.Status != StatusRestored {
		t.Fatalf("文字差异不得阻断：%+v", resp)
	}
	w := findWarning(resp.Warnings, "pptx_text_mismatch")
	if w == nil {
		t.Fatalf("缺少 pptx_text_mismatch：%+v", resp.Warnings)
	}
	if w.Message != "还原后的 PPTX 文字指纹与原件不一致；仅供参考，不代表还原失败或 mapping 配错。" {
		t.Fatalf("固定文案错误：%q", w.Message)
	}
}

func mutatePPTX(t *testing.T, data []byte, fn func(map[string][]byte)) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	parts := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		parts[f.Name] = body
	}
	fn(parts)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range parts {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPPTXResidualConfirmationIsSeparate(t *testing.T) {
	app, root := newTestApp(t, fakeMasker{})
	// 自定义词 PHONE 会在脱敏后命中占位符 [PHONE_1]，从而产生可确认的正向残留。
	seedCards(t, root, "phone.txt", []byte("PHONE\n"))
	project := t.TempDir()
	seedOriginal(t, root, "报告.pptx", pptxtest.Minimal("13800138000"))
	resp := app.Prepare(PrepareRequest{SourceName: "报告.pptx", ProjectPath: project, Profile: "custom"})
	if resp.Status != StatusAwaitingResidual || resp.ConfirmationKind != KindResidual {
		t.Fatalf("应进入残留确认：%+v", resp)
	}
	if resp.ResidualCount < 1 {
		t.Fatalf("应报告残留计数：%+v", resp)
	}
	if _, err := os.Stat(filepath.Join(project, ".masked-input", resp.JobID)); !os.IsNotExist(err) {
		t.Fatal("残留确认前不得交付项目副本")
	}
	confirmed := app.Confirm(ConfirmRequest{
		JobID: resp.JobID, ConfirmationID: resp.ConfirmationID,
		Decision: DecisionDeliverResidual,
	})
	if confirmed.Status != StatusDelivered {
		t.Fatalf("确认后应交付：%+v", confirmed)
	}
	if w := findWarning(confirmed.Warnings, "residual_forward"); w == nil || w.Count < 1 {
		t.Fatalf("应保留正向残留告警：%+v", confirmed.Warnings)
	}
}

func TestPPTXFixedWarningMessagesExact(t *testing.T) {
	if got := warningMessages["pptx_cleanup"]; got != "PPTX 批注及相关人员信息按支持范围清除；未检查内容不在此保证内。" {
		t.Fatalf("pptx_cleanup 文案错误：%q", got)
	}
	if got := warningMessages["pptx_history"]; got != "回程只恢复映射文字，不能恢复已清除的 PPTX 批注或原始排版。" {
		t.Fatalf("pptx_history 文案错误：%q", got)
	}
	if got := warningMessages["pptx_text_mismatch"]; got != "还原后的 PPTX 文字指纹与原件不一致；仅供参考，不代表还原失败或 mapping 配错。" {
		t.Fatalf("pptx_text_mismatch 文案错误：%q", got)
	}
	if warningMessages["docx_history"] != "已清除批注并接受修订；回程只恢复映射文字，不能恢复批注或修订历史。" {
		t.Fatalf("DOCX 原有文案被改动")
	}
}
