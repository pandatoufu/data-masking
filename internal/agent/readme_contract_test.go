package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoDoc 读取仓库内的文档；内容归属按主题拆分，避免把多个文件拼接后做模糊匹配。
func repoDoc(t *testing.T, elem ...string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, elem...)...))
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", filepath.Join(elem...), err)
	}
	return string(raw)
}

// MCP 指南的 headless 输入说明必须与门禁测试示例一致，且不引导 agent 读取原文或扫描目录；
// GUI 不扫描 cards 的差异由使用指南承载，构建模式与版本来源由开发指南承载。
func TestReadmeHeadlessInputContract(t *testing.T) {
	mcp := repoDoc(t, "docs", "mcp.md")
	for _, want := range []string{
		"../secret.txt", "NUL.txt", "profile 必填", "custom_words",
		"cards/", "cwd", "不会静默回退",
		"直属 TXT", "custom 关闭时不列举", "每行一个词", "256 字符",
	} {
		if !strings.Contains(mcp, want) {
			t.Errorf("docs/mcp.md headless 说明应含 %q", want)
		}
	}
	for _, banned := range []string{"需要先读取原文", "请读取 original 目录"} {
		if strings.Contains(mcp, banned) {
			t.Errorf("docs/mcp.md 不应引导 agent %q", banned)
		}
	}
	if !strings.Contains(mcp, "也不应预先读取原文") {
		t.Error("docs/mcp.md 应明确 agent 不需预读原文")
	}

	usage := repoDoc(t, "docs", "usage.md")
	// GUI 不再“完全不扫描”，而是只在用户显式选择时列举直属文件名并读取单个文件。
	if !strings.Contains(usage, "不自动扫描") {
		t.Error("docs/usage.md 应说明 GUI 不自动扫描或自动载入 cards/ 词库")
	}
	for _, want := range []string{"固定目录快捷选择", "显式选中", "不递归", "32 MiB", "不自动配对"} {
		if !strings.Contains(usage, want) {
			t.Errorf("docs/usage.md 快捷选择说明应含 %q", want)
		}
	}

	for _, want := range []string{"job_id", ".agent-state/", "不自动清理", "跨重启恢复", "不会"} {
		if !strings.Contains(mcp, want) {
			t.Errorf("docs/mcp.md 任务/恢复说明应含 %q", want)
		}
	}
	for _, want := range []string{
		"awaiting_frequency_confirmation", "awaiting_residual_confirmation",
		"continue_masking", "deliver_with_residual_risk", "confirmation_stale",
		"必须转问用户", "合作式确认",
	} {
		if !strings.Contains(mcp, want) {
			t.Errorf("docs/mcp.md 两类确认说明应含 %q", want)
		}
	}
	for _, want := range []string{"restored/<job_id>/<attempt_id>/", "不会拿到还原正文", "一次成功回程", "不恢复批注", "不承诺语义级无泄露"} {
		if !strings.Contains(mcp, want) {
			t.Errorf("docs/mcp.md 回程说明应含 %q", want)
		}
	}
	for _, want := range []string{
		`"mcpServers"`, "data-masking-agent.exe", "data-masking-agent", "--version",
		"只需要 executable 的完整路径", "不声称任何具体客户端已通过兼容性测试",
	} {
		if !strings.Contains(mcp, want) {
			t.Errorf("docs/mcp.md 客户端接入说明应含 %q", want)
		}
	}

	dev := repoDoc(t, "docs", "development.md")
	for _, want := range []string{
		"go run ./scripts/build -headless-only", "go run ./scripts/build -bundle",
		"go run ./scripts/build -bundle -platform windows/amd64",
		"release.json", "info.productVersion", "互斥", "始终串行",
	} {
		if !strings.Contains(dev, want) {
			t.Errorf("docs/development.md 构建说明应含 %q", want)
		}
	}

	if strings.Contains(mcp, "force=true") && !strings.Contains(mcp, "没有通用 `force`") {
		t.Error("docs/mcp.md 不得提供通用 force 指引")
	}
}

// MCP 指南的安全指引必须同步软性禁读、正常回程边界与「非权限隔离」定位，
// 并保留原有客户端接入与生命周期说明。
func TestReadmeAgentGuidanceContract(t *testing.T) {
	mcp := repoDoc(t, "docs", "mcp.md")
	for _, want := range []string{
		"合作式禁读指引", "不是权限隔离",
		"文件读取", "搜索", "终端", "其他工具",
		"`original/`", "`cards/`", "`mapping/`", "`restored/`", "`.agent-state/`",
		"`config.json`", "`project_file`", "`response_path`", "`restore_document`",
		"合作式软约束", "OS 沙箱",
		"客户端可能忽略", "不保证模型遵守",
		"不读取 `restored/` 正文",
	} {
		if !strings.Contains(mcp, want) {
			t.Errorf("docs/mcp.md 指引应含 %q", want)
		}
	}
	// 正常闭环与既有说明保持：交付后可读副本，但不得引导读取原文或私有产物。
	for _, banned := range []string{"请读取 restored", "请读取 mapping 正文", "可以直接读取 original"} {
		if strings.Contains(mcp, banned) {
			t.Errorf("docs/mcp.md 不应引导 agent %q", banned)
		}
	}
	for _, want := range []string{"`.agent-state/`", "不自动清理", "本仓库不声称任何具体客户端已通过兼容性测试"} {
		if !strings.Contains(mcp, want) {
			t.Errorf("docs/mcp.md 既有说明应保持 %q", want)
		}
	}
}

// MCP 指南的档位反馈口径必须与固定 code/message 一致：先询问用户、不换档、不原样重试，
// 并说明本机可覆盖/自建与客户端前置校验。
func TestReadmeProfileFeedbackContract(t *testing.T) {
	mcp := repoDoc(t, "docs", "mcp.md")
	for _, want := range []string{
		"必须由用户明确选择", "已明确时不重复询问", "不自行猜测", "默认 `balanced`",
		"缺少脱敏档位 profile。请先询问用户选择本机有效档位，明确选择后再调用；不得自行选择档位或原样重复调用。",
		"profile 无效。请先询问用户确认本机配置中有效的档位名，明确选择后再调用；不得自行选择档位或原样重复调用。",
		"`invalid_request`", "`invalid_profile`",
		"不要原样重试", "也不要换档重试",
		"不要让 agent 读配置发现档位", "可覆盖同名档位或自建新档位", "`custom_words`",
		"必填 schema 在发送前拦截", "服务端收不到请求",
	} {
		if !strings.Contains(mcp, want) {
			t.Errorf("docs/mcp.md 档位反馈说明应含 %q", want)
		}
	}
	// 不得引导读取 config.json 作为发现档位或排错手段。
	for _, banned := range []string{"请读取 config.json", "读取配置以确认档位"} {
		if strings.Contains(mcp, banned) {
			t.Errorf("docs/mcp.md 不应引导 agent %q", banned)
		}
	}
	if !strings.Contains(mcp, "不会静默回退 `balanced`") {
		t.Error("docs/mcp.md 应保持不静默回退 balanced 的口径")
	}
}
