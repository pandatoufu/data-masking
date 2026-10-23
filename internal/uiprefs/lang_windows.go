//go:build windows

package uiprefs

import (
	"syscall"

	"github.com/pandatoufu/data-masking/internal/i18n"
)

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procGetUserDefaultUILanguage = kernel32.NewProc("GetUserDefaultUILanguage")
)

// platformSystemLanguage 用 GetUserDefaultUILanguage 判断系统主语言是否中文。
func platformSystemLanguage() i18n.Lang {
	r, _, _ := procGetUserDefaultUILanguage.Call()
	return primaryLangFromLANGID(uint16(r))
}
