package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeWailsJSON(t *testing.T, root, version string) {
	t.Helper()
	body := `{"info":{"productVersion":"` + version + `"}}`
	mustWrite(t, filepath.Join(root, "wails.json"), []byte(body))
}

// headlessFakeRunner 模拟 go build 与 GUI 构建命令；failHeadless 可注入第二产物失败。
func headlessFakeRunner(t *testing.T, failHeadless bool) commandRunner {
	t.Helper()
	return func(name string, args, env []string, dir string) ([]byte, error) {
		switch {
		case name == "go" && len(args) > 0 && args[0] == "build":
			if failHeadless {
				return nil, errors.New("headless build failed")
			}
			out := argValue(args, "-o")
			mustWrite(t, out, []byte("headless-binary"))
			return nil, nil
		case name == "go" && len(args) > 1 && args[0] == "run" && args[1] == "./scripts/genicon":
			mustWrite(t, filepath.Join(dir, "build", "appicon.png"), []byte("png"))
			return nil, nil
		case name == "wails-fake":
			if len(args) == 1 && args[0] == "version" {
				return []byte("v2.11.0\n"), nil
			}
			out := argValue(args, "-o")
			mustWrite(t, filepath.Join(dir, "build", "bin", out), []byte("gui-binary"))
			return nil, nil
		}
		return nil, errors.New("unexpected command: " + name)
	}
}

// headless-only 不定位/调用 Wails，也不生成 GUI 图标。
func TestHeadlessOnlySkipsWails(t *testing.T) {
	b, calls := newTestBuilder(t, "linux", "amd64", []string{"PATH=/usr/bin"}, headlessFakeRunner(t, false))
	writeWailsJSON(t, b.root, "1.0.0")
	b.lookPath = func(string) (string, error) { return "", errors.New("wails must not be located") }

	if err := b.buildHeadlessOnly(""); err != nil {
		t.Fatalf("headless-only 应成功：%v", err)
	}
	if callCount(calls, "wails-fake", "") != 0 {
		t.Fatal("headless-only 不得调用 Wails CLI")
	}
	if callCount(calls, "go", "run") != 0 {
		t.Fatal("headless-only 不得生成 GUI 图标")
	}
	if _, err := os.Stat(filepath.Join(b.root, "dist", "linux-amd64", "data-masking-agent")); err != nil {
		t.Fatalf("应产出 headless：%v", err)
	}
	for _, c := range *calls {
		if c.name != "go" || len(c.args) == 0 || c.args[0] != "build" {
			continue
		}
		if !hasEnv(c.env, "CGO_ENABLED=0") {
			t.Fatalf("headless 必须 CGO_ENABLED=0：%v", c.env)
		}
		if strings.Contains(strings.Join(c.args, " "), "desktop") {
			t.Fatalf("headless 不得启用 desktop tags：%v", c.args)
		}
		if strings.Contains(strings.Join(c.args, " "), "windowsgui") {
			t.Fatalf("headless 不得使用 windowsgui：%v", c.args)
		}
		if !strings.Contains(argValue(c.args, "-ldflags"), "-X "+buildinfoSymbol+"=1.0.0") {
			t.Fatalf("应注入 productVersion：%v", c.args)
		}
	}
}

// buildinfoSymbol 是 headless 版本注入使用的完整符号路径，必须跟随当前公开 module 路径。
const buildinfoSymbol = "github.com/pandatoufu/data-masking/internal/buildinfo.Version"

// headless 的 -ldflags 必须注入完整符号路径（module + 包 + 变量），而不是只匹配尾部子串；
// 两个目标都保留 -trimpath。module 迁移后若只改 go.mod 而漏掉 -X，会静默丢失版本注入。
func TestHeadlessLdflagsUseFullSymbolPath(t *testing.T) {
	for _, target := range []buildTarget{
		{OS: "linux", Arch: "amd64"},
		{OS: "windows", Arch: "amd64"},
	} {
		args := headlessBuildArgs(target, "1.2.0", "out")
		if !hasToken(args, "-trimpath") {
			t.Fatalf("%s/%s headless 应保留 -trimpath：%v", target.OS, target.Arch, args)
		}
		ldflags := argValue(args, "-ldflags")
		if ldflags == "" {
			t.Fatalf("%s/%s headless 应设置 -ldflags：%v", target.OS, target.Arch, args)
		}
		if got, want := xSymbol(ldflags), buildinfoSymbol+"=1.2.0"; got != want {
			t.Fatalf("%s/%s -X 符号应为完整路径 %q，实际 %q（-ldflags=%q）", target.OS, target.Arch, want, got, ldflags)
		}
	}
}

// xSymbol 返回 -ldflags 中 -X 之后的符号赋值 token。
func xSymbol(ldflags string) string {
	tokens := strings.Fields(ldflags)
	for i := 0; i+1 < len(tokens); i++ {
		if tokens[i] == "-X" {
			return tokens[i+1]
		}
	}
	return ""
}

// hasToken 判断 tokens 中是否存在与 want 完全相等的元素。
func hasToken(tokens []string, want string) bool {
	for _, tok := range tokens {
		if tok == want {
			return true
		}
	}
	return false
}

func TestHeadlessWindowsTargetEnvAndName(t *testing.T) {
	b, calls := newTestBuilder(t, "linux", "amd64", []string{"PATH=/usr/bin"}, headlessFakeRunner(t, false))
	writeWailsJSON(t, b.root, "1.2.3")
	if err := b.buildHeadlessOnly("windows/amd64"); err != nil {
		t.Fatalf("Windows headless 交叉构建应成功：%v", err)
	}
	if _, err := os.Stat(filepath.Join(b.root, "dist", "windows-amd64", "data-masking-agent.exe")); err != nil {
		t.Fatalf("应产出 .exe：%v", err)
	}
	for _, c := range *calls {
		if c.name == "go" && len(c.args) > 0 && c.args[0] == "build" {
			if !hasEnv(c.env, "GOOS=windows") || !hasEnv(c.env, "GOARCH=amd64") || !hasEnv(c.env, "CGO_ENABLED=0") {
				t.Fatalf("Windows headless 环境不符：%v", c.env)
			}
			if strings.Contains(strings.Join(c.args, " "), "-H windowsgui") {
				t.Fatal("headless 不得使用 -H windowsgui（应保留 console subsystem）")
			}
		}
	}
}

// bundle：GUI 先、headless 后，两者成功后才写 release.json。
func TestBundleOrderAndManifest(t *testing.T) {
	b, calls := newTestBuilder(t, "linux", "amd64", []string{"PATH=/usr/bin"}, headlessFakeRunner(t, false))
	writeWailsJSON(t, b.root, "1.0.0")
	if err := b.buildBundle(""); err != nil {
		t.Fatalf("bundle 应成功：%v", err)
	}
	var order []string
	for _, c := range *calls {
		switch {
		case c.name == "wails-fake" && len(c.args) > 0 && c.args[0] == "build":
			order = append(order, "gui")
		case c.name == "go" && len(c.args) > 0 && c.args[0] == "build":
			order = append(order, "headless")
		}
	}
	if len(order) != 2 || order[0] != "gui" || order[1] != "headless" {
		t.Fatalf("bundle 构建顺序应为 GUI → headless：%v", order)
	}

	raw, err := os.ReadFile(filepath.Join(b.root, "dist", "linux-amd64", "release.json"))
	if err != nil {
		t.Fatalf("应写 release.json：%v", err)
	}
	var manifest releaseManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "1.0.0" || manifest.Target != "linux/amd64" {
		t.Fatalf("清单元数据不符：%+v", manifest)
	}
	if len(manifest.Files) != 2 {
		t.Fatalf("清单应含两产物：%+v", manifest.Files)
	}
	wantNames := map[string]bool{"data-masking": false, "data-masking-agent": false}
	for _, f := range manifest.Files {
		// name 仅同目录 basename，不含 dist 前缀、目录分隔符或绝对路径。
		if f.Name != filepath.Base(f.Name) || strings.ContainsAny(f.Name, `/\`) {
			t.Fatalf("清单 name 应为同目录 basename：%q", f.Name)
		}
		if _, ok := wantNames[f.Name]; !ok {
			t.Fatalf("清单含意外产物名：%q", f.Name)
		}
		wantNames[f.Name] = true
		got, err := fileSHA256(filepath.Join(b.root, "dist", "linux-amd64", f.Name))
		if err != nil || got != f.SHA256 {
			t.Fatalf("%s SHA 不符：%q vs %q err=%v", f.Name, got, f.SHA256, err)
		}
	}
	// 清单最后写入：两个产物都已存在。
	for _, f := range manifest.Files {
		if _, err := os.Stat(filepath.Join(b.root, "dist", "linux-amd64", f.Name)); err != nil {
			t.Fatalf("产物缺失：%s", f.Name)
		}
	}
}

// 第二产物失败：不写 release.json，也不发布半套 GUI。
func TestBundleSecondProductFailureNoManifest(t *testing.T) {
	b, _ := newTestBuilder(t, "linux", "amd64", []string{"PATH=/usr/bin"}, headlessFakeRunner(t, true))
	writeWailsJSON(t, b.root, "1.0.0")
	if err := b.buildBundle(""); err == nil {
		t.Fatal("headless 失败时 bundle 应失败")
	}
	if _, err := os.Stat(filepath.Join(b.root, "dist", "linux-amd64", "release.json")); !os.IsNotExist(err) {
		t.Fatal("失败不得写 release.json")
	}
	if _, err := os.Stat(filepath.Join(b.root, "dist", "linux-amd64", "data-masking")); !os.IsNotExist(err) {
		t.Fatal("失败不得把半套 GUI 产物称为新发布")
	}
}

// -headless-only 与 -bundle 互斥（在写文件前拒绝）。
func TestHeadlessOnlyAndBundleMutuallyExclusive(t *testing.T) {
	if _, err := resolveMode(true, true); err == nil {
		t.Fatal("两参数同时出现应拒绝")
	}
	for _, c := range []struct {
		headless, bundle bool
		want             string
	}{
		{false, false, "gui"},
		{true, false, "headless"},
		{false, true, "bundle"},
	} {
		got, err := resolveMode(c.headless, c.bundle)
		if err != nil || got != c.want {
			t.Fatalf("resolveMode(%v,%v)=%q,%v want %q", c.headless, c.bundle, got, err, c.want)
		}
	}
}

func TestProductVersionRequired(t *testing.T) {
	b, _ := newTestBuilder(t, "linux", "amd64", nil, headlessFakeRunner(t, false))
	mustWrite(t, filepath.Join(b.root, "wails.json"), []byte(`{"info":{"productVersion":""}}`))
	if err := b.buildHeadlessOnly(""); err == nil {
		t.Fatal("缺少 productVersion 应失败")
	}
}

func hasEnv(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}
