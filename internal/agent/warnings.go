package agent

import (
	"github.com/pandatoufu/data-masking/internal/core"
	"github.com/pandatoufu/data-masking/internal/docx"
	"github.com/pandatoufu/data-masking/internal/pptx"
)

// allowedWarningKinds 是公开 warnings 的 kind 全集，与冻结 specs/design 的白名单一致。
// text_mismatch 是独立公开 kind，绝不与 sha_mismatch 归并。
var allowedWarningKinds = map[string]bool{
	"residual_forward":   true,
	"residual_reverse":   true,
	"sha_mismatch":       true,
	"text_mismatch":      true,
	"cross_format":       true,
	"docx_history":       true,
	"docx_cleanup":       true,
	"pptx_history":       true,
	"pptx_cleanup":       true,
	"pptx_text_mismatch": true,
	"unchecked_content":  true,
	"audit_failed":       true,
	"cleanup_warning":    true,
}

// coreWarningByMessageID 按核心 MessageID 精确选择公开 kind。
// 三类一致性参考信号语义不同，不得互换或归并：
//   - warning.text_mismatch：DOCX 文字指纹与 mapping 的 SourceTextSHA256 不同 → text_mismatch；
//   - warning.sha_mismatch：纯文本还原字节与原件的 SHA 不同 → sha_mismatch；
//   - warning.cross_format：跨格式缺少同格式文字基线（核心内部 kind 可能是 sha_mismatch）→ cross_format。
//
// 适配层查固定表重建 message，绝不透传核心 Warning.Message/Text/Params。
var coreWarningByMessageID = map[string]string{
	"warning.cross_format":          "cross_format",
	"warning.sha_mismatch":          "sha_mismatch",
	"warning.text_mismatch":         "text_mismatch",
	"warning.pptx_text_mismatch":    "pptx_text_mismatch",
	"warning.residual_reverse_docx": "residual_reverse",
	"warning.residual_reverse_pptx": "residual_reverse",
	"warning.residual_reverse":      "residual_reverse",
	"warning.residual_forward":      "residual_forward",
	"warning.residual_forward_pptx": "residual_forward",
	"warning.docx_history":          "docx_history",
	"warning.pptx_history":          "pptx_history",
}

// warningMessages 是各告警 kind 的固定文案（不含文本片段、部件名或私有路径）。
var warningMessages = map[string]string{
	"residual_forward":   "脱敏输出仍命中已启用规则；该检测仅验证规则覆盖率，不构成语义无泄露保证。",
	"residual_reverse":   "还原后仍存在占位符残留，无法找回被改写或翻译的占位符；请确认 mapping 为本机配对文件。",
	"sha_mismatch":       "还原结果与 mapping 记录的原文指纹不一致；仅供参考，不构成拿错 mapping 的断言。",
	"text_mismatch":      "还原后的 DOCX 文字指纹与原件不一致；仅供参考，不代表还原失败或 mapping 配错。",
	"cross_format":       "本次回程为跨格式，缺少同格式文字基线，无法确证文本一致。",
	"docx_history":       "已清除批注并接受修订；回程只恢复映射文字，不能恢复批注或修订历史。",
	"docx_cleanup":       "DOCX 清理按既有范围执行；未覆盖内容不在此保证内。",
	"pptx_history":       "回程只恢复映射文字，不能恢复已清除的 PPTX 批注或原始排版。",
	"pptx_cleanup":       "PPTX 批注及相关人员信息按支持范围清除；未检查内容不在此保证内。",
	"pptx_text_mismatch": "还原后的 PPTX 文字指纹与原件不一致；仅供参考，不代表还原失败或 mapping 配错。",
	"unchecked_content":  "存在未检查的内容类型（如图片或属性），本工具不保证其不含敏感信息。",
	"audit_failed":       "审计写入失败；主结果仍已保存，未伪装审计成功。",
	"cleanup_warning":    "发布已完成但临时文件清理失败，请检查本地目录。",
}

// WarningCatalog 返回公开告警 kind→固定文案的副本（供 MCP 输出 schema/枚举同步使用）。
func WarningCatalog() map[string]string {
	out := make(map[string]string, len(warningMessages))
	for k, v := range warningMessages {
		out[k] = v
	}
	return out
}

// publicWarnings 把核心告警映射到公开白名单；出现未审定 kind 时整体失败，不新增或透传告警。
func publicWarnings(in []core.Warning) ([]Warning, error) {
	out := make([]Warning, 0, len(in))
	for _, w := range in {
		kind := ""
		if mapped, ok := coreWarningByMessageID[w.MessageID]; ok {
			kind = mapped
		} else {
			// 无 MessageID 时按核心 kind 直接取白名单；text_mismatch 同样保留为独立 kind。
			kind = w.Kind
		}
		if !allowedWarningKinds[kind] {
			return nil, NewError(CodeProcessingFailed, MessageFor(CodeProcessingFailed))
		}
		count := w.Count
		if count <= 0 && kind != "pptx_cleanup" {
			count = 1
		}
		out = append(out, Warning{Kind: kind, Message: warningMessages[kind], Count: count})
	}
	return out, nil
}

// publicWarningsFromRecord 从任务记录中的匿名告警重建固定文案：
// 只按审定 kind 查固定表，绝不依赖记录里的自由字符串；未知 kind 按 failed/processing_failed。
func publicWarningsFromRecord(in []Warning) ([]Warning, error) {
	out := make([]Warning, 0, len(in))
	for _, w := range in {
		msg, ok := warningMessages[w.Kind]
		if !allowedWarningKinds[w.Kind] || !ok {
			return nil, NewError(CodeProcessingFailed, MessageFor(CodeProcessingFailed))
		}
		count := w.Count
		if count <= 0 && w.Kind != "pptx_cleanup" {
			count = 1
		}
		out = append(out, Warning{Kind: w.Kind, Message: msg, Count: count})
	}
	return out, nil
}

// docxWarnings 生成 DOCX 清理与未检查内容的固定摘要（不暴露部件名或文字）。
func docxWarnings(cleanup *docx.CleanupSummary, isDocx bool) []Warning {
	if !isDocx {
		return nil
	}
	var out []Warning
	if cleanup != nil {
		count := cleanup.CommentsDeleted + cleanup.RevisionsAccepted + cleanup.PartsRemoved + cleanup.ResourcesRemoved
		out = append(out, Warning{Kind: "docx_cleanup", Message: warningMessages["docx_cleanup"], Count: count})
	}
	out = append(out, Warning{Kind: "unchecked_content", Message: warningMessages["unchecked_content"], Count: 1})
	return out
}

// pptxWarnings 生成 PPTX 清理与未检查内容的固定摘要；清理计数为实际删除批注数（可为 0）。
func pptxWarnings(cleanup *pptx.CleanupSummary, isPPTX bool) []Warning {
	if !isPPTX {
		return nil
	}
	var out []Warning
	if cleanup != nil {
		out = append(out, Warning{Kind: "pptx_cleanup", Message: warningMessages["pptx_cleanup"], Count: cleanup.CommentsDeleted})
	}
	out = append(out, Warning{Kind: "unchecked_content", Message: warningMessages["unchecked_content"], Count: 1})
	return out
}

// auditWarning 在审计失败时返回固定匿名告警（非阻断）。
func auditWarning(auditFailed bool) []Warning {
	if !auditFailed {
		return nil
	}
	return []Warning{{Kind: "audit_failed", Message: warningMessages["audit_failed"], Count: 1}}
}

// cleanupWarning 在发布成功但临时清理失败时返回固定告警（非阻断）。
func cleanupWarning(failed bool) []Warning {
	if !failed {
		return nil
	}
	return []Warning{{Kind: "cleanup_warning", Message: warningMessages["cleanup_warning"], Count: 1}}
}
