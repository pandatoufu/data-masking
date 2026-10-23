// Command build 构建桌面应用，使用固定版本的 Wails CLI。
//
// 用法（Windows PowerShell 与 Linux 通用，无需 shell 脚本）：
//
//	go run ./scripts/build                          # 当前主机原生构建
//	go run ./scripts/build -platform windows/amd64  # Linux 主机交叉构建 Windows 单 exe
//
// 仅支持 Linux / Windows amd64 主机，以及主机原生与 Linux → Windows 两种目标组合；
// 其它目标或组合在生成任何项目文件前被拒绝。目标决定 tags、资源、编译环境与产物后缀。
// Windows 目标复用固定 Wails CLI 的 ICO、manifest、版本信息与临时资源打包，无需 MinGW。
//
// 前置工具（开发前置，非最终用户运行时依赖）：
//
//	go install github.com/wailsapp/wails/v2/cmd/wails@v2.11.0
//
// 产物位于 dist/<os>-<arch>/（如 dist/linux-amd64/、dist/windows-amd64/），
// 由已校验的目标平台决定，不按主机另行推算。同一工作区不支持并发构建，请串行执行。
// 交叉构建产物不作为 Windows GUI 已验收依据。
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// WailsVersion 是固定的 Wails SDK/CLI 版本。
const WailsVersion = "v2.11.0"

// outputBase 是 Wails 产物的基础名（wails.json 的 outputfilename）。
const outputBase = "data-masking"

// sysoName 是 Wails 资源打包在项目根生成的临时资源名。
// 依据固定版本 Wails v2.11.0 packager.go：project name（wails.json "name"）替换空格后追加 "-res.syso"。
const sysoName = "data-masking-res.syso"

// buildTarget 是校验通过后的构建目标。
type buildTarget struct {
	OS   string
	Arch string
}

func (t buildTarget) outputName() string {
	if t.OS == "windows" {
		return outputBase + ".exe"
	}
	return outputBase
}

// commandRunner 在 dir 目录下以 env 运行外部命令并返回合并输出。
// 默认实现同时把输出透传到标准流，便于真实构建观察进度。
type commandRunner func(name string, args []string, env []string, dir string) ([]byte, error)

type builder struct {
	root     string
	hostOS   string
	hostArch string
	environ  []string
	lookPath func(string) (string, error)
	run      commandRunner
	stdout   io.Writer
	stderr   io.Writer
}

func main() {
	platform := flag.String("platform", "", "目标平台；留空表示当前主机。支持 linux/amd64、windows/amd64")
	headlessOnly := flag.Bool("headless-only", false, "仅构建 headless（不检查 Wails/GTK）")
	bundle := flag.Bool("bundle", false, "同一目标同时构建 GUI 与 headless，并发布 release.json")
	flag.Parse()
	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "构建失败：未知参数 %v\n", flag.Args())
		os.Exit(2)
	}
	mode, err := resolveMode(*headlessOnly, *bundle)
	if err != nil {
		fmt.Fprintln(os.Stderr, "构建失败：", err)
		os.Exit(2)
	}

	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "构建失败：无法获取当前目录：", err)
		os.Exit(1)
	}

	b := newBuilder(root)
	switch mode {
	case "headless":
		err = b.buildHeadlessOnly(*platform)
	case "bundle":
		err = b.buildBundle(*platform)
	default:
		err = b.build(*platform)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "构建失败：", err)
		os.Exit(1)
	}
}

// resolveMode 校验 -headless-only 与 -bundle 互斥，返回 gui/headless/bundle。
func resolveMode(headlessOnly, bundle bool) (string, error) {
	switch {
	case headlessOnly && bundle:
		return "", errors.New("-headless-only 与 -bundle 互斥")
	case headlessOnly:
		return "headless", nil
	case bundle:
		return "bundle", nil
	default:
		return "gui", nil
	}
}

func newBuilder(root string) *builder {
	return &builder{
		root:     root,
		hostOS:   runtime.GOOS,
		hostArch: runtime.GOARCH,
		environ:  os.Environ(),
		lookPath: exec.LookPath,
		run:      realRunner,
		stdout:   os.Stdout,
		stderr:   os.Stderr,
	}
}

func realRunner(name string, args []string, env []string, dir string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = env
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &buf)
	cmd.Stderr = io.MultiWriter(os.Stderr, &buf)
	err := cmd.Run()
	return buf.Bytes(), err
}

// distDir 返回已校验目标的最终发布目录 dist/<os>-<arch>。
// GUI、headless-only 与 bundle 三个发布入口统一复用该映射，避免按主机、
// 当前目录或外部 GOOS 另行推算而分叉；Linux → Windows 与 Windows 原生目标
// 均落入 dist/windows-amd64。
func (b *builder) distDir(target buildTarget) string {
	return filepath.Join(b.root, "dist", target.OS+"-"+target.Arch)
}

// build 构建并发布 GUI 产物（默认真实平台发布流程）。
func (b *builder) build(platform string) error {
	target, err := resolveTarget(platform, b.hostOS, b.hostArch)
	if err != nil {
		return err
	}
	built, err := b.buildGUI(target)
	if err != nil {
		return err
	}
	return b.publish(target, built)
}

// buildGUI 按顺序校验 Wails CLI、保护临时资源、生成图案、刷新 Windows 派生图标并执行构建，
// 返回 build/bin 中的产物路径（尚未复制到 dist）。任一步失败即返回错误，不静默回退。
func (b *builder) buildGUI(target buildTarget) (string, error) {

	wailsBin, err := b.locateWails()
	if err != nil {
		return "", err
	}
	if err := b.checkWailsVersion(wailsBin); err != nil {
		return "", err
	}

	// 生成任何项目文件前先拒绝预存临时资源，避免覆盖不明内容。
	syso := filepath.Join(b.root, sysoName)
	if err := ensureAbsent(syso); err != nil {
		return "", err
	}

	if err := b.generateIcon(); err != nil {
		return "", fmt.Errorf("生成图案阶段失败：%w", err)
	}

	if target.OS == "windows" {
		if err := b.refreshWindowsIcon(); err != nil {
			return "", err
		}
	}

	args, err := buildArgs(target)
	if err != nil {
		return "", err
	}

	fmt.Fprintf(b.stdout, "使用 Wails %s 构建 %s/%s\n", WailsVersion, target.OS, target.Arch)
	buildErr := b.runBuild(wailsBin, args, wailsEnv(b.environ, target))

	// Wails 成功时自行移除临时资源；普通失败可能遗留。仅清理本次资源路径。
	cleanupErr := cleanupFile(syso)

	if buildErr != nil {
		if cleanupErr != nil {
			return "", fmt.Errorf("wails build 阶段失败：%w；且临时资源清理失败：%v", buildErr, cleanupErr)
		}
		return "", fmt.Errorf("wails build 阶段失败：%w", buildErr)
	}
	if cleanupErr != nil {
		return "", fmt.Errorf("临时资源清理失败：%w", cleanupErr)
	}

	built := filepath.Join(b.root, "build", "bin", target.outputName())
	info, err := os.Stat(built)
	if err != nil || info.IsDir() {
		return "", fmt.Errorf("产物阶段失败：未找到构建产物 %s", built)
	}
	return built, nil
}

// publish 从 build/bin 复制目标产物到平台发布目录；缺失或复制失败均报告阶段并非零。
// 仅在构建成功后调用，旧 dist 文件不会被宣称为本次新产物。只确保目标平台目录存在，
// 不清空或重建目录，其他平台、旧顶层产物与无关内容保持不动。
func (b *builder) publish(target buildTarget, built string) error {
	info, err := os.Stat(built)
	if err != nil || info.IsDir() {
		return fmt.Errorf("产物阶段失败：未找到构建产物 %s", built)
	}
	dist := b.distDir(target)
	if err := os.MkdirAll(dist, 0o755); err != nil {
		return fmt.Errorf("创建 dist 目录失败：%w", err)
	}
	dst := filepath.Join(dist, target.outputName())
	if err := copyFile(built, dst); err != nil {
		return fmt.Errorf("复制产物到 dist 阶段失败：%w", err)
	}
	fmt.Fprintf(b.stdout, "完成。%s 为本次构建产物。\n", dst)
	return nil
}

// generateIcon 使用主机可执行程序生成源图，避免外部 GOOS / GOARCH 把生成器交叉编译成目标程序。
func (b *builder) generateIcon() error {
	if _, err := b.run("go", []string{"run", "./scripts/genicon"}, hostEnv(b.environ), b.root); err != nil {
		return err
	}
	return nil
}

// refreshWindowsIcon 在 Wails 打包前精确移除本入口管理的派生 ICO，确保旧图案不被静默复用。
// 不递归清理 build/windows/；目录、符号链接等非普通文件一律拒绝。
func (b *builder) refreshWindowsIcon() error {
	icon := filepath.Join(b.root, "build", "windows", "icon.ico")
	info, err := os.Lstat(icon)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("检查派生图标 %s 失败：%w", icon, err)
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		return fmt.Errorf("派生图标路径 %s 是符号链接，拒绝构建", icon)
	case info.IsDir():
		return fmt.Errorf("派生图标路径 %s 是目录，拒绝构建", icon)
	case !info.Mode().IsRegular():
		return fmt.Errorf("派生图标路径 %s 不是普通文件，拒绝构建", icon)
	}
	if err := os.Remove(icon); err != nil {
		return fmt.Errorf("刷新派生图标 %s 失败：%w", icon, err)
	}
	return nil
}

func (b *builder) runBuild(bin string, args, env []string) error {
	_, err := b.run(bin, args, env, b.root)
	return err
}

// resolveTarget 校验平台参数与主机组合，返回目标。
// 仅接受 linux/amd64、windows/amd64；仅支持主机原生与 Linux → Windows 组合。
func resolveTarget(platform, hostOS, hostArch string) (buildTarget, error) {
	if hostArch != "amd64" {
		return buildTarget{}, fmt.Errorf("不支持的构建主机架构 %q：仅支持 amd64", hostArch)
	}
	if hostOS != "linux" && hostOS != "windows" {
		return buildTarget{}, fmt.Errorf("不支持的构建主机 %q：仅支持 Linux 与 Windows amd64", hostOS)
	}
	if platform == "" {
		return buildTarget{OS: hostOS, Arch: "amd64"}, nil
	}
	switch platform {
	case "linux/amd64":
		if hostOS != "linux" {
			return buildTarget{}, fmt.Errorf("不支持从 %s 构建 linux/amd64：仅支持 Linux 主机原生构建或 Linux → Windows 交叉构建", hostOS)
		}
		return buildTarget{OS: "linux", Arch: "amd64"}, nil
	case "windows/amd64":
		return buildTarget{OS: "windows", Arch: "amd64"}, nil
	default:
		return buildTarget{}, fmt.Errorf("不支持的构建目标 %q：仅支持 linux/amd64 与 windows/amd64", platform)
	}
}

// buildArgs 返回目标对应的 wails build 参数。
// 显式传递 -platform，避免外部 GOOS / GOARCH 改变 Wails 的默认目标；
// 两个目标都显式启用 Wails v2.11.0 的 -trimpath（去掉产物中的本机文件系统路径），
// 不启用 -skipbindings / -nopackage，保留资源打包与生产标签。
func buildArgs(t buildTarget) ([]string, error) {
	switch {
	case t.OS == "windows" && t.Arch == "amd64":
		return []string{
			"build",
			"-trimpath",
			"-platform", "windows/amd64",
			"-tags", "desktop",
			"-webview2", "browser",
			"-o", outputBase + ".exe",
		}, nil
	case t.OS == "linux" && t.Arch == "amd64":
		return []string{
			"build",
			"-trimpath",
			"-platform", "linux/amd64",
			"-tags", "desktop,webkit2_41",
			"-o", outputBase,
		}, nil
	default:
		return nil, fmt.Errorf("不支持的构建目标 %s/%s", t.OS, t.Arch)
	}
}

// hostEnv 生成主机可执行程序的环境：清除外部 GOOS / GOARCH，其余保持不变。
func hostEnv(base []string) []string {
	return filterEnv(base, "GOOS", "GOARCH")
}

// wailsEnv 生成子进程环境：清除冲突 GOOS / GOARCH，使 Wails 按显式目标设置应用编译、
// 按主机设置 bindings；CGO_ENABLED 由目标决定（Windows=0，Linux=1）。
func wailsEnv(base []string, t buildTarget) []string {
	env := filterEnv(base, "GOOS", "GOARCH", "CGO_ENABLED")
	cgo := "0"
	if t.OS == "linux" {
		cgo = "1"
	}
	return append(env, "CGO_ENABLED="+cgo)
}

func filterEnv(base []string, keys ...string) []string {
	drop := make(map[string]bool, len(keys))
	for _, k := range keys {
		drop[k] = true
	}
	out := make([]string, 0, len(base))
	for _, kv := range base {
		k := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			k = kv[:i]
		}
		if drop[k] {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// ensureAbsent 拒绝已存在的临时资源路径（文件、目录或符号链接）。
func ensureAbsent(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	kind := "文件"
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		kind = "符号链接"
	case info.IsDir():
		kind = "目录"
	}
	return fmt.Errorf("临时资源 %s 已存在（%s），拒绝构建以避免覆盖；请先处理该路径", path, kind)
}

// cleanupFile 移除本次临时资源；目录拒绝递归删除，避免误删不明内容。
func cleanupFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s 是目录，拒绝递归清理", path)
	}
	return os.Remove(path)
}

// locateWails 查找 Wails CLI：优先 PATH，其次 GOPATH/bin（go install 的默认位置）。
func (b *builder) locateWails() (string, error) {
	if p, err := b.lookPath("wails"); err == nil {
		return p, nil
	}
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		if out, err := b.run("go", []string{"env", "GOPATH"}, b.environ, b.root); err == nil {
			gopath = strings.TrimSpace(string(out))
		}
	}
	if gopath != "" {
		name := "wails"
		if b.hostOS == "windows" {
			name = "wails.exe"
		}
		candidate := filepath.Join(gopath, "bin", name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("未找到 Wails CLI。请先安装固定版本：go install github.com/wailsapp/wails/v2/cmd/wails@%s", WailsVersion)
}

// checkWailsVersion 校验 CLI 版本与固定版本精确一致；不等时拒绝构建，避免隐式漂移。
// 仅取首个非空行（去除 ANSI 颜色）做相等比较，避免 "v2.11.0-rc1" 等预发布版本因子串命中被误判通过。
func (b *builder) checkWailsVersion(bin string) error {
	out, err := b.run(bin, []string{"version"}, b.environ, b.root)
	if err != nil {
		return fmt.Errorf("无法运行 %s version：%w（%s）", bin, err, strings.TrimSpace(string(out)))
	}
	got := firstVersionLine(string(out))
	if got != WailsVersion {
		return fmt.Errorf("Wails CLI 版本不匹配：期望 %q，实际 %q。请安装：go install github.com/wailsapp/wails/v2/cmd/wails@%s", WailsVersion, got, WailsVersion)
	}
	return nil
}

// firstVersionLine 返回输出的首个非空行并去除 ANSI 颜色。
func firstVersionLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if clean := strings.TrimSpace(stripANSI(line)); clean != "" {
			return clean
		}
	}
	return ""
}

// stripANSI 去除形如 ESC '[' 参数 终止字母 的 ANSI 转义序列。
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && !isANSITerminator(s[j]) {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isANSITerminator(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o755)
}

// headlessOutputName 返回 headless 产物名（无 -H windowsgui，保留控制台 subsystem）。
func headlessOutputName(t buildTarget) string {
	if t.OS == "windows" {
		return outputBase + "-agent.exe"
	}
	return outputBase + "-agent"
}

// ProductVersion 从 wails.json 的 info.productVersion 读取唯一发布版本。
func (b *builder) ProductVersion() (string, error) {
	raw, err := os.ReadFile(filepath.Join(b.root, "wails.json"))
	if err != nil {
		return "", fmt.Errorf("读取 wails.json 失败：%w", err)
	}
	var meta struct {
		Info struct {
			ProductVersion string `json:"productVersion"`
		} `json:"info"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return "", fmt.Errorf("解析 wails.json 失败：%w", err)
	}
	v := strings.TrimSpace(meta.Info.ProductVersion)
	if v == "" {
		return "", fmt.Errorf("wails.json 缺少 info.productVersion")
	}
	return v, nil
}

// headlessBuildArgs 返回 headless 的 go build 参数：CGO 关闭、无 GUI tags、无 windowsgui。
func headlessBuildArgs(t buildTarget, version, out string) []string {
	ldflags := "-s -w -X github.com/pandatoufu/data-masking/internal/buildinfo.Version=" + version
	return []string{
		"build",
		"-trimpath",
		"-ldflags", ldflags,
		"-o", out,
		"./cmd/data-masking-agent",
	}
}

// headlessEnv 生成 headless 编译环境：显式目标、CGO_ENABLED=0。
func headlessEnv(base []string, t buildTarget) []string {
	env := filterEnv(base, "GOOS", "GOARCH", "CGO_ENABLED")
	return append(env, "GOOS="+t.OS, "GOARCH="+t.Arch, "CGO_ENABLED=0")
}

// buildHeadless 编译 headless 产物并返回 build/bin 中的路径。
func (b *builder) buildHeadless(platform string) (string, error) {
	target, err := resolveTarget(platform, b.hostOS, b.hostArch)
	if err != nil {
		return "", err
	}
	return b.buildHeadlessTarget(target)
}

func (b *builder) buildHeadlessTarget(target buildTarget) (string, error) {
	version, err := b.ProductVersion()
	if err != nil {
		return "", err
	}
	out := filepath.Join(b.root, "build", "bin", headlessOutputName(target))
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", fmt.Errorf("创建 headless 产物目录失败：%w", err)
	}
	if _, err := b.run("go", headlessBuildArgs(target, version, out), headlessEnv(b.environ, target), b.root); err != nil {
		return "", fmt.Errorf("headless 编译阶段失败：%w", err)
	}
	info, err := os.Stat(out)
	if err != nil || info.IsDir() || info.Size() == 0 {
		return "", fmt.Errorf("headless 产物阶段失败：未找到有效产物 %s", out)
	}
	return out, nil
}

// buildHeadlessOnly 仅构建 headless：不定位 Wails/GTK，不生成 GUI 图标/资源。
func (b *builder) buildHeadlessOnly(platform string) error {
	target, err := resolveTarget(platform, b.hostOS, b.hostArch)
	if err != nil {
		return err
	}
	built, err := b.buildHeadlessTarget(target)
	if err != nil {
		return err
	}
	dst := filepath.Join(b.distDir(target), headlessOutputName(target))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("创建 dist 目录失败：%w", err)
	}
	if err := copyFile(built, dst); err != nil {
		return fmt.Errorf("复制 headless 产物阶段失败：%w", err)
	}
	fmt.Fprintf(b.stdout, "完成。%s 为本次 headless 构建产物。\n", dst)
	return nil
}

// releaseManifest 是同版双产物发布清单。
type releaseManifest struct {
	Version string        `json:"version"`
	Target  string        `json:"target"`
	Files   []releaseFile `json:"files"`
}

type releaseFile struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// buildBundle 在同一目标的平台发布目录构建 GUI 与 headless，两者都成功并校验后才一起发布，
// 最后写入该平台目录的 release.json；任一失败不写清单，不把半套产物称为新发布。
func (b *builder) buildBundle(platform string) error {
	target, err := resolveTarget(platform, b.hostOS, b.hostArch)
	if err != nil {
		return err
	}
	version, err := b.ProductVersion()
	if err != nil {
		return err
	}

	guiBuilt, err := b.buildGUI(target)
	if err != nil {
		return fmt.Errorf("GUI 产物失败，bundle 不发布：%w", err)
	}
	headBuilt, err := b.buildHeadlessTarget(target)
	if err != nil {
		return fmt.Errorf("headless 产物失败，bundle 不发布：%w", err)
	}

	dist := b.distDir(target)
	if err := os.MkdirAll(dist, 0o755); err != nil {
		return fmt.Errorf("创建 dist 目录失败：%w", err)
	}
	guiDst := filepath.Join(dist, target.outputName())
	headDst := filepath.Join(dist, headlessOutputName(target))
	if err := copyFile(guiBuilt, guiDst); err != nil {
		return fmt.Errorf("复制 GUI 产物阶段失败：%w", err)
	}
	if err := copyFile(headBuilt, headDst); err != nil {
		return fmt.Errorf("复制 headless 产物阶段失败：%w", err)
	}

	guiInfo, err := os.Stat(guiDst)
	if err != nil || guiInfo.Size() == 0 {
		return fmt.Errorf("GUI 产物校验失败：%w", err)
	}
	headInfo, err := os.Stat(headDst)
	if err != nil || headInfo.Size() == 0 {
		return fmt.Errorf("headless 产物校验失败：%w", err)
	}

	guiSHA, err := fileSHA256(guiDst)
	if err != nil {
		return fmt.Errorf("计算 GUI SHA 失败：%w", err)
	}
	headSHA, err := fileSHA256(headDst)
	if err != nil {
		return fmt.Errorf("计算 headless SHA 失败：%w", err)
	}
	manifest := releaseManifest{
		Version: version,
		Target:  target.OS + "/" + target.Arch,
		Files: []releaseFile{
			{Name: filepath.Base(guiDst), SHA256: guiSHA},
			{Name: filepath.Base(headDst), SHA256: headSHA},
		},
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("生成 release.json 失败：%w", err)
	}
	if err := os.WriteFile(filepath.Join(dist, "release.json"), raw, 0o644); err != nil {
		return fmt.Errorf("写入 release.json 失败：%w", err)
	}
	fmt.Fprintf(b.stdout, "完成。bundle %s 已发布：%s、%s、release.json\n", manifest.Target, filepath.Base(guiDst), filepath.Base(headDst))
	return nil
}

// fileSHA256 计算产物 SHA-256。
func fileSHA256(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
