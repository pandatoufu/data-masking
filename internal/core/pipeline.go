// Package core 装配去程/回程流水线：门禁 → 碰撞预检 → 单遍扫描 → 校验 → 导出。
package core

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pandatoufu/data-masking/internal/audit"
	"github.com/pandatoufu/data-masking/internal/config"
	"github.com/pandatoufu/data-masking/internal/detector"
	"github.com/pandatoufu/data-masking/internal/docx"
	"github.com/pandatoufu/data-masking/internal/i18n"
	"github.com/pandatoufu/data-masking/internal/loader"
	"github.com/pandatoufu/data-masking/internal/mapping"
	"github.com/pandatoufu/data-masking/internal/masker"
	"github.com/pandatoufu/data-masking/internal/pptx"
	"github.com/pandatoufu/data-masking/internal/verifier"
)

// Warning 是给用户看的非阻断告警。
// 纯文本使用 Line/Column；DOCX 使用 Part/Paragraph/Segment/Column（1-based Unicode 字符位置）。
type Warning struct {
	Kind      string         `json:"kind"`
	Message   string         `json:"message"`
	MessageID string         `json:"message_id,omitempty"`
	Params    map[string]any `json:"params,omitempty"`
	Line      int            `json:"line,omitempty"`
	Column    int            `json:"column,omitempty"`
	Part      string         `json:"part,omitempty"`
	Paragraph int            `json:"paragraph,omitempty"`
	Segment   int            `json:"segment,omitempty"`
	Text      string         `json:"text,omitempty"`
	Count     int            `json:"count"`
}

// 告警文案（内联能力边界与误发风险）。
const (
	forwardBoundary = "注意：该校验仅验证规则覆盖率，不构成语义级无泄露保证。"
	forwardAdvice   = "如仍需交付，可强制导出；建议检查输出中的残留内容。"
	reverseBoundary = "还原后仍存在占位符残留，疑似使用了与文本不配对的 mapping 文件。"
	reverseAdvice   = "本工具只做大小写/全角的 best-effort 匹配，无法找回被 LLM 改写或翻译的占位符；请确认 mapping 来源为本机上次下载的配对文件。"
	reverseSHAHint  = "无法确证一致：还原结果与 mapping 记录的原文文件指纹不一致。该提示仅供参考，不构成拿错 mapping 的断言，也不阻断导出。"
	crossFormatHint = "无法确证文本一致：本次回程为跨格式（缺少与输入同格式的文字基线）。该提示仅供参考，不构成拿错 mapping 的断言，也不阻断导出。"
	docxHistoryHint = "注意：本工具已清除批注并接受修订；回程只恢复映射文字，不能恢复已清除的批注或修订历史。"
)

// PreviewRequiredError 表示自定义词高频命中，需用户预览确认后才执行。
// 命中按来源分组：Extra 为本次词，Builtin 为常备词（cfg.CustomWords）。
type PreviewRequiredError struct {
	Extra   map[string]int `json:"extra"`
	Builtin map[string]int `json:"builtin"`
}

func (e *PreviewRequiredError) Error() string {
	parts := make([]string, 0, len(e.Extra)+len(e.Builtin))
	appendGroup := func(prefix string, hits map[string]int) {
		names := make([]string, 0, len(hits))
		for w := range hits {
			names = append(names, w)
		}
		sort.Strings(names)
		for _, w := range names {
			parts = append(parts, fmt.Sprintf("%s %q 命中 %d 次", prefix, w, hits[w]))
		}
	}
	appendGroup("本次词", e.Extra)
	appendGroup("常备词", e.Builtin)
	return "自定义词命中次数过高，需先预览确认：" + strings.Join(parts, "、")
}

// Presentation 返回 409 确认态的展示元数据。
func (e *PreviewRequiredError) Presentation() *i18n.Presentation {
	return &i18n.Presentation{
		MessageID: "preview.required",
		Params: map[string]any{
			"extra":   previewHitList(e.Extra),
			"builtin": previewHitList(e.Builtin),
		},
	}
}

func previewHitList(hits map[string]int) []i18n.Nested {
	if len(hits) == 0 {
		return []i18n.Nested{i18n.N("preview.empty_group", nil)}
	}
	names := make([]string, 0, len(hits))
	for w := range hits {
		names = append(names, w)
	}
	sort.Strings(names)
	out := make([]i18n.Nested, 0, len(names))
	for _, w := range names {
		out = append(out, i18n.N("preview.hit", map[string]any{"word": w, "count": hits[w]}))
	}
	return out
}

// MaskOptions 是去程参数。
// RuleOverrides 是仅 custom 档生效的规则覆盖（由 server 层门禁，core 直接应用）。
type MaskOptions struct {
	Profile           string
	RuleOverrides     map[detector.RuleID]bool
	ExtraCustom       []string
	ConfirmedCustom   []string
	MaxSize           int64
	HighFreqThreshold int
}

// MaskOutput 是去程产物。
type MaskOutput struct {
	MaskedName   string                  `json:"masked_name"`
	MaskedBytes  []byte                  `json:"-"`
	MappingName  string                  `json:"mapping_name"`
	MappingBytes []byte                  `json:"-"`
	Encoding     loader.Encoding         `json:"encoding"`
	Format       string                  `json:"format"`
	Warnings     []Warning               `json:"warnings"`
	Coverage     map[detector.RuleID]int `json:"coverage"`
	Cleanup      *docx.CleanupSummary    `json:"cleanup,omitempty"`
	PPTXCleanup  *pptx.CleanupSummary    `json:"pptx_cleanup,omitempty"`
	AuditFailed  bool                    `json:"audit_failed"`
}

// MaskFile 执行去程流水线。任一步失败返回错误且不产出任何文件。
func MaskFile(fileName string, data []byte, opts MaskOptions, cfg *config.Config, auditLog *audit.Log) (*MaskOutput, error) {
	if err := loader.ValidateFileName(fileName); err != nil {
		return nil, err
	}
	limit := opts.MaxSize
	if limit <= 0 {
		limit = cfg.MaxFileSize
	}
	if err := loader.ValidateSize(int64(len(data)), limit); err != nil {
		return nil, err
	}
	// DOCX/PPTX 在纯文本 NUL/编码判定之前按扩展名分流。
	if isDocxName(fileName) {
		return maskDocx(fileName, data, opts, cfg, auditLog)
	}
	if isPPTXName(fileName) {
		return maskPPTX(fileName, data, opts, cfg, auditLog)
	}
	doc, err := loader.Load(data, limit)
	if err != nil {
		return nil, err
	}
	text := loader.JoinLines(doc.Lines)

	// 占位符碰撞预检：以文法为准。
	if hits := mapping.GrammarHits(text); len(hits) > 0 {
		return nil, &loader.HumanError{
			Reason:    fmt.Sprintf("原文已含有占位符文法（如 %s），与脱敏占位符冲突", hits[0]),
			Advice:    "请先移除或替换原文中的占位符样式文本（形如 [PHONE_1]）后再试",
			MessageID: "collision.grammar_text",
			Params:    map[string]any{"hit": hits[0]},
		}
	}

	enabled := applyRuleOverrides(cfg.RulesForProfile(opts.Profile), opts.RuleOverrides)

	var words []string
	// 规则关 + 词提交：保持静默忽略（不合并、不校验、不参与，兼容现网）。
	if enabled[detector.RuleCustom] {
		words, err = mergeCustom(cfg.CustomWords, opts.ExtraCustom)
		if err != nil {
			return nil, err
		}
		// 合并后单次参与集（本次词 ∪ 常备词，去重后）上限，执行前校验。
		if len(words) > MaxWordCount {
			return nil, &loader.HumanError{
				Reason:    fmt.Sprintf("本次参与的自定义词（本次词与常备词去重后）共 %d 个，超过 %d 个上限", len(words), MaxWordCount),
				Advice:    "请删减本次词或常备词后重试",
				MessageID: "words.merge_too_many",
				Params:    map[string]any{"count": len(words), "max": MaxWordCount},
			}
		}

		threshold := opts.HighFreqThreshold
		if threshold <= 0 {
			threshold = cfg.HighFreqThreshold
		}
		confirmed := map[string]bool{}
		for _, w := range opts.ConfirmedCustom {
			confirmed[w] = true
		}
		baseSet := make(map[string]bool, len(cfg.CustomWords))
		for _, w := range cfg.CustomWords {
			baseSet[w] = true
		}
		needExtra := map[string]int{}
		needBuiltin := map[string]int{}
		for _, w := range words {
			c := strings.Count(text, w)
			if c <= threshold || confirmed[w] {
				continue
			}
			if baseSet[w] {
				needBuiltin[w] = c
			} else {
				needExtra[w] = c
			}
		}
		if len(needExtra) > 0 || len(needBuiltin) > 0 {
			return nil, &PreviewRequiredError{Extra: needExtra, Builtin: needBuiltin}
		}
	}

	reg := detector.NewRegistry(detector.NewFixed(), detector.NewCustom(words))
	m := masker.New(reg, enabled)

	var b strings.Builder
	for _, ln := range doc.Lines {
		b.WriteString(m.MaskLine(ln.Text))
		b.WriteString(ln.Term)
	}
	maskedText := b.String()

	// 正向残留校验（复用同一规则引擎扫输出）。
	var warnings []Warning
	if residual := verifier.ForwardScan(maskedText, reg, enabled); len(residual) > 0 {
		warnings = append(warnings, Warning{
			Kind:      "residual_forward",
			Message:   fmt.Sprintf("检测到输出中仍有 %d 处命中启用规则的内容（首处：第 %d 行）。%s%s", len(residual), residual[0].Line, forwardBoundary, forwardAdvice),
			MessageID: "warning.residual_forward",
			Params:    map[string]any{"count": len(residual), "line": residual[0].Line, "column": residual[0].Column},
			Line:      residual[0].Line,
			Column:    residual[0].Column,
			Text:      residual[0].Text,
			Count:     len(residual),
		})
	}

	entries := make([]mapping.Entry, 0, len(m.Entries()))
	for _, e := range m.Entries() {
		entries = append(entries, mapping.Entry{
			Placeholder: e.Placeholder,
			Category:    mapping.PrefixByRule[e.Rule],
			Value:       e.Value,
		})
	}
	sha := sha256Hex(data)
	mp := mapping.New(fileName, string(doc.Encoding), sha, entries)
	mappingBytes, err := mp.Marshal()
	if err != nil {
		return nil, err
	}
	maskedBytes, err := loader.Encode(maskedText, doc.Encoding)
	if err != nil {
		return nil, err
	}

	out := &MaskOutput{
		MaskedName:   mapping.MaskedName(fileName),
		MaskedBytes:  maskedBytes,
		MappingName:  mapping.MappingName(fileName),
		MappingBytes: mappingBytes,
		Encoding:     doc.Encoding,
		Format:       "text",
		Warnings:     warnings,
		Coverage:     m.Counts(),
	}
	out.AuditFailed = logMaskAudit(auditLog, m.Entries(), maskedText, sha)
	return out, nil
}

// RestoreOutput 是回程产物。
type RestoreOutput struct {
	Name          string               `json:"name"`
	Bytes         []byte               `json:"-"`
	Encoding      loader.Encoding      `json:"encoding"`
	Format        string               `json:"format"`
	RestoredCount int                  `json:"restored_count"`
	Warnings      []Warning            `json:"warnings"`
	Cleanup       *docx.CleanupSummary `json:"cleanup,omitempty"`
	PPTXCleanup   *pptx.CleanupSummary `json:"pptx_cleanup,omitempty"`
	AuditFailed   bool                 `json:"audit_failed"`
}

var placeholderToken = regexp.MustCompile(`[\[［][A-Za-zＡ-Ｚａ-ｚ]+_[0-9０-９]+[\]］]`)

// RestoreFile 执行回程：按上传格式分流到 DOCX 或纯文本适配。
func RestoreFile(fileName string, data []byte, mappingData []byte, cfg *config.Config, auditLog *audit.Log) (*RestoreOutput, error) {
	mp, err := mapping.Unmarshal(mappingData)
	if err != nil {
		var fe *mapping.FormatError
		if errors.As(err, &fe) {
			return nil, &loader.HumanError{
				Reason:    fe.Reason,
				Advice:    fe.Advice,
				MessageID: fe.MessageID,
				Params:    fe.Params,
			}
		}
		return nil, &loader.HumanError{
			Reason: err.Error(), Advice: "请选择本机上次脱敏后下载的 .mapping.json 文件",
			MessageID: "mapping.generic_invalid",
		}
	}
	if fileName == "" {
		fileName = "paste.txt"
	}
	if err := loader.ValidateFileName(fileName); err != nil {
		return nil, err
	}
	limit := cfg.MaxFileSize
	if err := loader.ValidateSize(int64(len(data)), limit); err != nil {
		return nil, err
	}
	if isDocxName(fileName) {
		if mp.Version == 3 {
			return nil, unsupportedCombination("PPTX")
		}
		return restoreDocx(fileName, data, mp, cfg, auditLog)
	}
	if isPPTXName(fileName) {
		if mp.Version != 3 || mp.SourceFormat != "pptx" {
			return nil, unsupportedCombination("PPTX")
		}
		return restorePPTX(fileName, data, mp, cfg, auditLog)
	}
	if mp.Version == 3 {
		return nil, unsupportedCombination("PPTX")
	}
	return restoreText(fileName, data, mp, cfg, auditLog)
}

// unsupportedCombination 返回本期路径 A 的格式组合拒绝：仅 PPTX + PPTX 同格式。
func unsupportedCombination(source string) error {
	return &loader.HumanError{
		Reason:    "不支持的格式组合：本期 PPTX 仅支持 PPTX 原件与 PPTX 回复的同格式闭环",
		Advice:    "请使用同一任务的 .pptx 脱敏副本与 PPTX 回复；不要用 PPTX mapping 还原文本/DOCX，也不要用旧文本/DOCX mapping 还原 PPTX",
		MessageID: "pptx.combination_unsupported",
		Params:    map[string]any{"source": source},
	}
}

// restoreText 是既有纯文本回程路径；对 version 2（DOCX 来源）mapping 使用文本命名与 UTF-8。
func restoreText(fileName string, data []byte, mp *mapping.Mapping, cfg *config.Config, auditLog *audit.Log) (*RestoreOutput, error) {
	limit := cfg.MaxFileSize
	doc, err := loader.Load(data, limit)
	if err != nil {
		return nil, err
	}

	count := 0
	var b strings.Builder
	for _, ln := range doc.Lines {
		b.WriteString(replacePlaceholders(ln.Text, mp, &count))
		b.WriteString(ln.Term)
	}
	restored := b.String()

	var warnings []Warning
	if residual := verifier.ReverseScan(restored); len(residual) > 0 {
		warnings = append(warnings, Warning{
			Kind:      "residual_reverse",
			Message:   fmt.Sprintf("%s（共 %d 处，首处：第 %d 行 %s）。%s", reverseBoundary, len(residual), residual[0].Line, residual[0].Text, reverseAdvice),
			MessageID: "warning.residual_reverse",
			Params:    map[string]any{"count": len(residual), "line": residual[0].Line, "column": residual[0].Column, "text": residual[0].Text},
			Line:      residual[0].Line,
			Column:    residual[0].Column,
			Text:      residual[0].Text,
			Count:     len(residual),
		})
	}

	if mp.Version == 2 {
		// DOCX 来源 mapping 还原为纯文本：跨格式，无同格式文字基线，仅非阻断提示。
		enc := loader.EncodingUTF8
		restoredBytes := []byte(restored)
		warnings = append(warnings, Warning{
			Kind:      "sha_mismatch",
			Message:   crossFormatHint,
			MessageID: "warning.cross_format",
			Count:     1,
		})
		warnings = append(warnings, docxHistoryWarning())
		out := &RestoreOutput{
			Name:          mapping.RestoredTextName(mp.SourceFile, textExt(fileName)),
			Bytes:         restoredBytes,
			Encoding:      enc,
			Format:        "text",
			RestoredCount: count,
			Warnings:      warnings,
		}
		out.AuditFailed = logRestoreAudit(auditLog, count, mp.SourceSHA256)
		return out, nil
	}

	enc := loader.Encoding(mp.SourceEncoding)
	if enc == "" {
		enc = loader.EncodingUTF8
	}
	restoredBytes, err := loader.Encode(restored, enc)
	if err != nil {
		return nil, err
	}

	// 回程 SHA 一致性参考信号：仅不等时提示「无法确证一致」，不阻断、不断言拿错。
	if mp.SourceSHA256 != "" && sha256Hex(restoredBytes) != mp.SourceSHA256 {
		warnings = append(warnings, Warning{
			Kind:      "sha_mismatch",
			Message:   reverseSHAHint,
			MessageID: "warning.sha_mismatch",
			Count:     1,
		})
	}

	out := &RestoreOutput{
		Name:          mapping.RestoredName(mp.SourceFile),
		Bytes:         restoredBytes,
		Encoding:      enc,
		Format:        "text",
		RestoredCount: count,
		Warnings:      warnings,
	}
	out.AuditFailed = logRestoreAudit(auditLog, count, mp.SourceSHA256)
	return out, nil
}

func replacePlaceholders(line string, mp *mapping.Mapping, count *int) string {
	return placeholderToken.ReplaceAllStringFunc(line, func(tok string) string {
		if v, ok := mp.Lookup(tok); ok {
			*count++
			return v
		}
		return tok
	})
}

// applyRuleOverrides 复制 profile 基础开关并叠加覆盖，绝不改动 cfg 内部 map。
func applyRuleOverrides(base, overrides map[detector.RuleID]bool) map[detector.RuleID]bool {
	out := make(map[detector.RuleID]bool, len(base)+len(overrides))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overrides {
		out[k] = v
	}
	return out
}

func mergeCustom(base, extra []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	appendWord := func(w string) error {
		if seen[w] {
			return nil
		}
		if err := detector.ValidateWord(w); err != nil {
			return err
		}
		seen[w] = true
		out = append(out, w)
		return nil
	}
	for _, w := range base {
		if err := appendWord(w); err != nil {
			return nil, err
		}
	}
	for _, w := range extra {
		if err := appendWord(w); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func logMaskAudit(l *audit.Log, entries []masker.Entry, maskedText, sha string) bool {
	if l == nil {
		return false
	}
	ts := time.Now().Format(time.RFC3339)
	failed := false
	for _, e := range entries {
		rec := audit.Entry{
			RuleID:          string(e.Rule),
			Placeholder:     e.Placeholder,
			Count:           strings.Count(maskedText, e.Placeholder),
			Timestamp:       ts,
			FileFingerprint: sha,
		}
		if err := l.Append(rec); err != nil {
			failed = true
		}
	}
	return failed
}

func logRestoreAudit(l *audit.Log, count int, fingerprint string) bool {
	if l == nil {
		return false
	}
	return l.Append(audit.Entry{
		Count:           count,
		Timestamp:       time.Now().Format(time.RFC3339),
		FileFingerprint: fingerprint,
	}) != nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
