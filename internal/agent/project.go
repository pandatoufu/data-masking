package agent

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/pandatoufu/data-masking/internal/loader"
	"github.com/pandatoufu/data-masking/internal/workspace"
)

// ValidateProjectPath 校验单次调用显式给出的项目路径：
// 必须是现有绝对目录，规范化后不得与私有工作区相同或互为祖先/后代，且不是链接/reparse 路径。
// 绝不使用 cwd 猜测项目。
func ValidateProjectPath(workspaceRoot, projectPath string) (string, error) {
	invalid := NewError(CodeInvalidProject, MessageFor(CodeInvalidProject))
	p := strings.TrimSpace(projectPath)
	if p == "" || !filepath.IsAbs(p) {
		return "", invalid
	}
	cleaned := filepath.Clean(p)
	if info, err := os.Lstat(cleaned); err != nil || !info.IsDir() || workspace.IsLinkOrReparse(cleaned, info) {
		return "", invalid
	}
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		return "", invalid
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", invalid
	}
	abs = filepath.Clean(abs)

	info, err := os.Lstat(abs)
	if err != nil || !info.IsDir() || workspace.IsLinkOrReparse(abs, info) {
		return "", invalid
	}
	root := filepath.Clean(workspaceRoot)
	if root != "" && root != "." {
		if isWithin(root, abs) || isWithin(abs, root) {
			return "", invalid
		}
	}
	return abs, nil
}

// ValidateResponsePath 校验回程回复路径：必须是该任务 project 子树内的现有常规文件，
// 拒绝链接/reparse 越界、目录、私有工作区路径与不支持格式。校验失败不写 pending。
func ValidateResponsePath(projectPath, responsePath string) (string, error) {
	invalid := NewError(CodeInvalidProject, MessageFor(CodeInvalidProject))
	p := strings.TrimSpace(responsePath)
	if p == "" || !filepath.IsAbs(p) {
		return "", invalid
	}
	cleaned := filepath.Clean(p)
	if info, err := os.Lstat(cleaned); err != nil || !info.Mode().IsRegular() || workspace.IsLinkOrReparse(cleaned, info) {
		return "", invalid
	}
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		return "", invalid
	}
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", invalid
	}
	abs = filepath.Clean(abs)

	info, err := os.Lstat(abs)
	if err != nil || !info.Mode().IsRegular() || workspace.IsLinkOrReparse(abs, info) {
		return "", invalid
	}
	project := filepath.Clean(projectPath)
	if !isWithin(project, abs) {
		return "", invalid
	}
	if err := loader.ValidateFileName(abs); err != nil {
		return "", NewError(CodeUnsupportedInput, MessageFor(CodeUnsupportedInput))
	}
	return abs, nil
}

// isWithin 报告 child 是否等于 parent 或位于 parent 子树内（按清理后的绝对路径）。
func isWithin(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return !filepath.IsAbs(rel)
}

// EnsureExistsRegular 报告路径是否为现有常规文件（不跟随链接），供交付/回程共用。
func EnsureExistsRegular(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return NewError(CodeInvalidProject, MessageFor(CodeInvalidProject))
		}
		return NewError(CodeInvalidProject, MessageFor(CodeInvalidProject))
	}
	if !info.Mode().IsRegular() || workspace.IsLinkOrReparse(path, info) {
		return NewError(CodeInvalidProject, MessageFor(CodeInvalidProject))
	}
	return nil
}
