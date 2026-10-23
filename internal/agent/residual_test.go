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

// residualMasker 返回带正向残留告警的脱敏结果（模拟规则复核仍有命中）。
func residualMasker() fakeMasker {
	return fakeMasker{mask: func(fileName string, data []byte, opts core.MaskOptions, cfg *config.Config, al *audit.Log) (*core.MaskOutput, error) {
		return &core.MaskOutput{
			MaskedName:   "notes.masked.txt",
			MaskedBytes:  []byte("masked"),
			MappingName:  "notes.masked.mapping.json",
			MappingBytes: []byte(`{"v":1}`),
			Format:       "text",
			Warnings: []core.Warning{{
				Kind:    "residual_forward",
				Message: "SECRET-RESIDUAL-TEXT",
				Text:    "SECRET-RESIDUAL-TEXT",
				Params:  map[string]any{"text": "SECRET-RESIDUAL-TEXT"},
				Count:   3,
			}},
		}, nil
	}}
}

func TestResidualGatePausesDeliveryAndKeepsPrivatePair(t *testing.T) {
	app, root := newTestApp(t, residualMasker())
	seedOriginal(t, root, "notes.txt", []byte("hello"))
	project := t.TempDir()

	prep := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: project, Profile: "balanced"})
	if prep.Status != StatusAwaitingResidual {
		t.Fatalf("正向残留应单独确认：%+v", prep)
	}
	if prep.ResidualCount != 3 || prep.ConfirmationKind != KindResidual {
		t.Fatalf("应披露残留数量与独立种类：%+v", prep)
	}
	if len(prep.AllowedDecisions) != 2 || prep.AllowedDecisions[0] != DecisionDeliverResidual {
		t.Fatalf("允许动作应仅 deliver_with_residual_risk/cancel：%+v", prep.AllowedDecisions)
	}
	if prep.Message == renderFrequencyMessage(1, []int{1}, 1) {
		t.Fatal("残留文案不得复用高频文案")
	}
	if prep.ProjectFile != "" {
		t.Fatal("残留确认前不得返回项目文件")
	}
	if _, err := os.Stat(filepath.Join(root, workspace.DirMasked, prep.JobID, "notes.masked.txt")); err != nil {
		t.Fatalf("私有配对应先保存：%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, workspace.DirMapping, prep.JobID, "notes.masked.mapping.json")); err != nil {
		t.Fatalf("私有 mapping 应先保存：%v", err)
	}
	if _, err := os.Stat(filepath.Join(project, ".masked-input", prep.JobID)); !os.IsNotExist(err) {
		t.Fatal("残留确认前不得交付项目")
	}
}

func TestResidualGateRejectsFrequencyDecision(t *testing.T) {
	app, root := newTestApp(t, residualMasker())
	seedOriginal(t, root, "notes.txt", []byte("hello"))
	project := t.TempDir()
	prep := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: project, Profile: "balanced"})

	res := app.Confirm(ConfirmRequest{JobID: prep.JobID, ConfirmationID: prep.ConfirmationID, Decision: DecisionContinueMasking})
	if res.Status != StatusFailed || res.Code != string(CodeInvalidConfirm) {
		t.Fatalf("高频动作不得批准残留：%+v", res)
	}
	if _, err := os.Stat(filepath.Join(project, ".masked-input", prep.JobID)); !os.IsNotExist(err) {
		t.Fatal("误用授权不得交付")
	}
	// 原任务仍可正常接受风险交付。
	ok := app.Confirm(ConfirmRequest{JobID: prep.JobID, ConfirmationID: prep.ConfirmationID, Decision: DecisionDeliverResidual})
	if ok.Status != StatusDelivered {
		t.Fatalf("应可接受风险交付：%+v", ok)
	}
	if len(ok.Warnings) != 1 || ok.Warnings[0].Kind != "residual_forward" || ok.Warnings[0].Count != 3 {
		t.Fatalf("交付应带匿名残留告警：%+v", ok.Warnings)
	}
	if ok.Warnings[0].Message == "" || ok.Warnings[0].Message == "SECRET-RESIDUAL-TEXT" {
		t.Fatal("告警必须使用固定文案，不透传核心文本")
	}
}

func TestResidualCancelDoesNotDeliver(t *testing.T) {
	app, root := newTestApp(t, residualMasker())
	seedOriginal(t, root, "notes.txt", []byte("hello"))
	project := t.TempDir()
	prep := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: project, Profile: "balanced"})

	res := app.Confirm(ConfirmRequest{JobID: prep.JobID, ConfirmationID: prep.ConfirmationID, Decision: DecisionCancel})
	if res.Status != StatusCancelled {
		t.Fatalf("应取消：%+v", res)
	}
	if _, err := os.Stat(filepath.Join(project, ".masked-input", prep.JobID)); !os.IsNotExist(err) {
		t.Fatal("取消交付不得复制项目")
	}
	if _, err := os.Stat(filepath.Join(root, workspace.DirMasked, prep.JobID, "notes.masked.txt")); err != nil {
		t.Fatal("取消不删除已保存的私有配对")
	}
}

// 项目复制失败：保留完整私有配对，返回 publish_failed 且无 project_file。
func TestProjectPublishFailureKeepsPrivatePair(t *testing.T) {
	app, root := newTestApp(t, fakeMasker{})
	seedOriginal(t, root, "notes.txt", []byte("hello"))
	project := t.TempDir()
	// .masked-input 被普通文件占位 → 无法创建交付目录。
	if err := os.WriteFile(filepath.Join(project, ".masked-input"), []byte("blocker"), 0o600); err != nil {
		t.Fatal(err)
	}
	resp := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: project, Profile: "balanced"})
	if resp.Status != StatusFailed || resp.Code != string(CodePublishFailed) {
		t.Fatalf("项目发布失败应 publish_failed：%+v", resp)
	}
	if resp.ProjectFile != "" {
		t.Fatal("失败不得返回 project_file")
	}
	if resp.JobID != "" {
		t.Fatalf("失败信封不应泄露 job 细节字段：%+v", resp)
	}
	// 私有配对仍完整保留（可用 store 记录核对，不从响应取 job）。
	jobs, err := app.Store.Jobs()
	if err != nil || len(jobs) != 1 {
		t.Fatalf("私有任务记录应保留：%v %v", jobs, err)
	}
	records, err := app.Store.Load(jobs[0])
	if err != nil {
		t.Fatal(err)
	}
	if !hasType(records, RecMappingSaved) || !hasType(records, RecProjectIntent) {
		t.Fatalf("应保留完整私有配对与失败意图：%+v", records)
	}
	if hasType(records, RecDelivered) {
		t.Fatal("项目复制失败不得记录 delivered")
	}
}

// 任务目录已存在（非全新）时拒绝，不覆盖已有文件。
func TestProjectJobDirMustBeNew(t *testing.T) {
	app, root := newTestApp(t, fakeMasker{})
	seedOriginal(t, root, "notes.txt", []byte("hello"))
	project := t.TempDir()
	jobDir := filepath.Join(project, ".masked-input", "existing")
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jobDir, "input.masked.txt"), []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	resp := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: project, Profile: "balanced"})
	if resp.Status != StatusDelivered {
		t.Fatalf("新 job 应交付到自己的新目录：%+v", resp)
	}
	if got, _ := os.ReadFile(filepath.Join(jobDir, "input.masked.txt")); string(got) != "other" {
		t.Fatalf("已有任务目录内容不得被覆盖：%q", got)
	}
}
