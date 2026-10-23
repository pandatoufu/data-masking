package detector

import "regexp"

var (
	rePhone  = regexp.MustCompile(`1[3-9][0-9]{9}`)
	reIDCard = regexp.MustCompile(`[1-9][0-9]{5}(18|19|20)[0-9]{2}(0[1-9]|1[0-2])(0[1-9]|[12][0-9]|3[01])[0-9]{3}[0-9Xx]`)
	reEmail  = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	reIP     = regexp.MustCompile(`(?:25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])(?:\.(?:25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])){3}`)
	reAPIKey = regexp.MustCompile(`sk-[A-Za-z0-9_\-]{8,}|AKIA[0-9A-Z]{16}|gh[a-z]_[A-Za-z0-9]{20,}|xox[baprs]-[A-Za-z0-9\-]{10,}|github_pat_[A-Za-z0-9_]{20,}`)
)

type guardFunc func(line string, start, end int) bool

type fixedRule struct {
	id    RuleID
	re    *regexp.Regexp
	guard guardFunc
}

// Fixed 是内置强格式规则检测器。
type Fixed struct {
	rules []fixedRule
}

// NewFixed 构造五个内置规则（phone/idcard/email/ip/api_key），均带边界守卫。
func NewFixed() *Fixed {
	return &Fixed{rules: []fixedRule{
		{id: RuleIDCard, re: reIDCard, guard: digitBoundary},
		{id: RulePhone, re: rePhone, guard: digitBoundary},
		{id: RuleEmail, re: reEmail},
		{id: RuleIP, re: reIP, guard: dotDigitBoundary},
		{id: RuleAPIKey, re: reAPIKey, guard: wordBoundaryLeft},
	}}
}

// Type 返回检测器类型。
func (f *Fixed) Type() string { return "fixed" }

// Detect 返回单行上所有内置规则命中（未做优先级消解）。
func (f *Fixed) Detect(line string) []Match {
	var out []Match
	for _, r := range f.rules {
		for _, loc := range r.re.FindAllStringIndex(line, -1) {
			if r.guard != nil && !r.guard(line, loc[0], loc[1]) {
				continue
			}
			out = append(out, Match{Rule: r.id, Start: loc[0], End: loc[1], Text: line[loc[0]:loc[1]]})
		}
	}
	return out
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func isAlnum(b byte) bool {
	return isDigit(b) || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_'
}

func prevByte(line string, i int) byte {
	if i <= 0 {
		return 0
	}
	return line[i-1]
}

func nextByte(line string, j int) byte {
	if j >= len(line) {
		return 0
	}
	return line[j]
}

// digitBoundary 保证命中两侧不是数字（不把更长数字串的子串误命中）。
func digitBoundary(line string, start, end int) bool {
	return !isDigit(prevByte(line, start)) && !isDigit(nextByte(line, end))
}

// dotDigitBoundary 保证 IPv4 两侧不是数字或点（不把 1.2.3.4.5 之类当 IP）。
func dotDigitBoundary(line string, start, end int) bool {
	p, n := prevByte(line, start), nextByte(line, end)
	return !(isDigit(p) || p == '.') && !(isDigit(n) || n == '.')
}

// wordBoundaryLeft 保证前缀左侧不是字母/数字/下划线：拒 risk-、task- 中的 sk-。
func wordBoundaryLeft(line string, start, _ int) bool {
	return !isAlnum(prevByte(line, start))
}
