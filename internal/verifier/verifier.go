// Package verifier 实现双向残留校验：正向复用规则引擎扫输出，反向扫占位符残留。
package verifier

import (
	"strings"

	"github.com/pandatoufu/data-masking/internal/detector"
	"github.com/pandatoufu/data-masking/internal/loader"
	"github.com/pandatoufu/data-masking/internal/mapping"
)

// Residual 是一条残留定位。
type Residual struct {
	Line   int
	Column int
	Text   string
}

// ForwardScan 在输出文本上复用同一套规则做残留检测（仅证规则覆盖率）。
func ForwardScan(text string, reg *detector.Registry, enabled map[detector.RuleID]bool) []Residual {
	var out []Residual
	for i, ln := range loader.SplitLines(text) {
		sel := detector.Resolve(reg.Detect(ln.Text, enabled))
		for _, s := range sel {
			out = append(out, Residual{Line: i + 1, Column: s.Start + 1, Text: s.Text})
		}
	}
	return out
}

// ReverseScan 扫描回程输出中的占位符残留（疑似拿错 mapping）。
func ReverseScan(text string) []Residual {
	var out []Residual
	for i, ln := range loader.SplitLines(text) {
		normalized := mapping.Normalize(ln.Text)
		for _, h := range mapping.ResidualHits(ln.Text) {
			out = append(out, Residual{Line: i + 1, Column: strings.Index(normalized, h) + 1, Text: h})
		}
	}
	return out
}
