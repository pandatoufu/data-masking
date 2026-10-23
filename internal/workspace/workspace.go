// Package workspace 定义 GUI 与 headless 共用的固定工作区：目录名称、根定位
// 与字节级无覆盖文件原语。它不依赖 desktop、server、Wails 或 MCP 适配层。
package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// 固定工作区目录名称。GUI 与 headless 共用同一集合，名称不可由用户配置。
const (
	DirCards    = "cards"
	DirMasked   = "masked"
	DirMapping  = "mapping"
	DirOriginal = "original"
	DirPending  = "pending"
	DirRestored = "restored"
)

// guidDirs 是 GUI 入口自动保存判定必需的五目录（顺序固定，便于日志与测试）。
// GUI 的可选输入目录 pending/ 由 EnsureDir 独立初始化，不参与该五目录判定；
// GUI 任何入口都不创建、不检查、不要求 .agent-state。
var guidDirs = []string{
	DirCards,
	DirMasked,
	DirMapping,
	DirOriginal,
	DirRestored,
}

// headlessDirs 是 headless 入口必需的六目录：五目录加 pending/。
var headlessDirs = []string{
	DirCards,
	DirMasked,
	DirMapping,
	DirOriginal,
	DirPending,
	DirRestored,
}

// GUIDirs 返回 GUI 入口五目录的副本，供适配层/测试按入口断言。
func GUIDirs() []string { return append([]string(nil), guidDirs...) }

// HeadlessDirs 返回 headless 入口六目录的副本，供适配层/测试按入口断言。
func HeadlessDirs() []string { return append([]string(nil), headlessDirs...) }

// LockFileName 是协调 GUI 自动发布与 headless 文件/任务事务的固定工作区锁文件。
// 锁文件不删除；残留文件不等于活跃锁。
const LockFileName = ".workspace.lock"

// AgentStateDir 是 headless 私有任务记录的相对目录（软件元数据，不是第七个业务目录或配置项）。
const AgentStateDir = ".agent-state"

// SessionLockFileName 是 headless 会话级 OS 锁文件名（位于 AgentStateDir 下）。
const SessionLockFileName = "session.lock"

// ErrUnresolvedRoot 表示无法从 executable 位置确定工作区根目录。
// 调用方不得回退到进程工作目录。
var ErrUnresolvedRoot = errors.New("workspace: 无法确定程序所在目录")

// ExecutableRoot 返回真实 executable 所在目录：
// os.Executable → EvalSymlinks → Abs/Clean → Dir。
// 解析失败明确返回错误，不回退 cwd。
func ExecutableRoot() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnresolvedRoot, err)
	}
	return RootFromPath(exe)
}

// RootFromPath 从给定 executable 路径解析根目录。路径必须可被 EvalSymlinks 解析；
// 空路径、缺失路径或链接解析失败均返回 ErrUnresolvedRoot。
func RootFromPath(exe string) (string, error) {
	exe = strings.TrimSpace(exe)
	if exe == "" {
		return "", ErrUnresolvedRoot
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnresolvedRoot, err)
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnresolvedRoot, err)
	}
	return filepath.Dir(filepath.Clean(abs)), nil
}

// ErrUnsafeDir 表示固定目录被普通文件、符号链接或 Windows reparse point 占位。
var ErrUnsafeDir = errors.New("workspace: 固定目录被非目录或链接占位")

// DirError 指出具体哪个固定目录不可用，便于适配层给固定诊断而不回显私有内容。
type DirError struct {
	Name   string
	Path   string
	Reason error
}

func (e *DirError) Error() string {
	return "workspace: 目录 " + e.Name + " 不可用: " + e.Reason.Error()
}

func (e *DirError) Unwrap() error { return e.Reason }

// IsLinkOrReparse 报告路径是否为符号链接或 Windows reparse point。
// info 必须来自 Lstat（不跟随链接）。
func IsLinkOrReparse(path string, info os.FileInfo) bool {
	return isLinkOrReparse(path, info)
}

// EnsureGUI 在 root 下幂等创建 GUI 自动保存判定所需的五目录，并拒绝被普通文件、
// 符号链接或 Windows reparse point 占位的必需目录（不跟随其写入）。
// GUI 的 pending/ 输入目录由 EnsureDir 独立初始化，不参与五目录判定；
// .agent-state 仍不由 GUI 创建、检查或修改。已有内容不得清空或删除。
func EnsureGUI(root string) error { return ensure(root, guidDirs) }

// EnsureHeadless 在 root 下幂等创建 headless 六目录（含 pending/），并拒绝被
// 普通文件、符号链接或 Windows reparse point 占位的必需目录（不跟随其写入）。
func EnsureHeadless(root string) error { return ensure(root, headlessDirs) }

// EnsureDir 在 root 下幂等创建单个入口无关的固定目录（如 GUI 的 pending/），
// 并拒绝被普通文件、符号链接或 Windows reparse point 占位。它不判断、不改变
// 五/六目录集合或自动保存可用性，失败只由调用方反馈到对应入口。
func EnsureDir(root, name string) error {
	if strings.TrimSpace(root) == "" {
		return ErrUnresolvedRoot
	}
	if strings.TrimSpace(name) == "" {
		return ErrUnsafeDir
	}
	return ensureDir(filepath.Join(root, name), name)
}

func ensure(root string, dirs []string) error {
	if strings.TrimSpace(root) == "" {
		return ErrUnresolvedRoot
	}
	for _, name := range dirs {
		if err := ensureDir(filepath.Join(root, name), name); err != nil {
			return err
		}
	}
	return nil
}

func ensureDir(path, name string) error {
	info, err := os.Lstat(path)
	switch {
	case err == nil:
		if !info.IsDir() {
			return &DirError{Name: name, Path: path, Reason: ErrUnsafeDir}
		}
		if isLinkOrReparse(path, info) {
			return &DirError{Name: name, Path: path, Reason: ErrUnsafeDir}
		}
		return nil
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(path, 0o755); err != nil {
			return err
		}
		created, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !created.IsDir() || isLinkOrReparse(path, created) {
			return &DirError{Name: name, Path: path, Reason: ErrUnsafeDir}
		}
		return nil
	default:
		return err
	}
}
