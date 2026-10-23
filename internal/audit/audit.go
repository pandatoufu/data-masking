// Package audit 只记录元数据，绝不落盘原文或映射关系；写失败由调用方 best-effort 处理。
package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Entry 是一条审计记录，字段严格限定为 spec 规定的元数据：
// rule_id、占位符、命中计数、时间戳、文件指纹。原文与映射关系绝不落盘。
type Entry struct {
	RuleID          string `json:"rule_id,omitempty"`
	Placeholder     string `json:"placeholder,omitempty"`
	Count           int    `json:"count"`
	Timestamp       string `json:"timestamp"`
	FileFingerprint string `json:"file_fingerprint,omitempty"`
}

// Log 是追加式 JSONL 审计日志。
type Log struct {
	Path string
	mu   sync.Mutex
}

// New 构造审计日志。
func New(path string) *Log { return &Log{Path: path} }

// DefaultPath 返回与业务文件路径隔离的审计路径。
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "data-masking", "audit", "audit.jsonl"), nil
}

// Append 追加一条记录。
func (l *Log) Append(e Entry) error {
	if l == nil || l.Path == "" {
		return os.ErrInvalid
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(l.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = f.Write(data)
	return err
}
