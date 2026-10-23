package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pandatoufu/data-masking/internal/audit"
	"github.com/pandatoufu/data-masking/internal/docxtest"
	"github.com/pandatoufu/data-masking/internal/workspace"
)

// 规范固定文案（与冻结 design/specs 字面一致）。
const (
	fixedTextMismatchMsg = "还原后的 DOCX 文字指纹与原件不一致；仅供参考，不代表还原失败或 mapping 配错。"
	fixedSHAMismatchMsg  = "还原结果与 mapping 记录的原文指纹不一致；仅供参考，不构成拿错 mapping 的断言。"
	fixedCrossFormatMsg  = "本次回程为跨格式，缺少同格式文字基线，无法确证文本一致。"
)

func warningByKind(list []Warning, kind string) (Warning, bool) {
	for _, w := range list {
		if w.Kind == kind {
			return w, true
		}
	}
	return Warning{}, false
}

// prepareForRealCore 用真实 core 完成一次去程并返回交付响应。
func prepareForRealCore(t *testing.T, app *App, root, project, source string, content []byte) Response {
	t.Helper()
	seedOriginal(t, root, source, content)
	resp := app.Prepare(PrepareRequest{SourceName: source, ProjectPath: project, Profile: "strict"})
	if resp.Status != StatusDelivered {
		t.Fatalf("前置交付失败：%+v", resp)
	}
	return resp
}

func writeProjectFile(t *testing.T, project, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(project, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// 真实 core：DOCX 来源 mapping 还原 DOCX 后文字指纹不同 → text_mismatch（非阻断、独立固定文案）。
func TestRestoreDocxTextMismatchIsTextMismatch(t *testing.T) {
	app, root := newTestApp(t, fakeMasker{})
	project := t.TempDir()
	orig, err := docxtest.BuildFullFixture(docxtest.FixtureOptions{Body: []string{"电话 13812345678"}})
	if err != nil {
		t.Fatal(err)
	}
	delivered := prepareForRealCore(t, app, root, project, "合同.docx", orig)

	// 正文被改写的 DOCX 回复：文字指纹与 mapping 的 SourceTextSHA256 不同。
	rewritten, err := docxtest.BuildFullFixture(docxtest.FixtureOptions{Body: []string{"完全不同的回复内容"}})
	if err != nil {
		t.Fatal(err)
	}
	reply := writeProjectFile(t, project, "reply.docx", rewritten)

	resp := app.Restore(RestoreRequest{JobID: delivered.JobID, ResponsePath: reply})
	if resp.Status != StatusRestored {
		t.Fatalf("DOCX 文字不一致不应阻断：%+v", resp)
	}
	if resp.ConfirmationID != "" || resp.ConfirmationKind != "" || len(resp.AllowedDecisions) != 0 {
		t.Fatalf("一致性告警不得新增确认：%+v", resp)
	}
	got, ok := warningByKind(resp.Warnings, "text_mismatch")
	if !ok {
		t.Fatalf("应返回 text_mismatch：%+v", resp.Warnings)
	}
	if got.Message != fixedTextMismatchMsg {
		t.Fatalf("text_mismatch 文案不符：%q", got.Message)
	}
	if got.Count != 1 {
		t.Fatalf("text_mismatch 计数应为 1：%d", got.Count)
	}
	if _, ok := warningByKind(resp.Warnings, "sha_mismatch"); ok {
		t.Fatalf("DOCX 文字不一致不得归并为 sha_mismatch：%+v", resp.Warnings)
	}
	if _, ok := warningByKind(resp.Warnings, "cross_format"); ok {
		t.Fatalf("同格式 DOCX 不得误报 cross_format：%+v", resp.Warnings)
	}
	assertNoLeak(t, resp)
}

// 真实 core：纯文本来源 mapping 还原纯文本后字节 SHA 不同 → sha_mismatch（不与 text_mismatch 混淆）。
func TestRestoreTextSHAMismatchIsSHAMismatch(t *testing.T) {
	app, root := newTestApp(t, fakeMasker{})
	project := t.TempDir()
	delivered := prepareForRealCore(t, app, root, project, "notes.txt", []byte("联系 13800138000"))

	masked, err := os.ReadFile(delivered.ProjectFile)
	if err != nil {
		t.Fatal(err)
	}
	// 保留占位符、追加内容：还原字节与原件不同但成功。
	reply := writeProjectFile(t, project, "reply.txt", append(append([]byte{}, masked...), []byte(" 补充")...))

	resp := app.Restore(RestoreRequest{JobID: delivered.JobID, ResponsePath: reply})
	if resp.Status != StatusRestored {
		t.Fatalf("字节 SHA 不一致不应阻断：%+v", resp)
	}
	if resp.ConfirmationID != "" {
		t.Fatalf("一致性告警不得新增确认：%+v", resp)
	}
	got, ok := warningByKind(resp.Warnings, "sha_mismatch")
	if !ok {
		t.Fatalf("应返回 sha_mismatch：%+v", resp.Warnings)
	}
	if got.Message != fixedSHAMismatchMsg || got.Count != 1 {
		t.Fatalf("sha_mismatch 文案/计数不符：%+v", got)
	}
	if _, ok := warningByKind(resp.Warnings, "text_mismatch"); ok {
		t.Fatalf("纯文本不得误报 text_mismatch：%+v", resp.Warnings)
	}
	if _, ok := warningByKind(resp.Warnings, "cross_format"); ok {
		t.Fatalf("同格式文本不得误报 cross_format：%+v", resp.Warnings)
	}
	assertNoLeak(t, resp)
}

// 真实 core：双向跨格式（DOCX→文本、文本→DOCX）都返回 cross_format，不误报指纹不一致。
func TestRestoreCrossFormatBothDirections(t *testing.T) {
	t.Run("docx-source-text-reply", func(t *testing.T) {
		app, root := newTestApp(t, fakeMasker{})
		project := t.TempDir()
		orig, err := docxtest.BuildFullFixture(docxtest.FixtureOptions{Body: []string{"电话 13812345678"}})
		if err != nil {
			t.Fatal(err)
		}
		delivered := prepareForRealCore(t, app, root, project, "合同.docx", orig)
		reply := writeProjectFile(t, project, "reply.txt", []byte("回复文本 [PHONE_1]"))

		resp := app.Restore(RestoreRequest{JobID: delivered.JobID, ResponsePath: reply})
		if resp.Status != StatusRestored {
			t.Fatalf("跨格式不应阻断：%+v", resp)
		}
		got, ok := warningByKind(resp.Warnings, "cross_format")
		if !ok {
			t.Fatalf("应返回 cross_format：%+v", resp.Warnings)
		}
		if got.Message != fixedCrossFormatMsg || got.Count != 1 {
			t.Fatalf("cross_format 文案/计数不符：%+v", got)
		}
		for _, banned := range []string{"text_mismatch", "sha_mismatch"} {
			if _, ok := warningByKind(resp.Warnings, banned); ok {
				t.Fatalf("跨格式不得误报 %s：%+v", banned, resp.Warnings)
			}
		}
		assertNoLeak(t, resp)
	})

	t.Run("text-source-docx-reply", func(t *testing.T) {
		app, root := newTestApp(t, fakeMasker{})
		project := t.TempDir()
		delivered := prepareForRealCore(t, app, root, project, "notes.txt", []byte("电话 13812345678"))
		docxReply, err := docxtest.BuildFullFixture(docxtest.FixtureOptions{Body: []string{"回复 [PHONE_1]"}})
		if err != nil {
			t.Fatal(err)
		}
		reply := writeProjectFile(t, project, "reply.docx", docxReply)

		resp := app.Restore(RestoreRequest{JobID: delivered.JobID, ResponsePath: reply})
		if resp.Status != StatusRestored {
			t.Fatalf("跨格式不应阻断：%+v", resp)
		}
		got, ok := warningByKind(resp.Warnings, "cross_format")
		if !ok {
			t.Fatalf("应返回 cross_format：%+v", resp.Warnings)
		}
		if got.Message != fixedCrossFormatMsg || got.Count != 1 {
			t.Fatalf("cross_format 文案/计数不符：%+v", got)
		}
		for _, banned := range []string{"text_mismatch", "sha_mismatch"} {
			if _, ok := warningByKind(resp.Warnings, banned); ok {
				t.Fatalf("跨格式不得误报 %s：%+v", banned, resp.Warnings)
			}
		}
		assertNoLeak(t, resp)
	})
}

// 真实 core + 真实审计写失败：同字节幂等重试保留告警与 audit_failed，不新增文件/重复正常审计。
func TestRestoreIdempotentRetainsWarningsAndAuditFailed(t *testing.T) {
	app, root := newTestApp(t, fakeMasker{})
	// 真实审计写失败：审计路径被目录占位（core best-effort 标记 audit_failed，不阻断）。
	badAudit := filepath.Join(t.TempDir(), "audit.jsonl")
	if err := os.Mkdir(badAudit, 0o755); err != nil {
		t.Fatal(err)
	}
	app.Audit = audit.New(badAudit)

	project := t.TempDir()
	delivered := prepareForRealCore(t, app, root, project, "notes.txt", []byte("联系 13800138000"))
	if !delivered.AuditFailed {
		t.Fatalf("前置应真实标记审计失败：%+v", delivered)
	}
	masked, err := os.ReadFile(delivered.ProjectFile)
	if err != nil {
		t.Fatal(err)
	}
	reply := writeProjectFile(t, project, "reply.txt", append(append([]byte{}, masked...), []byte(" 补充")...))

	first := app.Restore(RestoreRequest{JobID: delivered.JobID, ResponsePath: reply})
	if first.Status != StatusRestored {
		t.Fatalf("首次还原应成功：%+v", first)
	}
	if _, ok := warningByKind(first.Warnings, "sha_mismatch"); !ok {
		t.Fatalf("首次应带 sha_mismatch：%+v", first.Warnings)
	}
	if !first.AuditFailed {
		t.Fatalf("首次应保留真实 audit_failed：%+v", first)
	}
	if w, ok := warningByKind(first.Warnings, "audit_failed"); !ok || w.Message != warningMessages["audit_failed"] {
		t.Fatalf("首次应带 audit_failed 告警：%+v", first.Warnings)
	}
	beforeFiles := restoredFileCount(t, root, delivered.JobID)
	beforePending := pendingAttemptCount(t, root, delivered.JobID)

	second := app.Restore(RestoreRequest{JobID: delivered.JobID, ResponsePath: reply})
	if second.Status != StatusRestored {
		t.Fatalf("同字节应幂等返回：%+v", second)
	}
	got, ok := warningByKind(second.Warnings, "sha_mismatch")
	if !ok || got.Message != fixedSHAMismatchMsg || got.Count != 1 {
		t.Fatalf("同字节重试应保留告警分类与固定文案：%+v", second.Warnings)
	}
	if !second.AuditFailed {
		t.Fatalf("同字节重试应保留 audit_failed：%+v", second)
	}
	if _, ok := warningByKind(second.Warnings, "audit_failed"); !ok {
		t.Fatalf("同字节重试应保留 audit_failed 告警：%+v", second.Warnings)
	}
	if after := restoredFileCount(t, root, delivered.JobID); after != beforeFiles {
		t.Fatalf("同字节不得新增文件：%d → %d", beforeFiles, after)
	}
	if after := pendingAttemptCount(t, root, delivered.JobID); after != beforePending {
		t.Fatalf("同字节不得新增 pending attempt：%d → %d", beforePending, after)
	}
	records, err := app.Store.Load(delivered.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if countType(records, RecRestored) != 1 {
		t.Fatalf("不得重复正常审计/完成事件：%+v", records)
	}
}

func pendingAttemptCount(t *testing.T, root, job string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, workspace.DirPending, job))
	if err != nil {
		return 0
	}
	return len(entries)
}
