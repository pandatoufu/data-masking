// Package loader 负责去程输入门禁与 I/O：
// 统一名称候选策略（DOCX 分流、有限拒绝集合、纯文本候选）、大小上限、
// 二进制/NUL 启发式、UTF-8 strict → GBK strict 解码，以及 CRLF/LF 逐行容忍与保留。
//
// 所有拒绝都返回带中文说明与“下一步做法”的 *HumanError，供 GUI 直接展示。
package loader

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"

	"github.com/pandatoufu/data-masking/internal/i18n"
)

// Encoding 是系统检出的文本编码。
type Encoding string

const (
	EncodingUTF8 Encoding = "UTF-8"
	EncodingGBK  Encoding = "GBK"
)

// DefaultMaxSize 是 fallback 使用的单文件大小上限（50MiB）。
// 与 config.DefaultMaxFileSize 保持一致，由测试断言避免两处漂移。
const DefaultMaxSize int64 = 50 * 1024 * 1024

// docxExt 是进入专用 DOCX 文档流程的最终扩展名（大小写不敏感）。
const docxExt = ".docx"

// pptxExt 是进入专用 PPTX 文档流程的最终扩展名（大小写不敏感）。
const pptxExt = ".pptx"

// NameKind 是统一名称候选策略的分类结果。
type NameKind string

const (
	// NameKindDocx 表示最终扩展名为 .docx，进入 DOCX 包门禁。
	NameKindDocx NameKind = "docx"
	// NameKindPPTX 表示最终扩展名为 .pptx，进入 PPTX 包门禁。
	NameKindPPTX NameKind = "pptx"
	// NameKindDenied 表示名称属于已知不支持格式的有限拒绝集合。
	NameKindDenied NameKind = "denied"
	// NameKindText 表示其余名称，作为纯文本候选交由大小与内容门禁。
	NameKindText NameKind = "text"
)

// deniedExts 是已知不支持格式的最终扩展名有限集合（比较前统一小写）。
// 这是可审查的名称策略，不是通用 MIME/二进制识别：未列出的后缀、无后缀与点文件
// 仍是纯文本候选，只由大小、NUL/UTF-16 与严格编码门禁决定是否接受。
var deniedExts = map[string]bool{
	// 文档类：不解析、不转换、不保格式。
	".pdf": true, ".doc": true, ".docm": true, ".dot": true, ".dotx": true, ".dotm": true,
	".odt": true, ".ott": true, ".fodt": true, ".rtf": true,
	".ppt": true, ".pptm": true, ".pot": true, ".potx": true, ".potm": true,
	".pps": true, ".ppsx": true, ".ppsm": true, ".odp": true, ".otp": true, ".fodp": true,
	".xls": true, ".xlsx": true, ".xlsm": true, ".xlsb": true, ".xlt": true, ".xltx": true, ".xltm": true,
	".ods": true, ".ots": true, ".fods": true,
	".pages": true, ".numbers": true, ".key": true,
	// 常见容器、程序与图片类。
	".zip": true, ".7z": true, ".rar": true, ".gz": true, ".bz2": true, ".xz": true,
	".tar": true, ".tgz": true, ".exe": true, ".dll": true, ".so": true, ".dylib": true, ".bin": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true,
	".ico": true, ".tif": true, ".tiff": true,
}

// finalExt 返回名称的最终扩展名（小写、含点）；无普通扩展名返回空串。
// 与 filepath.Ext 一致：只看最后一个分隔符之后的点，目录中的点不算扩展名。
func finalExt(name string) string { return strings.ToLower(filepath.Ext(name)) }

// HumanError 是可展示给用户的中文错误。
//
// 兼容性：Reason/Advice 保持既有中文字段（旧 API 的 error/message），
// 另附 Presentation 元数据供桌面双语呈现；MessageID 为空表示暂无稳定标识。
type HumanError struct {
	Reason string
	Advice string
	// MessageID/Params 是追加的展示元数据，不改变错误分类与语义。
	MessageID string
	Params    map[string]any
}

func (e *HumanError) Error() string {
	if e.Advice == "" {
		return e.Reason
	}
	return e.Reason + "。" + e.Advice
}

// Presentation 返回展示元数据（实现 server 的呈现接口）。
func (e *HumanError) Presentation() *i18n.Presentation {
	if e == nil || e.MessageID == "" {
		return nil
	}
	return &i18n.Presentation{MessageID: e.MessageID, Params: e.Params}
}

func humanErr(reason, advice string) error { return &HumanError{Reason: reason, Advice: advice} }

// humanErrPres 构造带稳定 message_id/params 的 HumanError。
func humanErrPres(reason, advice, id string, params map[string]any) error {
	return &HumanError{Reason: reason, Advice: advice, MessageID: id, Params: params}
}

// Line 是一行文本及其原始换行符。Term 为 "\r\n"、"\n" 或 ""（文件末尾无换行）。
type Line struct {
	Text string
	Term string
}

// Document 是解码后的文本行集合。
type Document struct {
	Encoding Encoding
	Lines    []Line
}

// ClassifyName 按统一候选策略分类名称：.docx/.pptx 优先分流入专用文档流程
// （单段点文件 .docx/.pptx 亦然），已知不支持格式按最终扩展名拒绝，其余
// （含无后缀与 .env 等点文件）为纯文本候选。只按名称判断，不代表内容已验证。
func ClassifyName(name string) NameKind {
	ext := finalExt(name)
	if ext == docxExt {
		return NameKindDocx
	}
	if ext == pptxExt {
		return NameKindPPTX
	}
	if deniedExts[ext] {
		return NameKindDenied
	}
	return NameKindText
}

// IsDocxName 报告名称是否按最终扩展名进入 DOCX 流程（大小写不敏感）。
func IsDocxName(name string) bool { return finalExt(name) == docxExt }

// IsPPTXName 报告名称是否按最终扩展名进入 PPTX 流程（大小写不敏感）。
func IsPPTXName(name string) bool { return finalExt(name) == pptxExt }

// IsTextCandidate 报告名称是否为纯文本候选（非 DOCX/PPTX、非已知拒绝格式）。
func IsTextCandidate(name string) bool { return ClassifyName(name) == NameKindText }

// IsDocumentCandidate 报告名称是否可作为文档入口候选（DOCX、PPTX 或纯文本候选）。
func IsDocumentCandidate(name string) bool { return ClassifyName(name) != NameKindDenied }

// ValidateFileName 按统一候选策略校验文档名称：拒绝已知不支持的格式名称，
// 接受 DOCX、PPTX 与纯文本候选（含无后缀、点文件）。名称允许不代表内容已经验证。
func ValidateFileName(name string) error {
	if ClassifyName(name) != NameKindDenied {
		return nil
	}
	ext := finalExt(name)
	return humanErrPres(fmt.Sprintf("不支持的文件格式 %q", ext),
		"本工具只做纯文本替换、.docx 与 .pptx 处理；请用原程序转换为纯文本（UTF-8/GBK）或另存为 .docx/.pptx 后再试",
		"file.unsupported_format", map[string]any{"ext": ext})
}

// ValidateSize 校验文件大小上限。limit <= 0 时取默认值。
func ValidateSize(size, limit int64) error {
	if limit <= 0 {
		limit = DefaultMaxSize
	}
	if size > limit {
		return humanErrPres(fmt.Sprintf("文件大小 %s 超过上限 %s", humanSize(size), humanSize(limit)),
			"请拆分文件，或在配置中调高大小上限后重试", "file.too_large",
			map[string]any{"size": humanSize(size), "limit": humanSize(limit)})
	}
	return nil
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f%cB", float64(n)/float64(div), "KMGT"[exp])
}

// Decode 按决策树判定编码并解码，绝不使用宽容解码。
func Decode(data []byte) (Encoding, string, error) {
	if hasNULOrUTF16BOM(data) {
		return "", "", humanErrPres("检测到二进制或 UTF-16 内容，不是受支持的纯文本",
			"请用纯文本编辑器另存为 UTF-8 或 GBK 后再试", "decode.binary_or_utf16", nil)
	}
	if utf8.Valid(data) {
		return EncodingUTF8, string(data), nil
	}
	if s, ok := decodeGBKStrict(data); ok {
		return EncodingGBK, s, nil
	}
	return "", "", humanErrPres("无法按 UTF-8 或 GBK 解码文件内容",
		"文件可能是其他编码或已损坏，请转码为 UTF-8/GBK 后重试", "decode.unsupported_encoding", nil)
}

// Load 完成大小门禁 + 编码判定 + 解码 + 按行切分。
func Load(data []byte, limit int64) (*Document, error) {
	if err := ValidateSize(int64(len(data)), limit); err != nil {
		return nil, err
	}
	enc, text, err := Decode(data)
	if err != nil {
		return nil, err
	}
	return &Document{Encoding: enc, Lines: SplitLines(text)}, nil
}

// SplitLines 仅把 LF 与 CRLF 计为换行符；孤立 CR 视为普通字符。
func SplitLines(s string) []Line {
	lines := make([]Line, 0, 16)
	start := 0
	i := 0
	for i < len(s) {
		switch {
		case s[i] == '\r' && i+1 < len(s) && s[i+1] == '\n':
			lines = append(lines, Line{Text: s[start:i], Term: "\r\n"})
			i += 2
			start = i
		case s[i] == '\n':
			lines = append(lines, Line{Text: s[start:i], Term: "\n"})
			i++
			start = i
		default:
			i++
		}
	}
	if start < len(s) || len(lines) == 0 {
		lines = append(lines, Line{Text: s[start:], Term: ""})
	}
	return lines
}

// JoinLines 逐行还原，保留每行原有换行符。
func JoinLines(lines []Line) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.Text)
		b.WriteString(l.Term)
	}
	return b.String()
}

// Encode 按指定编码写回。
func Encode(text string, enc Encoding) ([]byte, error) {
	switch enc {
	case EncodingGBK:
		out, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(text))
		if err != nil {
			return nil, humanErrPres("按 GBK 写回失败",
				"请检查文件内容是否包含 GBK 无法表示的字符", "encode.gbk_failed", nil)
		}
		return out, nil
	default:
		return []byte(text), nil
	}
}

func hasNULOrUTF16BOM(data []byte) bool {
	if len(data) >= 2 {
		if (data[0] == 0xFF && data[1] == 0xFE) || (data[0] == 0xFE && data[1] == 0xFF) {
			return true
		}
	}
	for _, b := range data {
		if b == 0x00 {
			return true
		}
	}
	return false
}

func decodeGBKStrict(data []byte) (string, bool) {
	out, err := simplifiedchinese.GBK.NewDecoder().Bytes(data)
	if err != nil {
		return "", false
	}
	back, err := simplifiedchinese.GBK.NewEncoder().Bytes(out)
	if err != nil {
		return "", false
	}
	if len(back) != len(data) {
		return "", false
	}
	for i := range back {
		if back[i] != data[i] {
			return "", false
		}
	}
	return string(out), true
}

// OverLimit 报告流式读取已判定超过有效上限：只给出上限，不伪造未保留的完整字节数。
// 语义与 ValidateSize 的 file.too_large 一致，用于 GUI 传输入口在构建快照阶段提前判定。
func OverLimit(limit int64) error {
	if limit <= 0 {
		limit = DefaultMaxSize
	}
	return humanErrPres(fmt.Sprintf("文件大小超过上限 %s", humanSize(limit)),
		"请拆分文件，或在配置中调高大小上限后重试", "file.too_large_stream",
		map[string]any{"limit": humanSize(limit)})
}
