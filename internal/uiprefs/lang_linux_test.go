//go:build linux

package uiprefs

import (
	"testing"

	"github.com/pandatoufu/data-masking/internal/i18n"
)

// Linux 环境变量优先级：LC_ALL > LC_MESSAGES > LANG；C/POSIX 跳过；全缺失 en。
func TestLanguageFromEnvPriority(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want i18n.Lang
	}{
		{"empty", map[string]string{}, i18n.EN},
		{"lang zh", map[string]string{"LANG": "zh_CN.UTF-8"}, i18n.ZH},
		{"lc_all overrides lang", map[string]string{"LC_ALL": "en_US.UTF-8", "LANG": "zh_CN.UTF-8"}, i18n.EN},
		{"lc_messages used when lc_all empty", map[string]string{"LC_MESSAGES": "zh_CN.UTF-8", "LANG": "en_US.UTF-8"}, i18n.ZH},
		{"lc_all beats lc_messages", map[string]string{"LC_ALL": "zh_CN.UTF-8", "LC_MESSAGES": "en_US.UTF-8"}, i18n.ZH},
		{"C falls through to lang", map[string]string{"LC_ALL": "C", "LANG": "zh_CN.UTF-8"}, i18n.ZH},
		{"all C", map[string]string{"LC_ALL": "C", "LC_MESSAGES": "POSIX", "LANG": "C"}, i18n.EN},
		{"non zh locale", map[string]string{"LANG": "fr_FR.UTF-8"}, i18n.EN},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := languageFromEnv(func(k string) string { return tc.env[k] })
			if got != tc.want {
				t.Fatalf("env %v: got %s want %s", tc.env, got, tc.want)
			}
		})
	}
}
