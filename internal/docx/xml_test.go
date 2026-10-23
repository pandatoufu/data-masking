package docx

import (
	"github.com/pandatoufu/data-masking/internal/docxtest"
	"strings"
	"testing"
)

func rawMainDoc(t *testing.T, inner string, extraRootAttrs string) []byte {
	t.Helper()
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:document xmlns:w="` + nsWMLTransitional + `" xmlns:r="` + nsRelTransitional + `"` + extraRootAttrs + `><w:body>` + inner + `</w:body></w:document>`)
}

// 2.4 实体、CDATA、中文、非 BMP、不同前缀、BOM 的正确偏移；拒绝损坏 XML 与不支持编码。
func TestXMLSourceSpansAndEncoding(t *testing.T) {
	// 实体 + 中文 + 非 BMP + CDATA。
	b := docxtest.NewBuilder(false)
	inner := `<w:p><w:r><w:t xml:space="preserve">A&amp;B 中😀文</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t><![CDATA[CDATA 内容]]></w:t></w:r></w:p>`
	b.AddPart("word/document.xml", ctMain, rawMainDoc(t, inner, ""))
	b.SetMain("word/document.xml")
	doc, err := Open(b.Bytes(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Prepare(); err != nil {
		t.Fatal(err)
	}
	units := doc.Units()
	if len(units) != 2 {
		t.Fatalf("应有 2 个文字单元，got %d", len(units))
	}
	if units[0].Text != "A&B 中😀文" {
		t.Fatalf("实体/非 BMP 解码错误：%q", units[0].Text)
	}
	if units[1].Text != "CDATA 内容" {
		t.Fatalf("CDATA 解码错误：%q", units[1].Text)
	}
	// 整单元替换，验证源码 span 覆盖实体/CDATA。
	if err := doc.Apply(units[0], []Edit{{Start: 0, End: len(units[0].Text), Replacement: "REPL"}}); err != nil {
		t.Fatal(err)
	}
	if err := doc.Apply(units[1], []Edit{{Start: 0, End: len(units[1].Text), Replacement: "REPL2"}}); err != nil {
		t.Fatal(err)
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.ValidateOutput(out); err != nil {
		t.Fatalf("ValidateOutput: %v", err)
	}
	od, _ := Open(out, DefaultLimits())
	od.rebuildUnits()
	if od.units[0].Text != "REPL" || od.units[1].Text != "REPL2" {
		t.Fatalf("替换结果错误：%q / %q", od.units[0].Text, od.units[1].Text)
	}

	// 不同前缀。
	b = docxtest.NewBuilder(false)
	custom := []byte(`<?xml version="1.0" encoding="UTF-8"?>` +
		`<x:document xmlns:x="` + nsWMLTransitional + `"><x:body><x:p><x:r><x:t>自定义前缀文字</x:t></x:r></x:p></x:body></x:document>`)
	b.AddPart("word/document.xml", ctMain, custom)
	b.SetMain("word/document.xml")
	doc, err = Open(b.Bytes(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Prepare(); err != nil {
		t.Fatal(err)
	}
	if len(doc.Units()) != 1 || doc.Units()[0].Text != "自定义前缀文字" {
		t.Fatalf("不同前缀应被识别：%+v", doc.Units())
	}

	// UTF-8 BOM 偏移。
	b = docxtest.NewBuilder(false)
	withBOM := append([]byte{0xEF, 0xBB, 0xBF}, rawMainDoc(t, docxtest.Para("BOM 文字"), "")...)
	b.AddPart("word/document.xml", ctMain, withBOM)
	b.SetMain("word/document.xml")
	doc, err = Open(b.Bytes(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Prepare(); err != nil {
		t.Fatal(err)
	}
	u := doc.Units()[0]
	if u.Text != "BOM 文字" {
		t.Fatalf("BOM 解码错误：%q", u.Text)
	}
	if err := doc.Apply(u, []Edit{{Start: 0, End: len(u.Text), Replacement: "BOMREPL"}}); err != nil {
		t.Fatal(err)
	}
	out, err = doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.ValidateOutput(out); err != nil {
		t.Fatalf("BOM 偏移写回失败：%v", err)
	}

	// 损坏 XML。
	b = docxtest.NewBuilder(false)
	b.AddPart("word/document.xml", ctMain, []byte(`<w:document xmlns:w="`+nsWMLTransitional+`"><w:body><w:p><w:r><w:t>x</w:t></w:r></w:p></w:body>`))
	b.SetMain("word/document.xml")
	doc, err = Open(b.Bytes(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Prepare(); err == nil {
		t.Fatal("未闭合 XML 应拒绝")
	}

	// 不支持编码（声明 GBK）。
	b = docxtest.NewBuilder(false)
	gbkDecl := []byte(`<?xml version="1.0" encoding="GBK"?><w:document xmlns:w="` + nsWMLTransitional + `"><w:body><w:p><w:r><w:t>x</w:t></w:r></w:p></w:body></w:document>`)
	b.AddPart("word/document.xml", ctMain, gbkDecl)
	b.SetMain("word/document.xml")
	doc, err = Open(b.Bytes(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Prepare(); err == nil {
		t.Fatal("非 UTF-8 编码应拒绝")
	}
}

// 2.5 命名空间绑定栈、未声明/保留前缀与深度门禁。
func TestNamespaceBindingAndDepth(t *testing.T) {
	openDoc := func(docXML []byte, lim Limits) (*Document, error) {
		b := docxtest.NewBuilder(false)
		b.AddPart("word/document.xml", ctMain, docXML)
		b.SetMain("word/document.xml")
		return Open(b.Bytes(), lim)
	}

	// 未声明前缀。
	unbound := []byte(`<w:document><w:body><w:p><w:r><w:t>x</w:t></w:r></w:p></w:body></w:document>`)
	doc, err := openDoc(unbound, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Prepare(); err == nil {
		t.Fatal("未声明前缀应拒绝")
	}

	// 局部重绑定（同一 URI）允许。
	rebind := []byte(`<w:document xmlns:w="` + nsWMLTransitional + `"><w:body><w:p xmlns:w="` + nsWMLTransitional + `"><w:r><w:t>x</w:t></w:r></w:p></w:body></w:document>`)
	doc, err = openDoc(rebind, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Prepare(); err != nil {
		t.Fatalf("合法局部重绑定应通过：%v", err)
	}

	// 保留前缀 xml 被错误重绑定。
	badXML := []byte(`<w:document xmlns:w="` + nsWMLTransitional + `" xmlns:xml="urn:wrong"><w:body><w:p><w:r><w:t>x</w:t></w:r></w:p></w:body></w:document>`)
	doc, err = openDoc(badXML, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Prepare(); err == nil {
		t.Fatal("重绑定保留前缀 xml 应拒绝")
	}

	// 深度门禁：N 个 customXml 包裹 + document/body/p/r/t。
	nested := func(n int) []byte {
		inner := strings.Repeat("<w:customXml>", n) + docxtest.Para("x") + strings.Repeat("</w:customXml>", n)
		return rawMainDoc(t, inner, "")
	}
	// depth = 2(document,body) + n + 3(p,r,t)。
	atLimit := nested(123) // 128
	doc, err = openDoc(atLimit, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Prepare(); err != nil {
		t.Fatalf("恰好 128 层应通过：%v", err)
	}
	overLimit := nested(124) // 129
	doc, err = openDoc(overLimit, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Prepare(); err == nil {
		t.Fatal("超过 128 层应拒绝")
	}
}
