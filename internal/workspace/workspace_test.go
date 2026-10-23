package workspace

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDirsFixedContract(t *testing.T) {
	gui := GUIDirs()
	wantGUI := []string{"cards", "masked", "mapping", "original", "restored"}
	if len(gui) != len(wantGUI) {
		t.Fatalf("GUI 目录数量=%d want %d", len(gui), len(wantGUI))
	}
	for i, name := range wantGUI {
		if gui[i] != name {
			t.Fatalf("GUIDirs[%d]=%q want %q", i, gui[i], name)
		}
	}
	headless := HeadlessDirs()
	wantHeadless := []string{"cards", "masked", "mapping", "original", "pending", "restored"}
	if len(headless) != len(wantHeadless) {
		t.Fatalf("headless 目录数量=%d want %d", len(headless), len(wantHeadless))
	}
	for i, name := range wantHeadless {
		if headless[i] != name {
			t.Fatalf("HeadlessDirs[%d]=%q want %q", i, headless[i], name)
		}
	}
	// 返回副本：调用方修改不得污染入口集合。
	gui[0] = "changed"
	if GUIDirs()[0] != DirCards {
		t.Fatal("GUIDirs 应返回副本")
	}
}

func TestExecutableRootIndependentOfCwd(t *testing.T) {
	before, err := ExecutableRoot()
	if err != nil {
		t.Fatalf("解析当前 executable 失败：%v", err)
	}
	if !filepath.IsAbs(before) {
		t.Fatalf("根目录应为绝对路径：%q", before)
	}
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	if err := os.Chdir(other); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(prev) }()

	after, err := ExecutableRoot()
	if err != nil {
		t.Fatalf("切换 cwd 后解析失败：%v", err)
	}
	if after != before {
		t.Fatalf("根目录不应随 cwd 变化：before=%q after=%q", before, after)
	}
	if after == other {
		t.Fatalf("根目录不得取 cwd：%q", after)
	}
}

func TestRootFromPathFailuresDoNotFallBack(t *testing.T) {
	cases := []struct {
		name string
		exe  string
	}{
		{"empty", ""},
		{"missing", filepath.Join(t.TempDir(), "missing-binary")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, err := RootFromPath(c.exe)
			if err == nil || !errors.Is(err, ErrUnresolvedRoot) {
				t.Fatalf("应返回 ErrUnresolvedRoot，got root=%q err=%v", root, err)
			}
			if root != "" {
				t.Fatalf("失败时不得返回回退目录：%q", root)
			}
		})
	}
}

// 链接解析失败（断链/循环）必须明确失败，不得回退 cwd 或父目录。
func TestRootFromPathBrokenSymlinkDoesNotFallBack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 无创建符号链接权限：平台特有链接用例按平台运行")
	}
	dir := t.TempDir()
	broken := filepath.Join(dir, "broken-agent")
	if err := os.Symlink(filepath.Join(dir, "missing-target"), broken); err != nil {
		t.Fatalf("创建断链失败：%v", err)
	}
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(prev) }()

	root, err := RootFromPath(broken)
	if err == nil || !errors.Is(err, ErrUnresolvedRoot) {
		t.Fatalf("断链应返回 ErrUnresolvedRoot，got root=%q err=%v", root, err)
	}
	if root != "" {
		t.Fatalf("失败时不得回退：%q", root)
	}
}

func TestRootFromPathResolvesSymlink(t *testing.T) {
	dir := t.TempDir()
	realExe := filepath.Join(dir, "data-masking-agent")
	if err := os.WriteFile(realExe, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "agent-link")
	if err := os.Symlink(realExe, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("Windows 无创建符号链接权限：仅按平台运行链接解析用例")
		}
		t.Fatalf("创建符号链接失败：%v", err)
	}
	root, err := RootFromPath(link)
	if err != nil {
		t.Fatalf("解析链接失败：%v", err)
	}
	if root != dir {
		t.Fatalf("应解析到真实目录 %q，got %q", dir, root)
	}
}

// 已存在五目录只补 pending/，且不重建、不清空已有内容。
func TestEnsureAddsMissingPendingPreservingContent(t *testing.T) {
	root := t.TempDir()
	existing := []string{DirCards, DirMasked, DirMapping, DirOriginal, DirRestored}
	for _, name := range existing {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		keep := filepath.Join(dir, name+"-keep.txt")
		if err := os.WriteFile(keep, []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := EnsureHeadless(root); err != nil {
		t.Fatalf("Ensure 失败：%v", err)
	}
	for _, name := range HeadlessDirs() {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil || !info.IsDir() {
			t.Fatalf("%s 应为目录：err=%v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, DirPending)); err != nil {
		t.Fatalf("应补建 pending/：%v", err)
	}
	for _, name := range existing {
		got, err := os.ReadFile(filepath.Join(root, name, name+"-keep.txt"))
		if err != nil || string(got) != name {
			t.Fatalf("%s 已有内容不得清空：%q err=%v", name, got, err)
		}
	}
}

// 固定目录被普通文件占位即不可用，且不创建/不写入其它布局。
func TestEnsureRejectsFilePlaceholder(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, DirMasked), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := EnsureHeadless(root)
	if err == nil || !errors.Is(err, ErrUnsafeDir) {
		t.Fatalf("应返回 ErrUnsafeDir，got %v", err)
	}
	var de *DirError
	if !errors.As(err, &de) || de.Name != DirMasked {
		t.Fatalf("应指出 masked 目录：%v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(root, DirMasked)); string(got) != "not a dir" {
		t.Fatalf("占位文件不得被覆盖：%q", got)
	}
}

// 固定目录是符号链接时不可用，且不跟随链接写入。
func TestEnsureRejectsSymlinkDirWithoutFollowing(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, DirMapping)
	if err := os.Symlink(outside, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("Windows 无创建符号链接权限：平台特有 reparse 用例按平台运行")
		}
		t.Fatalf("创建符号链接失败：%v", err)
	}
	err := EnsureHeadless(root)
	if err == nil || !errors.Is(err, ErrUnsafeDir) {
		t.Fatalf("应返回 ErrUnsafeDir，got %v", err)
	}
	if names, _ := os.ReadDir(outside); len(names) != 0 {
		t.Fatalf("不得跟随链接写入目标：%v", names)
	}
}

// EnsureGUI 自身只负责五目录自动保存判定所需集合，不附带创建 pending/ 或 .agent-state；
// GUI 的 pending/ 由 EnsureDir 独立补建（见 TestEnsureDir... 用例）。
func TestEnsureGUICreatesOnlyFiveDirs(t *testing.T) {
	root := t.TempDir()
	if err := EnsureGUI(root); err != nil {
		t.Fatalf("EnsureGUI 失败：%v", err)
	}
	for _, name := range GUIDirs() {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil || !info.IsDir() {
			t.Fatalf("%s 应为目录：err=%v", name, err)
		}
	}
	for _, absent := range []string{DirPending, AgentStateDir} {
		if _, err := os.Lstat(filepath.Join(root, absent)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("GUI 不应创建 %s：err=%v", absent, err)
		}
	}
}

// pending/ 缺失、被普通文件占位或被符号链接占位都不影响 GUI 五目录初始化，
// 且 EnsureGUI 不修改该路径或 headless 私有任务记录。
func TestEnsureGUIIgnoresPendingAndAgentState(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, root string)
		check func(t *testing.T, root string)
		skip  bool
	}{
		{
			name: "pending 普通文件",
			setup: func(t *testing.T, root string) {
				if err := os.WriteFile(filepath.Join(root, DirPending), []byte("occupied"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, root string) {
				got, err := os.ReadFile(filepath.Join(root, DirPending))
				if err != nil || string(got) != "occupied" {
					t.Fatalf("GUI 不得修改 pending 占位文件：%q err=%v", got, err)
				}
			},
		},
		{
			name: "pending 符号链接",
			setup: func(t *testing.T, root string) {
				outside := t.TempDir()
				if err := os.Symlink(outside, filepath.Join(root, DirPending)); err != nil {
					t.Fatalf("创建符号链接失败：%v", err)
				}
			},
			check: func(t *testing.T, root string) {
				info, err := os.Lstat(filepath.Join(root, DirPending))
				if err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("GUI 不得替换 pending 符号链接：%v err=%v", info, err)
				}
			},
			skip: runtime.GOOS == "windows",
		},
		{
			name: "已有 pending 与任务记录",
			setup: func(t *testing.T, root string) {
				pendingFile := filepath.Join(root, DirPending, "job", "response.txt")
				stateFile := filepath.Join(root, AgentStateDir, "job", "events.jsonl")
				for path, content := range map[string]string{pendingFile: "reply", stateFile: "private"} {
					if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			},
			check: func(t *testing.T, root string) {
				for path, want := range map[string]string{
					filepath.Join(root, DirPending, "job", "response.txt"):    "reply",
					filepath.Join(root, AgentStateDir, "job", "events.jsonl"): "private",
				} {
					got, err := os.ReadFile(path)
					if err != nil || string(got) != want {
						t.Fatalf("GUI 不得改动历史内容 %s：%q err=%v", path, got, err)
					}
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
			if err := EnsureGUI(root); err != nil {
				t.Fatalf("pending 占位不得影响 GUI：%v", err)
			}
			for _, name := range GUIDirs() {
				info, err := os.Stat(filepath.Join(root, name))
				if err != nil || !info.IsDir() {
					t.Fatalf("%s 应为目录：err=%v", name, err)
				}
			}
			c.check(t, root)
		})
	}
}

// GUI 必需目录是符号链接时同样不可用，且不跟随链接写入。
func TestEnsureGUIRejectsRequiredSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 无创建符号链接权限：平台特有 reparse 用例按平台运行")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, DirMapping)); err != nil {
		t.Fatalf("创建符号链接失败：%v", err)
	}
	err := EnsureGUI(root)
	if err == nil || !errors.Is(err, ErrUnsafeDir) {
		t.Fatalf("GUI 应拒绝必需目录符号链接，got %v", err)
	}
	var de *DirError
	if !errors.As(err, &de) || de.Name != DirMapping {
		t.Fatalf("应指出 mapping 目录：%v", err)
	}
	if names, _ := os.ReadDir(outside); len(names) != 0 {
		t.Fatalf("不得跟随链接写入目标：%v", names)
	}
}

// EnsureDir 是入口无关的单目录安全 helper（GUI pending/ 使用）：幂等补建、
// 已有内容保留，非目录/链接/reparse 占位拒绝，空 root 明确失败。
func TestEnsureDirSingleDirSafety(t *testing.T) {
	t.Run("补建并保留已有内容", func(t *testing.T) {
		root := t.TempDir()
		if err := EnsureDir(root, DirPending); err != nil {
			t.Fatalf("EnsureDir 失败：%v", err)
		}
		assertIsDir(t, filepath.Join(root, DirPending))
		keep := filepath.Join(root, DirPending, "notes.txt")
		if err := os.WriteFile(keep, []byte("user"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := EnsureDir(root, DirPending); err != nil {
			t.Fatalf("重复调用应幂等：%v", err)
		}
		if got, err := os.ReadFile(keep); err != nil || string(got) != "user" {
			t.Fatalf("已有内容不得清空：%q err=%v", got, err)
		}
		// 不附带创建其它固定目录或私有任务记录。
		for _, absent := range []string{DirCards, AgentStateDir} {
			if _, err := os.Lstat(filepath.Join(root, absent)); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("EnsureDir 不应创建 %s：err=%v", absent, err)
			}
		}
	})

	t.Run("普通文件占位拒绝且不覆盖", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, DirPending)
		if err := os.WriteFile(path, []byte("occupied"), 0o644); err != nil {
			t.Fatal(err)
		}
		err := EnsureDir(root, DirPending)
		if err == nil || !errors.Is(err, ErrUnsafeDir) {
			t.Fatalf("应返回 ErrUnsafeDir，got %v", err)
		}
		var de *DirError
		if !errors.As(err, &de) || de.Name != DirPending {
			t.Fatalf("应指出 pending 目录：%v", err)
		}
		if got, _ := os.ReadFile(path); string(got) != "occupied" {
			t.Fatalf("占位不得被替换：%q", got)
		}
	})

	t.Run("符号链接占位拒绝且不跟随", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows 无创建符号链接权限：平台特有 reparse 用例按平台运行")
		}
		root := t.TempDir()
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(root, DirPending)); err != nil {
			t.Fatalf("创建符号链接失败：%v", err)
		}
		err := EnsureDir(root, DirPending)
		if err == nil || !errors.Is(err, ErrUnsafeDir) {
			t.Fatalf("应返回 ErrUnsafeDir，got %v", err)
		}
		if names, _ := os.ReadDir(outside); len(names) != 0 {
			t.Fatalf("不得跟随链接写入：%v", names)
		}
	})

	t.Run("空 root 或空目录名明确失败", func(t *testing.T) {
		if err := EnsureDir("", DirPending); !errors.Is(err, ErrUnresolvedRoot) {
			t.Fatalf("空 root 应返回 ErrUnresolvedRoot，got %v", err)
		}
		if err := EnsureDir(t.TempDir(), ""); !errors.Is(err, ErrUnsafeDir) {
			t.Fatalf("空目录名应返回 ErrUnsafeDir，got %v", err)
		}
	})
}

func assertIsDir(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		t.Fatalf("%s 应为目录：err=%v", path, err)
	}
}

// headless 入口要求六目录：pending/ 被普通文件或符号链接占位时必须拒绝。
func TestEnsureHeadlessRejectsPendingPlaceholder(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, root string)
		skip  bool
	}{
		{
			name: "普通文件",
			setup: func(t *testing.T, root string) {
				if err := os.WriteFile(filepath.Join(root, DirPending), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "符号链接",
			setup: func(t *testing.T, root string) {
				if err := os.Symlink(t.TempDir(), filepath.Join(root, DirPending)); err != nil {
					t.Fatalf("创建符号链接失败：%v", err)
				}
			},
			skip: runtime.GOOS == "windows",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.skip {
				t.Skip("Windows 无创建符号链接权限：平台特有 reparse 用例按平台运行")
			}
			root := t.TempDir()
			c.setup(t, root)
			err := EnsureHeadless(root)
			if err == nil || !errors.Is(err, ErrUnsafeDir) {
				t.Fatalf("headless 应拒绝 pending 占位，got %v", err)
			}
			var de *DirError
			if !errors.As(err, &de) || de.Name != DirPending {
				t.Fatalf("应指出 pending 目录：%v", err)
			}
		})
	}
}

// 同址部署：GUI 启动先建五目录并由 EnsureDir 补 pending/，headless 随后沿用；
// 反过来 headless 的 pending 与任务记录不被 GUI 删除，已有内容保持不变。
func TestSameRootGUIToHeadlessKeepsHistory(t *testing.T) {
	root := t.TempDir()
	if err := EnsureGUI(root); err != nil {
		t.Fatalf("EnsureGUI 失败：%v", err)
	}
	// 桌面入口另以 EnsureDir 独立补建 pending/。
	if err := EnsureDir(root, DirPending); err != nil {
		t.Fatalf("GUI pending 补建失败：%v", err)
	}
	keep := filepath.Join(root, DirMasked, "keep.txt")
	if err := os.WriteFile(keep, []byte("masked"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureHeadless(root); err != nil {
		t.Fatalf("同址 headless 应成功：%v", err)
	}
	for _, name := range HeadlessDirs() {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil || !info.IsDir() {
			t.Fatalf("%s 应为目录：err=%v", name, err)
		}
	}
	if got, _ := os.ReadFile(keep); string(got) != "masked" {
		t.Fatalf("同址初始化不得清空已有内容：%q", got)
	}
	history := filepath.Join(root, DirPending, "old-job", "response.txt")
	if err := os.MkdirAll(filepath.Dir(history), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(history, []byte("history"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureGUI(root); err != nil {
		t.Fatalf("再次启动 GUI 失败：%v", err)
	}
	if got, err := os.ReadFile(history); err != nil || string(got) != "history" {
		t.Fatalf("GUI 不得删除/修改已有 pending 历史：%q err=%v", got, err)
	}
}

// 分址部署：两个入口各自独立；GUI 在自己的 root 下补五目录与 pending，
// 不读取也不补建 headless root 的任务记录。
func TestSeparateRootsStayIndependent(t *testing.T) {
	guiRoot := t.TempDir()
	agentRoot := t.TempDir()
	if err := EnsureGUI(guiRoot); err != nil {
		t.Fatalf("EnsureGUI 失败：%v", err)
	}
	if err := EnsureDir(guiRoot, DirPending); err != nil {
		t.Fatalf("GUI pending 补建失败：%v", err)
	}
	if err := EnsureHeadless(agentRoot); err != nil {
		t.Fatalf("EnsureHeadless 失败：%v", err)
	}
	if _, err := os.Stat(filepath.Join(guiRoot, DirPending)); err != nil {
		t.Fatalf("GUI root 应有自己的 pending：%v", err)
	}
	if _, err := os.Lstat(filepath.Join(agentRoot, DirPending)); err != nil {
		t.Fatalf("headless root 应有 pending：%v", err)
	}
	if _, err := os.Lstat(filepath.Join(agentRoot, AgentStateDir)); err == nil {
		t.Fatal("EnsureHeadless 不应创建 .agent-state（由会话打开时创建）")
	}
}

// 幂等启动：重复 Ensure 不清空既有内容。
func TestEnsureIdempotent(t *testing.T) {
	root := t.TempDir()
	if err := EnsureHeadless(root); err != nil {
		t.Fatalf("首次 Ensure 失败：%v", err)
	}
	keep := filepath.Join(root, DirRestored, "keep.txt")
	if err := os.WriteFile(keep, []byte("user"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureHeadless(root); err != nil {
		t.Fatalf("再次 Ensure 失败：%v", err)
	}
	if got, err := os.ReadFile(keep); err != nil || string(got) != "user" {
		t.Fatalf("二次启动不得清空内容：%q err=%v", got, err)
	}
}
