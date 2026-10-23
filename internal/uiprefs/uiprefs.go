// Package uiprefs 管理独立、非敏感的 UI 偏好（仅 version/theme/language）。
//
// 约束：
//   - 固定文件 filepath.Join(os.UserConfigDir(), "data-masking", "ui-prefs.json")，
//     不经业务 config.Save，绝不写文件内容、词表、映射、路径、占位符或结果。
//   - 缺失/损坏/未知值按字段回退，一个字段损坏不影响另一字段。
//   - 写入串行、先写临时文件再跨平台完整替换，失败保留旧有效文件。
package uiprefs

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/pandatoufu/data-masking/internal/i18n"
)

// Version 是当前偏好文件版本。
const Version = 1

// 主题取值。
const (
	ThemeSystem = "system"
	ThemeLight  = "light"
	ThemeDark   = "dark"
)

// 语言取值为 i18n.ZH / i18n.EN（string）。

// Prefs 是持久化的 UI 偏好，字段严格限定为三项。
type Prefs struct {
	Version  int    `json:"version"`
	Theme    string `json:"theme"`
	Language string `json:"language"`
}

// ValidTheme 报告主题值是否受支持。
func ValidTheme(s string) bool {
	return s == ThemeSystem || s == ThemeLight || s == ThemeDark
}

// ValidLanguage 报告语言值是否受支持（仅 zh/en，拒绝未知值）。
func ValidLanguage(s string) bool {
	return i18n.Lang(s).Valid()
}

// Default 返回默认偏好：主题 system，语言按系统首次规则选择。
func Default(system i18n.Lang) Prefs {
	if !system.Valid() {
		system = i18n.EN
	}
	return Prefs{Version: Version, Theme: ThemeSystem, Language: string(system)}
}

// DefaultPath 返回偏好文件路径（Windows %AppData% / Linux ~/.config）。
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "data-masking", "ui-prefs.json"), nil
}

// Load 读取偏好并按字段回退；缺失/损坏/未知值不影响有效字段。
func Load(path string, system i18n.Lang) Prefs {
	p := Default(system)
	data, err := os.ReadFile(path)
	if err != nil {
		return p
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return p
	}
	if v, ok := raw["version"]; ok {
		var n int
		if json.Unmarshal(v, &n) == nil && n > 0 {
			p.Version = n
		}
	}
	if v, ok := raw["theme"]; ok {
		var s string
		if json.Unmarshal(v, &s) == nil && ValidTheme(s) {
			p.Theme = s
		}
	}
	if v, ok := raw["language"]; ok {
		var s string
		if json.Unmarshal(v, &s) == nil && ValidLanguage(s) {
			p.Language = s
		}
	}
	return p
}

// Store 串行化偏好读写，保证并发写不产生半写文件。
type Store struct {
	mu     sync.Mutex
	path   string
	system i18n.Lang
}

// NewStore 构造偏好存储。system 为系统首次语言，用于默认与回退。
func NewStore(path string, system i18n.Lang) *Store {
	return &Store{path: path, system: system}
}

// Path 返回偏好文件路径。
func (s *Store) Path() string { return s.path }

// System 返回系统首次语言。
func (s *Store) System() i18n.Lang { return s.system }

// Load 读取当前偏好。
func (s *Store) Load() Prefs {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Load(s.path, s.system)
}

// Normalize 把任意偏好规范为合法值（版本/主题/语言越界时回退）。
func (s *Store) Normalize(p Prefs) Prefs {
	if p.Version <= 0 {
		p.Version = Version
	}
	if !ValidTheme(p.Theme) {
		p.Theme = ThemeSystem
	}
	if !ValidLanguage(p.Language) {
		p.Language = string(Default(s.system).Language)
	}
	return p
}

// Save 串行写入偏好：先完整写临时文件，再跨平台完整替换；失败时旧有效文件保留。
func (s *Store) Save(p Prefs) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p = s.Normalize(p)
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return writeReplace(s.path, data)
}

// writeReplace 写同目录临时文件并完整替换目标；任一失败都不留半成品目标。
func writeReplace(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".ui-prefs-*.tmp")
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
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := renameForWrite(tmpName, path); err != nil {
		// 替换失败：清理临时文件，绝不删除/破坏旧有效文件。
		cleanup()
		return err
	}
	return nil
}

// renameForWrite 可注入（测试故障注入用）。os.Rename 在 Unix 与 Windows 均完整替换已存在目标。
var renameForWrite = os.Rename

// ErrUnwritable 在目录不可写时用于测试注入的哨兵错误。
var ErrUnwritable = errors.New("uiprefs: destination not writable")
