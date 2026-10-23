//go:build desktop

package desktop

import (
	"strings"
	"testing"

	"github.com/pandatoufu/data-masking/internal/i18n"
)

// 校验 WebView2 缺失/过旧提示按启动语言本地化，且不含自动安装/静默语义。
func TestLocalizedWindowsMessages(t *testing.T) {
	zh := localizedWindowsMessages(i18n.ZH)
	en := localizedWindowsMessages(i18n.EN)
	checks := []struct {
		name string
		zh   string
		en   string
	}{
		{"InstallationRequired", zh.InstallationRequired, en.InstallationRequired},
		{"UpdateRequired", zh.UpdateRequired, en.UpdateRequired},
		{"DownloadPage", zh.DownloadPage, en.DownloadPage},
		{"FailedToInstall", zh.FailedToInstall, en.FailedToInstall},
	}
	for _, c := range checks {
		if c.zh == "" || c.en == "" {
			t.Fatalf("%s 双语都不应为空：zh=%q en=%q", c.name, c.zh, c.en)
		}
		if !containsHan(c.zh) {
			t.Fatalf("%s 中文提示应含中文：%q", c.name, c.zh)
		}
		if containsHan(c.en) {
			t.Fatalf("%s 英文提示不应含中文：%q", c.name, c.en)
		}
	}
	// InstallationRequired/UpdateRequired 应提示重新启动，且不承诺静默安装。
	for _, v := range []string{zh.InstallationRequired, zh.UpdateRequired} {
		if !strings.Contains(v, "重新启动") {
			t.Fatalf("应解释补装后重新启动：%q", v)
		}
	}
	for _, v := range []string{en.InstallationRequired, en.UpdateRequired} {
		if !strings.Contains(strings.ToLower(v), "restart") {
			t.Fatalf("English should mention restart: %q", v)
		}
	}
}

// Linux WebKitGTK 依赖提示同样按语言本地化。
func TestLocalizedLinuxMessages(t *testing.T) {
	zh := localizedLinuxMessages(i18n.ZH)
	en := localizedLinuxMessages(i18n.EN)
	if zh.WebKit2GTKMinRequired == "" || en.WebKit2GTKMinRequired == "" {
		t.Fatal("Linux 依赖提示不应为空")
	}
	if !containsHan(zh.WebKit2GTKMinRequired) || containsHan(en.WebKit2GTKMinRequired) {
		t.Fatalf("Linux 提示本地化错误：zh=%q en=%q", zh.WebKit2GTKMinRequired, en.WebKit2GTKMinRequired)
	}
}
