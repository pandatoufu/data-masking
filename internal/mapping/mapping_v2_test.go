package mapping

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	testSHA  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testText = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// 6.1 version 2 字段生成；version 1 新字段省略且字节不变。
func TestMappingVersion2Fields(t *testing.T) {
	m := NewDOCX("合同.docx", testSHA, testText, []Entry{{Placeholder: "[PHONE_1]", Category: "PHONE", Value: "13812345678"}})
	if m.Version != 2 || m.SourceFormat != "docx" || m.SourceEncoding != "" || m.SourceTextSHA256 != testText {
		t.Fatalf("version 2 字段错误：%+v", m)
	}
	data, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{`"version": 2`, `"source_format": "docx"`, `"source_encoding": ""`, `"source_text_sha256": "` + testText + `"`, `"source_sha256": "` + testSHA + `"`} {
		if !strings.Contains(s, want) {
			t.Errorf("version 2 mapping 应含 %s：%s", want, s)
		}
	}

	// version 1 序列化不得出现新增字段。
	v1 := New("notes.txt", "UTF-8", testSHA, []Entry{{Placeholder: "[PHONE_1]", Category: "PHONE", Value: "13812345678"}})
	d1, err := v1.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(d1), "source_format") || strings.Contains(string(d1), "source_text_sha256") {
		t.Fatalf("version 1 不应序列化新字段：%s", d1)
	}
}

// 6.3 version 2 必需字段/格式值/指纹格式校验与未来版本拒绝；v1 缺省兼容。
func TestMappingVersion2Validation(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{
			"version":            2,
			"source_file":        "合同.docx",
			"source_encoding":    "",
			"source_sha256":      testSHA,
			"source_format":      "docx",
			"source_text_sha256": testText,
			"placeholders":       []Entry{},
		}
	}
	// 合法空命中表通过。
	b, _ := json.Marshal(base())
	if _, err := Unmarshal(b); err != nil {
		t.Fatalf("合法 version 2 应通过：%v", err)
	}

	// 缺 source_format。
	m := base()
	delete(m, "source_format")
	b, _ = json.Marshal(m)
	if _, err := Unmarshal(b); err == nil {
		t.Fatal("缺 source_format 应拒绝")
	}
	// 错误 source_format。
	m = base()
	m["source_format"] = "text"
	b, _ = json.Marshal(m)
	if _, err := Unmarshal(b); err == nil {
		t.Fatal("错误 source_format 应拒绝")
	}
	// 非空 source_encoding。
	m = base()
	m["source_encoding"] = "UTF-8"
	b, _ = json.Marshal(m)
	if _, err := Unmarshal(b); err == nil {
		t.Fatal("非空 source_encoding 应拒绝")
	}
	// 非法指纹。
	m = base()
	m["source_text_sha256"] = "not-hex"
	b, _ = json.Marshal(m)
	if _, err := Unmarshal(b); err == nil {
		t.Fatal("非法指纹应拒绝")
	}
	// 未来版本。
	m = base()
	m["version"] = 99
	b, _ = json.Marshal(m)
	_, err := Unmarshal(b)
	if err == nil {
		t.Fatal("未知未来版本应拒绝")
	}
	if _, ok := err.(*FormatError); !ok {
		t.Fatalf("应为格式错误：%T", err)
	}

	// version 1 缺省/缺字段兼容。
	v1 := map[string]any{
		"version":         1,
		"source_file":     "notes.txt",
		"source_encoding": "GBK",
		"source_sha256":   testSHA,
		"placeholders":    []Entry{},
	}
	b, _ = json.Marshal(v1)
	if _, err := Unmarshal(b); err != nil {
		t.Fatalf("version 1 兼容应保留：%v", err)
	}
	// 无 version 字段按 1 处理。
	noVer := map[string]any{"source_file": "notes.txt", "source_encoding": "UTF-8", "source_sha256": testSHA}
	b, _ = json.Marshal(noVer)
	mp, err := Unmarshal(b)
	if err != nil || mp.Version != 1 {
		t.Fatalf("无 version 应缺省为 1：%v %+v", err, mp)
	}
}

// 6.4 四格命名与 basename 双分隔符。
func TestRestoredNamingGrid(t *testing.T) {
	// v1 + 文本：既有命名不变（RestoredName）。
	if got := RestoredName("notes.txt"); got != "notes.restored.txt" {
		t.Errorf("v1+文本命名变化：%q", got)
	}
	// v1/v2 + DOCX。
	if got := RestoredDocxName("notes.txt"); got != "notes.restored.docx" {
		t.Errorf("v1+DOCX 命名错误：%q", got)
	}
	if got := RestoredDocxName("合同.docx"); got != "合同.restored.docx" {
		t.Errorf("v2+DOCX 命名错误：%q", got)
	}
	// v2 + 文本/粘贴。
	if got := RestoredTextName("合同.docx", "txt"); got != "合同.restored.txt" {
		t.Errorf("v2+文本命名错误：%q", got)
	}
	if got := RestoredTextName("notes.txt", "md"); got != "notes.restored.md" {
		t.Errorf("v2+文本扩展名错误：%q", got)
	}
	// 空源名回退。
	if got := RestoredDocxName(""); got != "restored.docx" {
		t.Errorf("空源名 DOCX 回退错误：%q", got)
	}
	if got := RestoredTextName("", "txt"); got != "restored.txt" {
		t.Errorf("空源名文本回退错误：%q", got)
	}
	// Windows/Linux 源名只取 basename。
	if got := RestoredDocxName(`C:\docs\合同.docx`); got != "合同.restored.docx" {
		t.Errorf("Windows 源名 basename 错误：%q", got)
	}
	if got := RestoredDocxName("/home/u/notes.txt"); got != "notes.restored.docx" {
		t.Errorf("Linux 源名 basename 错误：%q", got)
	}
	// 文本绝不能命名为 .docx。
	if got := RestoredTextName("a.docx", "docx"); strings.HasSuffix(got, ".docx") {
		t.Errorf("文本不得命名为 .docx：%q", got)
	}
}
