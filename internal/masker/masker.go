// Package masker 实现单遍扫描替换、同值同号与首次顺序编号。
package masker

import (
	"strings"

	"github.com/pandatoufu/data-masking/internal/detector"
	"github.com/pandatoufu/data-masking/internal/mapping"
)

// Entry 是一条占位符对照记录（按首次出现顺序）。
type Entry struct {
	Placeholder string
	Rule        detector.RuleID
	Value       string
}

// Edit 是一段互不重叠的替换区间（字节偏移作用于已解码文本）。
type Edit struct {
	Start       int
	End         int
	Replacement string
}

// Masker 在一次脱敏内维护编号与同值映射。
type Masker struct {
	reg     *detector.Registry
	enabled map[detector.RuleID]bool
	next    map[detector.RuleID]int
	byKey   map[string]string
	entries []Entry
	counts  map[detector.RuleID]int
}

// New 构造 masker。
func New(reg *detector.Registry, enabled map[detector.RuleID]bool) *Masker {
	return &Masker{
		reg:     reg,
		enabled: enabled,
		next:    map[detector.RuleID]int{},
		byKey:   map[string]string{},
		counts:  map[detector.RuleID]int{},
	}
}

// MaskLineEdits 对单行执行扫描，返回互不重叠、按位置升序的替换区间；
// 编号与同值映射跨调用持续。
func (m *Masker) MaskLineEdits(line string) []Edit {
	cands := m.reg.Detect(line, m.enabled)
	sel := detector.Resolve(cands)
	if len(sel) == 0 {
		return nil
	}
	edits := make([]Edit, 0, len(sel))
	for _, s := range sel {
		key := string(s.Rule) + "\x00" + s.Text
		ph, ok := m.byKey[key]
		if !ok {
			m.next[s.Rule]++
			ph = mapping.Placeholder(s.Rule, m.next[s.Rule])
			m.byKey[key] = ph
			m.entries = append(m.entries, Entry{Placeholder: ph, Rule: s.Rule, Value: s.Text})
		}
		edits = append(edits, Edit{Start: s.Start, End: s.End, Replacement: ph})
		m.counts[s.Rule]++
	}
	return edits
}

// MaskLine 对单行执行扫描替换，返回替换后的行文本；编号跨行持续。
// 与 MaskLineEdits 共用同一份状态与结果。
func (m *Masker) MaskLine(line string) string {
	edits := m.MaskLineEdits(line)
	if len(edits) == 0 {
		return line
	}
	var b strings.Builder
	b.Grow(len(line) + len(edits)*8)
	prev := 0
	for _, e := range edits {
		b.WriteString(line[prev:e.Start])
		b.WriteString(e.Replacement)
		prev = e.End
	}
	b.WriteString(line[prev:])
	return b.String()
}

// Entries 返回按首次出现顺序的对照记录。
func (m *Masker) Entries() []Entry {
	out := make([]Entry, len(m.entries))
	copy(out, m.entries)
	return out
}

// Counts 返回各规则替换次数。
func (m *Masker) Counts() map[detector.RuleID]int {
	out := make(map[detector.RuleID]int, len(m.counts))
	for k, v := range m.counts {
		out[k] = v
	}
	return out
}
