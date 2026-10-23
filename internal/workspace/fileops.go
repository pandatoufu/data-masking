package workspace

import (
	"os"
)

// TempFile 是写入/同步/关闭所需的最小文件抽象，便于故障注入。
type TempFile interface {
	Name() string
	Write(p []byte) (int, error)
	Sync() error
	Close() error
}

// FileOps 抽象无覆盖发布所需的文件系统原语，便于测试注入写入/Sync/Close/Link/清理故障。
// 实现 MUST NOT 覆盖已存在目标：发布必须使用无覆盖原语（硬链接）。
type FileOps interface {
	// Lstat 报告路径信息，不跟随符号链接（用于识别已有目标或符号链接）。
	Lstat(path string) (os.FileInfo, error)
	// CreateTemp 在 dir 下创建权限 0600 的随机临时文件。
	CreateTemp(dir, pattern string) (TempFile, error)
	// Link 以硬链接把已完整写入的临时文件发布为 dest；目标已存在时失败，不覆盖。
	Link(oldname, newname string) error
	// Remove 删除路径；用于正常失败与发布成功后的临时文件清理。
	Remove(path string) error
}

// OSFileOps 是 FileOps 的本地操作系统实现。
type OSFileOps struct{}

func (OSFileOps) Lstat(path string) (os.FileInfo, error) { return os.Lstat(path) }

func (OSFileOps) CreateTemp(dir, pattern string) (TempFile, error) {
	return os.CreateTemp(dir, pattern)
}

func (OSFileOps) Link(oldname, newname string) error { return os.Link(oldname, newname) }

func (OSFileOps) Remove(path string) error { return os.Remove(path) }
