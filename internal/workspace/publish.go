package workspace

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
)

// PublishErrorKind 区分无覆盖发布失败发生在哪一步，供适配层映射为各自的状态码与文案。
type PublishErrorKind string

const (
	// PublishKindTemp 表示无法创建临时文件。
	PublishKindTemp PublishErrorKind = "temp"
	// PublishKindWrite 表示写入临时文件失败。
	PublishKindWrite PublishErrorKind = "write"
	// PublishKindSync 表示同步临时文件失败。
	PublishKindSync PublishErrorKind = "sync"
	// PublishKindClose 表示关闭临时文件失败。
	PublishKindClose PublishErrorKind = "close"
	// PublishKindLink 表示以无覆盖原语发布目标失败（含目标已存在）。
	PublishKindLink PublishErrorKind = "link"
	// PublishKindRollback 表示发布第二件失败后回滚第一件也失败。
	PublishKindRollback PublishErrorKind = "rollback"
	// PublishKindCleanup 表示目标已完整发布，但临时文件清理失败。
	PublishKindCleanup PublishErrorKind = "cleanup"
)

// PublishError 是共享发布原语的失败结果。Err 保留底层错误，可用 errors.Is 判断 fs.ErrExist。
type PublishError struct {
	Kind     PublishErrorKind
	Artifact int
	Err      error
	Rollback error
	Path     string
}

func (e *PublishError) Error() string {
	if e == nil {
		return "workspace: publish failed"
	}
	msg := fmt.Sprintf("workspace: publish %s failed", e.Kind)
	if e.Artifact > 0 {
		msg = fmt.Sprintf("%s (artifact %d)", msg, e.Artifact)
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *PublishError) Unwrap() error { return e.Err }

// Artifact 是一件待以无覆盖方式发布的字节产物。
type Artifact struct {
	Dir  string
	Name string
	Data []byte
}

// Publisher 以“目标卷内临时文件 + 硬链接”无覆盖发布字节，不使用覆盖式改名。
type Publisher struct {
	ops FileOps
}

// NewPublisher 构造发布器；ops 为 nil 时使用本地文件系统实现。
func NewPublisher(ops FileOps) Publisher {
	if ops == nil {
		ops = OSFileOps{}
	}
	return Publisher{ops: ops}
}

// Ops 返回发布器使用的文件原语（供适配层复用，不改变无覆盖契约）。
func (p Publisher) Ops() FileOps {
	if p.ops == nil {
		return OSFileOps{}
	}
	return p.ops
}

// Exists 报告路径是否已存在（不跟随符号链接）。
func (p Publisher) Exists(path string) (bool, error) {
	if _, err := p.Ops().Lstat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Publish 发布单件产物，返回实际目标路径。
// 清理失败（PublishKindCleanup）时目标已完整发布，仍返回路径与错误供调用方如实报告。
func (p Publisher) Publish(dir, name string, data []byte) (string, error) {
	paths, err := p.PublishPair(Artifact{Dir: dir, Name: name, Data: data})
	if err != nil {
		var pe *PublishError
		if errors.As(err, &pe) && pe.Kind == PublishKindCleanup && len(paths) > 0 {
			return paths[0], err
		}
		return "", err
	}
	return paths[0], nil
}

// PublishPair 按顺序无覆盖发布一件或两件产物（两件用于配对同名发布）。
// 第二件发布失败时回滚本次第一件；任一失败都清理本次临时文件，绝不覆盖已有目标。
// 返回的路径仅在对应用发布成功时有效；清理失败时返回已发布路径与 PublishKindCleanup。
func (p Publisher) PublishPair(artifacts ...Artifact) ([]string, error) {
	ops := p.Ops()
	if len(artifacts) == 0 {
		return nil, &PublishError{Kind: PublishKindLink, Err: errors.New("workspace: no artifact")}
	}
	temps := make([]TempFile, len(artifacts))
	for i, a := range artifacts {
		f, err := WriteTempFile(ops, a.Dir, a.Data)
		if err != nil {
			removeTemps(ops, temps[:i])
			return nil, reindex(err, i)
		}
		temps[i] = f
	}

	dests := make([]string, len(artifacts))
	for i, a := range artifacts {
		dests[i] = filepath.Join(a.Dir, a.Name)
		if err := ops.Link(temps[i].Name(), dests[i]); err != nil {
			var rollback error
			for j := 0; j < i; j++ {
				if rerr := ops.Remove(dests[j]); rerr != nil && rollback == nil {
					rollback = rerr
				}
			}
			removeTemps(ops, temps)
			kind := PublishKindLink
			if rollback != nil {
				kind = PublishKindRollback
			}
			return nil, &PublishError{Kind: kind, Artifact: i, Err: err, Rollback: rollback, Path: dests[i]}
		}
	}

	var cleanupErr error
	cleanupIdx := 0
	for i, f := range temps {
		if err := ops.Remove(f.Name()); err != nil && cleanupErr == nil {
			cleanupErr, cleanupIdx = err, i
		}
	}
	if cleanupErr != nil {
		return dests, &PublishError{Kind: PublishKindCleanup, Artifact: cleanupIdx, Err: cleanupErr}
	}
	return dests, nil
}

// WriteTempFile 在 dir 下写入完整字节并 Sync/Close；任何失败都清理临时文件。
func WriteTempFile(ops FileOps, dir string, data []byte) (TempFile, error) {
	if ops == nil {
		ops = OSFileOps{}
	}
	f, err := ops.CreateTemp(dir, ".data-masking-*.tmp")
	if err != nil {
		return nil, &PublishError{Kind: PublishKindTemp, Err: err}
	}
	if err := writeFull(f, data); err != nil {
		_ = f.Close()
		_ = ops.Remove(f.Name())
		return nil, &PublishError{Kind: PublishKindWrite, Err: err}
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = ops.Remove(f.Name())
		return nil, &PublishError{Kind: PublishKindSync, Err: err}
	}
	if err := f.Close(); err != nil {
		_ = ops.Remove(f.Name())
		return nil, &PublishError{Kind: PublishKindClose, Err: err}
	}
	return f, nil
}

// RemoveTemp 尽力删除临时文件，忽略错误（发布成功后的清理语义由调用方决定）。
func RemoveTemp(ops FileOps, f TempFile) {
	if f == nil {
		return
	}
	if ops == nil {
		ops = OSFileOps{}
	}
	_ = ops.Remove(f.Name())
}

func removeTemps(ops FileOps, files []TempFile) {
	for _, f := range files {
		RemoveTemp(ops, f)
	}
}

func reindex(err error, i int) error {
	var pe *PublishError
	if errors.As(err, &pe) {
		clone := *pe
		clone.Artifact = i
		return &clone
	}
	return err
}

func writeFull(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
