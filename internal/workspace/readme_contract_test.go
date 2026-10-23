package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoDoc(t *testing.T, elem ...string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, elem...)...))
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", filepath.Join(elem...), err)
	}
	return string(raw)
}

// 使用指南的工作区目录说明必须与两入口集合一致：GUI 五目录自动保存判定并独立
// 补建 pending、headless 六目录，root 不取 cwd、同址共享/分址独立、旧 pending
// 不自动删除；首页保留两平台运行前提并提供详细指南入口。
func TestReadmeWorkspaceContract(t *testing.T) {
	usage := repoDoc(t, "docs", "usage.md")
	for _, dirs := range [][]string{GUIDirs(), HeadlessDirs()} {
		for _, name := range dirs {
			if !strings.Contains(usage, "`"+name+"/`") {
				t.Errorf("docs/usage.md 应包含目录 %s/", name)
			}
		}
	}
	for _, want := range []string{
		"executable", "不使用进程工作目录", "硬链接", "用户可写",
		"GUI 五目录", "headless 六目录", "不会自动删除", "分开放置",
		"| `pending/` | GUI + headless |", ".agent-state/",
		// 本轮：GUI 独立补建 pending，仅列举/读取用户显式选择的直属文件。
		"独立补建", "显式选择", "快捷输入",
	} {
		if !strings.Contains(usage, want) {
			t.Errorf("docs/usage.md 工作区说明应含 %q", want)
		}
	}
	for _, banned := range []string{
		"#!/bin/bash", "npm install", "需要安装 Node", "不自动落盘", "Windows 工作区",
		"共享同一套六个业务目录", "六个目录在启动时", "六目录中任一创建失败",
		"| `pending/` | 仅 headless |",
	} {
		if strings.Contains(usage, banned) {
			t.Errorf("docs/usage.md 不应把 %q 作为运行依赖或保留旧入口契约", banned)
		}
	}

	readme := repoDoc(t, "README.md")
	for _, want := range []string{"GTK3", "WebKitGTK 4.1", "session bus"} {
		if !strings.Contains(readme, want) {
			t.Errorf("README 平台运行前提应保留 %q", want)
		}
	}
	if !strings.Contains(readme, "docs/usage.md") {
		t.Error("README 应提供详细使用指南入口")
	}

	// 中英文使用指南的固定目录快捷选择契约保持一致。
	en := repoDoc(t, "docs", "usage.en.md")
	for _, want := range []string{
		"Fixed-directory quick selection", "direct-child", "explicit selection",
		"32 MiB", "does not pair mappings automatically", "1 MiB",
	} {
		if !strings.Contains(en, want) {
			t.Errorf("docs/usage.en.md quick selection should contain %q", want)
		}
	}
	for _, want := range []string{"| `pending/` | GUI + headless |", "`original/`", "`cards/`", "`mapping/`"} {
		if !strings.Contains(en, want) {
			t.Errorf("docs/usage.en.md directory contract should contain %q", want)
		}
	}
}
