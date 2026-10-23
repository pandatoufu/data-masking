package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pandatoufu/data-masking/internal/audit"
	"github.com/pandatoufu/data-masking/internal/config"
	"github.com/pandatoufu/data-masking/internal/core"
	"github.com/pandatoufu/data-masking/internal/workspace"
)

// removeCards 删除 cards 下的直属文件（保留目录）。
func removeCards(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, workspace.DirCards))
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(root, workspace.DirCards, e.Name())); err != nil {
			t.Fatal(err)
		}
	}
}

// custom 启用且无 TXT 时按固定“先放词库”文案拒绝，且不产生任何产物。
func TestPrepareRequiresCardsTXTWhenCustomEnabled(t *testing.T) {
	app, root := newTestApp(t, fakeMasker{})
	removeCards(t, root)
	seedOriginal(t, root, "notes.txt", []byte("hello"))
	project := t.TempDir()

	resp := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: project, Profile: "balanced"})
	if resp.Status != StatusFailed || resp.Code != string(CodeInvalidConfig) || resp.Message != msgCardsMissing {
		t.Fatalf("应返回固定缺失词库错误：%+v", resp)
	}
	if entries, _ := os.ReadDir(filepath.Join(root, workspace.DirMasked)); len(entries) != 0 {
		t.Fatalf("缺失词库不得生成 masked：%v", entries)
	}
	if _, err := os.Stat(filepath.Join(project, ".masked-input")); !os.IsNotExist(err) {
		t.Fatal("缺失词库不得交付项目")
	}
}

// 损坏候选整批拒绝，固定文案不泄露候选名、词、路径或底层错误。
func TestPrepareInvalidCardsDoesNotLeak(t *testing.T) {
	app, root := newTestApp(t, fakeMasker{})
	removeCards(t, root)
	secret := "SECRET-CARD-VALUE"
	seedCards(t, root, "SECRET-CANDIDATE-NAME.txt", []byte(secret+"\x00\n"))
	seedOriginal(t, root, "notes.txt", []byte("hello"))
	project := t.TempDir()

	resp := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: project, Profile: "balanced"})
	if resp.Status != StatusFailed || resp.Code != string(CodeInvalidConfig) || resp.Message != msgCardsInvalid {
		t.Fatalf("应返回固定无效词库错误：%+v", resp)
	}
	raw, _ := json.Marshal(resp)
	for _, banned := range []string{secret, "SECRET-CANDIDATE-NAME", root} {
		if strings.Contains(string(raw), banned) {
			t.Fatalf("响应泄露 %q：%s", banned, raw)
		}
	}
}

// custom 关闭时不读取 cards：损坏/空的词文件不影响 prepare。
func TestPrepareCustomDisabledDoesNotReadCards(t *testing.T) {
	app, root := newTestApp(t, fakeMasker{})
	removeCards(t, root)
	seedCards(t, root, "bad.txt", []byte{0xff, 0xfe, 0x00, 0x00})
	seedOriginal(t, root, "key.txt", []byte("sk-abcdefghij0123456789"))
	project := t.TempDir()

	resp := app.Prepare(PrepareRequest{SourceName: "key.txt", ProjectPath: project, Profile: "minimal"})
	if resp.Status != StatusDelivered {
		t.Fatalf("custom 关闭不应读取词库：%+v", resp)
	}
	if strings.Contains(string(mustRead(t, resp.ProjectFile)), "sk-abcdefghij0123456789") {
		t.Fatal("minimal 应仍处理 api_key")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// prepare 把同一份 cards 词快照交给 core，旧 config.custom_words 不参与。
func TestPrepareUsesCardsSnapshotForCore(t *testing.T) {
	var gotWords []string
	gotThreshold := 0
	masker := fakeMasker{mask: func(fileName string, data []byte, opts core.MaskOptions, cfg *config.Config, _ *audit.Log) (*core.MaskOutput, error) {
		gotWords = append([]string(nil), cfg.CustomWords...)
		gotThreshold = opts.HighFreqThreshold
		return &core.MaskOutput{
			MaskedName:   "notes.masked.txt",
			MaskedBytes:  []byte("masked"),
			MappingName:  "notes.masked.mapping.json",
			MappingBytes: []byte(`{"v":1}`),
			Format:       "text",
		}, nil
	}}
	app, root := newTestApp(t, masker)
	removeCards(t, root)
	seedCards(t, root, "CARD-SECRET-NAME.txt", []byte("CARD-SECRET-WORD\n第二词\n"))
	seedOriginal(t, root, "notes.txt", []byte("hello"))
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"custom_words":["LEGACY-OLD-WORD"],"high_freq_threshold":9}`), 0o600); err != nil {
		t.Fatal(err)
	}
	app.ConfigPath = configPath
	project := t.TempDir()

	resp := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: project, Profile: "strict"})
	if resp.Status != StatusDelivered {
		t.Fatalf("应交付：%+v", resp)
	}
	if strings.Join(gotWords, "|") != "CARD-SECRET-WORD|第二词" {
		t.Fatalf("core 应收到 cards 词快照：%v", gotWords)
	}
	if gotThreshold != 9 {
		t.Fatalf("其他生效配置应保留：%d", gotThreshold)
	}

	records, err := app.Store.Load(resp.JobID)
	if err != nil || len(records) == 0 {
		t.Fatalf("应可读取任务记录：%v", err)
	}
	fp := records[0].ConfigFingerprint
	if !fingerprintPattern.MatchString(fp) {
		t.Fatalf("事件指纹应为 64 字符小写 hex：%q", fp)
	}
	// 事件中不得出现词值或候选名（源文件名/项目路径除外）。
	raw := string(mustRead(t, filepath.Join(app.Store.JobDir(resp.JobID), "000001.json")))
	for _, banned := range []string{"CARD-SECRET-WORD", "CARD-SECRET-NAME", "LEGACY-OLD-WORD"} {
		if strings.Contains(raw, banned) {
			t.Fatalf("任务事件泄露 %q：%s", banned, raw)
		}
	}
}

// 任一确认态之后，cards 增删改/改名/字节变化/身份替换/不可读都使确认 stale 且不交付。
func TestConfirmStaleWhenCardsChange(t *testing.T) {
	mutations := map[string]func(t *testing.T, root string){
		"byte-change-same-words": func(t *testing.T, root string) {
			seedCards(t, root, "fixture.txt", []byte("fixture-card-word\n\n"))
		},
		"rename": func(t *testing.T, root string) {
			if err := os.Rename(filepath.Join(root, workspace.DirCards, "fixture.txt"), filepath.Join(root, workspace.DirCards, "renamed.txt")); err != nil {
				t.Fatal(err)
			}
		},
		"add-file": func(t *testing.T, root string) {
			seedCards(t, root, "another.txt", []byte("another-word\n"))
		},
		"delete-file": func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, workspace.DirCards, "fixture.txt")); err != nil {
				t.Fatal(err)
			}
		},
		"replace-file-identity": func(t *testing.T, root string) {
			path := filepath.Join(root, workspace.DirCards, "fixture.txt")
			data := mustRead(t, path)
			tmp := path + ".tmp"
			if err := os.WriteFile(tmp, data, 0o600); err != nil {
				t.Fatal(err)
			}
			// 用另一个 inode 覆盖原候选，字节相同但身份已换。
			if err := os.Rename(tmp, path); err != nil {
				t.Fatal(err)
			}
		},
		"replace-directory-identity": func(t *testing.T, root string) {
			data := mustRead(t, filepath.Join(root, workspace.DirCards, "fixture.txt"))
			old := filepath.Join(root, "cards-old")
			if err := os.Rename(filepath.Join(root, workspace.DirCards), old); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, workspace.DirCards), 0o755); err != nil {
				t.Fatal(err)
			}
			seedCards(t, root, "fixture.txt", data)
		},
		"replaced-by-directory": func(t *testing.T, root string) {
			path := filepath.Join(root, workspace.DirCards, "fixture.txt")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0o755); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			app, root := newTestApp(t, maskThenConfirm())
			seedOriginal(t, root, "notes.txt", []byte("hello"))
			project := t.TempDir()
			prep := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: project, Profile: "balanced"})
			if prep.Status != StatusAwaitingFrequency {
				t.Fatalf("应先进入高频确认：%+v", prep)
			}
			mutate(t, root)

			res := app.Confirm(ConfirmRequest{JobID: prep.JobID, ConfirmationID: prep.ConfirmationID, Decision: DecisionContinueMasking})
			if res.Status != StatusFailed || res.Code != string(CodeConfirmStale) {
				t.Fatalf("词库变化应 confirmation_stale：%+v", res)
			}
			if _, err := os.Stat(filepath.Join(project, ".masked-input", prep.JobID)); !os.IsNotExist(err) {
				t.Fatal("过期确认不得交付")
			}
			records, err := app.Store.Load(prep.JobID)
			if err != nil || DeriveState(records) != StateFailed {
				t.Fatalf("旧任务应终止为 failed：%v %v", records, err)
			}
		})
	}
}

// 残留确认同样重读 cards：变化则 stale，不复制项目。
func TestResidualConfirmStaleWhenCardsChange(t *testing.T) {
	app, root := newTestApp(t, residualMasker())
	seedOriginal(t, root, "notes.txt", []byte("hello"))
	project := t.TempDir()
	prep := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: project, Profile: "balanced"})
	if prep.Status != StatusAwaitingResidual {
		t.Fatalf("应进入残留确认：%+v", prep)
	}
	seedCards(t, root, "fixture.txt", []byte("fixture-card-word\nchanged\n"))

	res := app.Confirm(ConfirmRequest{JobID: prep.JobID, ConfirmationID: prep.ConfirmationID, Decision: DecisionDeliverResidual})
	if res.Status != StatusFailed || res.Code != string(CodeConfirmStale) {
		t.Fatalf("词库变化应 confirmation_stale：%+v", res)
	}
	if _, err := os.Stat(filepath.Join(project, ".masked-input", prep.JobID)); !os.IsNotExist(err) {
		t.Fatal("过期残留确认不得交付")
	}
}

// cancel 只验证凭据，不因词库损坏阻塞取消。
func TestCancelDoesNotReadCards(t *testing.T) {
	app, root := newTestApp(t, maskThenConfirm())
	seedOriginal(t, root, "notes.txt", []byte("hello"))
	project := t.TempDir()
	prep := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: project, Profile: "balanced"})
	if prep.Status != StatusAwaitingFrequency {
		t.Fatalf("应先进入高频确认：%+v", prep)
	}
	removeCards(t, root)
	seedCards(t, root, "broken.txt", []byte{0xff, 0xfe, 0x00, 0x00})

	res := app.Confirm(ConfirmRequest{JobID: prep.JobID, ConfirmationID: prep.ConfirmationID, Decision: DecisionCancel})
	if res.Status != StatusCancelled {
		t.Fatalf("取消不应读取 cards：%+v", res)
	}
}

// custom 关闭时 cards 不参与指纹/重验；旧 custom_words 变化也不影响指纹。
func TestCustomDisabledAndLegacyWordsDoNotAffectFingerprint(t *testing.T) {
	app, root := newTestApp(t, maskThenConfirm())
	seedOriginal(t, root, "notes.txt", []byte("hello"))
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"custom_words":["LEGACY-A"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	app.ConfigPath = configPath
	project := t.TempDir()
	// minimal 关闭 custom；fake core 仍返回高频预览，用于验证确认重验不读 cards。
	prep := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: project, Profile: "minimal"})
	if prep.Status != StatusAwaitingFrequency {
		t.Fatalf("应先进入高频确认：%+v", prep)
	}
	removeCards(t, root)
	seedCards(t, root, "broken.txt", []byte{0xff, 0xfe, 0x00, 0x00})
	if err := os.WriteFile(configPath, []byte(`{"custom_words":["LEGACY-B"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	res := app.Confirm(ConfirmRequest{JobID: prep.JobID, ConfirmationID: prep.ConfirmationID, Decision: DecisionContinueMasking})
	if res.Status != StatusDelivered {
		t.Fatalf("custom 关闭/旧词变化不应使确认失效：%+v", res)
	}
}

// custom 启用时，旧 custom_words 变化也不影响 cards 指纹/确认有效。
func TestLegacyCustomWordsDoNotAffectCardsFingerprint(t *testing.T) {
	app, root := newTestApp(t, maskThenConfirm())
	seedOriginal(t, root, "notes.txt", []byte("hello"))
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"custom_words":["LEGACY-A"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	app.ConfigPath = configPath
	project := t.TempDir()
	prep := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: project, Profile: "balanced"})
	if prep.Status != StatusAwaitingFrequency {
		t.Fatalf("应先进入高频确认：%+v", prep)
	}
	if err := os.WriteFile(configPath, []byte(`{"custom_words":["LEGACY-B"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	res := app.Confirm(ConfirmRequest{JobID: prep.JobID, ConfirmationID: prep.ConfirmationID, Decision: DecisionContinueMasking})
	if res.Status != StatusDelivered {
		t.Fatalf("旧 custom_words 不得影响 cards 指纹：%+v", res)
	}
}

// 已交付任务回程只使用记录 mapping；cards 改变、删除或重启都不阻断。
func TestDeliveredRestoreIndependentOfCards(t *testing.T) {
	app, root := newTestApp(t, fakeMasker{})
	seedOriginal(t, root, "notes.txt", []byte("联系 13800138000"))
	project := t.TempDir()
	delivered := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: project, Profile: "strict"})
	if delivered.Status != StatusDelivered {
		t.Fatalf("应交付：%+v", delivered)
	}
	reply := filepath.Join(project, "reply.txt")
	if err := os.WriteFile(reply, mustRead(t, delivered.ProjectFile), 0o600); err != nil {
		t.Fatal(err)
	}

	removeCards(t, root)
	seedCards(t, root, "broken.txt", []byte{0xff, 0xfe, 0x00, 0x00})
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := NewApp(root, app.ConfigPath)
	if err := restarted.Open(); err != nil {
		t.Fatalf("重启失败：%v", err)
	}
	defer func() { _ = restarted.Close() }()

	resp := restarted.Restore(RestoreRequest{JobID: delivered.JobID, ResponsePath: reply})
	if resp.Status != StatusRestored {
		t.Fatalf("回程不应依赖 cards：%+v", resp)
	}
	// 同字节幂等仍可用。
	again := restarted.Restore(RestoreRequest{JobID: delivered.JobID, ResponsePath: reply})
	if again.Status != StatusRestored {
		t.Fatalf("幂等回程不应依赖 cards：%+v", again)
	}
}
