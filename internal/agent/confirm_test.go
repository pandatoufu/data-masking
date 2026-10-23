package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pandatoufu/data-masking/internal/audit"
	"github.com/pandatoufu/data-masking/internal/config"
	"github.com/pandatoufu/data-masking/internal/core"
	"github.com/pandatoufu/data-masking/internal/workspace"
)

// maskThenConfirm 返回一个先触发高频、授权后正常返回的 fake core。
func maskThenConfirm() fakeMasker {
	calls := 0
	return fakeMasker{mask: func(fileName string, data []byte, opts core.MaskOptions, cfg *config.Config, al *audit.Log) (*core.MaskOutput, error) {
		calls++
		if calls == 1 {
			return nil, &core.PreviewRequiredError{Extra: map[string]int{"常备词B": 68}, Builtin: map[string]int{}}
		}
		return &core.MaskOutput{
			MaskedName:   "notes.masked.txt",
			MaskedBytes:  []byte("masked:" + string(data)),
			MappingName:  "notes.masked.mapping.json",
			MappingBytes: []byte(`{"v":1}`),
			Format:       "text",
		}, nil
	}}
}

func TestConfirmContinueMaskingDeliversAndReplayRejected(t *testing.T) {
	app, root := newTestApp(t, maskThenConfirm())
	seedOriginal(t, root, "notes.txt", []byte("13800138000"))
	project := t.TempDir()

	prep := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: project, Profile: "strict"})
	if prep.Status != StatusAwaitingFrequency {
		t.Fatalf("应先进入高频确认：%+v", prep)
	}
	ok := app.Confirm(ConfirmRequest{JobID: prep.JobID, ConfirmationID: prep.ConfirmationID, Decision: DecisionContinueMasking})
	if ok.Status != StatusDelivered {
		t.Fatalf("授权后应交付：%+v", ok)
	}
	got, err := os.ReadFile(ok.ProjectFile)
	if err != nil || string(got) != "masked:13800138000" {
		t.Fatalf("交付字节应为 core 结果：%q err=%v", got, err)
	}
	// 同类重放：不重复副作用，返回 invalid_state。
	replay := app.Confirm(ConfirmRequest{JobID: prep.JobID, ConfirmationID: prep.ConfirmationID, Decision: DecisionContinueMasking})
	if replay.Status != StatusFailed || replay.Code != string(CodeInvalidState) {
		t.Fatalf("重放应 invalid_state：%+v", replay)
	}
}

func TestConfirmCancelHasNoSideEffects(t *testing.T) {
	app, root := newTestApp(t, maskThenConfirm())
	seedOriginal(t, root, "notes.txt", []byte("hello"))
	project := t.TempDir()

	prep := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: project, Profile: "balanced"})
	res := app.Confirm(ConfirmRequest{JobID: prep.JobID, ConfirmationID: prep.ConfirmationID, Decision: DecisionCancel})
	if res.Status != StatusCancelled {
		t.Fatalf("应取消：%+v", res)
	}
	if res.ProjectFile != "" {
		t.Fatal("取消不得交付")
	}
	if _, err := os.Stat(filepath.Join(root, workspace.DirMasked, prep.JobID)); !os.IsNotExist(err) {
		t.Fatal("取消不得产出 masked")
	}
	if _, err := os.Stat(filepath.Join(project, ".masked-input", prep.JobID)); !os.IsNotExist(err) {
		t.Fatal("取消不得产出项目副本")
	}
	// 取消后不得再继续。
	after := app.Confirm(ConfirmRequest{JobID: prep.JobID, ConfirmationID: prep.ConfirmationID, Decision: DecisionContinueMasking})
	if after.Status != StatusFailed || after.Code != string(CodeInvalidState) {
		t.Fatalf("取消后应 invalid_state：%+v", after)
	}
}

func TestConfirmCredentialBinding(t *testing.T) {
	app, root := newTestApp(t, maskThenConfirm())
	seedOriginal(t, root, "notes.txt", []byte("hello"))
	project := t.TempDir()

	first := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: project, Profile: "balanced"})
	// 错误凭据。
	bad := app.Confirm(ConfirmRequest{JobID: first.JobID, ConfirmationID: "deadbeef", Decision: DecisionContinueMasking})
	if bad.Status != StatusFailed || bad.Code != string(CodeInvalidConfirm) {
		t.Fatalf("错误凭据应 invalid_confirmation：%+v", bad)
	}
	// 未知任务。
	unknown := app.Confirm(ConfirmRequest{JobID: "0123456789abcdef0123456789abcdef", ConfirmationID: first.ConfirmationID, Decision: DecisionContinueMasking})
	if unknown.Status != StatusFailed || unknown.Code != string(CodeUnknownJob) {
		t.Fatalf("未知任务应 unknown_job：%+v", unknown)
	}
	// 跨任务凭据：另一任务的确认不能批准本任务。
	project2 := t.TempDir()
	second := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: project2, Profile: "balanced"})
	cross := app.Confirm(ConfirmRequest{JobID: first.JobID, ConfirmationID: second.ConfirmationID, Decision: DecisionContinueMasking})
	if cross.Status != StatusFailed || cross.Code != string(CodeInvalidConfirm) {
		t.Fatalf("跨任务凭据应 invalid_confirmation：%+v", cross)
	}
	// 原任务仍可正常确认。
	ok := app.Confirm(ConfirmRequest{JobID: first.JobID, ConfirmationID: first.ConfirmationID, Decision: DecisionContinueMasking})
	if ok.Status != StatusDelivered {
		t.Fatalf("原任务确认应成功：%+v", ok)
	}
}

func TestConfirmStaleAfterSourceOrConfigChange(t *testing.T) {
	app, root := newTestApp(t, maskThenConfirm())
	path := seedOriginal(t, root, "notes.txt", []byte("hello"))
	project := t.TempDir()

	prep := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: project, Profile: "balanced"})
	if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	res := app.Confirm(ConfirmRequest{JobID: prep.JobID, ConfirmationID: prep.ConfirmationID, Decision: DecisionContinueMasking})
	if res.Status != StatusFailed || res.Code != string(CodeConfirmStale) {
		t.Fatalf("原件变化应 confirmation_stale：%+v", res)
	}
	if _, err := os.Stat(filepath.Join(project, ".masked-input", prep.JobID)); !os.IsNotExist(err) {
		t.Fatal("过期确认不得交付")
	}
	// 旧任务终止，旧凭据不可再用。
	again := app.Confirm(ConfirmRequest{JobID: prep.JobID, ConfirmationID: prep.ConfirmationID, Decision: DecisionContinueMasking})
	if again.Status != StatusFailed {
		t.Fatalf("终止后旧凭据应失败：%+v", again)
	}
}

func TestConfirmBusyDoesNotChangeStableState(t *testing.T) {
	app, root := newTestApp(t, maskThenConfirm())
	seedOriginal(t, root, "notes.txt", []byte("hello"))
	project := t.TempDir()
	prep := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: project, Profile: "balanced"})

	if err := app.Session.BeginTool(); err != nil {
		t.Fatal(err)
	}
	res := app.Confirm(ConfirmRequest{JobID: prep.JobID, ConfirmationID: prep.ConfirmationID, Decision: DecisionContinueMasking})
	app.Session.EndTool()
	if res.Status != StatusFailed || res.Code != string(CodeBusy) {
		t.Fatalf("并发调用应 busy：%+v", res)
	}
	records, err := app.Store.Load(prep.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if DeriveState(records) != StateAwaitingFreq {
		t.Fatalf("busy 不得改变稳定状态：%v", DeriveState(records))
	}
}
