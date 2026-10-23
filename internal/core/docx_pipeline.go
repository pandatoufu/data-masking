// 本文件实现 core 的 DOCX 双向流水线集成（D1/D7/D9）：
// 包门禁与清理、逐 unit 碰撞/高频、共享 Masker 回写、确定性打包、重开验证、
// version 2 mapping、跨格式命名与文字参考。
package core

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pandatoufu/data-masking/internal/audit"
	"github.com/pandatoufu/data-masking/internal/config"
	"github.com/pandatoufu/data-masking/internal/detector"
	"github.com/pandatoufu/data-masking/internal/docx"
	"github.com/pandatoufu/data-masking/internal/loader"
	"github.com/pandatoufu/data-masking/internal/mapping"
	"github.com/pandatoufu/data-masking/internal/masker"
)

const docxTextMismatchHint = "无法确证文本一致：还原后 DOCX 的清理后覆盖文字与 mapping 记录的文字参考不一致。该提示仅供参考，不阻断导出。"

func isDocxName(name string) bool {
	return loader.IsDocxName(name)
}

// textExt 返回纯文本实际扩展名（不含点，小写）；无普通扩展名回退 txt。
func textExt(name string) string {
	return mapping.TextExt(name)
}

func docxHistoryWarning() Warning {
	return Warning{Kind: "docx_history", Message: docxHistoryHint, MessageID: "warning.docx_history", Count: 1}
}

// maskDocx 执行 DOCX 去程：清理 → 逐 unit 预检 → 共享 Masker 回写 → 打包重开 → v2 mapping。
func maskDocx(fileName string, data []byte, opts MaskOptions, cfg *config.Config, auditLog *audit.Log) (*MaskOutput, error) {
	limit := opts.MaxSize
	if limit <= 0 {
		limit = cfg.MaxFileSize
	}
	doc, err := docx.Open(data, docx.DefaultLimits())
	if err != nil {
		return nil, err
	}
	summary, err := doc.Prepare()
	if err != nil {
		return nil, err
	}
	units := doc.Units()

	// 逐 unit 占位符碰撞预检（跨 run 可发现，已删除历史/批注不在 units 中）。
	for _, u := range units {
		if hits := mapping.GrammarHits(u.Text); len(hits) > 0 {
			return nil, &loader.HumanError{
				Reason: fmt.Sprintf("DOCX 原文已含占位符文法（如 %s，位于 %s 第 %d 段第 %d 片段），与脱敏占位符冲突",
					hits[0], u.Part, u.Paragraph, u.Segment),
				Advice:    "请先移除或替换原文中的占位符样式文本（形如 [PHONE_1]）后再试",
				MessageID: "collision.grammar_docx",
				Params: map[string]any{
					"hit": hits[0], "part": u.Part, "paragraph": u.Paragraph, "segment": u.Segment,
				},
			}
		}
	}

	enabled := applyRuleOverrides(cfg.RulesForProfile(opts.Profile), opts.RuleOverrides)

	var words []string
	if enabled[detector.RuleCustom] {
		words, err = mergeCustom(cfg.CustomWords, opts.ExtraCustom)
		if err != nil {
			return nil, err
		}
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
			// 逐 unit 计数之和，不跨硬边界拼接。
			c := 0
			for _, u := range units {
				c += strings.Count(u.Text, w)
			}
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
	for _, u := range units {
		edits := m.MaskLineEdits(u.Text)
		if len(edits) > 0 {
			if err := doc.Apply(u, toDocxEdits(edits)); err != nil {
				return nil, err
			}
		}
	}
	// 替换前基线：清理完成后的有序文字单元指纹。
	baseline := docx.TextSHA256(units)

	outBytes, err := doc.Bytes()
	if err != nil {
		return nil, err
	}
	// 输出文件大小门禁（解压预算已由 docx.Bytes 内部守住）。
	if err := loader.ValidateSize(int64(len(outBytes)), limit); err != nil {
		return nil, err
	}
	reopened, err := doc.ReopenValidated(outBytes)
	if err != nil {
		return nil, err
	}
	warnings := docxForwardResidual(reopened.Units(), reg, enabled)

	entries := make([]mapping.Entry, 0, len(m.Entries()))
	for _, e := range m.Entries() {
		entries = append(entries, mapping.Entry{
			Placeholder: e.Placeholder,
			Category:    mapping.PrefixByRule[e.Rule],
			Value:       e.Value,
		})
	}
	sha := sha256Hex(data)
	mp := mapping.NewDOCX(fileName, sha, baseline, entries)
	mappingBytes, err := mp.Marshal()
	if err != nil {
		return nil, err
	}

	out := &MaskOutput{
		MaskedName:   mapping.MaskedName(fileName),
		MaskedBytes:  outBytes,
		MappingName:  mapping.MappingName(fileName),
		MappingBytes: mappingBytes,
		Encoding:     "",
		Format:       "docx",
		Warnings:     warnings,
		Coverage:     m.Counts(),
		Cleanup:      summary,
	}
	out.AuditFailed = logMaskAuditDocx(auditLog, m.Entries(), reopened.Units(), sha)
	return out, nil
}

// restoreDocx 执行 DOCX 回程：清理 → 跨 run 查表 edits → 打包重开 → 参考/残留。
func restoreDocx(fileName string, data []byte, mp *mapping.Mapping, cfg *config.Config, auditLog *audit.Log) (*RestoreOutput, error) {
	limit := cfg.MaxFileSize
	doc, err := docx.Open(data, docx.DefaultLimits())
	if err != nil {
		return nil, err
	}
	if _, err := doc.Prepare(); err != nil {
		return nil, err
	}
	units := doc.Units()
	count := 0
	for _, u := range units {
		edits := docxRestoreEdits(u.Text, mp, &count)
		if len(edits) > 0 {
			if err := doc.Apply(u, edits); err != nil {
				return nil, err
			}
		}
	}
	outBytes, err := doc.Bytes()
	if err != nil {
		return nil, err
	}
	if err := loader.ValidateSize(int64(len(outBytes)), limit); err != nil {
		return nil, err
	}
	reopened, err := doc.ReopenValidated(outBytes)
	if err != nil {
		return nil, err
	}

	var warnings []Warning
	if w := docxReverseResidual(reopened.Units()); w != nil {
		warnings = append(warnings, *w)
	}
	warnings = append(warnings, docxConsistencyWarnings(mp, reopened.Units())...)
	if mp.SourceFormat == "docx" {
		warnings = append(warnings, docxHistoryWarning())
	}

	cleanup := doc.Cleanup()
	out := &RestoreOutput{
		Name:          mapping.RestoredDocxName(mp.SourceFile),
		Bytes:         outBytes,
		Encoding:      "",
		Format:        "docx",
		RestoredCount: count,
		Warnings:      warnings,
		Cleanup:       &cleanup,
	}
	out.AuditFailed = logRestoreAudit(auditLog, count, mp.SourceSHA256)
	return out, nil
}

// docxRestoreEdits 在 unit 拼接文字上查找占位符（大小写/全角 best-effort），
// 生成写回首节点所需的 edits；完整占位符可跨 runs。
func docxRestoreEdits(text string, mp *mapping.Mapping, count *int) []docx.Edit {
	locs := placeholderToken.FindAllStringIndex(text, -1)
	var edits []docx.Edit
	for _, loc := range locs {
		tok := text[loc[0]:loc[1]]
		if v, ok := mp.Lookup(tok); ok {
			edits = append(edits, docx.Edit{Start: loc[0], End: loc[1], Replacement: v})
			*count++
		}
	}
	return edits
}

// docxForwardResidual 在实际输出重开后的原生文字上复用规则做残留检测（非阻断）。
func docxForwardResidual(units []docx.TextUnit, reg *detector.Registry, enabled map[detector.RuleID]bool) []Warning {
	total := 0
	var first *Warning
	for _, u := range units {
		sel := detector.Resolve(reg.Detect(u.Text, enabled))
		for _, s := range sel {
			total++
			if first == nil {
				first = &Warning{
					Part:      u.Part,
					Paragraph: u.Paragraph,
					Segment:   u.Segment,
					Column:    runeColumn(u.Text, s.Start),
					Text:      s.Text,
				}
			}
		}
	}
	if first == nil {
		return nil
	}
	first.Kind = "residual_forward"
	first.Count = total
	first.Message = fmt.Sprintf("检测到输出中仍有 %d 处命中启用规则的内容（首处：%s 第 %d 段第 %d 片段第 %d 字符）。%s%s",
		total, first.Part, first.Paragraph, first.Segment, first.Column, forwardBoundary, forwardAdvice)
	first.MessageID = "warning.residual_forward_docx"
	first.Params = map[string]any{
		"count": total, "part": first.Part, "paragraph": first.Paragraph,
		"segment": first.Segment, "column": first.Column,
	}
	return []Warning{*first}
}

// docxReverseResidual 在实际输出重开后的原生文字上扫描占位符残留（非阻断）。
func docxReverseResidual(units []docx.TextUnit) *Warning {
	total := 0
	var first *Warning
	for _, u := range units {
		normalized := mapping.Normalize(u.Text)
		for _, h := range mapping.ResidualHits(u.Text) {
			total++
			if first == nil {
				col := 1
				if i := strings.Index(normalized, h); i >= 0 {
					col = utf8.RuneCountInString(normalized[:i]) + 1
				}
				first = &Warning{
					Part:      u.Part,
					Paragraph: u.Paragraph,
					Segment:   u.Segment,
					Column:    col,
					Text:      h,
				}
			}
		}
	}
	if first == nil {
		return nil
	}
	first.Kind = "residual_reverse"
	first.Count = total
	first.Message = fmt.Sprintf("%s（共 %d 处，首处：%s 第 %d 段第 %d 片段第 %d 字符 %s）。%s",
		reverseBoundary, total, first.Part, first.Paragraph, first.Segment, first.Column, first.Text, reverseAdvice)
	first.MessageID = "warning.residual_reverse_docx"
	first.Params = map[string]any{
		"count": total, "part": first.Part, "paragraph": first.Paragraph,
		"segment": first.Segment, "column": first.Column, "text": first.Text,
	}
	return first
}

// docxConsistencyWarnings 按四格组合给出文字一致性参考信号。
func docxConsistencyWarnings(mp *mapping.Mapping, units []docx.TextUnit) []Warning {
	if mp.Version == 2 {
		if got := docx.TextSHA256(units); got != mp.SourceTextSHA256 {
			return []Warning{{Kind: "text_mismatch", Message: docxTextMismatchHint, MessageID: "warning.text_mismatch", Count: 1}}
		}
		return nil
	}
	// version 1（纯文本来源）+ DOCX 输入：无 DOCX 文字基线。
	return []Warning{{Kind: "sha_mismatch", Message: crossFormatHint, MessageID: "warning.cross_format", Count: 1}}
}

func runeColumn(s string, byteStart int) int {
	if byteStart < 0 {
		byteStart = 0
	}
	if byteStart > len(s) {
		byteStart = len(s)
	}
	return utf8.RuneCountInString(s[:byteStart]) + 1
}

// logMaskAuditDocx 以重开后各 unit 中占位符出现次数之和记录审计计数，只写原有字段。
func logMaskAuditDocx(l *audit.Log, entries []masker.Entry, units []docx.TextUnit, sha string) bool {
	if l == nil {
		return false
	}
	counts := make(map[string]int, len(entries))
	for _, u := range units {
		for _, e := range entries {
			counts[e.Placeholder] += strings.Count(u.Text, e.Placeholder)
		}
	}
	ts := time.Now().Format(time.RFC3339)
	failed := false
	for _, e := range entries {
		rec := audit.Entry{
			RuleID:          string(e.Rule),
			Placeholder:     e.Placeholder,
			Count:           counts[e.Placeholder],
			Timestamp:       ts,
			FileFingerprint: sha,
		}
		if err := l.Append(rec); err != nil {
			failed = true
		}
	}
	return failed
}

func toDocxEdits(edits []masker.Edit) []docx.Edit {
	out := make([]docx.Edit, len(edits))
	for i, e := range edits {
		out[i] = docx.Edit{Start: e.Start, End: e.End, Replacement: e.Replacement}
	}
	return out
}
