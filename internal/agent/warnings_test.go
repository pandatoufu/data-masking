package agent

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/pandatoufu/data-masking/internal/audit"
	"github.com/pandatoufu/data-masking/internal/config"
	"github.com/pandatoufu/data-masking/internal/core"
)

// 正向残留、回程残留、SHA 不一致与跨格式必须分别映射，不互相混淆。
func TestWarningKindMappingNotConfused(t *testing.T) {
	cases := []struct {
		name string
		in   core.Warning
		want string
	}{
		{"forward", core.Warning{Kind: "residual_forward", MessageID: "warning.residual_forward", Count: 3}, "residual_forward"},
		{"reverse", core.Warning{Kind: "residual_reverse", MessageID: "warning.residual_reverse", Count: 2}, "residual_reverse"},
		{"reverse-docx", core.Warning{Kind: "residual_reverse", MessageID: "warning.residual_reverse_docx", Count: 1}, "residual_reverse"},
		{"sha", core.Warning{Kind: "sha_mismatch", MessageID: "warning.sha_mismatch", Count: 1}, "sha_mismatch"},
		{"cross-format", core.Warning{Kind: "sha_mismatch", MessageID: "warning.cross_format", Count: 1}, "cross_format"},
		{"text-mismatch", core.Warning{Kind: "text_mismatch", MessageID: "warning.text_mismatch", Count: 1}, "text_mismatch"},
		// 核心只给 kind 或只给 MessageID 时都必须保留为独立 text_mismatch，不归并为 sha_mismatch。
		{"text-mismatch-kind-only", core.Warning{Kind: "text_mismatch", Count: 1}, "text_mismatch"},
		{"text-mismatch-messageid-only", core.Warning{MessageID: "warning.text_mismatch", Count: 1}, "text_mismatch"},
		{"docx-history", core.Warning{Kind: "docx_history", MessageID: "warning.docx_history", Count: 1}, "docx_history"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := publicWarnings([]core.Warning{c.in})
			if err != nil {
				t.Fatalf("映射失败：%v", err)
			}
			if len(out) != 1 || out[0].Kind != c.want {
				t.Fatalf("%s 应映射为 %s，got %+v", c.in.Kind, c.want, out)
			}
			if out[0].Message == "" || out[0].Count != c.in.Count {
				t.Fatalf("应保留固定文案与计数：%+v", out[0])
			}
		})
	}
}

// text_mismatch 使用规范固定文案，且不与 sha_mismatch/cross_format 混淆。
func TestTextMismatchFixedMessage(t *testing.T) {
	want := "还原后的 DOCX 文字指纹与原件不一致；仅供参考，不代表还原失败或 mapping 配错。"
	out, err := publicWarnings([]core.Warning{{Kind: "text_mismatch", MessageID: "warning.text_mismatch", Count: 4}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Kind != "text_mismatch" || out[0].Message != want || out[0].Count != 4 {
		t.Fatalf("text_mismatch 应使用规范固定文案与计数：%+v", out)
	}
	if sha := warningMessages["sha_mismatch"]; sha == out[0].Message {
		t.Fatal("text_mismatch 不得沿用 sha_mismatch 文案")
	}
}

// 核心自由 Message/Text/Params/部件名不得随 text_mismatch 透传。
func TestTextMismatchDropsPrivateFields(t *testing.T) {
	in := core.Warning{
		Kind:      "text_mismatch",
		MessageID: "warning.text_mismatch",
		Message:   secret,
		Text:      secret,
		Params:    map[string]any{"text": secret, "part": "word/document.xml"},
		Part:      "word/document.xml",
		Paragraph: 3,
		Count:     2,
	}
	out, err := publicWarnings([]core.Warning{in})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Kind != "text_mismatch" {
		t.Fatalf("应映射 text_mismatch：%+v", out)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{secret, "word/document.xml", "part", "params", "paragraph"} {
		if strings.Contains(string(raw), banned) {
			t.Fatalf("text_mismatch 不得透传 %q：%s", banned, raw)
		}
	}
}

// 审计失败与清理警告是独立、非阻断告警。
func TestAuditAndCleanupWarningsIndependent(t *testing.T) {
	if got := auditWarning(false); got != nil {
		t.Fatalf("审计成功不应告警：%+v", got)
	}
	if got := auditWarning(true); len(got) != 1 || got[0].Kind != "audit_failed" {
		t.Fatalf("审计失败应告警：%+v", got)
	}
	if got := cleanupWarning(true); len(got) != 1 || got[0].Kind != "cleanup_warning" {
		t.Fatalf("清理失败应告警：%+v", got)
	}
	if got := cleanupWarning(false); got != nil {
		t.Fatalf("清理成功不应告警：%+v", got)
	}
}

// 失败标志重试保留：审计失败记录在任务事件中，不因后续重试变为成功。
func TestAuditFailedPersistsAcrossRetry(t *testing.T) {
	masker := fakeMasker{mask: func(fileName string, data []byte, opts core.MaskOptions, cfg *config.Config, al *audit.Log) (*core.MaskOutput, error) {
		return &core.MaskOutput{
			MaskedName:   "notes.masked.txt",
			MaskedBytes:  []byte("m"),
			MappingName:  "notes.masked.mapping.json",
			MappingBytes: []byte("{}"),
			Format:       "text",
			AuditFailed:  true,
		}, nil
	}}
	app, root := newTestApp(t, masker)
	seedOriginal(t, root, "notes.txt", []byte("hello"))
	resp := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: t.TempDir(), Profile: "balanced"})
	if resp.Status != StatusDelivered || !resp.AuditFailed {
		t.Fatalf("应交付并保留 audit_failed：%+v", resp)
	}
	records, err := app.Store.Load(resp.JobID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range records {
		if r.Type == RecMaskedSaved && r.AuditFailed {
			found = true
		}
	}
	if !found {
		t.Fatalf("任务记录应保留审计失败标志：%+v", records)
	}
	if !hasWarning(resp.Warnings, "audit_failed") {
		t.Fatalf("应输出 audit_failed 告警：%+v", resp.Warnings)
	}
}

func hasWarning(list []Warning, kind string) bool {
	for _, w := range list {
		if w.Kind == kind {
			return true
		}
	}
	return false
}

// 映射失败（未知告警）应为 processing_failed，而不是透传。
func TestUnknownWarningMapsToProcessingFailed(t *testing.T) {
	_, err := publicWarnings([]core.Warning{{Kind: "mystery", Count: 1}})
	if err == nil {
		t.Fatal("未知告警应失败")
	}
	var ae *Error
	if !errors.As(err, &ae) || ae.Code != CodeProcessingFailed {
		t.Fatalf("应 processing_failed：%v", err)
	}
}
