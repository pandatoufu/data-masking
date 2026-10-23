package agent

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"

	"github.com/pandatoufu/data-masking/internal/config"
	"github.com/pandatoufu/data-masking/internal/detector"
)

// LoadLocalConfig 严格读取本地配置：
// 文件缺失使用内置默认；存在但读取失败、JSON 无效、顶层不是对象或生效字段非法时拒绝该次操作，
// 不静默换策略。headless 完全忽略 custom_words：先解码为 map[string]json.RawMessage 并
// 剔除该 key，再把剩余 JSON 交给既有合并逻辑，因此该字段为数组、对象、标量或 null 都不影响
// 本次操作，也不会进入生效配置或指纹。GUI 的 config.Load 行为不变。
func LoadLocalConfig(path string) (*config.Config, error) {
	if path == "" {
		return config.Defaults(), nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return config.Defaults(), nil
		}
		return nil, NewError(CodeInvalidConfig, MessageFor(CodeInvalidConfig))
	}
	scrubbed, err := sanitizeConfigJSON(raw)
	if err != nil {
		return nil, NewError(CodeInvalidConfig, MessageFor(CodeInvalidConfig))
	}
	return config.LoadBytes(scrubbed), nil
}

// sanitizeConfigJSON 剔除 custom_words 并严格检查其余生效字段。
// 合法性定义为 JSON 语法合法、顶层为对象、生效字段类型/取值合法；未知字段沿用既有忽略行为。
func sanitizeConfigJSON(raw []byte) ([]byte, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, err
	}
	if top == nil {
		// JSON null 能解码成 nil map 而不报错，但顶层不是对象，必须拒绝。
		return nil, errors.New("agent: 配置顶层不是对象")
	}
	delete(top, "custom_words")
	if err := validateEffectiveConfig(top); err != nil {
		return nil, err
	}
	return json.Marshal(top)
}

// validateEffectiveConfig 严格检查实际生效字段；custom_words 已剔除，不参与校验。
func validateEffectiveConfig(top map[string]json.RawMessage) error {
	probe := struct {
		MaxFileSize       *json.Number `json:"max_file_size"`
		HighFreqThreshold *json.Number `json:"high_freq_threshold"`
		IdleTimeoutMin    *json.Number `json:"idle_timeout_minutes"`
	}{}
	payload, err := json.Marshal(top)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return err
	}
	for _, n := range []*json.Number{probe.MaxFileSize, probe.HighFreqThreshold, probe.IdleTimeoutMin} {
		if n == nil {
			continue
		}
		v, err := n.Int64()
		if err != nil || v <= 0 {
			return errors.New("agent: 配置数值非法")
		}
	}
	if rawProfiles, ok := top["profiles"]; ok {
		var profiles map[string]map[string]bool
		if err := json.Unmarshal(rawProfiles, &profiles); err != nil {
			return err
		}
		if profiles == nil {
			// null 不是合法的 profile 集合。
			return errors.New("agent: profiles 非法")
		}
	}
	return nil
}

// RuleSnapshot 是本次操作实际生效的规则与词库快照（不引用配置内部 map）。
type RuleSnapshot struct {
	ProfileName       string
	Rules             map[detector.RuleID]bool
	CustomWords       []string
	MaxFileSize       int64
	HighFreqThreshold int
	// Cards 是本次生效的 cards 词库快照；custom 关闭时为空且未读取目录。
	Cards *CardSnapshot
}

// SnapshotProfile 返回指定 profile 的实际规则快照。
// profile 必填且必须存在于本地配置；不接受原文、敏感词数组或规则覆盖。
// headless 的词库不来自 config.json，CustomWords 由调用方从 cards 快照填充。
func SnapshotProfile(cfg *config.Config, profile string) (*RuleSnapshot, error) {
	if cfg == nil {
		return nil, NewError(CodeInvalidConfig, MessageFor(CodeInvalidConfig))
	}
	key := profile
	if profile == "" {
		return nil, NewError(CodeInvalidProfile, MessageFor(CodeInvalidProfile))
	}
	if profile == config.DisplayName(config.CustomProfile) {
		key = config.CustomProfile
	}
	src, ok := cfg.Profiles[key]
	if !ok {
		return nil, NewError(CodeInvalidProfile, MessageFor(CodeInvalidProfile))
	}
	rules := make(map[detector.RuleID]bool, len(src))
	for k, v := range src {
		rules[k] = v
	}
	return &RuleSnapshot{
		ProfileName:       key,
		Rules:             rules,
		CustomWords:       []string{},
		MaxFileSize:       cfg.MaxFileSize,
		HighFreqThreshold: cfg.HighFreqThreshold,
	}, nil
}

// LoadRuleSnapshot 加载 profile 与实际 cards 词库，形成本次用例的同一份快照。
// custom 关闭时不枚举/读取 cards，词集合为空；启用时任何词库错误都按固定 invalid_config 拒绝。
func LoadRuleSnapshot(cfg *config.Config, profile, root string) (*RuleSnapshot, error) {
	snap, err := SnapshotProfile(cfg, profile)
	if err != nil {
		return nil, err
	}
	cards, err := LoadCardSnapshot(root, snap.Enabled(detector.RuleCustom))
	if err != nil {
		return nil, err
	}
	snap.Cards = cards
	snap.CustomWords = cards.Words()
	return snap, nil
}

// CoreConfig 返回供共享 core 使用的配置副本：custom 词来自本次 cards 快照，
// 不透传 config.json 的旧词库。其他生效字段保持同一份配置。
func (s *RuleSnapshot) CoreConfig(cfg *config.Config) *config.Config {
	if cfg == nil {
		cfg = config.Defaults()
	}
	out := *cfg
	out.CustomWords = append([]string(nil), s.CustomWords...)
	return &out
}

// CardsFingerprint 返回本次 cards 私有指纹（custom 关闭时为空串）。
func (s *RuleSnapshot) CardsFingerprint() string {
	if s == nil || s.Cards == nil {
		return ""
	}
	return s.Cards.Fingerprint()
}

// Enabled 汇总某规则在快照中是否启用（缺项按 false）。
func (s *RuleSnapshot) Enabled(rule detector.RuleID) bool {
	if s == nil {
		return false
	}
	return s.Rules[rule]
}
