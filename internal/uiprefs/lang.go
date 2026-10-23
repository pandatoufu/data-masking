package uiprefs

import (
	"strings"
	"sync"

	"github.com/pandatoufu/data-masking/internal/i18n"
)

// detectFn 是系统语言探测入口，封装为可注入函数便于 headless 测试，不调用运行时 shell。
var (
	detectMu sync.RWMutex
	detectFn = platformSystemLanguage
)

// SystemLanguage 返回当前系统的首次语言：中文系统 zh，其余 en。
func SystemLanguage() i18n.Lang {
	detectMu.RLock()
	fn := detectFn
	detectMu.RUnlock()
	if fn == nil {
		return i18n.EN
	}
	return fn()
}

// SetSystemLanguageForTest 注入系统语言探测函数（测试/适配用），返回恢复函数。
func SetSystemLanguageForTest(fn func() i18n.Lang) func() {
	detectMu.Lock()
	prev := detectFn
	detectFn = fn
	detectMu.Unlock()
	return func() {
		detectMu.Lock()
		detectFn = prev
		detectMu.Unlock()
	}
}

// isUninformativeLocale 报告 C/POSIX 这类不携带语言的空 locale。
func isUninformativeLocale(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	s = stripLocaleModifiers(s)
	return s == "c" || s == "posix"
}

func stripLocaleModifiers(s string) string {
	if i := strings.IndexByte(s, '.'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '@'); i >= 0 {
		s = s[:i]
	}
	return s
}

// localeToLang 把非空、非 C/POSIX 的 locale 归一为 zh/en。
// 只有中文 locale 选 zh，其余一律 en（未知语言不必翻译）。
func localeToLang(s string) i18n.Lang {
	s = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), "_", "-"))
	s = stripLocaleModifiers(s)
	if s == "zh" || strings.HasPrefix(s, "zh-") {
		return i18n.ZH
	}
	return i18n.EN
}

// primaryLangFromLANGID 依据 Windows LANGID 的主语言判断是否中文（LANG_CHINESE=0x04）。
func primaryLangFromLANGID(id uint16) i18n.Lang {
	if id&0x03FF == 0x04 {
		return i18n.ZH
	}
	return i18n.EN
}
