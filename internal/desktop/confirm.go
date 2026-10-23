package desktop

import "strings"

// isAffirmativeDialogResult 判断原生 Question 返回值是否为“是”。
// Wails v2.11 的 Windows 映射为 MB_YESNO、Linux 映射为 GTK_BUTTONS_YES_NO，
// 只返回 "Yes"/"No"；自定义 Buttons/CancelButton 不生效。
// 仅 Yes 视为确认，其余（含空串、Error、No）一律不确认，保证 No/取消不放行。
func isAffirmativeDialogResult(result string) bool {
	return strings.EqualFold(strings.TrimSpace(result), "Yes")
}
