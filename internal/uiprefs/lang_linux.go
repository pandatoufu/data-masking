//go:build linux

package uiprefs

import (
	"os"
	"strings"

	"github.com/pandatoufu/data-masking/internal/i18n"
)

// platformSystemLanguage 按 LC_ALL、LC_MESSAGES、LANG 首个非空有效 locale 判断；
// C/POSIX/全部缺失回退 en。不调用运行时 shell。
func platformSystemLanguage() i18n.Lang {
	return languageFromEnv(os.Getenv)
}

// languageFromEnv 可注入 getenv 以便环境注入测试。
func languageFromEnv(getenv func(string) string) i18n.Lang {
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		v := strings.TrimSpace(getenv(key))
		if v == "" || isUninformativeLocale(v) {
			continue
		}
		return localeToLang(v)
	}
	return i18n.EN
}
