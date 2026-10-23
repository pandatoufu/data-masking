package agent

import (
	"errors"
	"io"
	"io/fs"
	"os"

	"github.com/pandatoufu/data-masking/internal/loader"
	"github.com/pandatoufu/data-masking/internal/workspace"
)

// ReadSource 从 original/ 读取单个常规文件，返回受限字节快照。
// 目标必须是 original/ 直属常规文件且非符号链接/reparse point；限量读取 maxSize+1 字节，
// 超限即拒绝，不先无限读入。打开的句柄与 Lstat 身份必须一致（防替换）。
func ReadSource(root, name string, maxSize int64) ([]byte, error) {
	if err := ValidateSourceName(name); err != nil {
		return nil, err
	}
	limit := maxSize
	if limit <= 0 {
		limit = loader.DefaultMaxSize
	}
	path := OriginalPath(root, name)

	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, NewError(CodeInvalidSource, MessageFor(CodeInvalidSource))
		}
		return nil, NewError(CodeInvalidSource, MessageFor(CodeInvalidSource))
	}
	if !info.Mode().IsRegular() || workspace.IsLinkOrReparse(path, info) {
		return nil, NewError(CodeInvalidSource, MessageFor(CodeInvalidSource))
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, NewError(CodeInvalidSource, MessageFor(CodeInvalidSource))
	}
	defer func() { _ = f.Close() }()

	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, NewError(CodeInvalidSource, MessageFor(CodeInvalidSource))
	}

	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, NewError(CodeInvalidSource, MessageFor(CodeInvalidSource))
	}
	if int64(len(data)) > limit {
		return nil, NewError(CodeInputTooLarge, MessageFor(CodeInputTooLarge))
	}
	return data, nil
}
