package agent

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pandatoufu/data-masking/internal/audit"
	"github.com/pandatoufu/data-masking/internal/config"
	"github.com/pandatoufu/data-masking/internal/core"
	"github.com/pandatoufu/data-masking/internal/docxtest"
	"github.com/pandatoufu/data-masking/internal/workspace"
)

type fakeMasker struct {
	mask    func(fileName string, data []byte, opts core.MaskOptions, cfg *config.Config, auditLog *audit.Log) (*core.MaskOutput, error)
	restore func(fileName string, data, mappingData []byte, cfg *config.Config, auditLog *audit.Log) (*core.RestoreOutput, error)
}

func (f fakeMasker) Mask(fileName string, data []byte, opts core.MaskOptions, cfg *config.Config, auditLog *audit.Log) (*core.MaskOutput, error) {
	if f.mask == nil {
		return DefaultMasker{}.Mask(fileName, data, opts, cfg, auditLog)
	}
	return f.mask(fileName, data, opts, cfg, auditLog)
}

func (f fakeMasker) Restore(fileName string, data, mappingData []byte, cfg *config.Config, auditLog *audit.Log) (*core.RestoreOutput, error) {
	if f.restore == nil {
		return DefaultMasker{}.Restore(fileName, data, mappingData, cfg, auditLog)
	}
	return f.restore(fileName, data, mappingData, cfg, auditLog)
}

// newTestApp 构造已打开的测试应用层（缺省配置 + 注入 core）。
// headless 词库完全来自 root/cards，因此预置一个中性词文件满足 custom 启用前提；
// 需要缺失/损坏词库的用例自行替换或删除该文件。
func newTestApp(t *testing.T, masker Masker) (*App, string) {
	t.Helper()
	root := t.TempDir()
	if err := workspace.EnsureHeadless(root); err != nil {
		t.Fatal(err)
	}
	seedCards(t, root, "fixture.txt", []byte("fixture-card-word\n"))
	app := NewApp(root, filepath.Join(t.TempDir(), "absent-config.json"))
	app.Core = masker
	if err := app.Open(); err != nil {
		t.Fatalf("打开会话失败：%v", err)
	}
	t.Cleanup(func() { _ = app.Close() })
	return app, root
}

// seedCards 在 root/cards 下写入一个词文件。
func seedCards(t *testing.T, root, name string, data []byte) string {
	t.Helper()
	dir := filepath.Join(root, workspace.DirCards)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPrepareTextDeliversMaskedCopyMatchingCore(t *testing.T) {
	app, root := newTestApp(t, fakeMasker{})
	content := "联系 13800138000 或 user@example.com，密钥 sk-abcdefghij0123456789"
	seedOriginal(t, root, "notes.txt", []byte(content))
	project := t.TempDir()

	resp := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: project, Profile: "strict"})
	if resp.Status != StatusDelivered {
		t.Fatalf("应交付：%+v", resp)
	}
	if resp.Profile != "strict" || resp.JobID == "" {
		t.Fatalf("应回显 profile 与 job：%+v", resp)
	}
	got, err := os.ReadFile(resp.ProjectFile)
	if err != nil {
		t.Fatalf("项目副本应存在：%v", err)
	}
	cfg := config.Defaults()
	want, err := core.MaskFile("notes.txt", []byte(content), core.MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want.MaskedBytes) {
		t.Fatal("项目脱敏副本字节应与现有 core 一致")
	}
	if filepath.Base(resp.ProjectFile) != "input.masked.txt" {
		t.Fatalf("不得泄露原件名到项目副本名：%q", resp.ProjectFile)
	}
	// 私有配对已保存。
	job := resp.JobID
	if _, err := os.Stat(filepath.Join(root, workspace.DirMasked, job, "notes.masked.txt")); err != nil {
		t.Fatalf("私有 masked 应存在：%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, workspace.DirMapping, job, "notes.masked.mapping.json")); err != nil {
		t.Fatalf("私有 mapping 应存在：%v", err)
	}
	// 不得把 mapping 复制到项目。
	if _, err := os.Stat(filepath.Join(project, ".masked-input", job, "notes.masked.mapping.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("项目不得包含 mapping")
	}
}

// 3.2 新文本名称 prepare→交付：项目副本使用匿名 input.masked.<ext>；
// 无后缀/单段点文件回退 .txt；私有配对沿用真实名称，公开响应不泄露源名或正文。
func TestPrepareNewTextNamesDeliverAnonymous(t *testing.T) {
	app, root := newTestApp(t, fakeMasker{})
	project := t.TempDir()
	content := []byte("手机号 13800138000\n")
	cases := []struct {
		source    string
		projected string
		private   string
		mapping   string
	}{
		{"data.json", "input.masked.json", "data.masked.json", "data.masked.mapping.json"},
		{"README", "input.masked.txt", "README.masked.txt", "README.masked.mapping.json"},
		{".env", "input.masked.txt", ".env.masked.txt", ".env.masked.mapping.json"},
	}
	for _, c := range cases {
		t.Run(c.source, func(t *testing.T) {
			seedOriginal(t, root, c.source, content)
			resp := app.Prepare(PrepareRequest{SourceName: c.source, ProjectPath: project, Profile: "strict"})
			if resp.Status != StatusDelivered {
				t.Fatalf("%q 应交付：%+v", c.source, resp)
			}
			if filepath.Base(resp.ProjectFile) != c.projected {
				t.Fatalf("%q 项目副本命名应匿名：%q", c.source, resp.ProjectFile)
			}
			body, err := os.ReadFile(resp.ProjectFile)
			if err != nil {
				t.Fatalf("项目副本应存在：%v", err)
			}
			if bytes.Contains(body, []byte("13800138000")) {
				t.Fatalf("项目副本不得含明文手机号：%q", body)
			}
			assertNoLeak(t, resp)
			if _, err := os.Stat(filepath.Join(root, workspace.DirMasked, resp.JobID, c.private)); err != nil {
				t.Fatalf("私有脱敏副本命名不符：%v", err)
			}
			if _, err := os.Stat(filepath.Join(root, workspace.DirMapping, resp.JobID, c.mapping)); err != nil {
				t.Fatalf("私有 mapping 命名不符：%v", err)
			}
		})
	}
}

func TestPrepareDocxMatchesCore(t *testing.T) {
	data, err := docxtest.BuildFullFixture(docxtest.FixtureOptions{Body: []string{"手机号 13800138000"}})
	if err != nil {
		t.Fatal(err)
	}
	app, root := newTestApp(t, fakeMasker{})
	seedOriginal(t, root, "合同.docx", data)
	project := t.TempDir()

	resp := app.Prepare(PrepareRequest{SourceName: "合同.docx", ProjectPath: project, Profile: "balanced"})
	if resp.Status != StatusDelivered {
		t.Fatalf("DOCX 应交付：%+v", resp)
	}
	got, err := os.ReadFile(resp.ProjectFile)
	if err != nil {
		t.Fatal(err)
	}
	want, err := core.MaskFile("合同.docx", data, core.MaskOptions{Profile: "balanced"}, config.Defaults(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want.MaskedBytes) {
		t.Fatal("DOCX 项目副本字节应与现有 core 一致")
	}
	if filepath.Ext(resp.ProjectFile) != ".docx" {
		t.Fatalf("DOCX 应按原格式交付：%q", resp.ProjectFile)
	}
}

func TestPrepareHighFrequencyGateHasNoArtifacts(t *testing.T) {
	masker := fakeMasker{mask: func(string, []byte, core.MaskOptions, *config.Config, *audit.Log) (*core.MaskOutput, error) {
		return nil, &core.PreviewRequiredError{
			Extra:   map[string]int{"本次词A": 35},
			Builtin: map[string]int{"常备词B": 68},
		}
	}}
	app, root := newTestApp(t, masker)
	seedOriginal(t, root, "notes.txt", []byte("hello"))
	project := t.TempDir()

	resp := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: project, Profile: "balanced"})
	if resp.Status != StatusAwaitingFrequency {
		t.Fatalf("高频应先确认：%+v", resp)
	}
	if resp.WordCount != 2 || len(resp.HitCounts) != 2 || resp.HitCounts[0] != 68 || resp.HitCounts[1] != 35 {
		t.Fatalf("应返回降序匿名频次：%+v", resp)
	}
	if resp.Threshold != config.DefaultHighFreqThreshold {
		t.Fatalf("阈值应为实际配置：%d", resp.Threshold)
	}
	if resp.ConfirmationID == "" || resp.ConfirmationKind != KindFrequency {
		t.Fatalf("应返回确认凭据与种类：%+v", resp)
	}
	if len(resp.AllowedDecisions) != 2 || resp.AllowedDecisions[0] != DecisionContinueMasking {
		t.Fatalf("允许动作应为 continue_masking/cancel：%+v", resp.AllowedDecisions)
	}
	if resp.ProjectFile != "" {
		t.Fatal("确认前不得返回项目文件")
	}
	if _, err := os.Stat(filepath.Join(root, workspace.DirMasked, resp.JobID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("高频确认前不得产出 masked")
	}
	if names, _ := os.ReadDir(filepath.Join(project, ".masked-input")); len(names) != 0 {
		t.Fatalf("高频确认前不得交付项目：%v", names)
	}
}

func TestPrepareRejectsProfileAndProjectBeforeWork(t *testing.T) {
	app, root := newTestApp(t, fakeMasker{})
	seedOriginal(t, root, "notes.txt", []byte("hello"))
	project := t.TempDir()

	resp := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: project, Profile: "nope"})
	if resp.Status != StatusFailed || resp.Code != string(CodeInvalidProfile) {
		t.Fatalf("未知 profile 应拒绝：%+v", resp)
	}
	resp = app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: "relative", Profile: "balanced"})
	if resp.Status != StatusFailed || resp.Code != string(CodeInvalidProject) {
		t.Fatalf("相对项目路径应拒绝：%+v", resp)
	}
	resp = app.Prepare(PrepareRequest{SourceName: "../secret.txt", ProjectPath: project, Profile: "balanced"})
	if resp.Status != StatusFailed || resp.Code != string(CodeInvalidSource) {
		t.Fatalf("越界原件名应拒绝：%+v", resp)
	}
}
