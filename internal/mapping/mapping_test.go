package mapping

import (
	"testing"

	"github.com/pandatoufu/data-masking/internal/detector"
)

func TestPlaceholderGrammar(t *testing.T) {
	if got := Placeholder(detector.RuleAPIKey, 1); got != "[APIKEY_1]" {
		t.Fatalf("api_key 占位符应为 [APIKEY_1]，got %q", got)
	}
	if got := Placeholder(detector.RulePhone, 12); got != "[PHONE_12]" {
		t.Fatalf("got %q", got)
	}
	if !ValidPlaceholder("[APIKEY_1]") {
		t.Error("[APIKEY_1] 应合法")
	}
	if ValidPlaceholder("[API_KEY_1]") {
		t.Error("[API_KEY_1] 应非法（枚举外前缀）")
	}
	if !ValidPlaceholder("[CUSTOM_3]") {
		t.Error("[CUSTOM_3] 应合法")
	}
	if ValidPlaceholder("[FOO_1]") {
		t.Error("[FOO_1] 应非法")
	}
}

func TestCollisionGrammar(t *testing.T) {
	if !ContainsGrammar("原文里已经有 [PHONE_1] 了") {
		t.Error("应检测到占位符文法冲突")
	}
	if ContainsGrammar("API_KEY_1 不是占位符") {
		t.Error("无方括号不应视为冲突")
	}
	if len(GrammarHits("[A_1] [B_2]")) != 2 {
		t.Error("应检出两个文法命中")
	}
}

func TestNaming(t *testing.T) {
	if got := MaskedName("notes.txt"); got != "notes.masked.txt" {
		t.Errorf("got %q", got)
	}
	if got := MappingName("notes.txt"); got != "notes.masked.mapping.json" {
		t.Errorf("got %q", got)
	}
	if got := MaskedName("app.py"); got != "app.masked.py" {
		t.Errorf("got %q", got)
	}
	if got := MappingName("app.py"); got != "app.masked.mapping.json" {
		t.Errorf("got %q", got)
	}
	if got := RestoredName("notes.txt"); got != "notes.restored.txt" {
		t.Errorf("got %q", got)
	}
}

// 新增纯文本名称的命名表：普通后缀保留，无后缀与单段点文件完整词干 + .txt 回退。
func TestNamingNewPlainTextNames(t *testing.T) {
	cases := []struct {
		orig     string
		masked   string
		mapping  string
		restored string
	}{
		{"data.JSON", "data.masked.JSON", "data.masked.mapping.json", "data.restored.JSON"},
		{"README", "README.masked.txt", "README.masked.mapping.json", "README.restored.txt"},
		{".env", ".env.masked.txt", ".env.masked.mapping.json", ".env.restored.txt"},
		{".gitignore", ".gitignore.masked.txt", ".gitignore.masked.mapping.json", ".gitignore.restored.txt"},
		{".env.local", ".env.masked.local", ".env.masked.mapping.json", ".env.restored.local"},
		{"notes.txt", "notes.masked.txt", "notes.masked.mapping.json", "notes.restored.txt"},
		{"合同.docx", "合同.masked.docx", "合同.masked.mapping.json", "合同.restored.docx"},
	}
	for _, c := range cases {
		t.Run(c.orig, func(t *testing.T) {
			if got := MaskedName(c.orig); got != c.masked {
				t.Errorf("MaskedName=%q want %q", got, c.masked)
			}
			if got := MappingName(c.orig); got != c.mapping {
				t.Errorf("MappingName=%q want %q", got, c.mapping)
			}
			if got := RestoredName(c.orig); got != c.restored {
				t.Errorf("RestoredName=%q want %q", got, c.restored)
			}
		})
	}
}

// 跨格式文本扩展名：无普通扩展名回退 txt，普通后缀小写。
func TestTextExtNames(t *testing.T) {
	cases := map[string]string{
		"reply.json": "json",
		"reply.YAML": "yaml",
		"README":     "txt",
		".env":       "txt",
		".gitignore": "txt",
		".env.local": "local",
		"paste":      "txt",
		"":           "txt",
	}
	for name, want := range cases {
		if got := TextExt(name); got != want {
			t.Errorf("TextExt(%q)=%q want %q", name, got, want)
		}
	}
}

func TestNormalizeAndLookup(t *testing.T) {
	if got := Normalize("[ＰＨＯＮＥ_１]"); got != "[PHONE_1]" {
		t.Fatalf("全角应归一化为半角大写，got %q", got)
	}
	if got := Normalize("[phone_1]"); got != "[PHONE_1]" {
		t.Fatalf("小写应归一化为大写，got %q", got)
	}
	m := New("notes.txt", "UTF-8", "abc", []Entry{
		{Placeholder: "[PHONE_1]", Category: "PHONE", Value: "13812345678"},
	})
	if v, ok := m.Lookup("[PHONE_1]"); !ok || v != "13812345678" {
		t.Fatalf("精确查表失败：%q %v", v, ok)
	}
	if v, ok := m.Lookup("[phone_1]"); !ok || v != "13812345678" {
		t.Fatalf("大小写归一化查表失败：%q %v", v, ok)
	}
	if v, ok := m.Lookup("[ＰＨＯＮＥ_１]"); !ok || v != "13812345678" {
		t.Fatalf("全角归一化查表失败：%q %v", v, ok)
	}
	if _, ok := m.Lookup("[PHONE_9]"); ok {
		t.Error("不存在的占位符不应命中")
	}
}

func TestResidualHits(t *testing.T) {
	hits := ResidualHits("残留 [PHONE_1] 与 [custom_2] 以及 [FOO_3]")
	if len(hits) != 2 {
		t.Fatalf("应仅以枚举集检出 2 处残留，got %#v", hits)
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	m := New("notes.txt", "GBK", "deadbeef", []Entry{{Placeholder: "[PHONE_1]", Category: "PHONE", Value: "13812345678"}})
	data, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	back, err := Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if back.SourceEncoding != "GBK" || len(back.Placeholders) != 1 || back.Placeholders[0].Value != "13812345678" {
		t.Fatalf("round trip 失败：%+v", back)
	}
}
