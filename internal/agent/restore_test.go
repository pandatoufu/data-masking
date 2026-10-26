package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pandatoufu/data-masking/internal/docxtest"
	"github.com/pandatoufu/data-masking/internal/workspace"
)

// deliverForTest 执行一次 prepare 并返回已交付响应。
func deliverForTest(t *testing.T, app *App, root, project, source string, content []byte) Response {
	t.Helper()
	seedOriginal(t, root, source, content)
	resp := app.Prepare(PrepareRequest{SourceName: source, ProjectPath: project, Profile: "strict"})
	if resp.Status != StatusDelivered {
		t.Fatalf("前置交付失败：%+v", resp)
	}
	return resp
}

func TestRestoreTextWritesPendingAndRestored(t *testing.T) {
	app, root := newTestApp(t, fakeMasker{})
	project := t.TempDir()
	delivered := deliverForTest(t, app, root, project, "notes.txt", []byte("联系 13800138000"))

	reply := filepath.Join(project, "reply.txt")
	masked, err := os.ReadFile(delivered.ProjectFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reply, masked, 0o600); err != nil {
		t.Fatal(err)
	}
	resp := app.Restore(RestoreRequest{JobID: delivered.JobID, ResponsePath: reply})
	if resp.Status != StatusRestored {
		t.Fatalf("应还原：%+v", resp)
	}
	if resp.RestoredCount != 1 {
		t.Fatalf("应报告还原数量：%d", resp.RestoredCount)
	}
	// pending 快照存在。
	pendingDir := filepath.Join(root, workspace.DirPending, delivered.JobID)
	entries, err := os.ReadDir(pendingDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("应写 pending/<job>/<attempt>/：%v %v", entries, err)
	}
	// restored 结果存在且不复制回项目。
	restoredDir := filepath.Join(root, workspace.DirRestored, delivered.JobID)
	rd, err := os.ReadDir(restoredDir)
	if err != nil || len(rd) != 1 {
		t.Fatalf("应写 restored/<job>/<attempt>/：%v %v", rd, err)
	}
	names, _ := os.ReadDir(project)
	for _, n := range names {
		if n.Name() == "notes.restored.txt" {
			t.Fatal("不得把还原明文复制回项目")
		}
	}
}

func TestRestoreDocxAndCrossFormat(t *testing.T) {
	data, err := docxtest.BuildFullFixture(docxtest.FixtureOptions{Body: []string{"手机号 13800138000"}})
	if err != nil {
		t.Fatal(err)
	}
	app, root := newTestApp(t, fakeMasker{})
	project := t.TempDir()
	delivered := deliverForTest(t, app, root, project, "合同.docx", data)

	// DOCX 回复：按实际格式保存。
	maskedDocx, err := os.ReadFile(delivered.ProjectFile)
	if err != nil {
		t.Fatal(err)
	}
	replyDocx := filepath.Join(project, "reply.docx")
	if err := os.WriteFile(replyDocx, maskedDocx, 0o600); err != nil {
		t.Fatal(err)
	}
	resp := app.Restore(RestoreRequest{JobID: delivered.JobID, ResponsePath: replyDocx})
	if resp.Status != StatusRestored {
		t.Fatalf("DOCX 回程应成功：%+v", resp)
	}

	// 同一任务第二次回程（不同字节）应拒绝，状态保持 restored。
	other := filepath.Join(project, "other.txt")
	if err := os.WriteFile(other, []byte("different"), 0o600); err != nil {
		t.Fatal(err)
	}
	again := app.Restore(RestoreRequest{JobID: delivered.JobID, ResponsePath: other})
	if again.Status != StatusFailed || again.Code != string(CodeInvalidState) {
		t.Fatalf("异字节重试应 invalid_state：%+v", again)
	}
	records, err := app.Store.Load(delivered.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if DeriveState(records) != StateRestored {
		t.Fatalf("稳定状态应保持 restored：%v", DeriveState(records))
	}
}

func TestRestoreIdempotentSameBytes(t *testing.T) {
	app, root := newTestApp(t, fakeMasker{})
	project := t.TempDir()
	delivered := deliverForTest(t, app, root, project, "notes.txt", []byte("13800138000"))
	masked, _ := os.ReadFile(delivered.ProjectFile)
	reply := filepath.Join(project, "reply.txt")
	if err := os.WriteFile(reply, masked, 0o600); err != nil {
		t.Fatal(err)
	}
	first := app.Restore(RestoreRequest{JobID: delivered.JobID, ResponsePath: reply})
	if first.Status != StatusRestored {
		t.Fatalf("首次应成功：%+v", first)
	}
	before := restoredFileCount(t, root, delivered.JobID)
	second := app.Restore(RestoreRequest{JobID: delivered.JobID, ResponsePath: reply})
	if second.Status != StatusRestored {
		t.Fatalf("同字节应幂等返回：%+v", second)
	}
	after := restoredFileCount(t, root, delivered.JobID)
	if before != after {
		t.Fatalf("同字节不得新增文件：%d → %d", before, after)
	}
	records, err := app.Store.Load(delivered.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if countType(records, RecRestored) != 1 {
		t.Fatalf("不得重复处理审计：%+v", records)
	}
}

func restoredFileCount(t *testing.T, root, job string) int {
	t.Helper()
	dir := filepath.Join(root, workspace.DirRestored, job)
	var count int
	entries, _ := os.ReadDir(dir)
	for _, attempt := range entries {
		if !attempt.IsDir() {
			continue
		}
		files, _ := os.ReadDir(filepath.Join(dir, attempt.Name()))
		count += len(files)
	}
	return count
}

func countType(records []Record, t RecordType) int {
	n := 0
	for _, r := range records {
		if r.Type == t {
			n++
		}
	}
	return n
}

func TestRestoreRejectsBadResponseBeforePending(t *testing.T) {
	app, root := newTestApp(t, fakeMasker{})
	project := t.TempDir()
	delivered := deliverForTest(t, app, root, project, "notes.txt", []byte("13800138000"))

	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	badExt := filepath.Join(project, "reply.zip")
	if err := os.WriteFile(badExt, []byte("zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	badRTF := filepath.Join(project, "reply.rtf")
	if err := os.WriteFile(badRTF, []byte("{\\rtf1 text}"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"cross-project": outside,
		"bad-ext":       badExt,
		"bad-rtf":       badRTF,
		"missing":       filepath.Join(project, "missing.txt"),
		"relative":      "reply.txt",
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			resp := app.Restore(RestoreRequest{JobID: delivered.JobID, ResponsePath: path})
			if resp.Status != StatusFailed {
				t.Fatalf("应拒绝：%+v", resp)
			}
			if _, err := os.Stat(filepath.Join(root, workspace.DirPending, delivered.JobID)); !os.IsNotExist(err) {
				t.Fatal("校验失败不得写 pending")
			}
		})
	}
}

// 3.2 新文本名称 prepare→交付→restore 闭环：回复可为新后缀、无后缀或点文件；
// 同字节幂等、匿名响应，命名遵循 masking-reversible。
func TestRestoreNewTextNamesClosure(t *testing.T) {
	app, root := newTestApp(t, fakeMasker{})
	project := t.TempDir()
	cases := []struct {
		source   string
		reply    string
		restored string
	}{
		{"data.json", "reply.yaml", "data.restored.json"},
		{"README", "README", "README.restored.txt"},
		{".env", ".env", ".env.restored.txt"},
	}
	for _, c := range cases {
		t.Run(c.source, func(t *testing.T) {
			delivered := deliverForTest(t, app, root, project, c.source, []byte("手机号 13800138000\n"))
			masked, err := os.ReadFile(delivered.ProjectFile)
			if err != nil {
				t.Fatal(err)
			}
			reply := writeProjectFile(t, project, c.reply, masked)

			resp := app.Restore(RestoreRequest{JobID: delivered.JobID, ResponsePath: reply})
			if resp.Status != StatusRestored || resp.RestoredCount != 1 {
				t.Fatalf("%q 回复应还原：%+v", c.reply, resp)
			}
			if resp.Message != msgRestored {
				t.Fatalf("回程响应应为固定匿名文案：%q", resp.Message)
			}
			assertNoLeak(t, resp)
			names := restoredNames(t, root, delivered.JobID)
			if len(names) != 1 || names[0] != c.restored {
				t.Fatalf("还原产物命名不符：%v want %q", names, c.restored)
			}
			// 同字节重试幂等：不新增文件、不改变状态。
			again := app.Restore(RestoreRequest{JobID: delivered.JobID, ResponsePath: reply})
			if again.Status != StatusRestored || again.RestoredCount != resp.RestoredCount {
				t.Fatalf("同字节应幂等返回：%+v", again)
			}
			if got := restoredNames(t, root, delivered.JobID); len(got) != 1 {
				t.Fatalf("幂等重试不得新增文件：%v", got)
			}
		})
	}
}

// 3.2 DOCX 来源 mapping 接受新名称文本回复：跨格式提示、本地闭环、匿名响应。
func TestRestoreDocxMappingNewTextReplyNames(t *testing.T) {
	app, root := newTestApp(t, fakeMasker{})
	project := t.TempDir()
	orig, err := docxtest.BuildFullFixture(docxtest.FixtureOptions{Body: []string{"电话 13800138000"}})
	if err != nil {
		t.Fatal(err)
	}
	delivered := prepareForRealCore(t, app, root, project, "合同.docx", orig)
	reply := writeProjectFile(t, project, "README", []byte("回复文本 [PHONE_1]"))

	resp := app.Restore(RestoreRequest{JobID: delivered.JobID, ResponsePath: reply})
	if resp.Status != StatusRestored {
		t.Fatalf("无后缀文本回复应还原：%+v", resp)
	}
	if _, ok := warningByKind(resp.Warnings, "cross_format"); !ok {
		t.Fatalf("应返回 cross_format：%+v", resp.Warnings)
	}
	assertNoLeak(t, resp)
	if got := restoredNames(t, root, delivered.JobID); len(got) != 1 || got[0] != "合同.restored.txt" {
		t.Fatalf("DOCX mapping 的文本回复应回退 .txt：%v", got)
	}
}

func restoredNames(t *testing.T, root, job string) []string {
	t.Helper()
	dir := filepath.Join(root, workspace.DirRestored, job)
	attempts, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取 restored 失败：%v", err)
	}
	var names []string
	for _, attempt := range attempts {
		if !attempt.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(dir, attempt.Name()))
		if err != nil {
			t.Fatalf("读取 restored attempt 失败：%v", err)
		}
		for _, f := range files {
			names = append(names, f.Name())
		}
	}
	return names
}

// 回程失败后保持 delivered，可修正重试。
func TestRestoreFailureKeepsDeliveredAndRetry(t *testing.T) {
	app, root := newTestApp(t, fakeMasker{})
	project := t.TempDir()
	delivered := deliverForTest(t, app, root, project, "notes.txt", []byte("13800138000"))

	bad := filepath.Join(project, "bad.txt")
	if err := os.WriteFile(bad, []byte{0x00, 0x01, 0x02}, 0o600); err != nil {
		t.Fatal(err)
	}
	failed := app.Restore(RestoreRequest{JobID: delivered.JobID, ResponsePath: bad})
	if failed.Status != StatusFailed {
		t.Fatalf("无效内容应失败：%+v", failed)
	}
	records, err := app.Store.Load(delivered.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if DeriveState(records) != StateDelivered {
		t.Fatalf("失败 attempt 应保持 delivered：%v", DeriveState(records))
	}

	masked, _ := os.ReadFile(delivered.ProjectFile)
	good := filepath.Join(project, "good.txt")
	if err := os.WriteFile(good, masked, 0o600); err != nil {
		t.Fatal(err)
	}
	ok := app.Restore(RestoreRequest{JobID: delivered.JobID, ResponsePath: good})
	if ok.Status != StatusRestored {
		t.Fatalf("修正后应成功：%+v", ok)
	}
	// 失败与成功 attempt 各自隔离目录。
	attempts, err := os.ReadDir(filepath.Join(root, workspace.DirPending, delivered.JobID))
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 {
		t.Fatalf("失败与成功快照应各自隔离：%d", len(attempts))
	}
}
