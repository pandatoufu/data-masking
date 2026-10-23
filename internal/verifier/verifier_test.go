package verifier

import (
	"testing"

	"github.com/pandatoufu/data-masking/internal/detector"
)

func TestForwardScanResidual(t *testing.T) {
	reg := detector.NewRegistry(detector.NewFixed(), detector.NewCustom(nil))
	enabled := map[detector.RuleID]bool{detector.RulePhone: true}
	text := "第一行正常\n第二行残留 13812345678\n"
	res := ForwardScan(text, reg, enabled)
	if len(res) != 1 {
		t.Fatalf("应检出 1 处正向残留，got %#v", res)
	}
	if res[0].Line != 2 || res[0].Text != "13812345678" {
		t.Fatalf("残留定位不正确：%#v", res)
	}
}

func TestForwardScanIgnoresDisabledRules(t *testing.T) {
	reg := detector.NewRegistry(detector.NewFixed(), detector.NewCustom(nil))
	enabled := map[detector.RuleID]bool{detector.RulePhone: false}
	if res := ForwardScan("13812345678", reg, enabled); len(res) != 0 {
		t.Fatalf("未启用规则不应报残留：%#v", res)
	}
}

func TestReverseScanResidual(t *testing.T) {
	res := ReverseScan("正常还原\n残留 [PHONE_1] 与 [PHONE_2]")
	if len(res) != 2 {
		t.Fatalf("应检出 2 处反向残留，got %#v", res)
	}
}

func TestReverseScanNormalized(t *testing.T) {
	res := ReverseScan("［ＣＵＳＴＯＭ_９］")
	if len(res) != 1 {
		t.Fatalf("全角占位符应被检出为残留，got %#v", res)
	}
}
