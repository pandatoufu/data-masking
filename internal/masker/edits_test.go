package masker

import (
	"testing"

	"github.com/pandatoufu/data-masking/internal/detector"
)

// 5.1 验证 edit spans 与 MaskLine 共用同一结果，且优先级/同值同号/计数一致。
func TestMaskLineEditsMatchesMaskLine(t *testing.T) {
	reg := detector.NewRegistry(detector.NewFixed(), detector.NewCustom([]string{"内部词"}))
	m1 := New(reg, allOn())
	m2 := New(reg, allOn())

	lines := []string{
		"甲 13812345678 内部词",
		"乙 13900000000 sk-abcdefghijklmnop",
		"丙 13812345678",
	}
	for i, ln := range lines {
		want := m1.MaskLine(ln)
		edits := m2.MaskLineEdits(ln)
		got := applyEdits(ln, edits)
		if got != want {
			t.Fatalf("第 %d 行 edits 与 MaskLine 不一致：got %q want %q", i+1, got, want)
		}
	}
	if len(m1.Entries()) != len(m2.Entries()) {
		t.Fatalf("entries 数应一致：%d vs %d", len(m1.Entries()), len(m2.Entries()))
	}
	for i := range m1.Entries() {
		if m1.Entries()[i] != m2.Entries()[i] {
			t.Fatalf("entry %d 不一致：%+v vs %+v", i, m1.Entries()[i], m2.Entries()[i])
		}
	}
	if m1.Counts()[detector.RulePhone] != m2.Counts()[detector.RulePhone] {
		t.Fatalf("计数不一致")
	}
}

func TestMaskLineEditsNonOverlappingSorted(t *testing.T) {
	reg := detector.NewRegistry(detector.NewFixed(), detector.NewCustom(nil))
	m := New(reg, allOn())
	edits := m.MaskLineEdits("a 13812345678 b 13900000000 c")
	if len(edits) != 2 {
		t.Fatalf("应有两处 edit，got %+v", edits)
	}
	for i := 1; i < len(edits); i++ {
		if edits[i].Start < edits[i-1].End {
			t.Fatalf("edit 必须互不重叠且升序：%+v", edits)
		}
	}
	if edits[0].Replacement != "[PHONE_1]" || edits[1].Replacement != "[PHONE_2]" {
		t.Fatalf("编号应按首次出现：%+v", edits)
	}
}

func TestMaskLineEditsSixPrefixes(t *testing.T) {
	reg := detector.NewRegistry(detector.NewFixed(), detector.NewCustom([]string{"内部词"}))
	m := New(reg, allOn())
	line := "13812345678 110101199003078515 a@b.com 1.2.3.4 sk-abcdefghijklmnop 内部词"
	edits := m.MaskLineEdits(line)
	got := applyEdits(line, edits)
	for _, ph := range []string{"[PHONE_1]", "[IDCARD_1]", "[EMAIL_1]", "[IP_1]", "[APIKEY_1]", "[CUSTOM_1]"} {
		if !contains(got, ph) {
			t.Errorf("缺少 %s：%q", ph, got)
		}
	}
}

func applyEdits(line string, edits []Edit) string {
	out := ""
	prev := 0
	for _, e := range edits {
		out += line[prev:e.Start] + e.Replacement
		prev = e.End
	}
	return out + line[prev:]
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
