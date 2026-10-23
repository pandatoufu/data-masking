package uiprefs

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pandatoufu/data-masking/internal/i18n"
)

func TestLoadMissingReturnsDefaults(t *testing.T) {
	p := Load(filepath.Join(t.TempDir(), "nope.json"), i18n.ZH)
	if p.Version != Version || p.Theme != ThemeSystem || p.Language != "zh" {
		t.Fatalf("缺失文件应回退默认（中文系统 zh）：%+v", p)
	}
	p = Load(filepath.Join(t.TempDir(), "nope.json"), i18n.EN)
	if p.Language != "en" {
		t.Fatalf("英文系统默认应为 en：%+v", p)
	}
}

func TestLoadCorruptReturnsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ui-prefs.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := Load(path, i18n.EN)
	if p.Version != Version || p.Theme != ThemeSystem || p.Language != "en" {
		t.Fatalf("损坏文件应回退默认：%+v", p)
	}
}

// 单字段损坏/未知只影响该字段，其他有效字段保留。
func TestLoadFieldFallbackKeepsValidFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ui-prefs.json")
	body := `{"version":1,"theme":"dark","language":"fr"}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p := Load(path, i18n.ZH)
	if p.Theme != ThemeDark {
		t.Fatalf("有效 theme 应保留：%+v", p)
	}
	if p.Language != "zh" {
		t.Fatalf("未知 language 应回退系统默认：%+v", p)
	}

	// theme 损坏但 language 有效。
	body = `{"version":1,"theme":123,"language":"en"}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p = Load(path, i18n.ZH)
	if p.Theme != ThemeSystem || p.Language != "en" {
		t.Fatalf("theme 损坏应回退而 language 保留：%+v", p)
	}
}

// 未知多余字段被忽略，不写回。
func TestLoadIgnoresUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ui-prefs.json")
	body := `{"version":1,"theme":"light","language":"zh","words":["secret"],"mapping":"x"}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p := Load(path, i18n.EN)
	if p.Theme != ThemeLight || p.Language != "zh" {
		t.Fatalf("有效字段应保留：%+v", p)
	}
}

// 持久化仅含 version/theme/language，敏感字段绝不落盘。
func TestSaveWritesOnlyThreeSensitiveFreeKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ui-prefs.json")
	st := NewStore(path, i18n.EN)
	if err := st.Save(Prefs{Version: 1, Theme: ThemeDark, Language: "zh"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("写出内容应为合法 JSON：%v", err)
	}
	if len(m) != 3 {
		t.Fatalf("偏好文件只应含 version/theme/language：%v", m)
	}
	for _, k := range []string{"version", "theme", "language"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("缺少键 %s：%v", k, m)
		}
	}
	// 确认没有被误存的敏感内容（词/映射/路径）。
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "mapping") {
		t.Fatalf("偏好不得含敏感内容：%s", raw)
	}
}

// 重复更新按最后选择收敛。
func TestRepeatedUpdatesConvergeToLast(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ui-prefs.json")
	st := NewStore(path, i18n.EN)
	for _, theme := range []string{ThemeLight, ThemeDark, ThemeSystem, ThemeDark} {
		if err := st.Save(Prefs{Version: 1, Theme: theme, Language: "en"}); err != nil {
			t.Fatal(err)
		}
	}
	if got := st.Load(); got.Theme != ThemeDark {
		t.Fatalf("应收敛到最后一次选择：%+v", got)
	}
}

// 写入失败保留旧有效内容；失败不产生半写文件。
func TestSaveFailureKeepsOldValidContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ui-prefs.json")
	st := NewStore(path, i18n.EN)
	if err := st.Save(Prefs{Version: 1, Theme: ThemeLight, Language: "en"}); err != nil {
		t.Fatal(err)
	}
	prev, _ := os.ReadFile(path)

	saved := renameForWrite
	renameForWrite = func(_, _ string) error { return errors.New("boom") }
	defer func() { renameForWrite = saved }()

	if err := st.Save(Prefs{Version: 1, Theme: ThemeDark, Language: "zh"}); err == nil {
		t.Fatal("替换失败应返回错误")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("旧文件应保留：%v", err)
	}
	if string(after) != string(prev) {
		t.Fatalf("替换失败应保持旧有效内容：%q vs %q", after, prev)
	}
	// 无残留临时文件。
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".ui-prefs-") {
			t.Fatalf("失败后不应残留临时文件：%s", e.Name())
		}
	}
}

// 并发写串行、最终文件始终合法 JSON。
func TestSerializedConcurrentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ui-prefs.json")
	st := NewStore(path, i18n.EN)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			theme := ThemeLight
			if n%2 == 0 {
				theme = ThemeDark
			}
			_ = st.Save(Prefs{Version: 1, Theme: theme, Language: "en"})
		}(i)
	}
	wg.Wait()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("最终文件应合法：%v %s", err, raw)
	}
}

// Normalize 回退非法值。
func TestNormalize(t *testing.T) {
	st := NewStore("x", i18n.ZH)
	got := st.Normalize(Prefs{Version: 0, Theme: "neon", Language: "de"})
	if got.Version != Version || got.Theme != ThemeSystem || got.Language != "zh" {
		t.Fatalf("非法值应回退：%+v", got)
	}
}

// 系统语言探测可注入：任务要求首启中文系统选 zh、其余 en。
func TestSystemLanguageInjection(t *testing.T) {
	restore := SetSystemLanguageForTest(func() i18n.Lang { return i18n.ZH })
	defer restore()
	if SystemLanguage() != i18n.ZH {
		t.Fatal("注入后应返回 zh")
	}
}

// Windows LANGID 主语言判定（纯函数，跨平台可测）。
func TestPrimaryLangFromLANGID(t *testing.T) {
	cases := map[uint16]i18n.Lang{
		0x0804: i18n.ZH, // zh-CN
		0x0404: i18n.ZH, // zh-TW
		0x0409: i18n.EN, // en-US
		0x0411: i18n.EN, // ja-JP
	}
	for id, want := range cases {
		if got := primaryLangFromLANGID(id); got != want {
			t.Errorf("LANGID %#x: got %s want %s", id, got, want)
		}
	}
}

// locale 归一：C/POSIX 空、中文 zh、其余 en。
func TestLocaleToLang(t *testing.T) {
	for _, s := range []string{"zh_CN.UTF-8", "zh", "zh-TW", "zh_CN@latin"} {
		if got := localeToLang(s); got != i18n.ZH {
			t.Errorf("locale %q 应为 zh，got %s", s, got)
		}
	}
	for _, s := range []string{"en_US.UTF-8", "fr_FR", "de", "ja_JP"} {
		if got := localeToLang(s); got != i18n.EN {
			t.Errorf("locale %q 应为 en，got %s", s, got)
		}
	}
	if !isUninformativeLocale("C") || !isUninformativeLocale("POSIX") || isUninformativeLocale("zh_CN.UTF-8") {
		t.Fatal("C/POSIX 判定错误")
	}
}
