package desktop

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/pandatoufu/data-masking/internal/i18n"
	"github.com/pandatoufu/data-masking/internal/workspace"
)

// newEnabledWorkspace 构造测试用“已启用”工作区（绕过平台判定，注入临时根目录）。
func newEnabledWorkspace(t *testing.T, root string, ops FileOps) *Workspace {
	t.Helper()
	if ops == nil {
		ops = OSFileOps{}
	}
	w := &Workspace{ops: ops}
	w.SetRoot(root)
	w.Init()
	if !w.Enabled() {
		t.Fatalf("测试工作区应启用：%q", w.DisabledReason())
	}
	return w
}

func assertDir(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		t.Fatalf("%s 应为目录：err=%v", path, err)
	}
}

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取目录失败 %s：%v", dir, err)
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names
}

// --- 1.1 目录布局与启动创建 ---

func TestWorkspaceInitCreatesFiveGUIDirsAndPending(t *testing.T) {
	root := t.TempDir()
	w := newEnabledWorkspace(t, root, nil)
	if len(workspaceDirs) != 5 {
		t.Fatalf("GUI 自动保存判定应固定为五目录：%v", workspaceDirs)
	}
	for _, name := range workspaceDirs {
		assertDir(t, filepath.Join(root, name))
	}
	// pending/ 由 GUI 独立补建为快捷输入目录。
	assertDir(t, filepath.Join(root, workspaceDirPending))
	if err := w.PendingErr(); err != nil {
		t.Fatalf("pending/ 应可用：%v", err)
	}
	// cards/、original/ 仅创建，GUI 不会自动导入或落盘写入。
	for _, name := range []string{workspaceDirCards, workspaceDirOriginal} {
		if names := dirEntries(t, filepath.Join(root, name)); len(names) != 0 {
			t.Fatalf("%s/ 应保持为空：%v", name, names)
		}
	}
	// 任务私有记录仍不由 GUI 创建。
	if _, err := os.Lstat(filepath.Join(root, workspace.AgentStateDir)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("GUI 不应创建 %s：err=%v", workspace.AgentStateDir, err)
	}
}

func TestWorkspaceInitIdempotentPreservesContent(t *testing.T) {
	root := t.TempDir()
	w := newEnabledWorkspace(t, root, nil)
	keep := filepath.Join(root, workspaceDirMasked, "keep.txt")
	if err := os.WriteFile(keep, []byte("user"), 0o644); err != nil {
		t.Fatal(err)
	}
	pendingKeep := filepath.Join(root, workspaceDirPending, "keep.txt")
	if err := os.WriteFile(pendingKeep, []byte("reply"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 二次启动：不重建、不清空。
	w.Init()
	for path, want := range map[string]string{keep: "user", pendingKeep: "reply"} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Fatalf("二次启动不得清空已有内容 %s：%q err=%v", path, got, err)
		}
	}
	if err := w.PendingErr(); err != nil {
		t.Fatalf("二次启动后 pending/ 应可用：%v", err)
	}
}

func TestWorkspaceFilePlaceholderDisables(t *testing.T) {
	for _, name := range workspaceDirs {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, name), []byte("not a dir"), 0o644); err != nil {
				t.Fatal(err)
			}
			w := &Workspace{ops: OSFileOps{}}
			w.SetRoot(root)
			w.Init()
			if w.Enabled() {
				t.Fatal("同名文件占位应判为不可用")
			}
			if w.DisabledReason() == "" || !containsHan(w.DisabledReason()) {
				t.Fatalf("应给出中文降级提示：%q", w.DisabledReason())
			}
		})
	}
}

// 跨平台契约：Linux 同样创建 GUI 五目录，工作区可用（旧的非 Windows no-op 预期已被替代）。
func TestWorkspaceCreatesDirsOnCurrentPlatform(t *testing.T) {
	root := t.TempDir()
	w := NewWorkspace()
	w.SetRoot(root)
	w.Init()
	if !w.Enabled() {
		t.Fatalf("当前平台应创建工作区：%v", w.DisabledReason())
	}
	if w.DisabledReason() != "" {
		t.Fatalf("可用时不应有降级提示：%q", w.DisabledReason())
	}
	for _, name := range workspaceDirs {
		assertDir(t, filepath.Join(root, name))
	}
	if runtime.GOOS != "windows" {
		// Linux 也自动落盘：五目录与发布能力都可用。
		res := w.AutoSaveMask("notes.masked.txt", b64("m"), "notes.masked.mapping.json", b64("{}"))
		if res.Masked.Status != workspaceStatusSaved || res.Mapping.Status != workspaceStatusSaved {
			t.Fatalf("Linux 自动落盘应可用：%+v", res)
		}
	}
}

// 两平台文本/DOCX 均自动配对落盘（扩展名不改变发布契约）。
func TestAutoSavePairTextAndDocx(t *testing.T) {
	cases := []struct {
		name       string
		maskedName string
		mapping    string
	}{
		{"文本", "notes.masked.txt", "notes.masked.mapping.json"},
		{"DOCX", "合同.masked.docx", "合同.masked.mapping.json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			w := newEnabledWorkspace(t, root, nil)
			res := w.AutoSaveMask(c.maskedName, b64("m"), c.mapping, b64("{}"))
			if res.Masked.Status != workspaceStatusSaved || res.Mapping.Status != workspaceStatusSaved {
				t.Fatalf("应成对自动保存：%+v", res)
			}
			if filepath.Base(res.Masked.Path) != c.maskedName || filepath.Base(res.Mapping.Path) != c.mapping {
				t.Fatalf("命名应沿用原产物名：%+v", res)
			}
		})
	}
}

// GUI 降级只看五个自动保存必需目录：pending/ 被文件/链接占位或 .agent-state 存在
// 都不影响自动落盘；pending 占位只让快捷入口不可用，且不被替换/删除/修改。
func TestWorkspacePendingPlaceholderOnlyAffectsInput(t *testing.T) {
	cases := []struct {
		name          string
		setup         func(t *testing.T, root string)
		pendingExists bool
		skip          bool
	}{
		{
			name: "pending 普通文件",
			setup: func(t *testing.T, root string) {
				if err := os.WriteFile(filepath.Join(root, workspaceDirPending), []byte("occupied"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			pendingExists: true,
		},
		{
			name: "pending 符号链接",
			setup: func(t *testing.T, root string) {
				if err := os.Symlink(t.TempDir(), filepath.Join(root, workspaceDirPending)); err != nil {
					t.Fatalf("创建符号链接失败：%v", err)
				}
			},
			pendingExists: true,
			skip:          runtime.GOOS == "windows",
		},
		{
			name: ".agent-state 普通文件",
			setup: func(t *testing.T, root string) {
				if err := os.WriteFile(filepath.Join(root, workspace.AgentStateDir), []byte("state"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.skip {
				t.Skip("Windows 无创建符号链接权限：平台特有 reparse 用例按平台运行")
			}
			root := t.TempDir()
			c.setup(t, root)
			w := newEnabledWorkspace(t, root, nil)
			if w.DisabledReason() != "" {
				t.Fatalf("pending/.agent-state 占位不得使 GUI 降级：%q", w.DisabledReason())
			}
			if !w.Enabled() {
				t.Fatal("自动保存判定应仍可用")
			}
			if c.pendingExists && w.PendingErr() == nil {
				t.Fatal("pending 占位应使快捷输入入口报告不可用")
			}
			if !c.pendingExists && w.PendingErr() != nil {
				t.Fatalf("pending 可用时不应报错：%v", w.PendingErr())
			}
			pair := w.AutoSaveMask("notes.masked.txt", b64("m"), "notes.masked.mapping.json", b64("{}"))
			if pair.Masked.Status != workspaceStatusSaved || pair.Mapping.Status != workspaceStatusSaved {
				t.Fatalf("自动落盘应正常：%+v", pair)
			}
			if got := w.AutoSaveRestored("notes.restored.txt", b64("r")); got.Status != workspaceStatusSaved {
				t.Fatalf("还原自动落盘应正常：%+v", got)
			}
			if c.pendingExists {
				if _, err := os.Lstat(filepath.Join(root, workspaceDirPending)); err != nil {
					t.Fatalf("GUI 不得删除 pending 占位：%v", err)
				}
			}
			for path, want := range map[string]string{
				filepath.Join(root, workspaceDirPending):     "occupied",
				filepath.Join(root, workspace.AgentStateDir): "state",
			} {
				info, err := os.Lstat(path)
				if err != nil || !info.Mode().IsRegular() {
					continue
				}
				if got, err := os.ReadFile(path); err != nil || string(got) != want {
					t.Fatalf("GUI 不得修改占位内容 %s：%q err=%v", path, got, err)
				}
			}
		})
	}
}

// pending 占位/不可读只影响快捷输入入口：GUI 启动不重复提示自动落盘失效，
// 自动落盘仍可用，手动另存链路（SaveCoordinator）不受影响。
func TestWorkspacePendingFailureDoesNotNotifyOrDisableAutoSave(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, workspaceDirPending), []byte("occupied"), 0o644); err != nil {
		t.Fatal(err)
	}
	app := NewApp(nil, nil)
	w := &Workspace{ops: OSFileOps{}}
	w.SetRoot(root)
	app.SetWorkspace(w)
	notified := 0
	app.InitWorkspace(func(string) { notified++ })
	if notified != 0 {
		t.Fatalf("pending 失败不得触发自动落盘失效提示，实际 %d 次", notified)
	}
	if !app.Workspace.Enabled() {
		t.Fatal("五目录可用时自动落盘应保持可用")
	}
	if app.Workspace.PendingErr() == nil {
		t.Fatal("pending 占位应只让快捷入口报不可用")
	}
	if pair := app.Workspace.AutoSaveMask("notes.masked.txt", b64("m"), "notes.masked.mapping.json", b64("{}")); pair.Masked.Status != workspaceStatusSaved {
		t.Fatalf("自动落盘应正常：%+v", pair)
	}
}

// pending 目录不可写（权限失败）时仅该入口失败；修复后重试可恢复，自动落盘不受影响。
func TestWorkspacePendingPermissionFailureAndRetry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 目录权限语义不同：以真实 Windows 观察记录，不在 POSIX 权限断言")
	}
	if os.Geteuid() == 0 {
		t.Skip("以 root 运行时目录权限不生效：跳过权限失败用例")
	}
	root := t.TempDir()
	w := newEnabledWorkspace(t, root, nil)
	pendingDir := filepath.Join(root, workspaceDirPending)
	if err := os.Remove(pendingDir); err != nil {
		t.Fatal(err)
	}
	// 预置工作区锁文件，避免只读父目录同时影响自动落盘的跨进程锁，从而隔离
	// 本用例关注点：pending 权限失败只影响快捷入口。
	if err := os.WriteFile(filepath.Join(root, workspace.LockFileName), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(root, 0o755) }()
	if err := w.EnsurePending(); err == nil {
		t.Fatal("只读父目录时 pending 补建应失败")
	}
	if !w.Enabled() {
		t.Fatal("pending 权限失败不得关闭自动落盘")
	}
	if pair := w.AutoSaveMask("notes.masked.txt", b64("m"), "notes.masked.mapping.json", b64("{}")); pair.Masked.Status != workspaceStatusSaved {
		t.Fatalf("自动落盘应仍可用：%+v", pair)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := w.EnsurePending(); err != nil {
		t.Fatalf("修复权限后刷新重试应成功：%v", err)
	}
	assertDir(t, pendingDir)
	if w.PendingErr() != nil {
		t.Fatalf("成功后应清除旧错误：%v", w.PendingErr())
	}
}

// 五目录初始化失败时仍独立尝试补建 pending（两个判定互不覆盖）。
func TestWorkspacePendingCreatedEvenWhenFiveDirsFail(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, workspaceDirCards), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := &Workspace{ops: OSFileOps{}}
	w.SetRoot(root)
	w.Init()
	if w.Enabled() {
		t.Fatal("cards 占位应使自动落盘不可用")
	}
	assertDir(t, filepath.Join(root, workspaceDirPending))
	if w.PendingErr() != nil {
		t.Fatalf("pending 应独立补建成功：%v", w.PendingErr())
	}
}

// 同址部署：GUI 启动建/补五目录并独立补建 pending，headless 随后沿用；GUI 重启
// 不删除 headless 的 pending 与 .agent-state 历史。
func TestWorkspaceSameRootWithHeadlessKeepsHistory(t *testing.T) {
	root := t.TempDir()
	w := newEnabledWorkspace(t, root, nil)
	for _, name := range workspaceDirs {
		assertDir(t, filepath.Join(root, name))
	}
	assertDir(t, filepath.Join(root, workspaceDirPending))
	if err := workspace.EnsureHeadless(root); err != nil {
		t.Fatalf("同址 headless 应在同一根目录成功：%v", err)
	}
	history := filepath.Join(root, workspaceDirPending, "history", "response.txt")
	state := filepath.Join(root, workspace.AgentStateDir, "history", "events.jsonl")
	for path, content := range map[string]string{history: "reply", state: "private"} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	w.Init()
	for path, want := range map[string]string{history: "reply", state: "private"} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Fatalf("GUI 重启不得删除/修改 %s：%q err=%v", path, got, err)
		}
	}
}

// 分址部署：两个根目录各自独立；各自入口都在自己的 root 下补建目录，互不读取。
func TestWorkspaceSeparateRootsIndependent(t *testing.T) {
	guiRoot := t.TempDir()
	newEnabledWorkspace(t, guiRoot, nil)
	agentRoot := t.TempDir()
	if err := workspace.EnsureHeadless(agentRoot); err != nil {
		t.Fatal(err)
	}
	assertDir(t, filepath.Join(guiRoot, workspaceDirPending))
	assertDir(t, filepath.Join(agentRoot, workspaceDirPending))
	if _, err := os.Lstat(filepath.Join(agentRoot, workspace.AgentStateDir)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("GUI 不应触碰 headless root 的任务记录：%v", err)
	}
}

// 不同 cwd 启动：root 只取注入/真实的 executable 目录，pending/ 也建在该 root，
// 不在当前工作目录另建任何业务目录。
func TestWorkspacePendingUsesExecutableRootNotCwd(t *testing.T) {
	exeRoot := t.TempDir()
	cwd := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(prev) }()

	w := &Workspace{ops: OSFileOps{}}
	w.SetExecutableDir(func() string { return exeRoot })
	w.Init()
	if !w.Enabled() {
		t.Fatalf("可写 exe 目录应可用：%q", w.DisabledReason())
	}
	assertDir(t, filepath.Join(exeRoot, workspaceDirPending))
	if names := dirEntries(t, cwd); len(names) != 0 {
		t.Fatalf("cwd 不得被写入：%v", names)
	}
}

// pending 占位修复后，EnsurePending 重试可恢复；仍不改变自动保存判定。
func TestWorkspaceEnsurePendingRetryAfterFix(t *testing.T) {
	root := t.TempDir()
	placeholder := filepath.Join(root, workspaceDirPending)
	if err := os.WriteFile(placeholder, []byte("occupied"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := newEnabledWorkspace(t, root, nil)
	if w.PendingErr() == nil {
		t.Fatal("占位时 pending 应不可用")
	}
	if err := os.Remove(placeholder); err != nil {
		t.Fatal(err)
	}
	if err := w.EnsurePending(); err != nil {
		t.Fatalf("移除占位后重试应成功：%v", err)
	}
	assertDir(t, placeholder)
	if !w.Enabled() {
		t.Fatal("pending 修复不改变自动保存可用性")
	}
}

// 未解析 root 时 EnsurePending 明确失败，不猜测 executable/cwd，也不创建目录。
func TestWorkspaceEnsurePendingRequiresResolvedRoot(t *testing.T) {
	w := &Workspace{ops: OSFileOps{}}
	w.SetExecutableDir(func() string { return "" })
	if err := w.EnsurePending(); !errors.Is(err, workspace.ErrUnresolvedRoot) {
		t.Fatalf("未解析 root 应返回 ErrUnresolvedRoot，got %v", err)
	}
	if w.Root() != "" {
		t.Fatalf("不得自行猜测 root：%q", w.Root())
	}
	if !errors.Is(w.PendingErr(), workspace.ErrUnresolvedRoot) {
		t.Fatalf("应记录 pending 不可用原因：%v", w.PendingErr())
	}
}

func TestMappingAndMaskedCandidates(t *testing.T) {
	cases := []struct {
		name       string
		maskedName string
		wantStem   string
		wantExt    string
		wantOK     bool
		n          int
		wantMasked string
		wantMap    string
	}{
		{"txt", "notes.masked.txt", "notes", ".txt", true, 1, "notes-1.masked.txt", "notes-1.masked.mapping.json"},
		{"docx", "合同.masked.docx", "合同", ".docx", true, 2, "合同-2.masked.docx", "合同-2.masked.mapping.json"},
		{"源名已含 masked", "notes.masked.masked.txt", "notes.masked", ".txt", true, 1, "notes.masked-1.masked.txt", "notes.masked-1.masked.mapping.json"},
		{"无后缀回退 txt", "README.masked.txt", "README", ".txt", true, 1, "README-1.masked.txt", "README-1.masked.mapping.json"},
		{"单段点文件保留词干", ".env.masked.txt", ".env", ".txt", true, 2, ".env-2.masked.txt", ".env-2.masked.mapping.json"},
		{"非约定名", "plain.txt", "", ".txt", false, 1, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stem, ext, ok := deriveMaskedStem(c.maskedName)
			if ok != c.wantOK || stem != c.wantStem || ext != c.wantExt {
				t.Fatalf("deriveMaskedStem=%q,%q,%v want %q,%q,%v", stem, ext, ok, c.wantStem, c.wantExt, c.wantOK)
			}
			if !ok {
				return
			}
			if got := maskedCandidate(c.maskedName, stem, ext, c.n); got != c.wantMasked {
				t.Fatalf("maskedCandidate=%q want %q", got, c.wantMasked)
			}
			if got := mappingCandidate("orig.masked.mapping.json", stem, c.n); got != c.wantMap {
				t.Fatalf("mappingCandidate=%q want %q", got, c.wantMap)
			}
		})
	}
	// n=0 逐字使用 core 下发名。
	if got := maskedCandidate("notes.masked.txt", "notes", ".txt", 0); got != "notes.masked.txt" {
		t.Fatalf("n=0 应用原名，got %q", got)
	}
	if got := mappingCandidate("notes.masked.mapping.json", "notes", 0); got != "notes.masked.mapping.json" {
		t.Fatalf("n=0 应用原名，got %q", got)
	}
}

func TestRestoredCandidates(t *testing.T) {
	cases := []struct {
		name string
		n    int
		want string
	}{
		{"notes.restored.txt", 0, "notes.restored.txt"},
		{"notes.restored.txt", 1, "notes.restored-1.txt"},
		{"合同.restored.docx", 2, "合同.restored-2.docx"},
		{"restored.txt", 0, "restored.txt"},
		{"restored.txt", 1, "restored-1.txt"},
	}
	for _, c := range cases {
		if got := restoredCandidate(c.name, c.n); got != c.want {
			t.Fatalf("restoredCandidate(%q,%d)=%q want %q", c.name, c.n, got, c.want)
		}
	}
}

// --- 1.3 配对无覆盖落盘 ---

func TestAutoSaveMaskFirstUsesOriginalNames(t *testing.T) {
	root := t.TempDir()
	w := newEnabledWorkspace(t, root, nil)
	res := w.AutoSaveMask("notes.masked.txt", b64("masked"), "notes.masked.mapping.json", b64("{}"))
	if res.Masked.Status != workspaceStatusSaved || res.Mapping.Status != workspaceStatusSaved {
		t.Fatalf("应成对保存：%+v", res)
	}
	if got, err := os.ReadFile(filepath.Join(root, "masked", "notes.masked.txt")); err != nil || string(got) != "masked" {
		t.Fatalf("masked 字节应完整：%q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, "mapping", "notes.masked.mapping.json")); err != nil {
		t.Fatalf("mapping 应存在：%v", err)
	}
	// 不留临时文件。
	if names := dirEntries(t, filepath.Join(root, "masked")); len(names) != 1 {
		t.Fatalf("masked/ 只应有 1 个文件：%v", names)
	}
}

func TestAutoSaveMaskCollisionPairedSuffix(t *testing.T) {
	root := t.TempDir()
	w := newEnabledWorkspace(t, root, nil)
	existing := filepath.Join(root, "masked", "notes.masked.txt")
	if err := os.WriteFile(existing, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := w.AutoSaveMask("notes.masked.txt", b64("new"), "notes.masked.mapping.json", b64("{}"))
	if res.Masked.Status != workspaceStatusSaved {
		t.Fatalf("应保存到下一号：%+v", res)
	}
	if filepath.Base(res.Masked.Path) != "notes-1.masked.txt" || filepath.Base(res.Mapping.Path) != "notes-1.masked.mapping.json" {
		t.Fatalf("配对应共用 -1：%+v", res)
	}
	if got, _ := os.ReadFile(existing); string(got) != "original" {
		t.Fatalf("已存在文件不得被覆盖：%q", got)
	}
}

func TestAutoSaveMaskConcurrentExistRetries(t *testing.T) {
	root := t.TempDir()
	w := newEnabledWorkspace(t, root, &existOnceOps{})
	res := w.AutoSaveMask("notes.masked.txt", b64("m"), "notes.masked.mapping.json", b64("{}"))
	if res.Masked.Status != workspaceStatusSaved {
		t.Fatalf("并发 ErrExist 应顺延成功：%+v", res)
	}
	if filepath.Base(res.Masked.Path) != "notes-1.masked.txt" || filepath.Base(res.Mapping.Path) != "notes-1.masked.mapping.json" {
		t.Fatalf("应顺延到 -1 且配对同号：%+v", res)
	}
	if _, err := os.Stat(filepath.Join(root, "masked", "notes.masked.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("不应留下未配对产物")
	}
}

func TestAutoSaveMaskUnsupportedHardlinkFails(t *testing.T) {
	root := t.TempDir()
	ops := &linkFailOps{failOn: func(string, string) error { return errors.New("operation not permitted") }}
	w := newEnabledWorkspace(t, root, ops)
	res := w.AutoSaveMask("notes.masked.txt", b64("m"), "notes.masked.mapping.json", b64("{}"))
	if res.Masked.Status != workspaceStatusFailed || !containsHan(res.Masked.Message) {
		t.Fatalf("不支持硬链接应立即失败：%+v", res)
	}
	if names := dirEntries(t, filepath.Join(root, "masked")); len(names) != 0 {
		t.Fatalf("失败应清理 masked 临时文件：%v", names)
	}
	if names := dirEntries(t, filepath.Join(root, "mapping")); len(names) != 0 {
		t.Fatalf("失败应清理 mapping 临时文件：%v", names)
	}
}

func TestAutoSaveMaskPartialPublishRollsBack(t *testing.T) {
	root := t.TempDir()
	sep := string(filepath.Separator)
	ops := &linkFailOps{failOn: func(_, newname string) error {
		if strings.Contains(newname, sep+"mapping"+sep) {
			return errors.New("operation not permitted")
		}
		return nil
	}}
	w := newEnabledWorkspace(t, root, ops)
	res := w.AutoSaveMask("notes.masked.txt", b64("m"), "notes.masked.mapping.json", b64("{}"))
	if res.Masked.Status != workspaceStatusFailed {
		t.Fatalf("mapping 失败应整体 failed：%+v", res)
	}
	if names := dirEntries(t, filepath.Join(root, "masked")); len(names) != 0 {
		t.Fatalf("已发布的 masked 应回滚：%v", names)
	}
	if names := dirEntries(t, filepath.Join(root, "mapping")); len(names) != 0 {
		t.Fatalf("mapping 不应留下半成品：%v", names)
	}
}

func TestAutoSaveMaskPartialExistRollsBackAndRetries(t *testing.T) {
	root := t.TempDir()
	w := newEnabledWorkspace(t, root, &mappingExistOnceOps{})
	res := w.AutoSaveMask("notes.masked.txt", b64("m"), "notes.masked.mapping.json", b64("{}"))
	if res.Masked.Status != workspaceStatusSaved {
		t.Fatalf("mapping 并发冲突应回滚后顺延：%+v", res)
	}
	if filepath.Base(res.Masked.Path) != "notes-1.masked.txt" || filepath.Base(res.Mapping.Path) != "notes-1.masked.mapping.json" {
		t.Fatalf("回滚后应使用 -1 且配对同号：%+v", res)
	}
	if _, err := os.Stat(filepath.Join(root, "masked", "notes.masked.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("回滚应删除本次刚发布的 masked")
	}
}

func TestAutoSaveMaskExceedsAttempts(t *testing.T) {
	root := t.TempDir()
	w := newEnabledWorkspace(t, root, nil)
	maskedDir := filepath.Join(root, "masked")
	mappingDir := filepath.Join(root, "mapping")
	for n := 0; n <= workspaceMaxAttempts; n++ {
		mName := maskedCandidate("notes.masked.txt", "notes", ".txt", n)
		mapName := mappingCandidate("notes.masked.mapping.json", "notes", n)
		if err := os.WriteFile(filepath.Join(maskedDir, mName), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(mappingDir, mapName), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res := w.AutoSaveMask("notes.masked.txt", b64("m"), "notes.masked.mapping.json", b64("{}"))
	if res.Masked.Status != workspaceStatusFailed || !strings.Contains(res.Masked.Message, "1000") {
		t.Fatalf("超限应失败并提示：%+v", res)
	}
}

// 2.2 无后缀与点文件产物：完整词干 + .txt 回退，配对共用同号、不覆盖、不触碰原件。
func TestAutoSaveNewTextNamesPairingAndRestoreCollision(t *testing.T) {
	root := t.TempDir()
	w := newEnabledWorkspace(t, root, nil)

	first := w.AutoSaveMask("README.masked.txt", b64("one"), "README.masked.mapping.json", b64("{}"))
	if first.Masked.Status != workspaceStatusSaved || first.Mapping.Status != workspaceStatusSaved {
		t.Fatalf("README 首次应成对保存：%+v", first)
	}
	if filepath.Base(first.Masked.Path) != "README.masked.txt" || filepath.Base(first.Mapping.Path) != "README.masked.mapping.json" {
		t.Fatalf("README 命名不符：%+v", first)
	}
	second := w.AutoSaveMask("README.masked.txt", b64("two"), "README.masked.mapping.json", b64("{}"))
	if filepath.Base(second.Masked.Path) != "README-1.masked.txt" || filepath.Base(second.Mapping.Path) != "README-1.masked.mapping.json" {
		t.Fatalf("README 重名应共用 -1：%+v", second)
	}
	if got, _ := os.ReadFile(first.Masked.Path); string(got) != "one" {
		t.Fatalf("首次产物不得被覆盖：%q", got)
	}

	e1 := w.AutoSaveMask(".env.masked.txt", b64("k1"), ".env.masked.mapping.json", b64("{}"))
	e2 := w.AutoSaveMask(".env.masked.txt", b64("k2"), ".env.masked.mapping.json", b64("{}"))
	if filepath.Base(e1.Masked.Path) != ".env.masked.txt" || filepath.Base(e1.Mapping.Path) != ".env.masked.mapping.json" {
		t.Fatalf(".env 命名不符：%+v", e1)
	}
	if filepath.Base(e2.Masked.Path) != ".env-1.masked.txt" || filepath.Base(e2.Mapping.Path) != ".env-1.masked.mapping.json" {
		t.Fatalf(".env 重名应共用 -1：%+v", e2)
	}
	if got, _ := os.ReadFile(e1.Masked.Path); string(got) != "k1" {
		t.Fatalf(".env 首次产物不得被覆盖：%q", got)
	}

	r1 := w.AutoSaveRestored("README.restored.txt", b64("r1"))
	r2 := w.AutoSaveRestored("README.restored.txt", b64("r2"))
	if filepath.Base(r1.Path) != "README.restored.txt" || filepath.Base(r2.Path) != "README.restored-1.txt" {
		t.Fatalf("README 还原重名规则不符：%q %q", r1.Path, r2.Path)
	}
	p1 := w.AutoSaveRestored(".env.restored.txt", b64("p1"))
	p2 := w.AutoSaveRestored(".env.restored.txt", b64("p2"))
	if filepath.Base(p1.Path) != ".env.restored.txt" || filepath.Base(p2.Path) != ".env.restored-1.txt" {
		t.Fatalf(".env 还原重名规则不符：%q %q", p1.Path, p2.Path)
	}
	if got, _ := os.ReadFile(r1.Path); string(got) != "r1" {
		t.Fatalf("首次还原不得被覆盖：%q", got)
	}
}

// --- 1.4 单件还原落盘 ---

func TestAutoSaveRestoredTextAndDocx(t *testing.T) {
	root := t.TempDir()
	w := newEnabledWorkspace(t, root, nil)
	res := w.AutoSaveRestored("notes.restored.txt", b64("text"))
	if res.Status != workspaceStatusSaved {
		t.Fatalf("文本还原应落盘：%+v", res)
	}
	if res.Path != filepath.Join(root, "restored", "notes.restored.txt") {
		t.Fatalf("路径应为 restored/：%q", res.Path)
	}
	res = w.AutoSaveRestored("合同.restored.docx", b64("docx"))
	if res.Status != workspaceStatusSaved {
		t.Fatalf("DOCX 还原应落盘：%+v", res)
	}
	if filepath.Base(res.Path) != "合同.restored.docx" {
		t.Fatalf("DOCX 命名应沿用原有：%q", res.Path)
	}
}

func TestAutoSaveRestoredCollisionAndFallback(t *testing.T) {
	root := t.TempDir()
	w := newEnabledWorkspace(t, root, nil)
	if err := os.WriteFile(filepath.Join(root, "restored", "notes.restored.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "restored", "restored.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := w.AutoSaveRestored("notes.restored.txt", b64("new"))
	if filepath.Base(res.Path) != "notes.restored-1.txt" {
		t.Fatalf("重名应加号：%q", res.Path)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "restored", "notes.restored.txt")); string(got) != "old" {
		t.Fatalf("已存在文件不得覆盖：%q", got)
	}
	res = w.AutoSaveRestored("restored.txt", b64("new"))
	if filepath.Base(res.Path) != "restored-1.txt" {
		t.Fatalf("回退名应支持加号：%q", res.Path)
	}
}

// --- 1.5 可用性状态与降级 ---

func TestWorkspaceDegradeDisablesAllAutoSave(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, workspaceDirCards), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := &Workspace{ops: OSFileOps{}}
	w.SetRoot(root)
	w.Init()
	if w.Enabled() {
		t.Fatal("任一目录失败应整体不可用")
	}
	pair := w.AutoSaveMask("notes.masked.txt", b64("m"), "notes.masked.mapping.json", b64("{}"))
	if pair.Masked.Status != workspaceStatusDisabled || pair.Mapping.Status != workspaceStatusDisabled {
		t.Fatalf("降级后 AutoSaveMask 应 disabled：%+v", pair)
	}
	if got := w.AutoSaveRestored("notes.restored.txt", b64("r")); got.Status != workspaceStatusDisabled {
		t.Fatalf("降级后 AutoSaveRestored 应 disabled：%+v", got)
	}
}

// MINOR-1：exe 目录解析失败不得回退 CWD 建目录，应直接不可用。
func TestWorkspaceUnresolvedExeDirDisablesWithoutCwd(t *testing.T) {
	cwd := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(prev) }()

	w := &Workspace{ops: OSFileOps{}}
	w.SetRoot("")
	w.SetExecutableDir(func() string { return "" })
	w.Init()

	if w.Enabled() {
		t.Fatal("exe 目录解析失败应判为不可用")
	}
	if w.DisabledReason() == "" || !containsHan(w.DisabledReason()) {
		t.Fatalf("应给出中文降级提示：%q", w.DisabledReason())
	}
	for _, name := range workspaceDirs {
		if _, err := os.Stat(filepath.Join(cwd, name)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("不得在 CWD 创建 %s：err=%v", name, err)
		}
	}
	if names := dirEntries(t, cwd); len(names) != 0 {
		t.Fatalf("CWD 不得被写入：%v", names)
	}
	pair := w.AutoSaveMask("notes.masked.txt", b64("m"), "notes.masked.mapping.json", b64("{}"))
	if pair.Masked.Status != workspaceStatusDisabled || pair.Mapping.Status != workspaceStatusDisabled {
		t.Fatalf("AutoSaveMask 应 disabled：%+v", pair)
	}
	if got := w.AutoSaveRestored("notes.restored.txt", b64("r")); got.Status != workspaceStatusDisabled {
		t.Fatalf("AutoSaveRestored 应 disabled：%+v", got)
	}
}

// MINOR-2：原生关闭文案随工作区可用性切换，触发条件仍仅为 HasUnsaved；文案按当前语言本地化。
func TestWorkspaceCloseMessageSwitchesWithEnabled(t *testing.T) {
	cases := []struct {
		name    string
		enabled bool
		wantID  string
	}{
		{"workspace enabled", true, "native.confirm_close_workspace"},
		{"workspace disabled", false, "native.confirm_close_legacy"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			app := NewApp(nil, nil)
			w := &Workspace{ops: OSFileOps{}}
			if c.enabled {
				w.SetRoot(t.TempDir())
			} else {
				w.SetRoot("")
				w.SetExecutableDir(func() string { return "" })
			}
			app.SetWorkspace(w)
			app.InitWorkspace(nil)
			if app.Workspace.Enabled() != c.enabled {
				t.Fatalf("前置 Enabled()=%v want %v", app.Workspace.Enabled(), c.enabled)
			}
			wantMsg := i18n.T(app.Language(), c.wantID, nil)
			var gotMsg string
			confirmCalls := 0
			app.SetConfirm(func(title, message string) bool {
				confirmCalls++
				gotMsg = message
				return true
			})
			// 触发条件不变：无未保存时不弹确认。
			if action := app.Life.RequestClose(); action != CloseAllow || confirmCalls != 0 {
				t.Fatalf("无未保存不应确认：action=%v calls=%d", action, confirmCalls)
			}
			app.Life.ResetCloseReservation()
			app.Life.MarkUnsaved()
			if action := app.Life.RequestClose(); action != CloseAllow {
				t.Fatalf("确认放弃应放行：%v", action)
			}
			if confirmCalls != 1 || gotMsg != wantMsg {
				t.Fatalf("关闭文案应为 %q（calls=%d）：%q", wantMsg, confirmCalls, gotMsg)
			}
		})
	}
}

func TestInitWorkspaceNoticeOnlyOnce(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, workspaceDirMasked), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	app := NewApp(nil, nil)
	w := &Workspace{ops: OSFileOps{}}
	w.SetRoot(root)
	app.SetWorkspace(w)

	calls := 0
	notify := func(string) { calls++ }
	app.InitWorkspace(notify)
	app.InitWorkspace(notify)
	if calls != 1 {
		t.Fatalf("降级提示应只弹一次，实际 %d", calls)
	}
}

func TestSingleWriteFailureDoesNotDisable(t *testing.T) {
	root := t.TempDir()
	ops := &linkFailOnceOps{}
	w := newEnabledWorkspace(t, root, ops)
	first := w.AutoSaveMask("notes.masked.txt", b64("m"), "notes.masked.mapping.json", b64("{}"))
	if first.Masked.Status != workspaceStatusFailed {
		t.Fatalf("首次应因注入错误失败：%+v", first)
	}
	if !w.Enabled() {
		t.Fatal("单次写失败不得翻转全局可用性")
	}
	second := w.AutoSaveMask("notes.masked.txt", b64("m"), "notes.masked.mapping.json", b64("{}"))
	if second.Masked.Status != workspaceStatusSaved {
		t.Fatalf("后续落盘仍应可尝试：%+v", second)
	}
}

// --- 注入 ops ---

type linkFailOps struct {
	OSFileOps
	failOn func(oldname, newname string) error
}

func (o *linkFailOps) Link(a, b string) error {
	if o.failOn != nil {
		if err := o.failOn(a, b); err != nil {
			return err
		}
	}
	return o.OSFileOps.Link(a, b)
}

type existOnceOps struct {
	OSFileOps
	mu   sync.Mutex
	used bool
}

func (o *existOnceOps) Link(a, b string) error {
	o.mu.Lock()
	if !o.used {
		o.used = true
		o.mu.Unlock()
		return fmt.Errorf("并发创建: %w", fs.ErrExist)
	}
	o.mu.Unlock()
	return o.OSFileOps.Link(a, b)
}

type mappingExistOnceOps struct {
	OSFileOps
	mu   sync.Mutex
	used bool
}

func (o *mappingExistOnceOps) Link(a, b string) error {
	if strings.Contains(b, string(filepath.Separator)+"mapping"+string(filepath.Separator)) {
		o.mu.Lock()
		if !o.used {
			o.used = true
			o.mu.Unlock()
			return fmt.Errorf("并发创建: %w", fs.ErrExist)
		}
		o.mu.Unlock()
	}
	return o.OSFileOps.Link(a, b)
}

type linkFailOnceOps struct {
	OSFileOps
	mu   sync.Mutex
	used bool
}

func (o *linkFailOnceOps) Link(a, b string) error {
	o.mu.Lock()
	if !o.used {
		o.used = true
		o.mu.Unlock()
		return errors.New("operation not permitted")
	}
	o.mu.Unlock()
	return o.OSFileOps.Link(a, b)
}

// GUI 只创建五目录，不读取 cards/original，也不创建/修改 pending 或任务记录。
func TestWorkspaceDoesNotReadUserDirsOrTaskState(t *testing.T) {
	root := t.TempDir()
	seed := map[string]string{
		filepath.Join(root, workspaceDirCards, "words.txt"):      "内部代号",
		filepath.Join(root, workspaceDirOriginal, "report.txt"):  "secret",
		filepath.Join(root, workspaceDirPending, "response.txt"): "reply",
		filepath.Join(root, ".agent-state", "job", "events"):     "private",
	}
	for path, content := range seed {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	w := newEnabledWorkspace(t, root, nil)
	for path, content := range seed {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != content {
			t.Fatalf("GUI 不得改动用户/任务文件 %s：%q err=%v", path, got, err)
		}
	}
	// 自动落盘只写 masked/ 与 mapping/。
	res := w.AutoSaveMask("notes.masked.txt", b64("m"), "notes.masked.mapping.json", b64("{}"))
	if res.Masked.Status != workspaceStatusSaved || res.Mapping.Status != workspaceStatusSaved {
		t.Fatalf("自动落盘应成功：%+v", res)
	}
	if names := dirEntries(t, filepath.Join(root, workspaceDirCards)); len(names) != 1 {
		t.Fatalf("cards/ 不应被自动落盘改动：%v", names)
	}
}

// --- 2.3 跨进程发布协调 ---

// 工作区锁被他处持有时成对自动落盘返回可重试忙态，且不产生文件。
func TestAutoSaveBusyWhenWorkspaceLockHeld(t *testing.T) {
	root := t.TempDir()
	w := newEnabledWorkspace(t, root, nil)
	held, err := workspace.AcquireLock(filepath.Join(root, workspace.LockFileName))
	if err != nil {
		t.Fatalf("外部获取锁失败：%v", err)
	}
	pair := w.AutoSaveMask("notes.masked.txt", b64("m"), "notes.masked.mapping.json", b64("{}"))
	if pair.Masked.Status != workspaceStatusBusy || pair.Mapping.Status != workspaceStatusBusy {
		t.Fatalf("持锁期间应为 busy：%+v", pair)
	}
	if pair.Masked.MessageID != "workspace.busy" {
		t.Fatalf("busy 应给稳定 message_id：%+v", pair.Masked)
	}
	if names := dirEntries(t, filepath.Join(root, "masked")); len(names) != 0 {
		t.Fatalf("忙态不得产生文件：%v", names)
	}
	if got := w.AutoSaveRestored("notes.restored.txt", b64("r")); got.Status != workspaceStatusBusy {
		t.Fatalf("还原自动落盘也应 busy：%+v", got)
	}
	if err := held.Release(); err != nil {
		t.Fatalf("释放失败：%v", err)
	}
	ok := w.AutoSaveMask("notes.masked.txt", b64("m"), "notes.masked.mapping.json", b64("{}"))
	if ok.Masked.Status != workspaceStatusSaved || ok.Mapping.Status != workspaceStatusSaved {
		t.Fatalf("释放后应可重试成功：%+v", ok)
	}
}

// 发布完成后锁立即释放：等待用户确认期间工作区锁可用。
func TestAutoSaveReleasesWorkspaceLock(t *testing.T) {
	root := t.TempDir()
	w := newEnabledWorkspace(t, root, nil)
	_ = w.AutoSaveMask("notes.masked.txt", b64("m"), "notes.masked.mapping.json", b64("{}"))
	lock, err := workspace.AcquireLock(filepath.Join(root, workspace.LockFileName))
	if err != nil {
		t.Fatalf("发布后锁应可获取（确认等待不持锁）：%v", err)
	}
	_ = lock.Release()
}

// 同名目标已存在时忙态不覆盖，不混配。
func TestAutoSaveBusyDoesNotMixPairs(t *testing.T) {
	root := t.TempDir()
	w := newEnabledWorkspace(t, root, nil)
	if err := os.WriteFile(filepath.Join(root, "masked", "notes.masked.txt"), []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	held, err := workspace.AcquireLock(filepath.Join(root, workspace.LockFileName))
	if err != nil {
		t.Fatal(err)
	}
	pair := w.AutoSaveMask("notes.masked.txt", b64("m"), "notes.masked.mapping.json", b64("{}"))
	if pair.Masked.Status != workspaceStatusBusy {
		t.Fatalf("应 busy：%+v", pair)
	}
	_ = held.Release()
	if got, _ := os.ReadFile(filepath.Join(root, "masked", "notes.masked.txt")); string(got) != "other" {
		t.Fatalf("不得覆盖已有目标：%q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "mapping", "notes.masked.mapping.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("忙态不得产生 mapping")
	}
}
