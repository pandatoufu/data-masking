package agent

import (
	"path/filepath"
	"strings"

	"github.com/pandatoufu/data-masking/internal/loader"
	"github.com/pandatoufu/data-masking/internal/workspace"
)

// reservedDeviceNames 是 Windows 保留设备名（不区分大小写，带扩展名同样拒绝）。
var reservedDeviceNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// ValidateSourceName 校验 prepare_document 的 source_name：
// 只允许一个跨平台可用的单文件名，并复用 loader 的统一文档候选策略
// （拒绝已知不支持格式，接受 DOCX、新文本后缀、无后缀与安全点文件）。跨平台拒绝表一致。
func ValidateSourceName(name string) error {
	if err := validateBaseName(name); err != nil {
		return err
	}
	if err := loader.ValidateFileName(name); err != nil {
		return NewError(CodeUnsupportedInput, MessageFor(CodeUnsupportedInput))
	}
	return nil
}

// ValidateArtifactName 校验程序内部产物文件名（如 mapping 的 .json）：
// 只要求单文件名安全，不套用源件的文档候选策略。
func ValidateArtifactName(name string) error {
	return validateBaseName(name)
}

// validateBaseName 校验单个文件名的跨平台安全性（不含文档候选策略）。
func validateBaseName(name string) error {
	switch name {
	case "":
		return NewError(CodeInvalidSource, MessageFor(CodeInvalidSource))
	case ".", "..":
		return NewError(CodeInvalidSource, MessageFor(CodeInvalidSource))
	}
	if strings.ContainsAny(name, `/\`) {
		return NewError(CodeInvalidSource, MessageFor(CodeInvalidSource))
	}
	if strings.ContainsRune(name, ':') {
		// 盘符、ADS 与任何冒号形式一律拒绝。
		return NewError(CodeInvalidSource, MessageFor(CodeInvalidSource))
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return NewError(CodeInvalidSource, MessageFor(CodeInvalidSource))
		}
	}
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return NewError(CodeInvalidSource, MessageFor(CodeInvalidSource))
	}
	stem := name
	if ext := filepath.Ext(name); ext != "" {
		stem = strings.TrimSuffix(name, ext)
	}
	if reservedDeviceNames[strings.ToLower(stem)] {
		return NewError(CodeInvalidSource, MessageFor(CodeInvalidSource))
	}
	return nil
}

// OriginalPath 返回 original/ 下该单文件名的目标路径；调用方保证 name 已通过 ValidateSourceName。
func OriginalPath(root, name string) string {
	return filepath.Join(root, workspace.DirOriginal, name)
}
