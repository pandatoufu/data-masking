package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 文档必须与实现中的默认值/预算一致；旧数字不得残留在现行说明中。
// 配置文档可以提及旧显式值（10485760），但不能把旧默认写成当前默认。
func TestDocsMirrorSizeLimitDefaults(t *testing.T) {
	checks := map[string][]string{
		"README.md":             {"50 MiB（52428800 字节）", "200 MiB"},
		"README.en.md":          {"50 MiB (52428800 bytes)", "200 MiB"},
		"docs/configuration.md": {"默认 52428800（50 MiB）", "10485760", "200 MiB"},
		"docs/configuration.en.md": {
			"default 52428800 (50 MiB)", "10485760", "200 MiB",
		},
		"docs/usage.md": {
			"默认 50 MiB（52428800 字节）", "200 MiB（209715200 字节）", "GUI 上传与临时资源边界",
		},
		"docs/usage.en.md": {
			"50 MiB (52428800-byte)", "200 MiB (209715200 bytes)", "GUI upload and temporary-resource boundary",
		},
	}
	banned := []string{
		"默认 10 MiB，可用配置调整",
		"总解压最多 100 MiB",
		"10 MiB by default, configurable",
		"100 MiB total extracted size",
	}
	for name, wants := range checks {
		raw, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("读取 %s 失败：%v", name, err)
		}
		text := string(raw)
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Errorf("%s 应包含 %q", name, want)
			}
		}
		for _, bad := range banned {
			if strings.Contains(text, bad) {
				t.Errorf("%s 不应残留旧默认说明 %q", name, bad)
			}
		}
	}
}
