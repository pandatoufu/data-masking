package detector

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// MaxWordLength 是单个自定义词的字符数上限（256）。场景示例中的 300 字符超长词即触发此上限整批拒绝。
const MaxWordLength = 256

// Custom 是用户自定义词检测器：永远按字面量精确匹配（大小写敏感），永不解释为 regex。
type Custom struct {
	words []string
}

// NewCustom 构造 custom 检测器，忽略空词（空词应在录入前被 ValidateWord 拒绝）。
func NewCustom(words []string) *Custom {
	clean := make([]string, 0, len(words))
	for _, w := range words {
		if w != "" {
			clean = append(clean, w)
		}
	}
	return &Custom{words: clean}
}

// Type 返回检测器类型。
func (c *Custom) Type() string { return "custom" }

// Words 返回当前词表副本。
func (c *Custom) Words() []string {
	out := make([]string, len(c.words))
	copy(out, c.words)
	return out
}

// Detect 返回单行上所有字面量命中（非重叠、从左到右）。
func (c *Custom) Detect(line string) []Match {
	var out []Match
	for _, w := range c.words {
		start := 0
		for {
			i := strings.Index(line[start:], w)
			if i < 0 {
				break
			}
			s := start + i
			out = append(out, Match{Rule: RuleCustom, Start: s, End: s + len(w), Text: w})
			start = s + len(w)
		}
	}
	return out
}

// ValidateWord 校验单个 custom 词，拒绝空串、纯空白与含换行的词。
func ValidateWord(w string) error {
	if w == "" {
		return &WordError{Word: w, Reason: "自定义词不能为空", MessageID: "word.empty"}
	}
	if strings.TrimSpace(w) == "" {
		return &WordError{Word: w, Reason: "自定义词不能只由空白字符组成", MessageID: "word.whitespace_only"}
	}
	if strings.ContainsAny(w, "\r\n") {
		return &WordError{Word: w, Reason: "自定义词不能包含换行符", MessageID: "word.contains_newline"}
	}
	if utf8.RuneCountInString(w) > MaxWordLength {
		return &WordError{
			Word:      w,
			Reason:    fmt.Sprintf("自定义词超过 %d 字符上限", MaxWordLength),
			MessageID: "word.too_long",
			Params:    map[string]any{"max": MaxWordLength},
		}
	}
	return nil
}

// WordError 是 custom 词校验错误。
type WordError struct {
	Word   string
	Reason string
	// MessageID/Params 是追加的展示元数据。
	MessageID string
	Params    map[string]any
}

func (e *WordError) Error() string {
	return fmt.Sprintf("自定义词 %q 无效：%s", truncateForDisplay(e.Word), e.Reason)
}

// truncateForDisplay 截断超长词用于错误展示，避免把整条超长词回显给用户。
func truncateForDisplay(w string) string {
	const maxRunes = 40
	if utf8.RuneCountInString(w) <= maxRunes {
		return w
	}
	r := []rune(w)
	return string(r[:maxRunes]) + "…"
}
