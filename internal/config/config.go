// Package config 管理内置三 profile、用户 JSON 配置合并与 custom 词表。
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/pandatoufu/data-masking/internal/detector"
)

// DefaultMaxFileSize 是纯文本、DOCX、PPTX 共用的默认文件大小上限（50MiB）。
// 已有显式正值配置优先，不因默认值变化自动迁移用户配置。
const DefaultMaxFileSize int64 = 50 * 1024 * 1024

// DefaultHighFreqThreshold 默认高频命中阈值。
const DefaultHighFreqThreshold = 20

// DefaultIdleTimeoutMinutes 默认空闲关停时长（分钟）。
const DefaultIdleTimeoutMinutes = 30

// DefaultProfile 默认 profile。
const DefaultProfile = "balanced"

// CustomProfile 是第 4 档自定义 profile 的保留 token（显示名 custom）。
// Load 合并时跳过该 key，用户配置不可覆盖；初始由 Defaults 注册为全开。
const CustomProfile = "__custom"

// presetOrder 是内置三预设的展示顺序。
var presetOrder = []string{"strict", "balanced", "minimal"}

// builtinDescriptions 是随页面下发的一句话意图描述（前端零硬编码）。
var builtinDescriptions = map[string]string{
	"strict":      "最严格：六类规则全部开启，适合对外发送前彻底脱敏。",
	"balanced":    "日常默认：不处理 email/IP 与身份证号；处理手机号、API Key 与自定义词。",
	"minimal":     "最轻：仅处理 API Key，其余内容一律保留。",
	CustomProfile: "自选：可单独开关六类规则，并挑选本次参与的自定义词。",
}

// allRules 是规则全集。
var allRules = []detector.RuleID{
	detector.RulePhone, detector.RuleIDCard, detector.RuleEmail,
	detector.RuleIP, detector.RuleAPIKey, detector.RuleCustom,
}

// Config 是合并后的运行配置。
type Config struct {
	MaxFileSize       int64                               `json:"max_file_size"`
	HighFreqThreshold int                                 `json:"high_freq_threshold"`
	IdleTimeoutMin    int                                 `json:"idle_timeout_minutes"`
	CustomWords       []string                            `json:"custom_words"`
	Profiles          map[string]map[detector.RuleID]bool `json:"profiles"`
}

// Defaults 返回内置配置（三预设 + 空词表）。
func Defaults() *Config {
	return &Config{
		MaxFileSize:       DefaultMaxFileSize,
		HighFreqThreshold: DefaultHighFreqThreshold,
		IdleTimeoutMin:    DefaultIdleTimeoutMinutes,
		CustomWords:       []string{},
		Profiles: map[string]map[detector.RuleID]bool{
			"strict":      ruleSet(true, true, true, true, true, true),
			"balanced":    ruleSet(true, false, false, false, true, true),
			"minimal":     ruleSet(false, false, false, false, true, false),
			CustomProfile: ruleSet(true, true, true, true, true, true),
		},
	}
}

// ProfileInfo 是随页面下发的 profile 明细，供前端渲染描述与规则矩阵（前端零硬编码）。
type ProfileInfo struct {
	Name        string                   `json:"name"`
	DisplayName string                   `json:"display_name"`
	Description string                   `json:"description"`
	Rules       map[detector.RuleID]bool `json:"rules"`
}

// balanced = phone + api_key + custom 开，idcard 关；email/ip 不在开集内故为关。
func ruleSet(phone, idcard, email, ip, apiKey, custom bool) map[detector.RuleID]bool {
	return map[detector.RuleID]bool{
		detector.RulePhone:  phone,
		detector.RuleIDCard: idcard,
		detector.RuleEmail:  email,
		detector.RuleIP:     ip,
		detector.RuleAPIKey: apiKey,
		detector.RuleCustom: custom,
	}
}

// UserConfigPath 返回用户配置文件路径（Windows %AppData% / Linux ~/.config）。
func UserConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "data-masking", "config.json"), nil
}

// Load 加载并合并配置；文件不存在时返回默认配置。
// 该函数保持 GUI 原有的兼容行为（读取失败/JSON 无效时回退默认）。
func Load(path string) *Config {
	data, err := os.ReadFile(path)
	if err != nil {
		return Defaults()
	}
	return LoadBytes(data)
}

// LoadBytes 按与 Load 相同的合并规则解析 JSON 字节；GUI 与 headless 共用，
// 便于 headless 在剔除 custom_words 后仍走同一套合并逻辑。
func LoadBytes(data []byte) *Config {
	cfg := Defaults()
	user := struct {
		MaxFileSize       *int64                              `json:"max_file_size"`
		HighFreqThreshold *int                                `json:"high_freq_threshold"`
		IdleTimeoutMin    *int                                `json:"idle_timeout_minutes"`
		CustomWords       []string                            `json:"custom_words"`
		Profiles          map[string]map[detector.RuleID]bool `json:"profiles"`
	}{}
	if err := json.Unmarshal(data, &user); err != nil {
		return cfg
	}
	if user.MaxFileSize != nil && *user.MaxFileSize > 0 {
		cfg.MaxFileSize = *user.MaxFileSize
	}
	if user.HighFreqThreshold != nil && *user.HighFreqThreshold > 0 {
		cfg.HighFreqThreshold = *user.HighFreqThreshold
	}
	if user.IdleTimeoutMin != nil && *user.IdleTimeoutMin > 0 {
		cfg.IdleTimeoutMin = *user.IdleTimeoutMin
	}
	if user.CustomWords != nil {
		cfg.CustomWords = user.CustomWords
	}
	// 冲突以用户版为准；保留 token `__custom` 不受用户配置覆盖。
	for name, rules := range user.Profiles {
		if name == CustomProfile {
			continue
		}
		full := make(map[detector.RuleID]bool, len(allRules))
		for _, r := range allRules {
			full[r] = rules[r]
		}
		cfg.Profiles[name] = full
	}
	return cfg
}

// Save 写回用户配置。
func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// ProfileNames 返回排序后的 profile 名称列表（含内置与用户新建）。
func (c *Config) ProfileNames() []string {
	names := make([]string, 0, len(c.Profiles))
	for n := range c.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// DisplayName 返回 profile 的显示名：保留 token `__custom` 映射为 custom，其余同名。
func DisplayName(name string) string {
	if name == CustomProfile {
		return "custom"
	}
	return name
}

// ProfileInfos 返回页面展示用的 profile 明细列表：三预设（固定顺序）→ custom → 用户自建（按名排序）。
// 自建 profile 展示字段缺省合成：display_name 回退 name、description 置空、rules 缺项补 false。
func (c *Config) ProfileInfos() []ProfileInfo {
	seen := map[string]bool{}
	out := make([]ProfileInfo, 0, len(c.Profiles)+1)
	add := func(name string) {
		if seen[name] {
			return
		}
		seen[name] = true
		out = append(out, ProfileInfo{
			Name:        name,
			DisplayName: DisplayName(name),
			Description: builtinDescriptions[name],
			Rules:       c.ruleSetFor(name),
		})
	}
	for _, n := range presetOrder {
		if _, ok := c.Profiles[n]; ok {
			add(n)
		}
	}
	add(CustomProfile)
	var rest []string
	for n := range c.Profiles {
		if !seen[n] {
			rest = append(rest, n)
		}
	}
	sort.Strings(rest)
	for _, n := range rest {
		add(n)
	}
	return out
}

// ruleSetFor 返回指定 profile 的完整规则开关（缺项补 false），不返回内部 map 引用。
func (c *Config) ruleSetFor(name string) map[detector.RuleID]bool {
	src := c.Profiles[name]
	full := make(map[detector.RuleID]bool, len(allRules))
	for _, r := range allRules {
		full[r] = src[r]
	}
	return full
}

// RulesForProfile 返回指定 profile 的规则开关；未知 profile 回退默认。
func (c *Config) RulesForProfile(name string) map[detector.RuleID]bool {
	if name == "" {
		name = DefaultProfile
	}
	if r, ok := c.Profiles[name]; ok {
		return r
	}
	return c.Profiles[DefaultProfile]
}
