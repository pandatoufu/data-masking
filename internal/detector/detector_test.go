package detector

import (
	"strings"
	"testing"
)

func TestRegistryOnlyTwoDetectors(t *testing.T) {
	reg := NewRegistry(NewFixed(), NewCustom([]string{"内部词"}))
	types := reg.Types()
	if len(types) != 2 || types[0] != "fixed" || types[1] != "custom" {
		t.Fatalf("v1 检测器应仅含 fixed/custom，got %#v", types)
	}
}

func enabledAll() map[RuleID]bool {
	return map[RuleID]bool{
		RulePhone: true, RuleIDCard: true, RuleEmail: true,
		RuleIP: true, RuleAPIKey: true, RuleCustom: true,
	}
}

func TestFixedRulesHit(t *testing.T) {
	f := NewFixed()
	line := "电话13812345678 身份证110101199003078515 邮箱a.b@example.com 地址192.168.1.1 key sk-abcdefghijklmnop"
	got := map[RuleID]bool{}
	for _, m := range Resolve(f.Detect(line)) {
		got[m.Rule] = true
	}
	for _, r := range []RuleID{RulePhone, RuleIDCard, RuleEmail, RuleIP, RuleAPIKey} {
		if !got[r] {
			t.Errorf("规则 %s 应命中，line=%q", r, line)
		}
	}
}

func TestBoundaryGuardNegatives(t *testing.T) {
	f := NewFixed()
	for _, line := range []string{"risk-aware plan", "task-off today", "risk-abcdefghijklmnop", "task-abcdefghijklmnop"} {
		for _, m := range f.Detect(line) {
			if m.Rule == RuleAPIKey {
				t.Errorf("不应把 %q 判为 api_key（命中 %q）", line, m.Text)
			}
		}
	}
	// 更长数字串中的 phone 子串不应命中。
	for _, m := range f.Detect("卡号 91381234567890") {
		if m.Rule == RulePhone {
			t.Errorf("不应在更长数字串中命中 phone：%q", m.Text)
		}
	}
	// 1.2.3.4.5 不是合法 IP。
	for _, m := range f.Detect("版本 1.2.3.4.5") {
		if m.Rule == RuleIP {
			t.Errorf("不应命中非法 IP：%q", m.Text)
		}
	}
}

func TestCustomLiteralNeverRegex(t *testing.T) {
	c := NewCustom([]string{"a.b", `C:\Users\$1`})
	got := c.Detect("axb a.b C:\\Users\\$1")
	found := map[string]bool{}
	for _, m := range got {
		found[m.Text] = true
	}
	if !found["a.b"] {
		t.Error("字面量 a.b 应命中")
	}
	if found["axb"] {
		t.Error("a.b 不应被当作正则匹配 axb")
	}
	if !found[`C:\Users\$1`] {
		t.Errorf("包含元字符的字面量应精确命中，got %#v", got)
	}
}

func TestValidateWord(t *testing.T) {
	if err := ValidateWord(""); err == nil {
		t.Error("空串应被拒绝")
	}
	if err := ValidateWord("   "); err == nil {
		t.Error("纯空格应被拒绝")
	}
	if err := ValidateWord("a\nb"); err == nil {
		t.Error("含换行应被拒绝")
	}
	if err := ValidateWord("正常词"); err != nil {
		t.Errorf("正常词不应被拒绝：%v", err)
	}
}

func TestValidateWordLengthLimit(t *testing.T) {
	ok := strings.Repeat("a", MaxWordLength)
	if err := ValidateWord(ok); err != nil {
		t.Errorf("恰好 %d 字符应通过：%v", MaxWordLength, err)
	}
	tooLong := strings.Repeat("a", MaxWordLength+1)
	err := ValidateWord(tooLong)
	if err == nil {
		t.Fatalf("%d 字符超长词应被拒绝", MaxWordLength+1)
	}
	if !strings.Contains(err.Error(), "超过") {
		t.Errorf("拒绝原因应说明超长：%v", err)
	}
	if strings.Contains(err.Error(), strings.Repeat("a", MaxWordLength)) {
		t.Errorf("错误信息不应回显整条超长词：%v", err)
	}
}

func TestResolvePriorityOverlap(t *testing.T) {
	// 人为构造重叠：idcard 优先级高于 phone，整段只按 idcard 替换一次。
	cands := []Match{
		{Rule: RulePhone, Start: 0, End: 11, Text: "13812345678"},
		{Rule: RuleIDCard, Start: 0, End: 18, Text: "138123456781234567"},
	}
	sel := Resolve(cands)
	if len(sel) != 1 || sel[0].Rule != RuleIDCard || sel[0].End != 18 {
		t.Fatalf("重叠命中应唯一取 idcard，got %#v", sel)
	}
	// custom 高于 phone：同起点取 custom。
	cands = []Match{
		{Rule: RulePhone, Start: 0, End: 11, Text: "13812345678"},
		{Rule: RuleCustom, Start: 0, End: 11, Text: "13812345678"},
	}
	sel = Resolve(cands)
	if len(sel) != 1 || sel[0].Rule != RuleCustom {
		t.Fatalf("custom 应优先于 phone，got %#v", sel)
	}
	// 命中消耗：被消耗区间不再复扫。
	cands = []Match{
		{Rule: RuleCustom, Start: 0, End: 3, Text: "138"},
		{Rule: RulePhone, Start: 0, End: 11, Text: "13812345678"},
	}
	sel = Resolve(cands)
	if len(sel) != 1 || sel[0].End != 3 {
		t.Fatalf("命中区应被消耗，got %#v", sel)
	}
}
