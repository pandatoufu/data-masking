package docx

import (
	"archive/zip"
	"bytes"
	"github.com/pandatoufu/data-masking/internal/docxtest"
	"io"
	"strings"
	"testing"
	"time"
)

func rawDocFullNS(inner string) []byte {
	ns := map[string]string{
		"w":   nsWMLTransitional,
		"r":   nsRelTransitional,
		"mc":  "http://schemas.openxmlformats.org/markup-compatibility/2006",
		"wp":  "http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing",
		"a":   "http://schemas.openxmlformats.org/drawingml/2006/main",
		"wps": "http://schemas.microsoft.com/office/word/2010/wordprocessingShape",
		"v":   "urn:schemas-microsoft-com:vml",
	}
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document`)
	for _, k := range sortedKeys(ns) {
		sb.WriteString(` xmlns:` + k + `="` + ns[k] + `"`)
	}
	sb.WriteString(`><w:body>` + inner + `</w:body></w:document>`)
	return []byte(sb.String())
}

func buildRawDoc(t *testing.T, inner string) *Document {
	t.Helper()
	b := docxtest.NewBuilder(false)
	b.AddPart("word/document.xml", ctMain, rawDocFullNS(inner))
	b.SetMain("word/document.xml")
	doc, err := Open(b.Bytes(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Prepare(); err != nil {
		t.Fatal(err)
	}
	return doc
}

// 3.1 跨 runs/书签/超链接拼接；同一文字节点不重复归属。
func TestUnitsCrossRunsBookmarksHyperlinks(t *testing.T) {
	inner := `<w:p>` +
		`<w:bookmarkStart w:id="1" w:name="bm"/>` +
		`<w:r><w:t>138</w:t></w:r>` +
		`<w:bookmarkEnd w:id="1"/>` +
		`<w:hyperlink r:id="rIdX"><w:r><w:t>1234</w:t></w:r></w:hyperlink>` +
		`<w:r><w:t>5678</w:t></w:r>` +
		`</w:p>`
	doc := buildRawDoc(t, inner)
	units := doc.Units()
	if len(units) != 1 {
		t.Fatalf("应拼接为一个单元，got %d: %+v", len(units), units)
	}
	if units[0].Text != "13812345678" {
		t.Fatalf("跨 runs/书签/超链接拼接错误：%q", units[0].Text)
	}
	// 每个文字节点只归属一次。
	if len(units[0].pieces) != 3 {
		t.Fatalf("应有 3 个文字节点，got %d", len(units[0].pieces))
	}
}

// 3.2 全部声明区域、声明未引用部件、乱序 ZIP 的逻辑顺序。
func TestUnitsAllRegionsStableOrder(t *testing.T) {
	data, err := docxtest.BuildFullFixture(docxtest.FixtureOptions{
		Body:      []string{"正文"},
		Headers:   []string{"页眉默认", "页眉首页"},
		Footers:   []string{"页脚默认"},
		Footnotes: []string{"脚注"},
		Endnotes:  []string{"尾注"},
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
	// 声明但未引用的页眉：直接添加部件与 content type，不建立关系。
	b := docxtest.NewBuilder(false)
	b.AddPart("word/document.xml", ctMain, rawMainDoc(t, docxtest.Para("正文"), ""))
	b.SetMain("word/document.xml")
	b.AddPart("word/header1.xml", ctHeader, b.WMLPart("hdr", docxtest.Para("页眉默认")))
	b.AddPart("word/header2.xml", ctHeader, b.WMLPart("hdr", docxtest.Para("未引用页眉")))
	b.AddRel("word/document.xml", "rIdH", b.RelNS()+"/header", "header1.xml", false)
	doc2, err := Open(b.Bytes(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc2.Prepare(); err != nil {
		t.Fatal(err)
	}
	joined2 := strings.Join(unitTexts(doc2.Units()), "|")
	if !strings.Contains(joined2, "未引用页眉") {
		t.Fatalf("声明未引用部件应被处理：%s", joined2)
	}

	// 逻辑顺序：document -> header(URI 序) -> footer -> footnote -> endnote。
	kinds := []PartKind{}
	for _, u := range doc.Units() {
		if len(kinds) == 0 || kinds[len(kinds)-1] != u.PartKind {
			kinds = append(kinds, u.PartKind)
		}
	}
	want := []PartKind{KindDocument, KindHeader, KindFooter, KindFootnote, KindEndnote}
	if strings.Join(kindStrings(kinds), ",") != strings.Join(kindStrings(want), ",") {
		t.Fatalf("区域顺序错误：%v want %v", kinds, want)
	}

	// 乱序 ZIP 得到相同逻辑顺序。
	reordered := reorderZip(t, data, []string{"word/endnotes.xml", "word/footnotes.xml", "word/footer1.xml", "word/header2.xml", "word/header1.xml", "word/document.xml"})
	doc3, err := Open(reordered, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc3.Prepare(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(unitTexts(doc.Units()), "|") != strings.Join(unitTexts(doc3.Units()), "|") {
		t.Fatalf("乱序 ZIP 逻辑顺序应一致：\n%v\n%v", unitTexts(doc.Units()), unitTexts(doc3.Units()))
	}
}

// 3.3 DrawingML/VML 文本框、Choice/Fallback 双分支、隐藏文字；文本框不与宿主拼接。
func TestUnitsTextboxesChoiceFallbackHidden(t *testing.T) {
	inner := `<w:p><w:r><w:t>宿主前</w:t></w:r>` +
		`<w:r><w:drawing><wp:inline><a:graphic><a:graphicData><wps:wsp><wps:txbx><w:txbxContent><w:p><w:r><w:t>Drawing 文本</w:t></w:r></w:p></w:txbxContent></wps:txbx></wps:wsp></a:graphicData></a:graphic></wp:inline></w:drawing></w:r>` +
		`<w:r><w:t>宿主后</w:t></w:r></w:p>` +
		`<w:p><w:r><w:pict><v:shape><v:textbox><w:txbxContent><w:p><w:r><w:t>VML 文本</w:t></w:r></w:p></w:txbxContent></v:textbox></v:shape></w:pict></w:r></w:p>` +
		`<w:p><w:r><mc:AlternateContent><mc:Choice Requires="wps"><w:drawing><wp:inline><a:graphic><a:graphicData><wps:wsp><wps:txbx><w:txbxContent><w:p><w:r><w:t>Choice 文本</w:t></w:r></w:p></w:txbxContent></wps:txbx></wps:wsp></a:graphicData></a:graphic></wp:inline></w:drawing></mc:Choice><mc:Fallback><w:pict><v:shape><v:textbox><w:txbxContent><w:p><w:r><w:t>Fallback 文本</w:t></w:r></w:p></w:txbxContent></v:textbox></v:shape></w:pict></mc:Fallback></mc:AlternateContent></w:r></w:p>` +
		`<w:p><w:r><w:rPr><w:vanish/></w:rPr><w:t>隐藏文本</w:t></w:r></w:p>`
	doc := buildRawDoc(t, inner)
	joined := strings.Join(unitTexts(doc.Units()), "|")
	for _, want := range []string{"宿主前", "宿主后", "Drawing 文本", "VML 文本", "Choice 文本", "Fallback 文本", "隐藏文本"} {
		if !strings.Contains(joined, want) {
			t.Errorf("缺少 %q：%s", want, joined)
		}
	}
	// 宿主段落的两个片段不得与文本框文字拼接。
	for _, u := range doc.Units() {
		if strings.Contains(u.Text, "宿主前") && u.Text != "宿主前" {
			t.Errorf("宿主片段不应拼接文本框：%q", u.Text)
		}
	}
	// 文本框段落独立存在，且宿主段落有两个片段。
	hostSegs := 0
	for _, u := range doc.Units() {
		if u.Part == "word/document.xml" && u.Paragraph == 1 {
			hostSegs++
		}
	}
	if hostSegs != 2 {
		t.Fatalf("宿主段落应在 drawing 处切成 2 个片段，got %d", hostSegs)
	}
}

// 3.4 硬边界与 1-based Paragraph/Segment 编号。
func TestUnitsHardBoundariesAndNumbering(t *testing.T) {
	inner := `<w:p></w:p>` + // 空段占用 Paragraph 1
		`<w:p><w:r><w:t>138</w:t></w:r><w:r><w:tab/></w:r><w:r><w:t>12345678</w:t></w:r></w:p>` + // Paragraph 2, segments 1/2
		`<w:p><w:r><w:t>甲</w:t></w:r><w:r><w:br/></w:r><w:r><w:t>乙</w:t></w:r></w:p>` // Paragraph 3
	doc := buildRawDoc(t, inner)
	units := doc.Units()
	// 空段不产生 unit；Paragraph 1 被消耗。
	for _, u := range units {
		if u.Paragraph == 1 {
			t.Fatalf("空段不应产生 unit：%+v", u)
		}
	}
	var seg2 []TextUnit
	for _, u := range units {
		if u.Paragraph == 2 {
			seg2 = append(seg2, u)
		}
	}
	if len(seg2) != 2 || seg2[0].Text != "138" || seg2[1].Text != "12345678" {
		t.Fatalf("tab 应为硬边界：%+v", seg2)
	}
	if seg2[0].Segment != 1 || seg2[1].Segment != 2 {
		t.Fatalf("Segment 应为 1-based 且不跨边界：%+v", seg2)
	}
	for _, u := range units {
		if u.Paragraph == 3 {
			if u.Text != "甲" && u.Text != "乙" {
				t.Fatalf("br 应为硬边界：%+v", u)
			}
		}
	}
}

// 3.5 altChunk/孤儿文字拒绝；字段指令不扫描、显示结果扫描；属性不扫描。
func TestUnitsRejectAndFieldHandling(t *testing.T) {
	// altChunk。
	doc := buildRawDocExpectError(t, `<w:p><w:altChunk r:id="rIdA"/></w:p>`)
	if doc == nil {
		t.Fatal("altChunk 应拒绝")
	}

	// 无段落归属的 w:t。
	buildRawDocExpectError(t, `<w:t>孤儿文字</w:t>`)

	// 字段指令不扫描，显示结果扫描。
	inner := `<w:p>` +
		`<w:r><w:fldChar w:fldCharType="begin"/></w:r>` +
		`<w:r><w:instrText>PHONE 13812345678</w:instrText></w:r>` +
		`<w:r><w:fldChar w:fldCharType="separate"/></w:r>` +
		`<w:r><w:t>显示结果 13812345678</w:t></w:r>` +
		`<w:r><w:fldChar w:fldCharType="end"/></w:r>` +
		`</w:p>`
	doc2 := buildRawDoc(t, inner)
	joined := strings.Join(unitTexts(doc2.Units()), "|")
	if strings.Contains(joined, "PHONE 13812345678") {
		t.Fatalf("字段指令不应被扫描：%s", joined)
	}
	if !strings.Contains(joined, "显示结果 13812345678") {
		t.Fatalf("字段显示结果应被扫描：%s", joined)
	}

	// 属性值不扫描。
	inner2 := `<w:p><w:bookmarkStart w:id="1" w:name="13812345678"/><w:r><w:t>正常文字</w:t></w:r></w:p>`
	doc3 := buildRawDoc(t, inner2)
	joined3 := strings.Join(unitTexts(doc3.Units()), "|")
	if strings.Contains(joined3, "13812345678") {
		t.Fatalf("属性值不应作为文字扫描：%s", joined3)
	}
}

func buildRawDocExpectError(t *testing.T, inner string) *Document {
	t.Helper()
	b := docxtest.NewBuilder(false)
	b.AddPart("word/document.xml", ctMain, rawDocFullNS(inner))
	b.SetMain("word/document.xml")
	doc, err := Open(b.Bytes(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Prepare(); err == nil {
		t.Fatalf("应拒绝：%s", inner)
	}
	return doc
}

func unitTexts(units []TextUnit) []string {
	out := make([]string, len(units))
	for i, u := range units {
		out[i] = u.Text
	}
	return out
}

func kindStrings(kinds []PartKind) []string {
	out := make([]string, len(kinds))
	for i, k := range kinds {
		out[i] = string(k)
	}
	return out
}

func reorderZip(t *testing.T, data []byte, first []string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*zip.File{}
	for _, f := range zr.File {
		byName[f.Name] = f
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fixed := time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
	write := func(f *zip.File) {
		if f == nil {
			return
		}
		rc, _ := f.Open()
		content, _ := io.ReadAll(rc)
		rc.Close()
		fh := &zip.FileHeader{Name: f.Name, Method: zip.Deflate, Modified: fixed}
		w, _ := zw.CreateHeader(fh)
		w.Write(content)
	}
	for _, n := range first {
		write(byName[n])
		delete(byName, n)
	}
	for _, f := range zr.File {
		if _, ok := byName[f.Name]; ok {
			write(f)
		}
	}
	zw.Close()
	return buf.Bytes()
}

// 3.1 嵌套表格与内容控件文字。
func TestUnitsNestedTableAndContentControl(t *testing.T) {
	inner := `<w:sdt><w:sdtContent><w:p><w:r><w:t>控件文字</w:t></w:r></w:p></w:sdtContent></w:sdt>` +
		`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>外层单元格</w:t></w:r></w:p>` +
		`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>嵌套单元格</w:t></w:r></w:p></w:tc></w:tr></w:tbl>` +
		`</w:tc></w:tr></w:tbl>`
	doc := buildRawDoc(t, inner)
	joined := strings.Join(unitTexts(doc.Units()), "|")
	for _, want := range []string{"控件文字", "外层单元格", "嵌套单元格"} {
		if !strings.Contains(joined, want) {
			t.Errorf("缺少 %q：%s", want, joined)
		}
	}
}

// 3.4 子文本框段落编号按 XML 文档顺序；宿主段落后续片段编号。
func TestUnitsNestedParagraphNumbering(t *testing.T) {
	inner := `<w:p><w:r><w:t>宿主前</w:t></w:r>` +
		`<w:r><w:drawing><wp:inline><a:graphic><a:graphicData><wps:wsp><wps:txbx><w:txbxContent>` +
		`<w:p><w:r><w:t>文本框段落</w:t></w:r></w:p>` +
		`</w:txbxContent></wps:txbx></wps:wsp></a:graphicData></a:graphic></wp:inline></w:drawing></w:r>` +
		`<w:r><w:t>宿主后</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t>第三段</w:t></w:r></w:p>`
	doc := buildRawDoc(t, inner)
	byText := map[string]TextUnit{}
	for _, u := range doc.Units() {
		byText[u.Text] = u
	}
	if u, ok := byText["宿主前"]; !ok || u.Paragraph != 1 || u.Segment != 1 {
		t.Fatalf("宿主前应为 P1S1：%+v", byText["宿主前"])
	}
	if u, ok := byText["文本框段落"]; !ok || u.Paragraph != 2 || u.Segment != 1 {
		t.Fatalf("文本框段落应为 P2S1：%+v", byText["文本框段落"])
	}
	if u, ok := byText["宿主后"]; !ok || u.Paragraph != 1 || u.Segment != 2 {
		t.Fatalf("宿主后应为 P1S2：%+v", byText["宿主后"])
	}
	if u, ok := byText["第三段"]; !ok || u.Paragraph != 3 {
		t.Fatalf("第三段应为 P3：%+v", byText["第三段"])
	}
}

// W1: run/段落级 mc:AlternateContent 的 Choice/Fallback 是独立存储分支，
// 不得跨分支或与前后宿主文本拼接；分支内部跨 runs 仍可匹配。
func TestUnitsMCCompatibilityBranches(t *testing.T) {
	// 合法结构：AlternateContent 直接作为 w:p 的子元素（Choice/Fallback 内含 w:r）。
	inner := `<w:p>` +
		`<w:r><w:t>宿主前</w:t></w:r>` +
		`<mc:AlternateContent>` +
		`<mc:Choice Requires="wps"><w:r><w:t>138</w:t></w:r><w:r><w:t>1234</w:t></w:r></mc:Choice>` +
		`<mc:Fallback><w:r><w:t>12345678</w:t></w:r></mc:Fallback>` +
		`</mc:AlternateContent>` +
		`<w:r><w:t>宿主后</w:t></w:r>` +
		`</w:p>`
	doc := buildRawDoc(t, inner)
	units := doc.Units()
	got := unitTexts(units)
	want := []string{"宿主前", "1381234", "12345678", "宿主后"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("MC 分支应各自独立且与宿主隔离：got %v want %v", got, want)
	}
	// 同一段落内、Segment 1-based 且稳定。
	for i, u := range units {
		if u.Paragraph != 1 || u.Segment != i+1 {
			t.Fatalf("段落/片段编号应稳定：%+v", u)
		}
	}
	// 分支内部跨 runs 拼接：Choice 的 138+1234 在同一 unit。
	if units[1].Text != "1381234" {
		t.Fatalf("分支内部跨 runs 应拼接：%q", units[1].Text)
	}
}

// W1: 跨分支碎片不得拼成完整号码，也不得跨宿主边界。
func TestUnitsMCCrossBranchNoConcat(t *testing.T) {
	inner := `<w:p>` +
		`<w:r><w:t>前缀</w:t></w:r>` +
		`<mc:AlternateContent>` +
		`<mc:Choice Requires="wps"><w:r><w:t>138</w:t></w:r></mc:Choice>` +
		`<mc:Fallback><w:r><w:t>12345678</w:t></w:r></mc:Fallback>` +
		`</mc:AlternateContent>` +
		`<w:r><w:t>后缀</w:t></w:r>` +
		`</w:p>`
	doc := buildRawDoc(t, inner)
	joined := strings.Join(unitTexts(doc.Units()), "|")
	if strings.Contains(joined, "13812345678") {
		t.Fatalf("不得跨分支拼成号码：%s", joined)
	}
}
