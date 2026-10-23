package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recordedCall struct {
	name string
	args []string
	env  []string
	dir  string
}

func newTestBuilder(t *testing.T, hostOS, hostArch string, env []string, run commandRunner) (*builder, *[]recordedCall) {
	t.Helper()
	calls := &[]recordedCall{}
	wrapped := func(name string, args, e []string, dir string) ([]byte, error) {
		*calls = append(*calls, recordedCall{
			name: name,
			args: append([]string(nil), args...),
			env:  append([]string(nil), e...),
			dir:  dir,
		})
		return run(name, args, e, dir)
	}
	return &builder{
		root:     t.TempDir(),
		hostOS:   hostOS,
		hostArch: hostArch,
		environ:  env,
		lookPath: func(string) (string, error) { return "wails-fake", nil },
		run:      wrapped,
		stdout:   io.Discard,
		stderr:   io.Discard,
	}, calls
}

func argValue(args []string, flag string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

func callCount(calls *[]recordedCall, name string, arg0 string) int {
	n := 0
	for _, c := range *calls {
		if c.name == name && (arg0 == "" || (len(c.args) > 0 && c.args[0] == arg0)) {
			n++
		}
	}
	return n
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeSuccessRunner 模拟 genicon 与 wails CLI：生成预期产物，并在 Windows 目标构建时
// 遗留临时 .syso 以验证构建入口的清理逻辑。
func fakeSuccessRunner(t *testing.T) commandRunner {
	t.Helper()
	return func(name string, args, env []string, dir string) ([]byte, error) {
		switch {
		case name == "wails-fake":
			if len(args) == 1 && args[0] == "version" {
				return []byte("v2.11.0\n"), nil
			}
			if len(args) > 0 && args[0] == "build" {
				out := argValue(args, "-o")
				if out == "" {
					return nil, fmt.Errorf("fake wails: 缺少 -o")
				}
				mustWrite(t, filepath.Join(dir, "build", "bin", out), []byte("built:"+out))
				if argValue(args, "-platform") == "windows/amd64" {
					mustWrite(t, filepath.Join(dir, sysoName), []byte("leftover-syso"))
				}
				return nil, nil
			}
		case name == "go" && len(args) >= 2 && args[0] == "run" && args[1] == "./scripts/genicon":
			mustWrite(t, filepath.Join(dir, "build", "appicon.png"), []byte("png"))
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected command: %s %v", name, args)
	}
}

// --- 目标解析 ---

func TestResolveTargetMatrix(t *testing.T) {
	cases := []struct {
		platform, hostOS, hostArch, wantOS string
		wantErr                            bool
	}{
		{"", "linux", "amd64", "linux", false},
		{"linux/amd64", "linux", "amd64", "linux", false},
		{"windows/amd64", "linux", "amd64", "windows", false},
		{"", "windows", "amd64", "windows", false},
		{"windows/amd64", "windows", "amd64", "windows", false},
		{"linux/amd64", "windows", "amd64", "", true},
		{"windows/arm64", "linux", "amd64", "", true},
		{"darwin/amd64", "linux", "amd64", "", true},
		{"", "darwin", "amd64", "", true},
		{"windows/amd64", "darwin", "amd64", "", true},
		{"", "linux", "arm64", "", true},
		{"", "windows", "386", "", true},
	}
	for _, tc := range cases {
		got, err := resolveTarget(tc.platform, tc.hostOS, tc.hostArch)
		if tc.wantErr {
			if err == nil {
				t.Errorf("resolveTarget(%q,%s/%s) 应报错", tc.platform, tc.hostOS, tc.hostArch)
			}
			continue
		}
		if err != nil {
			t.Errorf("resolveTarget(%q,%s/%s) 意外错误：%v", tc.platform, tc.hostOS, tc.hostArch, err)
			continue
		}
		if got.OS != tc.wantOS || got.Arch != "amd64" {
			t.Errorf("resolveTarget(%q,%s/%s)=%+v，期望 %s/amd64", tc.platform, tc.hostOS, tc.hostArch, got, tc.wantOS)
		}
	}
}

func TestBuildArgsTargets(t *testing.T) {
	win, err := buildArgs(buildTarget{OS: "windows", Arch: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(win, " ")
	for _, want := range []string{"build", "-trimpath", "-platform windows/amd64", "-tags desktop", "-webview2 browser", "-o data-masking.exe"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Windows 参数缺少 %q：%v", want, win)
		}
	}
	if strings.Contains(joined, "linux/amd64") {
		t.Errorf("Windows 参数不应含 linux：%v", win)
	}

	lin, err := buildArgs(buildTarget{OS: "linux", Arch: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	joined = strings.Join(lin, " ")
	for _, want := range []string{"build", "-trimpath", "-platform linux/amd64", "-tags desktop,webkit2_41", "-o data-masking"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Linux 参数缺少 %q：%v", want, lin)
		}
	}
	for _, banned := range []string{"-skipbindings", "-nopackage", "-webview2"} {
		if strings.Contains(joined, banned) {
			t.Errorf("Linux 参数不应含 %q：%v", banned, lin)
		}
	}
}

// 两个 GUI 目标都必须显式启用 trimpath，且只能出现一次；headless 同样保留 -trimpath。
// 只断言 tags/输出名会漏掉“路径清理被移除”的回归。
func TestBuildArgsEnableTrimPathForBothTargets(t *testing.T) {
	for _, tc := range []buildTarget{
		{OS: "windows", Arch: "amd64"},
		{OS: "linux", Arch: "amd64"},
	} {
		args, err := buildArgs(tc)
		if err != nil {
			t.Fatalf("%s/%s: %v", tc.OS, tc.Arch, err)
		}
		count := 0
		for _, a := range args {
			if a == "-trimpath" {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("%s/%s GUI 参数应恰好包含一个 -trimpath，实际 %d：%v", tc.OS, tc.Arch, count, args)
		}
		if !hasToken(args, "-trimpath") {
			t.Fatalf("%s/%s GUI 参数应为独立 -trimpath token：%v", tc.OS, tc.Arch, args)
		}
		headless := headlessBuildArgs(tc, "1.2.0", "out")
		if !hasToken(headless, "-trimpath") {
			t.Fatalf("%s/%s headless 参数应保留 -trimpath：%v", tc.OS, tc.Arch, headless)
		}
	}
}

func TestWailsEnvIgnoresExternalConflicts(t *testing.T) {
	base := []string{"HOME=/root", "GOOS=darwin", "GOARCH=arm64", "CGO_ENABLED=1", "CC=clang"}
	orig := append([]string(nil), base...)

	win := wailsEnv(base, buildTarget{OS: "windows", Arch: "amd64"})
	assertNoEnvKey(t, win, "GOOS")
	assertNoEnvKey(t, win, "GOARCH")
	if got := envValue(win, "CGO_ENABLED"); got != "0" {
		t.Errorf("Windows 目标 CGO_ENABLED 应为 0，实际 %q", got)
	}
	if got := envValue(win, "CC"); got != "clang" {
		t.Errorf("无关环境变量应保留，CC=%q", got)
	}

	lin := wailsEnv(base, buildTarget{OS: "linux", Arch: "amd64"})
	if got := envValue(lin, "CGO_ENABLED"); got != "1" {
		t.Errorf("Linux 目标 CGO_ENABLED 应为 1，实际 %q", got)
	}

	if !equalStrings(base, orig) {
		t.Errorf("输入环境不应被修改：%v", base)
	}
}

func TestHostEnvDropsGOOSAndGOARCH(t *testing.T) {
	got := hostEnv([]string{"GOOS=windows", "GOARCH=386", "PATH=/bin"})
	assertNoEnvKey(t, got, "GOOS")
	assertNoEnvKey(t, got, "GOARCH")
	if envValue(got, "PATH") != "/bin" {
		t.Errorf("PATH 应保留：%v", got)
	}
}

// --- 构建编排 ---

func TestBuildWindowsSuccessPublishesAndCleans(t *testing.T) {
	env := []string{"PATH=/bin", "GOOS=darwin", "GOARCH=arm64", "CGO_ENABLED=1"}
	b, calls := newTestBuilder(t, "linux", "amd64", env, fakeSuccessRunner(t))
	var out bytes.Buffer
	b.stdout = &out

	// 旧派生 ICO 与手工资源。
	mustWrite(t, filepath.Join(b.root, "build", "windows", "icon.ico"), []byte("old-three-bars"))
	mustWrite(t, filepath.Join(b.root, "build", "windows", "wails.exe.manifest"), []byte("manifest-keep"))
	mustWrite(t, filepath.Join(b.root, "build", "windows", "info.json"), []byte("version-keep"))

	if err := b.build("windows/amd64"); err != nil {
		t.Fatalf("构建应成功：%v", err)
	}

	if got := readFile(t, filepath.Join(b.root, "dist", "windows-amd64", "data-masking.exe")); got != "built:data-masking.exe" {
		t.Errorf("dist 产物内容错误：%q", got)
	}
	if _, err := os.Lstat(filepath.Join(b.root, sysoName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("构建结束应无临时资源残留，err=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(b.root, "build", "windows", "icon.ico")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("旧派生 ICO 应被刷新移除，err=%v", err)
	}
	if readFile(t, filepath.Join(b.root, "build", "windows", "wails.exe.manifest")) != "manifest-keep" {
		t.Error("手工 manifest 不应被删除")
	}
	if readFile(t, filepath.Join(b.root, "build", "windows", "info.json")) != "version-keep" {
		t.Error("手工 info.json 不应被删除")
	}
	if !strings.Contains(out.String(), "完成") {
		t.Errorf("成功应输出完成提示：%q", out.String())
	}

	// 图案生成与构建调用。
	if callCount(calls, "go", "run") != 1 {
		t.Fatalf("应调用 genicon 一次：%v", calls)
	}
	if callCount(calls, "wails-fake", "build") != 1 {
		t.Fatalf("应调用 wails build 一次：%v", calls)
	}
	// 图案生成使用主机环境：不含目标 GOOS/GOARCH。
	for _, c := range *calls {
		if c.name == "go" && len(c.args) > 0 && c.args[0] == "run" {
			assertNoEnvKey(t, c.env, "GOOS")
			assertNoEnvKey(t, c.env, "GOARCH")
		}
		if c.name == "wails-fake" && len(c.args) > 0 && c.args[0] == "build" {
			if envValue(c.env, "CGO_ENABLED") != "0" {
				t.Errorf("Windows 构建子进程 CGO_ENABLED 应为 0：%v", c.env)
			}
		}
	}
	if b.environ[1] != "GOOS=darwin" {
		t.Errorf("调用者环境被修改：%v", b.environ)
	}
}

func TestBuildNativeLinuxKeepsWindowsIconAndBuildsNoExe(t *testing.T) {
	b, calls := newTestBuilder(t, "linux", "amd64", []string{"PATH=/bin"}, fakeSuccessRunner(t))
	mustWrite(t, filepath.Join(b.root, "build", "windows", "icon.ico"), []byte("keep"))

	if err := b.build(""); err != nil {
		t.Fatalf("原生构建应成功：%v", err)
	}
	if _, err := os.Stat(filepath.Join(b.root, "dist", "linux-amd64", "data-masking")); err != nil {
		t.Errorf("应产出 dist/linux-amd64/data-masking：%v", err)
	}
	if _, err := os.Lstat(filepath.Join(b.root, "dist", "windows-amd64", "data-masking.exe")); !errors.Is(err, os.ErrNotExist) {
		t.Error("Linux 原生构建不应产出 .exe")
	}
	if _, err := os.Lstat(filepath.Join(b.root, "dist", "data-masking")); !errors.Is(err, os.ErrNotExist) {
		t.Error("不应向 dist 顶层写发布产物")
	}
	if readFile(t, filepath.Join(b.root, "build", "windows", "icon.ico")) != "keep" {
		t.Error("Linux 目标不应触碰 Windows 派生 ICO")
	}
	for _, c := range *calls {
		if c.name == "wails-fake" && len(c.args) > 0 && c.args[0] == "build" {
			if envValue(c.env, "CGO_ENABLED") != "1" {
				t.Errorf("Linux 构建 CGO_ENABLED 应为 1：%v", c.env)
			}
			if argValue(c.args, "-platform") != "linux/amd64" {
				t.Errorf("应显式传递 linux 目标：%v", c.args)
			}
		}
	}
}

func TestBuildWindowsHostNativeDefault(t *testing.T) {
	b, _ := newTestBuilder(t, "windows", "amd64", []string{"PATH=/bin"}, fakeSuccessRunner(t))
	var out bytes.Buffer
	b.stdout = &out
	if err := b.build(""); err != nil {
		t.Fatalf("Windows 主机原生构建应成功：%v", err)
	}
	if readFile(t, filepath.Join(b.root, "dist", "windows-amd64", "data-masking.exe")) != "built:data-masking.exe" {
		t.Error("Windows 默认目标应产出 exe")
	}
}

func TestBuildRejectsInvalidTargetBeforeAnyWrite(t *testing.T) {
	cases := []struct {
		platform, hostOS string
	}{
		{"windows/arm64", "linux"},
		{"darwin/amd64", "linux"},
		{"linux/amd64", "windows"},
	}
	for _, mode := range []string{"gui", "headless", "bundle"} {
		for _, tc := range cases {
			b, calls := newTestBuilder(t, tc.hostOS, "amd64", []string{"PATH=/bin"}, fakeSuccessRunner(t))
			writeWailsJSON(t, b.root, "1.0.0")
			var err error
			switch mode {
			case "gui":
				err = b.build(tc.platform)
			case "headless":
				err = b.buildHeadlessOnly(tc.platform)
			case "bundle":
				err = b.buildBundle(tc.platform)
			}
			if err == nil {
				t.Errorf("mode=%s host=%s platform=%s 应被拒绝", mode, tc.hostOS, tc.platform)
			}
			if len(*calls) != 0 {
				t.Errorf("mode=%s 非法目标不应执行任何命令：%v", mode, *calls)
			}
			if _, statErr := os.Lstat(filepath.Join(b.root, "build", "appicon.png")); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("mode=%s 非法目标不应生成图案，err=%v", mode, statErr)
			}
			if _, statErr := os.Lstat(filepath.Join(b.root, "dist")); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("mode=%s 非法目标不应创建 dist，err=%v", mode, statErr)
			}
		}
	}
}

// 三模式 × 两目标均只写 dist/<os>-<arch>，不向顶层写发布文件；目录由已校验目标决定，
// 外部冲突环境不改变目标目录，也不修改调用者环境。
func TestThreeModesPublishOnlyToPlatformDirs(t *testing.T) {
	cases := []struct {
		name, mode, platform, hostOS string
		env                          []string
		wantDir                      string
		wantFiles                    []string
	}{
		{"gui-linux-host", "gui", "", "linux", []string{"PATH=/bin"}, "linux-amd64", []string{"data-masking"}},
		{"gui-windows-cross", "gui", "windows/amd64", "linux", []string{"PATH=/bin", "GOOS=darwin", "GOARCH=arm64", "CGO_ENABLED=1"}, "windows-amd64", []string{"data-masking.exe"}},
		{"gui-windows-native", "gui", "", "windows", []string{"PATH=/bin"}, "windows-amd64", []string{"data-masking.exe"}},
		{"headless-linux", "headless", "", "linux", []string{"PATH=/bin"}, "linux-amd64", []string{"data-masking-agent"}},
		{"headless-windows-cross", "headless", "windows/amd64", "linux", []string{"PATH=/bin"}, "windows-amd64", []string{"data-masking-agent.exe"}},
		{"headless-windows-native", "headless", "", "windows", []string{"PATH=/bin"}, "windows-amd64", []string{"data-masking-agent.exe"}},
		{"bundle-linux", "bundle", "", "linux", []string{"PATH=/bin"}, "linux-amd64", []string{"data-masking", "data-masking-agent", "release.json"}},
		{"bundle-windows-cross", "bundle", "windows/amd64", "linux", []string{"PATH=/bin"}, "windows-amd64", []string{"data-masking.exe", "data-masking-agent.exe", "release.json"}},
		{"bundle-windows-native", "bundle", "", "windows", []string{"PATH=/bin"}, "windows-amd64", []string{"data-masking.exe", "data-masking-agent.exe", "release.json"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := append([]string(nil), tc.env...)
			orig := append([]string(nil), env...)
			b, _ := newTestBuilder(t, tc.hostOS, "amd64", env, headlessFakeRunner(t, false))
			writeWailsJSON(t, b.root, "1.2.3")

			var err error
			switch tc.mode {
			case "gui":
				err = b.build(tc.platform)
			case "headless":
				err = b.buildHeadlessOnly(tc.platform)
			case "bundle":
				err = b.buildBundle(tc.platform)
			default:
				t.Fatalf("未知模式 %q", tc.mode)
			}
			if err != nil {
				t.Fatalf("mode=%s platform=%q 应成功：%v", tc.mode, tc.platform, err)
			}

			dir := filepath.Join(b.root, "dist", tc.wantDir)
			for _, name := range tc.wantFiles {
				if _, statErr := os.Stat(filepath.Join(dir, name)); statErr != nil {
					t.Errorf("应在 %s 生成 %s：%v", dir, name, statErr)
				}
			}
			entries, readErr := os.ReadDir(filepath.Join(b.root, "dist"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			for _, e := range entries {
				if !e.IsDir() {
					t.Errorf("dist 顶层不应有发布文件 %s", e.Name())
				}
				if e.Name() != tc.wantDir {
					t.Errorf("不应生成其他平台目录 %s", e.Name())
				}
			}
			if !equalStrings(env, orig) {
				t.Errorf("调用者环境被修改：%v", env)
			}
		})
	}
}

// 单入口构建不新建/改写 bundle 清单；清单仅在 bundle 成功时写在其平台目录。
func TestSingleEntryDoesNotCreateOrRewriteManifest(t *testing.T) {
	guiContent := "gui-v1"
	agentContent := "agent-v1"
	run := func(name string, args, env []string, dir string) ([]byte, error) {
		switch {
		case name == "wails-fake":
			if len(args) == 1 && args[0] == "version" {
				return []byte("v2.11.0\n"), nil
			}
			if len(args) > 0 && args[0] == "build" {
				mustWrite(t, filepath.Join(dir, "build", "bin", argValue(args, "-o")), []byte(guiContent))
				return nil, nil
			}
		case name == "go" && len(args) > 1 && args[0] == "run" && args[1] == "./scripts/genicon":
			mustWrite(t, filepath.Join(dir, "build", "appicon.png"), []byte("png"))
			return nil, nil
		case name == "go" && len(args) > 0 && args[0] == "build":
			mustWrite(t, argValue(args, "-o"), []byte(agentContent))
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected command: %s %v", name, args)
	}
	b, _ := newTestBuilder(t, "linux", "amd64", []string{"PATH=/bin"}, run)
	writeWailsJSON(t, b.root, "1.0.0")

	if err := b.buildBundle(""); err != nil {
		t.Fatalf("bundle 应成功：%v", err)
	}
	manifestPath := filepath.Join(b.root, "dist", "linux-amd64", "release.json")
	before := readFile(t, manifestPath)

	guiContent = "gui-v2"
	if err := b.build(""); err != nil {
		t.Fatalf("GUI 单入口应成功：%v", err)
	}
	if got := readFile(t, manifestPath); got != before {
		t.Error("单入口 GUI 构建不应改写 release.json")
	}

	agentContent = "agent-v2"
	if err := b.buildHeadlessOnly(""); err != nil {
		t.Fatalf("headless 单入口应成功：%v", err)
	}
	if got := readFile(t, manifestPath); got != before {
		t.Error("单入口 headless 构建不应改写 release.json")
	}

	if _, err := os.Lstat(filepath.Join(b.root, "dist", "release.json")); !errors.Is(err, os.ErrNotExist) {
		t.Error("不应在 dist 顶层生成清单")
	}
}

func TestBuildRejectsWrongWailsVersion(t *testing.T) {
	for _, version := range []string{"v2.10.0\n", "v2.11.0-rc1\n", "v2.11.1\n"} {
		run := func(name string, args, env []string, dir string) ([]byte, error) {
			if name == "wails-fake" && len(args) == 1 && args[0] == "version" {
				return []byte(version), nil
			}
			return nil, fmt.Errorf("不应执行：%s %v", name, args)
		}
		b, calls := newTestBuilder(t, "linux", "amd64", []string{"PATH=/bin"}, run)
		if err := b.build("windows/amd64"); err == nil {
			t.Errorf("版本 %q 应失败", strings.TrimSpace(version))
		}
		if callCount(calls, "go", "run") != 0 || callCount(calls, "wails-fake", "build") != 0 {
			t.Errorf("版本校验失败后不应继续构建（%q）：%v", strings.TrimSpace(version), *calls)
		}
	}
}

func TestCheckWailsVersionExactMatch(t *testing.T) {
	cases := []struct {
		name    string
		output  string
		wantErr bool
	}{
		{"exact", "v2.11.0\n", false},
		{"ansi-wrapped", "\x1b[92mv2.11.0\x1b[0m\n", false},
		{"trailing-banner", "v2.11.0\nsponsor https://example.test\n", false},
		{"release-candidate", "v2.11.0-rc1\n", true},
		{"different-patch", "v2.11.1\n", true},
		{"different-minor", "v2.10.0\n", true},
		{"prefixed-word", "Wails CLI v2.11.0\n", true},
		{"empty", "\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := func(name string, args, env []string, dir string) ([]byte, error) {
				return []byte(tc.output), nil
			}
			b, _ := newTestBuilder(t, "linux", "amd64", []string{"PATH=/bin"}, run)
			err := b.checkWailsVersion("wails-fake")
			if tc.wantErr && err == nil {
				t.Fatalf("输出 %q 应被拒绝", tc.output)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("输出 %q 应通过：%v", tc.output, err)
			}
		})
	}
}

func TestBuildRejectsPreExistingSyso(t *testing.T) {
	b, calls := newTestBuilder(t, "linux", "amd64", []string{"PATH=/bin"}, fakeSuccessRunner(t))
	syso := filepath.Join(b.root, sysoName)
	mustWrite(t, syso, []byte("user-content"))

	if err := b.build("windows/amd64"); err == nil {
		t.Fatal("预存临时资源应拒绝构建")
	}
	if readFile(t, syso) != "user-content" {
		t.Error("预存临时资源内容不应被改动")
	}
	if callCount(calls, "go", "run") != 0 || callCount(calls, "wails-fake", "build") != 0 {
		t.Errorf("预存保护应在生成图案前生效：%v", *calls)
	}
}

func TestBuildRejectsPreExistingSysoDirectoryAndSymlink(t *testing.T) {
	t.Run("directory", func(t *testing.T) {
		b, _ := newTestBuilder(t, "linux", "amd64", []string{"PATH=/bin"}, fakeSuccessRunner(t))
		if err := os.MkdirAll(filepath.Join(b.root, sysoName), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := b.build("windows/amd64"); err == nil {
			t.Error("目录形式的预存资源应被拒绝")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		b, _ := newTestBuilder(t, "linux", "amd64", []string{"PATH=/bin"}, fakeSuccessRunner(t))
		if err := os.Symlink(filepath.Join(b.root, "target"), filepath.Join(b.root, sysoName)); err != nil {
			t.Skipf("无法创建符号链接（无权限或不支持），跳过该特定用例：%v", err)
		}
		if err := b.build("windows/amd64"); err == nil {
			t.Error("符号链接形式的预存资源应被拒绝")
		}
	})
}

func TestBuildFailureRemovesSysoAndKeepsOldDist(t *testing.T) {
	run := func(name string, args, env []string, dir string) ([]byte, error) {
		switch {
		case name == "wails-fake" && len(args) == 1 && args[0] == "version":
			return []byte("v2.11.0\n"), nil
		case name == "go" && len(args) >= 2 && args[0] == "run":
			mustWrite(t, filepath.Join(dir, "build", "appicon.png"), []byte("png"))
			return nil, nil
		case name == "wails-fake" && len(args) > 0 && args[0] == "build":
			// 模拟打包已生成资源后编译失败。
			mustWrite(t, filepath.Join(dir, sysoName), []byte("leftover"))
			return nil, errors.New("compile failed")
		}
		return nil, fmt.Errorf("unexpected: %s %v", name, args)
	}
	b, _ := newTestBuilder(t, "linux", "amd64", []string{"PATH=/bin"}, run)
	var out bytes.Buffer
	b.stdout = &out
	oldDist := filepath.Join(b.root, "dist", "windows-amd64", "data-masking.exe")
	mustWrite(t, oldDist, []byte("old-artifact"))

	err := b.build("windows/amd64")
	if err == nil {
		t.Fatal("构建失败应返回错误")
	}
	if !strings.Contains(err.Error(), "wails build 阶段失败") {
		t.Errorf("错误应标明失败阶段：%v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(b.root, sysoName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("失败后应清理本次临时资源，err=%v", statErr)
	}
	if readFile(t, oldDist) != "old-artifact" {
		t.Error("失败不应改动旧 dist 产物")
	}
	if strings.Contains(out.String(), "完成") {
		t.Errorf("失败不得输出完成：%q", out.String())
	}
}

func TestBuildCleanupFailureIsReported(t *testing.T) {
	run := func(name string, args, env []string, dir string) ([]byte, error) {
		switch {
		case name == "wails-fake" && len(args) == 1 && args[0] == "version":
			return []byte("v2.11.0\n"), nil
		case name == "go" && len(args) >= 2 && args[0] == "run":
			mustWrite(t, filepath.Join(dir, "build", "appicon.png"), []byte("png"))
			return nil, nil
		case name == "wails-fake" && len(args) > 0 && args[0] == "build":
			out := argValue(args, "-o")
			mustWrite(t, filepath.Join(dir, "build", "bin", out), []byte("built"))
			// 临时资源被替换成目录，模拟清理失败。
			if err := os.MkdirAll(filepath.Join(dir, sysoName), 0o755); err != nil {
				return nil, err
			}
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected: %s %v", name, args)
	}
	b, _ := newTestBuilder(t, "linux", "amd64", []string{"PATH=/bin"}, run)
	err := b.build("windows/amd64")
	if err == nil || !strings.Contains(err.Error(), "临时资源清理失败") {
		t.Fatalf("清理失败应单独报告并非零：%v", err)
	}
}

func TestBuildMissingArtifactFailsAtPublishStage(t *testing.T) {
	run := func(name string, args, env []string, dir string) ([]byte, error) {
		switch {
		case name == "wails-fake" && len(args) == 1 && args[0] == "version":
			return []byte("v2.11.0\n"), nil
		case name == "go" && len(args) >= 2 && args[0] == "run":
			mustWrite(t, filepath.Join(dir, "build", "appicon.png"), []byte("png"))
			return nil, nil
		case name == "wails-fake" && len(args) > 0 && args[0] == "build":
			mustWrite(t, filepath.Join(dir, sysoName), []byte("leftover"))
			return nil, nil // 未产出 build/bin 文件
		}
		return nil, fmt.Errorf("unexpected: %s %v", name, args)
	}
	b, _ := newTestBuilder(t, "linux", "amd64", []string{"PATH=/bin"}, run)
	err := b.build("windows/amd64")
	if err == nil || !strings.Contains(err.Error(), "未找到构建产物") {
		t.Fatalf("缺失产物应报阶段错误：%v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(b.root, sysoName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("缺失产物也应清理临时资源，err=%v", statErr)
	}
}

func TestBuildCopyFailureReportsStage(t *testing.T) {
	run := func(name string, args, env []string, dir string) ([]byte, error) {
		switch {
		case name == "wails-fake" && len(args) == 1 && args[0] == "version":
			return []byte("v2.11.0\n"), nil
		case name == "go" && len(args) >= 2 && args[0] == "run":
			mustWrite(t, filepath.Join(dir, "build", "appicon.png"), []byte("png"))
			return nil, nil
		case name == "wails-fake" && len(args) > 0 && args[0] == "build":
			out := argValue(args, "-o")
			p := filepath.Join(dir, "build", "bin", out)
			mustWrite(t, p, []byte("x"))
			// 复制阶段失败：把平台目录内的目标文件名变成目录，使 os.WriteFile 失败。
			if err := os.MkdirAll(filepath.Join(dir, "dist", "windows-amd64", "data-masking.exe"), 0o755); err != nil {
				return nil, err
			}
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected: %s %v", name, args)
	}
	b, _ := newTestBuilder(t, "linux", "amd64", []string{"PATH=/bin"}, run)
	err := b.build("windows/amd64")
	if err == nil || !strings.Contains(err.Error(), "复制产物到 dist 阶段失败") {
		t.Fatalf("复制失败应报阶段错误：%v", err)
	}
}

// --- 资源保护 ---

func TestRefreshWindowsIconRemovesOrdinaryFile(t *testing.T) {
	b := &builder{root: t.TempDir()}
	icon := filepath.Join(b.root, "build", "windows", "icon.ico")
	mustWrite(t, icon, []byte("old"))
	if err := b.refreshWindowsIcon(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(icon); !errors.Is(err, os.ErrNotExist) {
		t.Error("普通派生 ICO 应被移除")
	}
}

func TestRefreshWindowsIconRejectsDirectory(t *testing.T) {
	b := &builder{root: t.TempDir()}
	icon := filepath.Join(b.root, "build", "windows", "icon.ico")
	if err := os.MkdirAll(icon, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := b.refreshWindowsIcon(); err == nil {
		t.Fatal("目录形式的派生图标应被拒绝")
	}
	if info, err := os.Stat(icon); err != nil || !info.IsDir() {
		t.Error("被拒绝的目录不应被删除")
	}
}

func TestRefreshWindowsIconRejectsSymlinkWithoutTouchingTarget(t *testing.T) {
	b := &builder{root: t.TempDir()}
	target := filepath.Join(b.root, "target.ico")
	mustWrite(t, target, []byte("target-content"))
	icon := filepath.Join(b.root, "build", "windows", "icon.ico")
	if err := os.MkdirAll(filepath.Dir(icon), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, icon); err != nil {
		t.Skipf("无法创建符号链接（无权限或不支持），跳过该特定用例：%v", err)
	}
	if err := b.refreshWindowsIcon(); err == nil {
		t.Fatal("符号链接形式的派生图标应被拒绝")
	}
	if readFile(t, target) != "target-content" {
		t.Error("链接目标内容不应被改动")
	}
	if _, err := os.Lstat(icon); err != nil {
		t.Error("被拒绝的符号链接不应被删除")
	}
}

func TestCleanupFileRejectsDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "as-dir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := cleanupFile(dir); err == nil {
		t.Fatal("清理目录应报错而非递归删除")
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Error("目录应保持存在")
	}
}

// --- 跨平台与数据保留 ---

// Linux → Windows → Linux 串行 bundle：每轮只更新当前平台受管文件，
// 另一平台三文件字节不变，两平台清单可同时按各自同目录程序校验。
func TestCrossPlatformSerialBundlesKeepOtherPlatform(t *testing.T) {
	seq := 0
	b, _ := newTestBuilder(t, "linux", "amd64", []string{"PATH=/bin"}, changingRunner(t, &seq))
	writeWailsJSON(t, b.root, "1.0.0")
	linuxDir := filepath.Join(b.root, "dist", "linux-amd64")
	windowsDir := filepath.Join(b.root, "dist", "windows-amd64")

	if err := b.buildBundle(""); err != nil {
		t.Fatalf("Linux bundle 应成功：%v", err)
	}
	if _, err := os.Lstat(windowsDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Linux bundle 不应创建 Windows 平台目录，err=%v", err)
	}
	verifyBundleManifest(t, linuxDir, "linux/amd64", "1.0.0", []string{"data-masking", "data-masking-agent"})
	linuxFirst := snapshotFiles(t, linuxDir, linuxBundleFiles)

	if err := b.buildBundle("windows/amd64"); err != nil {
		t.Fatalf("Windows bundle 应成功：%v", err)
	}
	assertSnapshot(t, linuxDir, linuxFirst)
	verifyBundleManifest(t, linuxDir, "linux/amd64", "1.0.0", []string{"data-masking", "data-masking-agent"})
	verifyBundleManifest(t, windowsDir, "windows/amd64", "1.0.0", []string{"data-masking.exe", "data-masking-agent.exe"})
	if _, evidence, err := verifyWindowsBundleManifest(windowsDir); err != nil || evidence != evidenceWindows {
		t.Fatalf("Windows 平台清单应可严格核验：evidence=%v err=%v", evidence, err)
	}
	windowsFirst := snapshotFiles(t, windowsDir, windowsBundleFiles)

	// 再回 Linux：Windows 三文件字节不变，Linux 受管程序与清单更新。
	if err := b.buildBundle(""); err != nil {
		t.Fatalf("第二轮 Linux bundle 应成功：%v", err)
	}
	assertSnapshot(t, windowsDir, windowsFirst)
	linuxSecond := snapshotFiles(t, linuxDir, linuxBundleFiles)
	if linuxSecond["data-masking"] == linuxFirst["data-masking"] || linuxSecond["data-masking-agent"] == linuxFirst["data-masking-agent"] {
		t.Error("同平台重复 bundle 应更新受管程序")
	}
	if linuxSecond["release.json"] == linuxFirst["release.json"] {
		t.Error("同平台重复 bundle 应更新本平台清单")
	}
	verifyBundleManifest(t, linuxDir, "linux/amd64", "1.0.0", []string{"data-masking", "data-masking-agent"})
	verifyBundleManifest(t, windowsDir, "windows/amd64", "1.0.0", []string{"data-masking.exe", "data-masking-agent.exe"})

	entries, err := os.ReadDir(filepath.Join(b.root, "dist"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("dist 应仅有 linux-amd64 与 windows-amd64：%v", entries)
	}
	for _, e := range entries {
		if !e.IsDir() {
			t.Errorf("dist 顶层不应有发布文件 %s", e.Name())
		}
	}
}

// 单入口构建保留旧清单字节；程序字节变化后清单 SHA 不再匹配，
// 该清单不得被当作本次完整 bundle 的证据。
func TestSingleEntryLeavesOldManifestBytesAndHashMismatch(t *testing.T) {
	guiContent := "gui-v1"
	agentContent := "agent-v1"
	b, _ := newTestBuilder(t, "linux", "amd64", []string{"PATH=/bin"}, mutableRunner(t, &guiContent, &agentContent))
	writeWailsJSON(t, b.root, "1.0.0")
	dir := filepath.Join(b.root, "dist", "windows-amd64")

	if err := b.buildBundle("windows/amd64"); err != nil {
		t.Fatalf("bundle 应成功：%v", err)
	}
	before := readFile(t, filepath.Join(dir, "release.json"))
	if _, evidence, err := verifyWindowsBundleManifest(dir); err != nil || evidence != evidenceWindows {
		t.Fatalf("初始清单应可核验：evidence=%v err=%v", evidence, err)
	}

	guiContent = "gui-v2"
	if err := b.build("windows/amd64"); err != nil {
		t.Fatalf("GUI 单入口应成功：%v", err)
	}
	if got := readFile(t, filepath.Join(dir, "release.json")); got != before {
		t.Error("单入口 GUI 构建不得改写旧清单字节")
	}
	if got := readFile(t, filepath.Join(dir, "data-masking.exe")); got != "gui-v2" {
		t.Errorf("GUI 单入口应更新程序，实际 %q", got)
	}
	if _, _, err := verifyWindowsBundleManifest(dir); err == nil {
		t.Error("程序字节变化后旧清单 SHA 不匹配，不得作为 bundle 发布证据")
	}

	// headless 单入口同样不改写清单。
	agentContent = "agent-v2"
	if err := b.buildHeadlessOnly("windows/amd64"); err != nil {
		t.Fatalf("headless 单入口应成功：%v", err)
	}
	if got := readFile(t, filepath.Join(dir, "release.json")); got != before {
		t.Error("单入口 headless 构建不得改写旧清单字节")
	}
	if got := readFile(t, filepath.Join(dir, "data-masking-agent.exe")); got != "agent-v2" {
		t.Errorf("headless 单入口应更新程序，实际 %q", got)
	}
}

// --- 数据保留与失败回归 ---

var businessDirNames = []string{"cards", "mapping", "masked", "original", "restored", "pending", ".agent-state"}

// seedPlatformData 在平台目录写入中性业务目录、任意文件与嵌套内容，返回相对路径→字节。
func seedPlatformData(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{"notes.txt": "user-notes", "cards/terms.txt": "neutral-term"}
	for _, d := range businessDirNames {
		if err := os.MkdirAll(filepath.Join(dir, d, "nested"), 0o755); err != nil {
			t.Fatal(err)
		}
		files[filepath.Join(d, "nested", "keep.txt")] = "keep:" + d
	}
	for rel, content := range files {
		mustWrite(t, filepath.Join(dir, rel), []byte(content))
	}
	return files
}

// 重复发布各模式后，两平台已有业务/任意内容与旧顶层文件字节不变，且不新写顶层产物。
func TestPublishPreservesExistingPlatformDataAndTopLevel(t *testing.T) {
	seq := 0
	b, _ := newTestBuilder(t, "linux", "amd64", []string{"PATH=/bin"}, changingRunner(t, &seq))
	writeWailsJSON(t, b.root, "1.0.0")
	dist := filepath.Join(b.root, "dist")
	linuxDir := filepath.Join(dist, "linux-amd64")
	windowsDir := filepath.Join(dist, "windows-amd64")

	linuxSentinels := seedPlatformData(t, linuxDir)
	windowsSentinels := seedPlatformData(t, windowsDir)
	oldTop := map[string]string{
		"data-masking":     "old-linux-gui",
		"data-masking.exe": "old-windows-gui",
		"release.json":     `{"version":"old","target":"linux/amd64","files":[]}`,
		"notes.txt":        "old-top-notes",
	}
	for name, content := range oldTop {
		mustWrite(t, filepath.Join(dist, name), []byte(content))
	}

	steps := []func() error{
		func() error { return b.buildBundle("") },
		func() error { return b.buildBundle("windows/amd64") },
		func() error { return b.build("") },
		func() error { return b.buildHeadlessOnly("windows/amd64") },
		func() error { return b.buildHeadlessOnly("") },
		func() error { return b.build("windows/amd64") },
	}
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("第 %d 次重复发布应成功：%v", i+1, err)
		}
	}

	for rel, content := range linuxSentinels {
		if got := readFile(t, filepath.Join(linuxDir, rel)); got != content {
			t.Errorf("Linux 平台目录内容被改变：%s", rel)
		}
	}
	for rel, content := range windowsSentinels {
		if got := readFile(t, filepath.Join(windowsDir, rel)); got != content {
			t.Errorf("Windows 平台目录内容被改变：%s", rel)
		}
	}
	for name, content := range oldTop {
		if got := readFile(t, filepath.Join(dist, name)); got != content {
			t.Errorf("旧顶层文件不应被迁移/覆盖/删除：%s", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dist, "data-masking-agent")); !errors.Is(err, os.ErrNotExist) {
		t.Error("不应在 dist 顶层生成新产物")
	}
}

// 构建只创建平台目录与本次受管产物，不预建 cards 等业务目录。
func TestBuildDoesNotPrecreateBusinessDirs(t *testing.T) {
	seq := 0
	b, _ := newTestBuilder(t, "linux", "amd64", []string{"PATH=/bin"}, changingRunner(t, &seq))
	writeWailsJSON(t, b.root, "1.0.0")
	if err := b.buildBundle("windows/amd64"); err != nil {
		t.Fatalf("bundle 应成功：%v", err)
	}
	dir := filepath.Join(b.root, "dist", "windows-amd64")
	for _, d := range businessDirNames {
		if _, err := os.Lstat(filepath.Join(dir, d)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("构建不应预建业务目录 %s", d)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	want := []string{"data-masking-agent.exe", "data-masking.exe", "release.json"}
	if len(names) != len(want) {
		t.Fatalf("平台目录应仅含受管产物，实际 %v", names)
	}
	for _, w := range want {
		if _, err := os.Stat(filepath.Join(dir, w)); err != nil {
			t.Errorf("缺少受管产物 %s", w)
		}
	}
}

// 平台目录被普通文件占位时，各模式明确失败且不删除占位、不回退顶层。
func TestPlatformDirCreationFailure(t *testing.T) {
	cases := []struct {
		mode     string
		platform string
	}{
		{"gui", "windows/amd64"},
		{"headless", "windows/amd64"},
		{"bundle", "windows/amd64"},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			seq := 0
			b, _ := newTestBuilder(t, "linux", "amd64", []string{"PATH=/bin"}, changingRunner(t, &seq))
			writeWailsJSON(t, b.root, "1.0.0")
			occupied := filepath.Join(b.root, "dist", "windows-amd64")
			mustWrite(t, occupied, []byte("occupied-by-file"))

			var err error
			switch tc.mode {
			case "gui":
				err = b.build(tc.platform)
			case "headless":
				err = b.buildHeadlessOnly(tc.platform)
			case "bundle":
				err = b.buildBundle(tc.platform)
			}
			if err == nil || !strings.Contains(err.Error(), "创建 dist 目录失败") {
				t.Fatalf("平台目录为文件时应报告创建失败：%v", err)
			}
			if got := readFile(t, occupied); got != "occupied-by-file" {
				t.Error("占位文件不应被删除或改写")
			}
			if _, statErr := os.Lstat(filepath.Join(b.root, "dist", "data-masking")); !errors.Is(statErr, os.ErrNotExist) {
				t.Error("失败不应回退向顶层发布")
			}
		})
	}
}

// 产物目标被目录占位时明确失败，不删除占位、不写清单、不回退顶层。
func TestArtifactTargetOccupiedByDirectory(t *testing.T) {
	cases := []struct {
		mode       string
		occupied   string
		wantErrMsg string
	}{
		{"gui", "data-masking.exe", "复制产物到 dist 阶段失败"},
		{"headless", "data-masking-agent.exe", "复制 headless 产物阶段失败"},
		{"bundle", "data-masking.exe", "复制 GUI 产物阶段失败"},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			seq := 0
			b, _ := newTestBuilder(t, "linux", "amd64", []string{"PATH=/bin"}, changingRunner(t, &seq))
			writeWailsJSON(t, b.root, "1.0.0")
			dir := filepath.Join(b.root, "dist", "windows-amd64")
			if err := os.MkdirAll(filepath.Join(dir, tc.occupied), 0o755); err != nil {
				t.Fatal(err)
			}

			var err error
			switch tc.mode {
			case "gui":
				err = b.build("windows/amd64")
			case "headless":
				err = b.buildHeadlessOnly("windows/amd64")
			case "bundle":
				err = b.buildBundle("windows/amd64")
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErrMsg) {
				t.Fatalf("产物被目录占位应报告失败（%s）：%v", tc.wantErrMsg, err)
			}
			if info, statErr := os.Stat(filepath.Join(dir, tc.occupied)); statErr != nil || !info.IsDir() {
				t.Error("被拒绝的目录占位不应被删除")
			}
			if _, statErr := os.Lstat(filepath.Join(dir, "release.json")); !errors.Is(statErr, os.ErrNotExist) {
				t.Error("失败不应写清单")
			}
			if _, statErr := os.Lstat(filepath.Join(b.root, "dist", "data-masking.exe")); !errors.Is(statErr, os.ErrNotExist) {
				t.Error("失败不应回退向顶层发布")
			}
		})
	}
}

// 第二产物编译失败时保留既有完整 bundle 的字节，不把半套结果称为新发布。
func TestBundleSecondProductFailureKeepsPreviousPublishBytes(t *testing.T) {
	b, _ := newTestBuilder(t, "linux", "amd64", []string{"PATH=/bin"}, headlessFakeRunner(t, true))
	writeWailsJSON(t, b.root, "1.0.0")
	dir := filepath.Join(b.root, "dist", "linux-amd64")
	oldGUI := "old-gui-binary"
	oldManifest := `{"version":"1.0.0","target":"linux/amd64","files":[]}`
	mustWrite(t, filepath.Join(dir, "data-masking"), []byte(oldGUI))
	mustWrite(t, filepath.Join(dir, "release.json"), []byte(oldManifest))

	if err := b.buildBundle(""); err == nil {
		t.Fatal("headless 失败时 bundle 应失败")
	}
	if got := readFile(t, filepath.Join(dir, "data-masking")); got != oldGUI {
		t.Error("失败不应改动既有 GUI 产物")
	}
	if got := readFile(t, filepath.Join(dir, "release.json")); got != oldManifest {
		t.Error("失败不应改写既有清单")
	}
	if _, err := os.Lstat(filepath.Join(b.root, "dist", "release.json")); !errors.Is(err, os.ErrNotExist) {
		t.Error("不应向 dist 顶层写清单")
	}
}

// --- helpers ---

// changingRunner 模拟随构建次数变化的产物字节，用于验证跨平台串行构建互不覆盖。
func changingRunner(t *testing.T, seq *int) commandRunner {
	t.Helper()
	return func(name string, args, env []string, dir string) ([]byte, error) {
		switch {
		case name == "wails-fake":
			if len(args) == 1 && args[0] == "version" {
				return []byte("v2.11.0\n"), nil
			}
			if len(args) > 0 && args[0] == "build" {
				*seq++
				mustWrite(t, filepath.Join(dir, "build", "bin", argValue(args, "-o")), []byte(fmt.Sprintf("gui-%d", *seq)))
				return nil, nil
			}
		case name == "go" && len(args) > 1 && args[0] == "run" && args[1] == "./scripts/genicon":
			mustWrite(t, filepath.Join(dir, "build", "appicon.png"), []byte("png"))
			return nil, nil
		case name == "go" && len(args) > 0 && args[0] == "build":
			*seq++
			mustWrite(t, argValue(args, "-o"), []byte(fmt.Sprintf("agent-%d", *seq)))
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected command: %s %v", name, args)
	}
}

// mutableRunner 按调用时的可变内容生成 GUI/headless 产物，用于单入口后旧清单过期检查。
func mutableRunner(t *testing.T, gui, agent *string) commandRunner {
	t.Helper()
	return func(name string, args, env []string, dir string) ([]byte, error) {
		switch {
		case name == "wails-fake":
			if len(args) == 1 && args[0] == "version" {
				return []byte("v2.11.0\n"), nil
			}
			if len(args) > 0 && args[0] == "build" {
				mustWrite(t, filepath.Join(dir, "build", "bin", argValue(args, "-o")), []byte(*gui))
				return nil, nil
			}
		case name == "go" && len(args) > 1 && args[0] == "run" && args[1] == "./scripts/genicon":
			mustWrite(t, filepath.Join(dir, "build", "appicon.png"), []byte("png"))
			return nil, nil
		case name == "go" && len(args) > 0 && args[0] == "build":
			mustWrite(t, argValue(args, "-o"), []byte(*agent))
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected command: %s %v", name, args)
	}
}

var (
	linuxBundleFiles   = []string{"data-masking", "data-masking-agent", "release.json"}
	windowsBundleFiles = []string{"data-masking.exe", "data-masking-agent.exe", "release.json"}
)

// snapshotFiles 读取目录内受管文件的字节，用于跨平台串行构建的字节不变断言。
func snapshotFiles(t *testing.T, dir string, names []string) map[string]string {
	t.Helper()
	out := make(map[string]string, len(names))
	for _, n := range names {
		out[n] = readFile(t, filepath.Join(dir, n))
	}
	return out
}

func assertSnapshot(t *testing.T, dir string, want map[string]string) {
	t.Helper()
	for n, v := range want {
		if got := readFile(t, filepath.Join(dir, n)); got != v {
			t.Errorf("%s 字节不应被其他平台构建改变", filepath.Join(dir, n))
		}
	}
}

// verifyBundleManifest 按同目录程序校验平台清单的 target/version/name/SHA。
func verifyBundleManifest(t *testing.T, dir, wantTarget, wantVersion string, wantNames []string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "release.json"))
	if err != nil {
		t.Fatalf("读取 %s/release.json 失败：%v", dir, err)
	}
	var m releaseManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("解析清单失败：%v", err)
	}
	if m.Target != wantTarget {
		t.Errorf("清单 target=%q，期望 %q", m.Target, wantTarget)
	}
	if m.Version != wantVersion {
		t.Errorf("清单 version=%q，期望 %q", m.Version, wantVersion)
	}
	if len(m.Files) != len(wantNames) {
		t.Fatalf("清单应含 %d 个产物，实际 %d：%+v", len(wantNames), len(m.Files), m.Files)
	}
	allowed := map[string]bool{}
	for _, n := range wantNames {
		allowed[n] = true
	}
	seen := map[string]bool{}
	for _, f := range m.Files {
		// target 使用斜杠形式；name 仅同目录 basename，不含 dist 前缀或目录分隔符。
		if f.Name != filepath.Base(f.Name) || strings.ContainsAny(f.Name, `/\`) {
			t.Errorf("清单 name 应为同目录 basename：%q", f.Name)
		}
		if !allowed[f.Name] {
			t.Errorf("清单含意外产物名：%q", f.Name)
		}
		if seen[f.Name] {
			t.Errorf("清单产物重复：%q", f.Name)
		}
		seen[f.Name] = true
		got, err := fileSHA256(filepath.Join(dir, f.Name))
		if err != nil || got != f.SHA256 {
			t.Errorf("%s SHA 不符：%q vs %q err=%v", f.Name, got, f.SHA256, err)
		}
	}
}

func envValue(env []string, key string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}

func assertNoEnvKey(t *testing.T, env []string, key string) {
	t.Helper()
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); ok && k == key {
			t.Errorf("环境不应含 %s：%v", key, env)
			return
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", path, err)
	}
	return string(data)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
