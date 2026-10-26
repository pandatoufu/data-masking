package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pandatoufu/data-masking/internal/audit"
	"github.com/pandatoufu/data-masking/internal/config"
	"github.com/pandatoufu/data-masking/internal/core"
	"github.com/pandatoufu/data-masking/internal/docx"
)

// SECRET 常量代表任何不得出现在 MCP 输出中的敏感内容。
const secret = "SECRET-DO-NOT-LEAK"

// 公开 DTO 字段集合固定：内部新增字段不得自动透传。
func TestResponseFieldWhitelist(t *testing.T) {
	allowed := map[string]bool{
		"schema_version": true, "status": true, "job_id": true, "code": true, "message": true,
		"profile": true, "project_file": true, "confirmation_id": true, "confirmation_kind": true,
		"allowed_decisions": true, "word_count": true, "hit_counts": true, "threshold": true,
		"residual_count": true, "restored_count": true, "warnings": true, "audit_failed": true,
	}
	resp := Response{
		SchemaVersion: SchemaVersion,
		Status:        StatusDelivered,
		JobID:         "0123456789abcdef0123456789abcdef",
		Profile:       "balanced",
		ProjectFile:   "/tmp/project/.masked-input/x/input.masked.txt",
		Message:       msgDelivered,
		Warnings:      []Warning{{Kind: "residual_forward", Message: "fixed", Count: 2}},
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for key := range decoded {
		if !allowed[key] {
			t.Fatalf("未审定字段 %q 不得出现在公开响应", key)
		}
	}
	// warnings 只允许 kind/count/message。
	warningRaw, _ := json.Marshal(decoded["warnings"].([]any)[0])
	var wmap map[string]any
	_ = json.Unmarshal(warningRaw, &wmap)
	for key := range wmap {
		if key != "kind" && key != "count" && key != "message" {
			t.Fatalf("告警不得含 %q", key)
		}
	}
}

// 核心自由错误字符串、告警 Text/Params 与私有路径不得出现在响应中。
func TestCoreFreeTextNotLeaked(t *testing.T) {
	masker := fakeMasker{mask: func(string, []byte, core.MaskOptions, *config.Config, *audit.Log) (*core.MaskOutput, error) {
		return nil, errors.New("core failure: " + secret + " /home/alice/private/original/notes.txt")
	}}
	app, root := newTestApp(t, masker)
	seedOriginal(t, root, "notes.txt", []byte("hello"))
	resp := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: t.TempDir(), Profile: "balanced"})
	assertNoLeak(t, resp)
	if resp.Status != StatusFailed || resp.Code != string(CodeProcessingFailed) {
		t.Fatalf("应映射固定 processing_failed：%+v", resp)
	}
}

// 告警 Text/Params/Part 不透传，只保留 kind/count 与固定文案。
func TestWarningPrivateFieldsDropped(t *testing.T) {
	masker := fakeMasker{mask: func(string, []byte, core.MaskOptions, *config.Config, *audit.Log) (*core.MaskOutput, error) {
		return &core.MaskOutput{
			MaskedName:   "notes.masked.txt",
			MaskedBytes:  []byte("m"),
			MappingName:  "notes.masked.mapping.json",
			MappingBytes: []byte("{}"),
			Format:       "text",
			Warnings: []core.Warning{{
				Kind:    "residual_forward",
				Message: secret,
				Text:    secret,
				Params:  map[string]any{"text": secret},
				Part:    "word/document.xml",
				Count:   2,
			}},
		}, nil
	}}
	app, root := newTestApp(t, masker)
	seedOriginal(t, root, "notes.txt", []byte("hello"))
	project := t.TempDir()
	prep := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: project, Profile: "balanced"})
	if prep.Status != StatusAwaitingResidual {
		t.Fatalf("正向残留应先确认：%+v", prep)
	}
	assertNoLeak(t, prep)
	resp := app.Confirm(ConfirmRequest{JobID: prep.JobID, ConfirmationID: prep.ConfirmationID, Decision: DecisionDeliverResidual})
	if resp.Status != StatusDelivered {
		t.Fatalf("应交付：%+v", resp)
	}
	found := false
	for _, w := range resp.Warnings {
		if w.Kind == "residual_forward" {
			found = true
			if w.Count != 2 {
				t.Fatalf("应保留 count：%+v", w)
			}
		}
	}
	if !found {
		t.Fatalf("应保留 residual_forward 匿名告警：%+v", resp.Warnings)
	}
	assertNoLeak(t, resp)
}

// 未知告警 kind 按 failed/processing_failed 失败，不新增 kind 或透传原文。
func TestUnknownWarningKindFails(t *testing.T) {
	masker := fakeMasker{mask: func(string, []byte, core.MaskOptions, *config.Config, *audit.Log) (*core.MaskOutput, error) {
		return &core.MaskOutput{
			MaskedName:   "notes.masked.txt",
			MaskedBytes:  []byte("m"),
			MappingName:  "notes.masked.mapping.json",
			MappingBytes: []byte("{}"),
			Format:       "text",
			Warnings:     []core.Warning{{Kind: "totally_new_kind", Message: secret, Count: 1}},
		}, nil
	}}
	app, root := newTestApp(t, masker)
	seedOriginal(t, root, "notes.txt", []byte("hello"))
	resp := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: t.TempDir(), Profile: "balanced"})
	if resp.Status != StatusFailed || resp.Code != string(CodeProcessingFailed) {
		t.Fatalf("未知告警应 failed/processing_failed：%+v", resp)
	}
	assertNoLeak(t, resp)
}

// 失败代码集合固定：未知错误映射 internal_error，不回显原异常。
func TestFailureCodeSetFixed(t *testing.T) {
	resp := FailureResponse(errors.New("boom: " + secret))
	if resp.Status != StatusFailed || resp.Code != string(CodeInternalError) {
		t.Fatalf("未知错误应 internal_error：%+v", resp)
	}
	assertNoLeak(t, resp)

	valid := map[string]bool{
		"invalid_request": true, "invalid_source": true, "invalid_project": true,
		"invalid_profile": true, "invalid_config": true, "unsupported_input": true,
		"input_too_large": true, "processing_failed": true, "workspace_unavailable": true,
		"publish_failed": true, "unknown_job": true, "invalid_confirmation": true,
		"confirmation_stale": true, "invalid_state": true, "busy": true,
		"interrupted": true, "internal_error": true,
	}
	for code := range fixedMessages {
		if !valid[string(code)] {
			t.Fatalf("失败代码 %q 不在固定集合内", code)
		}
	}
	if len(fixedMessages) != len(valid) {
		t.Fatalf("失败代码数量应固定：%d vs %d", len(fixedMessages), len(valid))
	}
	for code := range valid {
		if MessageFor(Code(code)) == "" {
			t.Fatalf("代码 %q 应有固定消息", code)
		}
	}
}

// 告警 kind 集合固定（含独立 text_mismatch，不与 sha_mismatch 归并）。
func TestWarningKindSetFixed(t *testing.T) {
	want := []string{
		"residual_forward", "residual_reverse", "sha_mismatch", "text_mismatch", "cross_format",
		"docx_history", "docx_cleanup", "pptx_history", "pptx_cleanup", "pptx_text_mismatch",
		"unchecked_content", "audit_failed", "cleanup_warning",
	}
	if len(allowedWarningKinds) != len(want) {
		t.Fatalf("告警 kind 数量应固定：%d", len(allowedWarningKinds))
	}
	if len(warningMessages) != len(want) {
		t.Fatalf("告警固定文案数量应与 kind 全集一致：%d", len(warningMessages))
	}
	for _, k := range want {
		if !allowedWarningKinds[k] || warningMessages[k] == "" {
			t.Fatalf("kind %q 应有固定文案", k)
		}
	}
}

// DOCX 告警映射为固定摘要，不暴露部件名或文字。
func TestDocxWarningsAreAnonymous(t *testing.T) {
	out := &core.MaskOutput{
		Format: "docx",
		Cleanup: &docx.CleanupSummary{
			CommentsDeleted:   2,
			RevisionsAccepted: 1,
			PartsRemoved:      1,
			ResourcesRemoved:  0,
		},
		AuditFailed: true,
	}
	warnings := docxWarnings(out.Cleanup, true)
	warnings = append(warnings, auditWarning(out.AuditFailed)...)
	kinds := map[string]int{}
	for _, w := range warnings {
		kinds[w.Kind] = w.Count
		if strings.Contains(w.Message, "word/") || strings.Contains(w.Message, secret) {
			t.Fatalf("告警不得含部件名或原文：%q", w.Message)
		}
	}
	if kinds["docx_cleanup"] != 4 || kinds["unchecked_content"] != 1 || kinds["audit_failed"] != 1 {
		t.Fatalf("DOCX 摘要计数不符：%+v", kinds)
	}
	if len(docxWarnings(nil, false)) != 0 {
		t.Fatal("非 DOCX 不应生成 DOCX 摘要")
	}
}

func assertNoLeak(t *testing.T, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, banned := range []string{secret, "/home/alice", "word/document.xml", "13800138000"} {
		if strings.Contains(text, banned) {
			t.Fatalf("输出泄露 %q：%s", banned, text)
		}
	}
}

// 审计失败保留为非阻断告警，且不因重试伪装成功。
func TestAuditFailedPreservedOnRetry(t *testing.T) {
	app, root := newTestApp(t, fakeMasker{})
	seedOriginal(t, root, "notes.txt", []byte("13800138000"))
	project := t.TempDir()
	resp := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: project, Profile: "balanced"})
	if resp.Status != StatusDelivered {
		t.Fatalf("应交付：%+v", resp)
	}
	// 审计路径与工作区业务文件隔离：.agent-state 与业务目录不含审计文件。
	for _, dir := range []string{"masked", "mapping", "pending", "restored", "original", "cards"} {
		entries, _ := os.ReadDir(filepath.Join(root, dir))
		for _, e := range entries {
			if strings.Contains(e.Name(), "audit") {
				t.Fatalf("审计文件不得混入业务目录 %s：%s", dir, e.Name())
			}
		}
	}
}
