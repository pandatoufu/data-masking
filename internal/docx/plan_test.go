package docx

import (
	"bytes"
	"github.com/pandatoufu/data-masking/internal/docxtest"
	"strings"
	"testing"
)

// 5.2 decoded spans 到文字节点规划；实体、中文/非 BMP、首尾空格与多处替换。
func TestApplyEscapingAndWhitespace(t *testing.T) {
	inner := `<w:p><w:r><w:t>A&amp;B 中😀文</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t>trim</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t>13812345678 与 13900000000</w:t></w:r></w:p>`
	doc := buildRawDoc(t, inner)
	units := doc.Units()
	if units[0].Text != "A&B 中😀文" {
		t.Fatalf("实体解码错误：%q", units[0].Text)
	}
	// 实体处替换为需转义的字符。
	amp := strings.Index(units[0].Text, "&")
	if err := doc.Apply(units[0], []Edit{{Start: amp, End: amp + 1, Replacement: "<X&Y>"}}); err != nil {
		t.Fatal(err)
	}
	// 首尾空格需要 xml:space。
	if err := doc.Apply(units[1], []Edit{{Start: 0, End: 4, Replacement: "  pad  "}}); err != nil {
		t.Fatal(err)
	}
	// 多处替换（字节偏移）。
	n1 := strings.Index(units[2].Text, "13812345678")
	n2 := strings.Index(units[2].Text, "13900000000")
	if err := doc.Apply(units[2], []Edit{
		{Start: n1, End: n1 + 11, Replacement: "[PHONE_1]"},
		{Start: n2, End: n2 + 11, Replacement: "[PHONE_2]"},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.ValidateOutput(out); err != nil {
		t.Fatalf("ValidateOutput: %v", err)
	}
	s := string(entriesMap(t, out)["word/document.xml"])
	if !strings.Contains(s, "A&lt;X&amp;Y&gt;B 中😀文") {
		t.Fatalf("实体转义错误：%s", s)
	}
	if !strings.Contains(s, `xml:space="preserve">  pad  </w:t>`) {
		t.Fatalf("首尾空格应加 xml:space：%s", s)
	}
	od, _ := Open(out, DefaultLimits())
	od.rebuildUnits()
	got := strings.Join(unitTexts(od.units), "|")
	if !strings.Contains(got, "[PHONE_1] 与 [PHONE_2]") {
		t.Fatalf("多处替换错误：%s", got)
	}
}

// 5.3 跨节点命中写首节点 run 样式；多 unit 不漂移；表格结构无无关修改。
func TestApplyCrossNodeAndNoDrift(t *testing.T) {
	inner := `<w:p><w:r><w:rPr><w:b/></w:rPr><w:t>138</w:t></w:r>` +
		`<w:r><w:rPr><w:i/></w:rPr><w:t>1234</w:t></w:r>` +
		`<w:r><w:rPr><w:u w:val="single"/></w:rPr><w:t>5678</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t>电话 13900000000</w:t></w:r></w:p>`
	doc := buildRawDoc(t, inner)
	units := doc.Units()
	if units[0].Text != "13812345678" {
		t.Fatalf("跨 run 拼接错误：%q", units[0].Text)
	}
	if err := doc.Apply(units[0], []Edit{{Start: 0, End: 11, Replacement: "[PHONE_1]"}}); err != nil {
		t.Fatal(err)
	}
	i := strings.Index(units[1].Text, "13900000000")
	if err := doc.Apply(units[1], []Edit{{Start: i, End: i + 11, Replacement: "[PHONE_2]"}}); err != nil {
		t.Fatal(err)
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.ValidateOutput(out); err != nil {
		t.Fatalf("ValidateOutput: %v", err)
	}
	s := string(entriesMap(t, out)["word/document.xml"])
	// 首节点样式保留，后续未命中属性保留。
	for _, want := range []string{"<w:b/>", "<w:i/>", `<w:u w:val="single"/>`, "[PHONE_1]"} {
		if !strings.Contains(s, want) {
			t.Errorf("应保留 %q：%s", want, s)
		}
	}
	od, _ := Open(out, DefaultLimits())
	od.rebuildUnits()
	got := strings.Join(unitTexts(od.units), "|")
	if !strings.Contains(got, "[PHONE_1]") || !strings.Contains(got, "[PHONE_2]") {
		t.Fatalf("多 unit 替换漂移：%s", got)
	}
}

func TestApplyTableStructurePreserved(t *testing.T) {
	data, err := docxtest.BuildFullFixture(docxtest.FixtureOptions{
		Body:  []string{"正文 13812345678"},
		Table: [][]string{{"单元格 13900000000", "普通"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Open(data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Prepare(); err != nil {
		t.Fatal(err)
	}
	baseSnap, err := Snapshot(mustBytes(t, doc))
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range doc.Units() {
		for _, num := range []string{"13812345678", "13900000000"} {
			if i := strings.Index(u.Text, num); i >= 0 {
				if err := doc.Apply(u, []Edit{{Start: i, End: i + 11, Replacement: "[PHONE_X]"}}); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	snap, err := Snapshot(out)
	if err != nil {
		t.Fatal(err)
	}
	if diffs := baseSnap.Diff(snap); len(diffs) != 0 {
		t.Fatalf("表格/结构不应因文字替换改变：%+v", diffs)
	}
}

// 5.4 URI 排序、raw copy、确定性打包与预算门禁。
func TestDeterministicPackaging(t *testing.T) {
	data := fullFixture(t, false)
	doc, err := Open(data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Prepare(); err != nil {
		t.Fatal(err)
	}
	u := doc.Units()[0]
	if err := doc.Apply(u, []Edit{{Start: 0, End: len(u.Text), Replacement: "替换"}}); err != nil {
		t.Fatal(err)
	}
	out1, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	out2, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out1, out2) {
		t.Fatal("同一输入重复打包应字节一致")
	}
	// 未改条目解压后字节一致。
	orig := entriesMap(t, data)
	packed := entriesMap(t, out1)
	if !bytes.Equal(orig["word/header1.xml"], packed["word/header1.xml"]) {
		t.Fatal("未改部件解压字节应保持一致")
	}
	if bytes.Equal(orig["word/document.xml"], packed["word/document.xml"]) {
		t.Fatal("已改部件应发生变化")
	}
	// 输出预算门禁。
	doc.limits.MaxXMLPart = 4
	if _, err := doc.Bytes(); err == nil {
		t.Fatal("输出超过单 XML 预算应拒绝")
	}
}

// 5.5 输出重开验证：文字计划/关系/清理/结构/图片后置条件。
func TestValidateOutputPostconditions(t *testing.T) {
	data, err := docxtest.BuildFullFixture(docxtest.FixtureOptions{
		Body:  []string{"电话 13812345678"},
		Table: [][]string{{"A", "B"}},
		Image: []byte{0x89, 'P', 'N', 'G', 9, 9, 9},
	})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Open(data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Prepare(); err != nil {
		t.Fatal(err)
	}
	u := doc.Units()[0]
	i := strings.Index(u.Text, "13812345678")
	if err := doc.Apply(u, []Edit{{Start: i, End: i + 11, Replacement: "[PHONE_1]"}}); err != nil {
		t.Fatal(err)
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.ValidateOutput(out); err != nil {
		t.Fatalf("正向验证应通过：%v", err)
	}
	prepared, _ := doc.PartBytes("word/document.xml")

	// 故意遗漏文字回写。
	missing := replacePartInZip(t, out, "word/document.xml", func([]byte) []byte { return prepared })
	if err := doc.ValidateOutput(missing); err == nil {
		t.Fatal("遗漏文字回写应拒绝")
	}
	// 破坏图片。
	brokenImg := replacePartInZip(t, out, "word/media/image1.png", func(b []byte) []byte {
		c := append([]byte(nil), b...)
		c[0] ^= 0xFF
		return c
	})
	if err := doc.ValidateOutput(brokenImg); err == nil {
		t.Fatal("破坏图片应拒绝")
	}
	// 改变表格列宽（结构）。
	brokenTable := replacePartInZip(t, out, "word/document.xml", func(b []byte) []byte {
		return bytes.ReplaceAll(b, []byte(`w:w="2000"`), []byte(`w:w="3000"`))
	})
	if err := doc.ValidateOutput(brokenTable); err == nil {
		t.Fatal("改变表格结构应拒绝")
	}
}

// 5.6 输出故障注入与失败原子性。
func TestOutputFaultInjection(t *testing.T) {
	data, err := docxtest.BuildFullFixture(docxtest.FixtureOptions{Body: []string{"正文 13812345678"}, Headers: []string{"页眉"}})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Open(data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Prepare(); err != nil {
		t.Fatal(err)
	}
	u := doc.Units()[0]
	i := strings.Index(u.Text, "13812345678")
	if err := doc.Apply(u, []Edit{{Start: i, End: i + 11, Replacement: "[PHONE_1]"}}); err != nil {
		t.Fatal(err)
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	// 缺关系目标：篡改 rels 指向不存在的部件。
	badRel := replacePartInZip(t, out, "word/_rels/document.xml.rels", func(b []byte) []byte {
		return bytes.ReplaceAll(b, []byte("header1.xml"), []byte("missing.xml"))
	})
	if err := doc.ValidateOutput(badRel); err == nil {
		t.Fatal("缺关系目标应阻断")
	}

	// 残留历史：注入 w:ins。
	residual := replacePartInZip(t, out, "word/document.xml", func(b []byte) []byte {
		return bytes.Replace(b, []byte("</w:body>"), []byte(`<w:p><w:ins w:id="1" w:author="a" w:date="2020-01-01T00:00:00Z"><w:r><w:t>残留</w:t></w:r></w:ins></w:p></w:body>`), 1)
	})
	if err := doc.ValidateOutput(residual); err == nil {
		t.Fatal("残留历史应阻断")
	}

	// 超输出预算：收紧预算后重新打包应失败且无产物。
	saved := doc.limits
	doc.limits.MaxTotalUncompressed = 8
	if b, err := doc.Bytes(); err == nil || b != nil {
		t.Fatalf("超预算应失败且不返回产物：%v", err)
	}
	doc.limits = saved

	// 失败原子性：原件字节不变。
	if !bytes.Equal(data, doc.original) {
		t.Fatal("失败不应修改原件字节")
	}
}

func mustBytes(t *testing.T, doc *Document) []byte {
	t.Helper()
	b, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func entriesMap(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	entries, err := openZip(data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, e := range entries {
		out[e.name] = e.data
	}
	return out
}

// 5.4 输出 ZIP 条目按规范化 URI 排序。
func TestOutputURISorted(t *testing.T) {
	data := fullFixture(t, false)
	doc, err := Open(data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Prepare(); err != nil {
		t.Fatal(err)
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := openZip(out, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(entries); i++ {
		if entries[i-1].name > entries[i].name {
			t.Fatalf("输出条目未按 URI 排序：%s > %s", entries[i-1].name, entries[i].name)
		}
	}
}
