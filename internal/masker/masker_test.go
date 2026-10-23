package masker

import (
	"strings"
	"testing"

	"github.com/pandatoufu/data-masking/internal/detector"
)

func allOn() map[detector.RuleID]bool {
	return map[detector.RuleID]bool{
		detector.RulePhone: true, detector.RuleIDCard: true, detector.RuleEmail: true,
		detector.RuleIP: true, detector.RuleAPIKey: true, detector.RuleCustom: true,
	}
}

func TestSameValueSameNumberFirstAppearance(t *testing.T) {
	reg := detector.NewRegistry(detector.NewFixed(), detector.NewCustom(nil))
	m := New(reg, allOn())
	lines := []string{"甲 13812345678", "乙 13900000000", "丙 13812345678"}
	var out []string
	for _, l := range lines {
		out = append(out, m.MaskLine(l))
	}
	if !strings.Contains(out[0], "[PHONE_1]") {
		t.Errorf("首个号码应为 [PHONE_1]：%q", out[0])
	}
	if !strings.Contains(out[1], "[PHONE_2]") {
		t.Errorf("第二个不同号码应为 [PHONE_2]：%q", out[1])
	}
	if !strings.Contains(out[2], "[PHONE_1]") {
		t.Errorf("重复号码应复用 [PHONE_1]：%q", out[2])
	}
	entries := m.Entries()
	if len(entries) != 2 {
		t.Fatalf("mapping 应只有 2 条记录，got %#v", entries)
	}
	if entries[0].Placeholder != "[PHONE_1]" || entries[1].Placeholder != "[PHONE_2]" {
		t.Fatalf("编号应按首次出现顺序：%#v", entries)
	}
	if m.Counts()[detector.RulePhone] != 3 {
		t.Fatalf("命中计数应为 3，got %d", m.Counts()[detector.RulePhone])
	}
}

func TestMaskLineOnlyReplacesHit(t *testing.T) {
	reg := detector.NewRegistry(detector.NewFixed(), detector.NewCustom(nil))
	m := New(reg, allOn())
	got := m.MaskLine("张三电话 13812345678 明天开会")
	if got != "张三电话 [PHONE_1] 明天开会" {
		t.Fatalf("应只替换号码本身，got %q", got)
	}
}

func TestCustomNumberingSeparateCategory(t *testing.T) {
	reg := detector.NewRegistry(detector.NewFixed(), detector.NewCustom([]string{"内部词"}))
	m := New(reg, allOn())
	got := m.MaskLine("内部词 与 13812345678")
	if !strings.Contains(got, "[CUSTOM_1]") || !strings.Contains(got, "[PHONE_1]") {
		t.Fatalf("类别编号应独立，got %q", got)
	}
}
