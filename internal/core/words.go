// 本文件实现 custom 词双入口的解析与三限校验：
// 文本框条目 + 导入 txt 文件，合并去重、整批原子、逐行校验并报行号。
package core

import (
	"fmt"
	"strings"

	"github.com/pandatoufu/data-masking/internal/detector"
	"github.com/pandatoufu/data-masking/internal/i18n"
	"github.com/pandatoufu/data-masking/internal/loader"
)

const (
	// MaxWordFileSize 是导入词文件的大小上限（1MB）。
	MaxWordFileSize int64 = 1 * 1024 * 1024
	// MaxWordCount 是本次词表（去重后）与合并后参与集的词数上限。
	MaxWordCount = 1000
)

// wordSource 是一组待解析的原始行及来源标签（用于报行号）。
// labelID 是稳定 message_id；中文 label 仅用于旧 message 兼容。
type wordSource struct {
	labelID string
	label   string
	lines   []string
}

// ParseCustomWords 解析本次自定义词。
//
// 解析规则：每行先 trim，trim 后为空按空行忽略；去重为字节级精确匹配（大小写敏感）。
// 整批原子：任一行非法即全拒并报行号，不留半批。输入非空但 0 合法词显式报错。
// 导入文件复用 loader 编码链（UTF-8 strict → GBK strict，NUL/UTF-16 拒绝），并受 1MB 上限。
// 本次词表（去重后）超过 MaxWordCount 时拒绝。
//
// filePresent 显式标记词文件字段是否被提交：0 字节真空文件（filePresent=true、fileData 为空）
// 也视为一次导入，走正常解析路径并按「0 合法词」显式报错，不得静默通过。
func ParseCustomWords(textValues []string, fileData []byte, filePresent bool) ([]string, error) {
	sources := make([]wordSource, 0, 2)
	if len(textValues) > 0 {
		sources = append(sources, wordSource{labelID: "words.source_text", label: "文本框", lines: splitLines(strings.Join(textValues, "\n"))})
	}
	if filePresent {
		if int64(len(fileData)) > MaxWordFileSize {
			return nil, &loader.HumanError{
				Reason:    fmt.Sprintf("词文件大小 %d 字节，超过 1MB 上限", len(fileData)),
				Advice:    "请拆分或精简词文件后整批重试",
				MessageID: "words.file_too_large",
				Params:    map[string]any{"size": len(fileData)},
			}
		}
		_, text, err := loader.Decode(fileData)
		if err != nil {
			return nil, err
		}
		sources = append(sources, wordSource{labelID: "words.source_file", label: "词文件", lines: splitLines(text)})
	}

	// 是否提供了输入：只要提交了词文件（含 0 字节真空文件）或非空文本框条目，即视为一次导入，
	// 此时若最终 0 合法词（含全为空行）应显式报错。
	hadInput := filePresent
	for _, v := range textValues {
		if v != "" {
			hadInput = true
			break
		}
	}

	seen := map[string]bool{}
	words := make([]string, 0)
	for _, src := range sources {
		for i, raw := range src.lines {
			w := strings.TrimSpace(raw)
			if w == "" {
				continue
			}
			if err := detector.ValidateWord(w); err != nil {
				detail := i18n.N("word.empty", nil)
				if we, ok := err.(*detector.WordError); ok && we.MessageID != "" {
					detail = i18n.N(we.MessageID, we.Params)
				}
				return nil, &loader.HumanError{
					Reason:    fmt.Sprintf("%s第 %d 行无效：%s", src.label, i+1, err.Error()),
					Advice:    "请修正该行后整批重试（导入为整批原子操作）",
					MessageID: "words.line_invalid",
					Params: map[string]any{
						"source": i18n.N(src.labelID, nil),
						"line":   i + 1,
						"detail": detail,
					},
				}
			}
			if seen[w] {
				continue
			}
			seen[w] = true
			words = append(words, w)
			if len(words) > MaxWordCount {
				return nil, &loader.HumanError{
					Reason:    fmt.Sprintf("本次自定义词（去重后）超过 %d 个上限", MaxWordCount),
					Advice:    "请删减本次词条后重试",
					MessageID: "words.too_many",
					Params:    map[string]any{"max": MaxWordCount},
				}
			}
		}
	}
	if hadInput && len(words) == 0 {
		return nil, &loader.HumanError{
			Reason:    "未解析到任何有效自定义词",
			Advice:    "请输入至少一个非空词条，或检查导入文件是否只含空行",
			MessageID: "words.none",
		}
	}
	return words, nil
}

// splitLines 按 loader 的换行语义切分文本（LF/CRLF 计换行，孤立 CR 保留行内）。
func splitLines(s string) []string {
	ls := loader.SplitLines(s)
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.Text
	}
	return out
}
