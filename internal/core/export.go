package core

import (
	"os"
	"path/filepath"
)

// AtomicWriteFile 原子落盘：写临时文件成功后改名，任何失败都不留目标半成品。
func AtomicWriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".dm-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		// Windows 上目标已存在时 Rename 会失败，先移除再改名。
		if rmErr := os.Remove(path); rmErr == nil {
			if err2 := os.Rename(tmpName, path); err2 == nil {
				return nil
			}
		}
		cleanup()
		return err
	}
	return nil
}
