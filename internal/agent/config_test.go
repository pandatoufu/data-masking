package agent

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pandatoufu/data-masking/internal/config"
	"github.com/pandatoufu/data-masking/internal/detector"
)

func TestLoadLocalConfigMissingUsesDefaults(t *testing.T) {
	cfg, err := LoadLocalConfig(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("缺文件应使用默认：%v", err)
	}
	if cfg.MaxFileSize != config.DefaultMaxFileSize || cfg.HighFreqThreshold != config.DefaultHighFreqThreshold {
		t.Fatalf("应返回内置默认：%+v", cfg)
	}
}

func TestLoadLocalConfigInvalidRejected(t *testing.T) {
	cases := map[string]string{
		"broken-json": "{ not json",
		"zero-size":   `{"max_file_size": 0}`,
		"negative":    `{"high_freq_threshold": -5}`,
		"wrong-type":  `{"max_file_size": "big"}`,
		"non-integer": `{"max_file_size": 1.5}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadLocalConfig(path)
			var ae *Error
			if !errors.As(err, &ae) || ae.Code != CodeInvalidConfig {
				t.Fatalf("应返回 invalid_config，got %v", err)
			}
		})
	}
}

// GUI 的 config.Load 兼容行为保持不变：损坏文件仍回退默认，不因 headless 严格模式改变。
func TestGUIConfigLoadCompatUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{ broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Load(path)
	if cfg.MaxFileSize != config.DefaultMaxFileSize {
		t.Fatalf("GUI Load 应回退默认：%+v", cfg)
	}
}

// headless 完全忽略 custom_words 的值与类型，只校验其他生效字段。
func TestLoadLocalConfigIgnoresCustomWords(t *testing.T) {
	cases := map[string]string{
		"array":  `{"custom_words":["旧词","SECRET-OLD-WORD"],"high_freq_threshold":7}`,
		"object": `{"custom_words":{"nested":["x"]},"high_freq_threshold":7}`,
		"string": `{"custom_words":"SECRET-OLD-WORD","high_freq_threshold":7}`,
		"number": `{"custom_words":42,"high_freq_threshold":7}`,
		"null":   `{"custom_words":null,"high_freq_threshold":7}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadLocalConfig(path)
			if err != nil {
				t.Fatalf("合法对象应可用：%v", err)
			}
			if cfg.HighFreqThreshold != 7 {
				t.Fatalf("其他生效字段应保留：%+v", cfg)
			}
			if len(cfg.CustomWords) != 0 {
				t.Fatalf("旧 custom_words 不得进入生效配置：%v", cfg.CustomWords)
			}
		})
	}
}

// 顶层不是对象、JSON 无效或其他生效字段非法仍 invalid_config，不因剔除词字段回退默认。
func TestLoadLocalConfigStrictEffectiveFields(t *testing.T) {
	cases := map[string]string{
		"array-top":         `[]`,
		"string-top":        `"x"`,
		"null-top":          `null`,
		"number-top":        `42`,
		"broken-json":       `{ not json`,
		"profiles-null":     `{"profiles":null}`,
		"profiles-wrong":    `{"profiles":{"team":{"phone":"yes"}}}`,
		"profiles-not-map":  `{"profiles":[]}`,
		"max-size-bool":     `{"max_file_size":true}`,
		"threshold-float":   `{"high_freq_threshold":1.5}`,
		"idle-timeout-zero": `{"idle_timeout_minutes":0}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadLocalConfig(path)
			var ae *Error
			if !errors.As(err, &ae) || ae.Code != CodeInvalidConfig {
				t.Fatalf("应返回 invalid_config，got %v", err)
			}
		})
	}
}

func TestSnapshotProfileStrict(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body := `{"custom_words":["内部代号","acme"],"profiles":{"team":{"phone":true,"api_key":true}}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadLocalConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := SnapshotProfile(cfg, ""); err == nil {
		t.Fatal("空 profile 应拒绝")
	} else {
		var ae *Error
		if !errors.As(err, &ae) || ae.Code != CodeInvalidProfile {
			t.Fatalf("应返回 invalid_profile，got %v", err)
		}
	}
	if _, err := SnapshotProfile(cfg, "nope"); err == nil {
		t.Fatal("未知 profile 应拒绝，不得回退 balanced")
	}

	snap, err := SnapshotProfile(cfg, "balanced")
	if err != nil {
		t.Fatalf("balanced 应可用：%v", err)
	}
	if !snap.Enabled(detector.RulePhone) || snap.Enabled(detector.RuleEmail) || snap.Enabled(detector.RuleIDCard) {
		t.Fatalf("balanced 实际开关不符：%+v", snap.Rules)
	}
	if len(snap.CustomWords) != 0 {
		t.Fatalf("headless 快照不得包含 config.json 旧词库：%v", snap.CustomWords)
	}

	team, err := SnapshotProfile(cfg, "team")
	if err != nil {
		t.Fatalf("自建 profile 应可用：%v", err)
	}
	if !team.Enabled(detector.RulePhone) || !team.Enabled(detector.RuleAPIKey) || team.Enabled(detector.RuleCustom) {
		t.Fatalf("自建 profile 开关不符：%+v", team.Rules)
	}

	// 快照是副本：修改返回 map 不影响配置。
	snap.Rules[detector.RuleEmail] = true
	if cfg.Profiles["balanced"][detector.RuleEmail] {
		t.Fatal("快照不得引用配置内部 map")
	}
}
