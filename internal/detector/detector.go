// Package detector 定义检测器插槽、命中结构、优先级选择与 v1 的 fixed/custom 两实现。
package detector

import "sort"

// RuleID 是规则/检测类别标识。
type RuleID string

const (
	RulePhone  RuleID = "phone"
	RuleIDCard RuleID = "idcard"
	RuleEmail  RuleID = "email"
	RuleIP     RuleID = "ip"
	RuleAPIKey RuleID = "api_key"
	RuleCustom RuleID = "custom"
)

// PriorityOrder 冻结扫描优先级：custom > idcard > phone > email > ip > api_key。
var PriorityOrder = []RuleID{RuleCustom, RuleIDCard, RulePhone, RuleEmail, RuleIP, RuleAPIKey}

// ValidRule 报告是否为已定义的规则 ID。
func ValidRule(r RuleID) bool {
	for _, x := range PriorityOrder {
		if x == r {
			return true
		}
	}
	return false
}

// Priority 返回规则的优先级序号（越小越优先）。
func Priority(r RuleID) int {
	for i, x := range PriorityOrder {
		if x == r {
			return i
		}
	}
	return len(PriorityOrder)
}

// Match 是一次命中（字节偏移，作用于单行）。
type Match struct {
	Rule  RuleID
	Start int
	End   int
	Text  string
}

// Detector 是检测器插槽。v1 仅有 fixed 与 custom 两个实现；未来可插 NER。
type Detector interface {
	Type() string
	Detect(line string) []Match
}

// Resolve 依据优先级与命中消耗选择互不重叠的命中，返回按位置升序排列的结果。
//
// 规则：优先级为主序（同优先级取最左），命中区间被消耗后不再被其他规则复扫。
func Resolve(cands []Match) []Match {
	if len(cands) == 0 {
		return nil
	}
	sorted := make([]Match, len(cands))
	copy(sorted, cands)
	sort.SliceStable(sorted, func(i, j int) bool {
		pi, pj := Priority(sorted[i].Rule), Priority(sorted[j].Rule)
		if pi != pj {
			return pi < pj
		}
		if sorted[i].Start != sorted[j].Start {
			return sorted[i].Start < sorted[j].Start
		}
		// 同优先级同起点：取更长的一个（更具体）。
		return sorted[i].End > sorted[j].End
	})
	var selected []Match
	for _, c := range sorted {
		overlap := false
		for _, s := range selected {
			if c.Start < s.End && s.Start < c.End {
				overlap = true
				break
			}
		}
		if !overlap {
			selected = append(selected, c)
		}
	}
	sort.Slice(selected, func(i, j int) bool {
		if selected[i].Start != selected[j].Start {
			return selected[i].Start < selected[j].Start
		}
		return selected[i].End < selected[j].End
	})
	return selected
}

// Registry 是检测器注册表。
type Registry struct {
	detectors []Detector
}

// NewRegistry 注册检测器。
func NewRegistry(ds ...Detector) *Registry { return &Registry{detectors: ds} }

// Types 返回已注册的检测器类型（v1 仅 fixed、custom）。
func (r *Registry) Types() []string {
	out := make([]string, 0, len(r.detectors))
	for _, d := range r.detectors {
		out = append(out, d.Type())
	}
	return out
}

// Detect 汇总所有检测器在单行上的命中，并按 enabled 过滤规则。
func (r *Registry) Detect(line string, enabled map[RuleID]bool) []Match {
	var out []Match
	for _, d := range r.detectors {
		for _, m := range d.Detect(line) {
			if enabled != nil && !enabled[m.Rule] {
				continue
			}
			out = append(out, m)
		}
	}
	return out
}
