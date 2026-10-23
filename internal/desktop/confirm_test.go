package desktop

import "testing"

// C1 回归：原生 Question 只返回 Yes/No；仅 Yes 视为确认，其余一律不放行。
func TestIsAffirmativeDialogResult(t *testing.T) {
	cases := map[string]bool{
		"Yes":    true,
		"yes":    true,
		"  YES ": true,
		"No":     false,
		"OK":     false,
		"Cancel": false,
		"":       false,
		"Error":  false,
		"退出":     false, // 自定义按钮文案不会出现
	}
	for in, want := range cases {
		if got := isAffirmativeDialogResult(in); got != want {
			t.Fatalf("isAffirmativeDialogResult(%q)=%v，期望 %v", in, got, want)
		}
	}
}
