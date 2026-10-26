// Package mapping 冻结占位符文法、类别前缀枚举、编号/对照表契约与回程归一化查表。
package mapping

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/pandatoufu/data-masking/internal/detector"
	"github.com/pandatoufu/data-masking/internal/i18n"
)

// PrefixByRule 冻结类别前缀枚举，与命中规则一一对应。
var PrefixByRule = map[detector.RuleID]string{
	detector.RulePhone:  "PHONE",
	detector.RuleIDCard: "IDCARD",
	detector.RuleEmail:  "EMAIL",
	detector.RuleIP:     "IP",
	detector.RuleAPIKey: "APIKEY",
	detector.RuleCustom: "CUSTOM",
}

// RuleByPrefix 是前缀到规则的反向枚举。
var RuleByPrefix = map[string]detector.RuleID{
	"PHONE":  detector.RulePhone,
	"IDCARD": detector.RuleIDCard,
	"EMAIL":  detector.RuleEmail,
	"IP":     detector.RuleIP,
	"APIKEY": detector.RuleAPIKey,
	"CUSTOM": detector.RuleCustom,
}

// grammarRe 是碰撞预检用语法的宽松形式：[A-Z]+_\d+ 包方括号。
var grammarRe = regexp.MustCompile(`\[[A-Z]+_[0-9]+\]`)

// strictRe 是合法占位符的严格形式：枚举前缀 + 数字。
var strictRe = regexp.MustCompile(`^\[([A-Z]+)_([0-9]+)\]$`)

// residualRe 反向残留扫描：以枚举集为准（输入先经 Normalize，故大小写/全角已归一）。
var residualRe = regexp.MustCompile(`\[(PHONE|IDCARD|EMAIL|IP|APIKEY|CUSTOM)_[0-9]+\]`)

// Placeholder 生成合法占位符，如 [PHONE_1]。
func Placeholder(rule detector.RuleID, n int) string {
	prefix, ok := PrefixByRule[rule]
	if !ok {
		prefix = strings.ToUpper(string(rule))
	}
	return "[" + prefix + "_" + strconv.Itoa(n) + "]"
}

// Parse 解析占位符，仅接受枚举内前缀。
func Parse(s string) (detector.RuleID, int, bool) {
	m := strictRe.FindStringSubmatch(s)
	if m == nil {
		return "", 0, false
	}
	rule, ok := RuleByPrefix[m[1]]
	if !ok {
		return "", 0, false
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return "", 0, false
	}
	return rule, n, true
}

// ValidPlaceholder 报告是否为合法占位符。
func ValidPlaceholder(s string) bool {
	_, _, ok := Parse(s)
	return ok
}

// ContainsGrammar 报告文本是否已含占位符文法（用于碰撞预检）。
func ContainsGrammar(text string) bool { return grammarRe.MatchString(text) }

// GrammarHits 返回文本中所有文法命中。
func GrammarHits(text string) []string { return grammarRe.FindAllString(text, -1) }

// ResidualHits 返回文本中的占位符残留（大小写/全角归一化后以枚举集为准）。
func ResidualHits(text string) []string { return residualRe.FindAllString(Normalize(text), -1) }

// splitName 把名称拆成词干与最终扩展名（扩展名含点）。
// 无后缀名称（README）与仅有开头一个点的单段点文件（.env）没有普通扩展名：
// 完整名称就是词干，扩展名为空。.env.local 的最终扩展名是 .local。
// 目录中的点不构成扩展名（dir/.env 仍视为无普通扩展名的点文件）。
func splitName(name string) (stem, ext string) {
	i := strings.LastIndexByte(name, '.')
	if i < 0 {
		return name, ""
	}
	j := strings.LastIndexAny(name, `/\`)
	if j >= i {
		return name, ""
	}
	if i == 0 || i == j+1 {
		return name, ""
	}
	return name[:i], name[i:]
}

// MaskedName 返回脱敏文件名：notes.txt -> notes.masked.txt。
// 无后缀与单段点文件以完整名称作词干并回退 .txt：
// README -> README.masked.txt，.env -> .env.masked.txt。
func MaskedName(orig string) string {
	stem, ext := splitName(orig)
	if ext == "" {
		ext = ".txt"
	}
	return stem + ".masked" + ext
}

// MappingName 返回配对 mapping 文件名：notes.txt -> notes.masked.mapping.json，
// README -> README.masked.mapping.json，.env -> .env.masked.mapping.json。
func MappingName(orig string) string {
	stem, _ := splitName(orig)
	return stem + ".masked.mapping.json"
}

// RestoredName 依据 mapping 记录的源文件名生成还原文件名：
// notes.txt -> notes.restored.txt；无后缀与单段点文件回退 .txt
// （README -> README.restored.txt，.env -> .env.restored.txt）；无源名时退化为 restored.txt。
func RestoredName(sourceFile string) string {
	if sourceFile == "" {
		return "restored.txt"
	}
	stem, ext := splitName(sourceFile)
	if ext == "" {
		ext = ".txt"
	}
	return stem + ".restored" + ext
}

// TextExt 返回纯文本实际扩展名（不含点、小写）；无普通扩展名（无后缀或单段点文件）回退 txt。
func TextExt(name string) string {
	_, ext := splitName(name)
	ext = strings.TrimPrefix(strings.ToLower(ext), ".")
	if ext == "" {
		return "txt"
	}
	return ext
}

// BaseName 仅取源文件名的 basename，同时识别 `/` 与 `\`，不保留任何目录。
func BaseName(sourceFile string) string {
	i := strings.LastIndexAny(sourceFile, `/\`)
	if i >= 0 {
		sourceFile = sourceFile[i+1:]
	}
	return sourceFile
}

// stemOf 返回 basename 去掉扩展名后的词干；空名返回空串。
// 无后缀与单段点文件（.env）保留完整名称。
func stemOf(sourceFile string) string {
	b := BaseName(sourceFile)
	if b == "" {
		return ""
	}
	stem, _ := splitName(b)
	return stem
}

// RestoredDocxName 生成 DOCX 回程文件名：源 basename 词干 + .restored.docx。
// 空源名回退 restored.docx；不接受源名中的目录，避免控制本机路径。
func RestoredDocxName(sourceFile string) string {
	s := stemOf(sourceFile)
	if s == "" {
		return "restored.docx"
	}
	return s + ".restored.docx"
}

// RestoredTextName 生成文本回程文件名：源 basename 词干 + .restored.<实际文本扩展名>。
// 空源名回退 restored.<ext>；绝不把纯文本命名为 .docx。
func RestoredTextName(sourceFile, ext string) string {
	ext = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(ext)), ".")
	if ext == "" || ext == "docx" {
		ext = "txt"
	}
	s := stemOf(sourceFile)
	if s == "" {
		return "restored." + ext
	}
	return s + ".restored." + ext
}

// RestoredPPTXName 生成 PPTX 回程文件名：源 basename 词干 + .restored.pptx。
// 空源名回退 restored.pptx；不接受源名中的目录，避免控制本机路径。
func RestoredPPTXName(sourceFile string) string {
	s := stemOf(sourceFile)
	if s == "" {
		return "restored.pptx"
	}
	return s + ".restored.pptx"
}

// FormatError 是 mapping 格式/版本校验错误（中文原因与下一步做法）。
type FormatError struct {
	Reason string
	Advice string
	// MessageID/Params 是追加的展示元数据。
	MessageID string
	Params    map[string]any
}

func (e *FormatError) Error() string {
	if e.Advice == "" {
		return e.Reason
	}
	return e.Reason + "。" + e.Advice
}

// Presentation 返回展示元数据（实现 server 的呈现接口）。
func (e *FormatError) Presentation() *i18n.Presentation {
	if e == nil || e.MessageID == "" {
		return nil
	}
	return &i18n.Presentation{MessageID: e.MessageID, Params: e.Params}
}

// Entry 是一条占位符 ↔ 原文值对照。
type Entry struct {
	Placeholder string `json:"placeholder"`
	Category    string `json:"category"`
	Value       string `json:"value"`
}

// Mapping 是明文对照表（用户自担误发风险）。
//
// version 1 为纯文本既有契约；version 2 为 DOCX 新增契约，多出
// source_format 与 source_text_sha256 两个字段（version 1 序列化时省略）。
type Mapping struct {
	Version          int     `json:"version"`
	SourceFile       string  `json:"source_file"`
	SourceEncoding   string  `json:"source_encoding"`
	SourceSHA256     string  `json:"source_sha256"`
	SourceFormat     string  `json:"source_format,omitempty"`
	SourceTextSHA256 string  `json:"source_text_sha256,omitempty"`
	Placeholders     []Entry `json:"placeholders"`

	exact map[string]string
	norm  map[string]string
}

// New 构造 version 1 纯文本对照表。
func New(sourceFile, encoding, sha256 string, entries []Entry) *Mapping {
	m := &Mapping{
		Version:        1,
		SourceFile:     sourceFile,
		SourceEncoding: encoding,
		SourceSHA256:   sha256,
		Placeholders:   entries,
	}
	m.build()
	return m
}

// NewDOCX 构造 version 2 DOCX 对照表：source_format 为 docx，
// source_encoding 为空字符串，并携带清理后文字参考指纹。
func NewDOCX(sourceFile, sourceSHA256, sourceTextSHA256 string, entries []Entry) *Mapping {
	m := &Mapping{
		Version:          2,
		SourceFile:       sourceFile,
		SourceEncoding:   "",
		SourceSHA256:     sourceSHA256,
		SourceFormat:     "docx",
		SourceTextSHA256: sourceTextSHA256,
		Placeholders:     entries,
	}
	m.build()
	return m
}

// NewPPTX 构造 version 3 PPTX 对照表：source_format 为 pptx，
// source_encoding 为空字符串，并携带清理后文字参考指纹。
func NewPPTX(sourceFile, sourceSHA256, sourceTextSHA256 string, entries []Entry) *Mapping {
	m := &Mapping{
		Version:          3,
		SourceFile:       sourceFile,
		SourceEncoding:   "",
		SourceSHA256:     sourceSHA256,
		SourceFormat:     "pptx",
		SourceTextSHA256: sourceTextSHA256,
		Placeholders:     entries,
	}
	m.build()
	return m
}

func (m *Mapping) build() {
	m.exact = make(map[string]string, len(m.Placeholders))
	m.norm = make(map[string]string, len(m.Placeholders))
	for _, e := range m.Placeholders {
		m.exact[e.Placeholder] = e.Value
		m.norm[Normalize(e.Placeholder)] = e.Value
	}
}

// Marshal 序列化为缩进 JSON，保证确定性输出。
func (m *Mapping) Marshal() ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}

// Unmarshal 解析对照表 JSON。
// version 1 保留既有缺省兼容；version 2 校验必需字段、格式值与指纹格式；
// 未知未来版本明确拒绝。
func Unmarshal(data []byte) (*Mapping, error) {
	var m Mapping
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, &FormatError{
			Reason:    fmt.Sprintf("mapping 文件不是合法 JSON：%s", err),
			Advice:    "请选择本机上次脱敏后下载的 .mapping.json 文件",
			MessageID: "mapping.json_invalid",
		}
	}
	if m.Version == 0 {
		m.Version = 1
	}
	switch {
	case m.Version == 1:
		// 既有 version 1 行为不变。
	case m.Version == 2:
		if err := m.validateV2(); err != nil {
			return nil, err
		}
	case m.Version == 3:
		if err := m.validateV3(); err != nil {
			return nil, err
		}
	default:
		return nil, &FormatError{
			Reason:    fmt.Sprintf("不支持的 mapping version %d", m.Version),
			Advice:    "该 mapping 可能来自更新的工具版本；请使用对应版本，或重新脱敏生成新 mapping",
			MessageID: "mapping.version_unsupported",
			Params:    map[string]any{"version": m.Version},
		}
	}
	m.build()
	return &m, nil
}

func (m *Mapping) validateV2() error {
	if m.SourceFormat != "docx" {
		return &FormatError{
			Reason:    "version 2 mapping 的 source_format 必须为 docx",
			Advice:    "请选择由本工具对 DOCX 脱敏后下载的 version 2 mapping 文件",
			MessageID: "mapping.v2_format_invalid",
		}
	}
	if m.SourceEncoding != "" {
		return &FormatError{
			Reason:    "version 2 mapping 的 source_encoding 必须为空字符串",
			Advice:    "DOCX 不为整个容器赋予文本编码；请重新对 DOCX 脱敏并下载配对 mapping",
			MessageID: "mapping.v2_encoding_invalid",
		}
	}
	if !isSHA256Hex(m.SourceSHA256) {
		return &FormatError{
			Reason:    "version 2 mapping 的 source_sha256 缺失或格式非法",
			Advice:    "请重新对 DOCX 脱敏并下载配对 mapping 文件",
			MessageID: "mapping.v2_source_sha_invalid",
		}
	}
	if !isSHA256Hex(m.SourceTextSHA256) {
		return &FormatError{
			Reason:    "version 2 mapping 的 source_text_sha256 缺失或格式非法",
			Advice:    "请重新对 DOCX 脱敏并下载配对 mapping 文件",
			MessageID: "mapping.v2_text_sha_invalid",
		}
	}
	return nil
}

func (m *Mapping) validateV3() error {
	if m.SourceFormat != "pptx" {
		return &FormatError{
			Reason:    "version 3 mapping 的 source_format 必须为 pptx",
			Advice:    "请选择由本工具对 PPTX 脱敏后下载的 version 3 mapping 文件",
			MessageID: "mapping.v3_format_invalid",
		}
	}
	if m.SourceEncoding != "" {
		return &FormatError{
			Reason:    "version 3 mapping 的 source_encoding 必须为空字符串",
			Advice:    "PPTX 不为整个容器赋予文本编码；请重新对 PPTX 脱敏并下载配对 mapping",
			MessageID: "mapping.v3_encoding_invalid",
		}
	}
	if !isSHA256Hex(m.SourceSHA256) {
		return &FormatError{
			Reason:    "version 3 mapping 的 source_sha256 缺失或格式非法",
			Advice:    "请重新对 PPTX 脱敏并下载配对 mapping 文件",
			MessageID: "mapping.v3_source_sha_invalid",
		}
	}
	if !isSHA256Hex(m.SourceTextSHA256) {
		return &FormatError{
			Reason:    "version 3 mapping 的 source_text_sha256 缺失或格式非法",
			Advice:    "请重新对 PPTX 脱敏并下载配对 mapping 文件",
			MessageID: "mapping.v3_text_sha_invalid",
		}
	}
	if m.Placeholders == nil {
		return &FormatError{
			Reason:    "version 3 mapping 缺少 placeholders 条目",
			Advice:    "请重新对 PPTX 脱敏并下载配对 mapping 文件",
			MessageID: "mapping.v3_placeholders_missing",
		}
	}
	return nil
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// Lookup 按归一化 best-effort（大小写/全角）查表还原。
func (m *Mapping) Lookup(raw string) (string, bool) {
	if m.exact == nil {
		m.build()
	}
	if v, ok := m.exact[raw]; ok {
		return v, true
	}
	if v, ok := m.norm[Normalize(raw)]; ok {
		return v, true
	}
	return "", false
}

// Normalize 做全角转半角 + 大写归一化，供回程 best-effort 匹配。
func Normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 0xFF01 && r <= 0xFF5E:
			r -= 0xFEE0
		case r == 0x3000:
			r = ' '
		}
		b.WriteRune(unicode.ToUpper(r))
	}
	return b.String()
}
