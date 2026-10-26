package mapping

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMappingVersion3FieldsAndNaming(t *testing.T) {
	m := NewPPTX("报告.pptx", testSHA, testText, []Entry{{Placeholder: "[PHONE_1]", Category: "PHONE", Value: "13812345678"}})
	if m.Version != 3 || m.SourceFormat != "pptx" || m.SourceEncoding != "" || m.SourceTextSHA256 != testText {
		t.Fatalf("version 3 字段错误：%+v", m)
	}
	data, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{`"version": 3`, `"source_format": "pptx"`, `"source_encoding": ""`, `"source_text_sha256": "` + testText + `"`, `"source_sha256": "` + testSHA + `"`, `"placeholders": [`} {
		if !strings.Contains(s, want) {
			t.Errorf("version 3 mapping 应含 %s：%s", want, s)
		}
	}
	// 固定 JSON 向量（字段顺序与缩进稳定）。
	if !strings.Contains(s, "\"version\": 3,\n  \"source_file\": \"报告.pptx\",") {
		t.Errorf("unexpected version 3 JSON layout：%s", s)
	}

	if got := RestoredPPTXName("报告.pptx"); got != "报告.restored.pptx" {
		t.Errorf("PPTX 回程命名错误：%q", got)
	}
	if got := RestoredPPTXName(""); got != "restored.pptx" {
		t.Errorf("空源名 PPTX 回退错误：%q", got)
	}
	if got := RestoredPPTXName(`C:\docs\报告.pptx`); got != "报告.restored.pptx" {
		t.Errorf("Windows basename 错误：%q", got)
	}
	if got := RestoredPPTXName("/home/u/报告.pptx"); got != "报告.restored.pptx" {
		t.Errorf("Linux basename 错误：%q", got)
	}
}

func TestMappingVersion3Validation(t *testing.T) {
	base := func() map[string]any {
		return map[string]any{
			"version":            3,
			"source_file":        "报告.pptx",
			"source_encoding":    "",
			"source_sha256":      testSHA,
			"source_format":      "pptx",
			"source_text_sha256": testText,
			"placeholders":       []Entry{},
		}
	}
	b, _ := json.Marshal(base())
	if _, err := Unmarshal(b); err != nil {
		t.Fatalf("合法 version 3 应通过：%v", err)
	}

	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing-format", func(m map[string]any) { delete(m, "source_format") }},
		{"wrong-format", func(m map[string]any) { m["source_format"] = "docx" }},
		{"non-empty-encoding", func(m map[string]any) { m["source_encoding"] = "UTF-8" }},
		{"bad-source-sha", func(m map[string]any) { m["source_sha256"] = "zz" }},
		{"missing-text-sha", func(m map[string]any) { delete(m, "source_text_sha256") }},
		{"missing-placeholders", func(m map[string]any) { delete(m, "placeholders") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := base()
			c.mutate(m)
			b, _ := json.Marshal(m)
			_, err := Unmarshal(b)
			if err == nil {
				t.Fatal("应拒绝非法 version 3 mapping")
			}
			if _, ok := err.(*FormatError); !ok {
				t.Fatalf("应为格式错误：%T", err)
			}
		})
	}
}

func TestVersionOneTwoSerializationUnchanged(t *testing.T) {
	v1 := New("notes.txt", "UTF-8", testSHA, []Entry{{Placeholder: "[PHONE_1]", Category: "PHONE", Value: "138"}})
	d1, _ := v1.Marshal()
	if strings.Contains(string(d1), "source_format") || strings.Contains(string(d1), "source_text_sha256") {
		t.Fatalf("version 1 序列化变化：%s", d1)
	}
	v2 := NewDOCX("合同.docx", testSHA, testText, []Entry{})
	d2, _ := v2.Marshal()
	if strings.Contains(string(d2), `"version": 3`) {
		t.Fatalf("version 2 被改成 version 3：%s", d2)
	}
	if !strings.Contains(string(d2), `"source_format": "docx"`) {
		t.Fatalf("version 2 source_format 变化：%s", d2)
	}
}
