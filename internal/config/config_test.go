package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pandatoufu/data-masking/internal/detector"
	"github.com/pandatoufu/data-masking/internal/loader"
)

func TestDefaultsProfiles(t *testing.T) {
	c := Defaults()
	if DefaultProfile != "balanced" {
		t.Fatalf("默认 profile 应为 balanced，got %q", DefaultProfile)
	}
	bal := c.RulesForProfile("balanced")
	wantOn := []detector.RuleID{detector.RulePhone, detector.RuleAPIKey, detector.RuleCustom}
	for _, r := range wantOn {
		if !bal[r] {
			t.Errorf("balanced 下 %s 应开启", r)
		}
	}
	for _, r := range []detector.RuleID{detector.RuleIDCard, detector.RuleEmail, detector.RuleIP} {
		if bal[r] {
			t.Errorf("balanced 下 %s 应关闭", r)
		}
	}
	strict := c.RulesForProfile("strict")
	for _, r := range allRules {
		if !strict[r] {
			t.Errorf("strict 下 %s 应开启", r)
		}
	}
	min := c.RulesForProfile("minimal")
	if !min[detector.RuleAPIKey] {
		t.Error("minimal 下 api_key 应开启")
	}
	for _, r := range []detector.RuleID{detector.RulePhone, detector.RuleIDCard, detector.RuleEmail, detector.RuleIP, detector.RuleCustom} {
		if min[r] {
			t.Errorf("minimal 下 %s 应关闭", r)
		}
	}
}

func TestUserConfigMergeWins(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	write := `{
	  "max_file_size": 2048,
	  "high_freq_threshold": 5,
	  "custom_words": ["内部代号"],
	  "profiles": {
	    "balanced": {"phone": true, "idcard": true, "email": false, "ip": false, "api_key": true, "custom": true},
	    "myprofile": {"custom": true}
	  }
	}`
	if err := os.WriteFile(path, []byte(write), 0o644); err != nil {
		t.Fatal(err)
	}
	c := Load(path)
	if c.MaxFileSize != 2048 || c.HighFreqThreshold != 5 {
		t.Fatalf("用户标量应生效：%+v", c)
	}
	if !c.RulesForProfile("balanced")[detector.RuleIDCard] {
		t.Error("用户版 balanced 覆盖内置（idcard 应开）")
	}
	if !c.RulesForProfile("myprofile")[detector.RuleCustom] {
		t.Error("新建 profile 应生效")
	}
	if len(c.CustomWords) != 1 || c.CustomWords[0] != "内部代号" {
		t.Fatalf("自定义词表应合并：%#v", c.CustomWords)
	}
	names := c.ProfileNames()
	if len(names) != 5 {
		t.Fatalf("应含 3 内置 + custom 保留档 + 1 新建，got %#v", names)
	}
	foundCustom := false
	for _, n := range names {
		if n == CustomProfile {
			foundCustom = true
		}
	}
	if !foundCustom {
		t.Fatalf("ProfileNames 应含保留 token %q：%#v", CustomProfile, names)
	}
}

func TestCustomProfileReservedToken(t *testing.T) {
	c := Defaults()
	rules := c.RulesForProfile(CustomProfile)
	for _, r := range allRules {
		if !rules[r] {
			t.Errorf("custom 档初始应全开，%s 却为关", r)
		}
	}

	// 用户版尝试覆盖保留 token 应被跳过（保持 Defaults 全开）。
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	write := `{"profiles": {"__custom": {"phone": false, "idcard": false, "email": false, "ip": false, "api_key": false, "custom": false}}}`
	if err := os.WriteFile(path, []byte(write), 0o644); err != nil {
		t.Fatal(err)
	}
	c2 := Load(path)
	rules2 := c2.RulesForProfile(CustomProfile)
	for _, r := range allRules {
		if !rules2[r] {
			t.Errorf("保留 token 不应被用户配置覆盖，%s 被置为关", r)
		}
	}
	if DisplayName(CustomProfile) != "custom" {
		t.Errorf("保留 token 显示名应为 custom，got %q", DisplayName(CustomProfile))
	}
}

func TestProfileInfosFallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	write := `{"profiles": {"myprofile": {"custom": true}}}`
	if err := os.WriteFile(path, []byte(write), 0o644); err != nil {
		t.Fatal(err)
	}
	infos := Load(path).ProfileInfos()

	byName := map[string]ProfileInfo{}
	for _, in := range infos {
		byName[in.Name] = in
	}

	// 三预设 + custom + 自建，顺序：strict, balanced, minimal, __custom, myprofile。
	wantOrder := []string{"strict", "balanced", "minimal", CustomProfile, "myprofile"}
	if len(infos) != len(wantOrder) {
		t.Fatalf("应含 %d 项，got %#v", len(wantOrder), infos)
	}
	for i, n := range wantOrder {
		if infos[i].Name != n {
			t.Fatalf("第 %d 项应为 %q，got %q", i, n, infos[i].Name)
		}
	}

	bal := byName["balanced"]
	if bal.DisplayName != "balanced" || bal.Description == "" {
		t.Errorf("builtin 描述应下发：%#v", bal)
	}
	if !strings.Contains(bal.Description, "email/IP") {
		t.Errorf("balanced 描述首句应披露不处理 email/IP：%q", bal.Description)
	}
	if !bal.Rules[detector.RulePhone] || bal.Rules[detector.RuleEmail] {
		t.Errorf("balanced 规则矩阵应实际反映开关：%#v", bal.Rules)
	}

	cu := byName[CustomProfile]
	if cu.DisplayName != "custom" || cu.Description == "" {
		t.Errorf("custom 档应映射显示名与描述：%#v", cu)
	}

	// 自建：display_name 回退 name、description 置空、rules 缺项补 false 且 6 类齐全。
	mp := byName["myprofile"]
	if mp.DisplayName != "myprofile" {
		t.Errorf("自建 display_name 应回退 name，got %q", mp.DisplayName)
	}
	if mp.Description != "" {
		t.Errorf("自建 description 应置空，got %q", mp.Description)
	}
	if len(mp.Rules) != len(allRules) {
		t.Fatalf("自建 rules 应补齐 6 类，got %#v", mp.Rules)
	}
	if !mp.Rules[detector.RuleCustom] {
		t.Error("自建 rules 应保留用户开启项")
	}
	for _, r := range []detector.RuleID{detector.RulePhone, detector.RuleIDCard, detector.RuleEmail, detector.RuleIP, detector.RuleAPIKey} {
		if mp.Rules[r] {
			t.Errorf("自建缺项应补 false，%s 却为开", r)
		}
	}
}

func TestLoadMissingFileFallsBackToDefaults(t *testing.T) {
	c := Load(filepath.Join(t.TempDir(), "nope.json"))
	if c.HighFreqThreshold != DefaultHighFreqThreshold {
		t.Fatalf("缺失文件应回退默认，got %+v", c)
	}
}

// 统一默认文件上限：config 与 loader 必须一致，且回退与显式配置语义分明。
func TestDefaultFileSizeLimitUnified(t *testing.T) {
	const want int64 = 52428800
	if DefaultMaxFileSize != want {
		t.Fatalf("config 默认上限应为 %d 字节，got %d", want, DefaultMaxFileSize)
	}
	if loader.DefaultMaxSize != want {
		t.Fatalf("loader fallback 默认上限应为 %d 字节，got %d", want, loader.DefaultMaxSize)
	}
	if Defaults().MaxFileSize != want {
		t.Fatalf("Defaults() 应携带新默认上限，got %d", Defaults().MaxFileSize)
	}

	// 无配置文件：回退到新默认值。
	if got := Load(filepath.Join(t.TempDir(), "missing.json")).MaxFileSize; got != want {
		t.Fatalf("无配置应回退 %d，got %d", want, got)
	}

	// 旧显式配置保留：默认值变化不得静默放宽用户设置。
	old := LoadBytes([]byte(`{"max_file_size": 10485760}`))
	if old.MaxFileSize != 10485760 {
		t.Fatalf("显式 10MiB 配置应保留，got %d", old.MaxFileSize)
	}

	// 显式其他正值同样保留；0/负数沿用原有回退语义。
	if got := LoadBytes([]byte(`{"max_file_size": 104857600}`)).MaxFileSize; got != 104857600 {
		t.Fatalf("显式 100MiB 配置应保留，got %d", got)
	}
	for _, raw := range []string{`{"max_file_size": 0}`, `{"max_file_size": -1}`} {
		if got := LoadBytes([]byte(raw)).MaxFileSize; got != want {
			t.Fatalf("%s 应回退新默认 %d，got %d", raw, want, got)
		}
	}
}
