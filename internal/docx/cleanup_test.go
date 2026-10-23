package docx

import (
	"github.com/pandatoufu/data-masking/internal/docxtest"
	"strings"
	"testing"
)

// 4.1 修订分类与拒绝表；已删除历史子树内的不支持修订不造成过度拒绝。
func TestRevisionClassificationReject(t *testing.T) {
	rejectCases := []string{
		`<w:p><w:pPr><w:rPr><w:del w:id="1" w:author="a" w:date="2020-01-01T00:00:00Z"/></w:rPr></w:pPr><w:r><w:t>x</w:t></w:r></w:p>`,
		`<w:tbl><w:tr><w:trPr><w:del w:id="2" w:author="a" w:date="2020-01-01T00:00:00Z"/></w:trPr><w:tc><w:p><w:r><w:t>x</w:t></w:r></w:p></w:tc></w:tr></w:tbl>`,
		`<w:p><w:r><w:t>x</w:t></w:r></w:p><w:cellMerge w:id="3" w:author="a"/>`,
		`<w:p><w:r><w:t>x</w:t></w:r></w:p><w:numberingChange w:id="4" w:author="a"/>`,
		`<w:p><w:r><w:t>x</w:t></w:r></w:p><w:customXmlInsRangeStart w:id="5" w:author="a"/>`,
	}
	for i, inner := range rejectCases {
		b := docxtest.NewBuilder(false)
		b.AddPart("word/document.xml", ctMain, rawMainDoc(t, inner, ""))
		b.SetMain("word/document.xml")
		doc, err := Open(b.Bytes(), DefaultLimits())
		if err != nil {
			t.Fatalf("case %d Open: %v", i, err)
		}
		if _, err := doc.Prepare(); err == nil {
			t.Errorf("case %d 应拒绝：%s", i, inner)
		}
	}

	// 已删除历史子树内的不支持修订随子树清除，不单独触发拒绝。
	inner := `<w:p><w:del w:id="9" w:author="a" w:date="2020-01-01T00:00:00Z"><w:r><w:delText>旧</w:delText></w:r><w:numberingChange w:id="10" w:author="a"/></w:del><w:r><w:t>新</w:t></w:r></w:p>`
	b := docxtest.NewBuilder(false)
	b.AddPart("word/document.xml", ctMain, rawMainDoc(t, inner, ""))
	b.SetMain("word/document.xml")
	doc, err := Open(b.Bytes(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Prepare(); err != nil {
		t.Fatalf("已删除历史子树内的修订不应过度拒绝：%v", err)
	}
	if joined := strings.Join(unitTexts(doc.Units()), "|"); strings.Contains(joined, "旧") || !strings.Contains(joined, "新") {
		t.Fatalf("最终稿文字错误：%s", joined)
	}
}

// 4.2 接受行内插入/删除/移动、移除范围标记并转移局部命名空间绑定。
func TestInlineRevisionAcceptAndNamespaceHoist(t *testing.T) {
	inner := `<w:p>` +
		`<w:del w:id="1" w:author="a" w:date="2020-01-01T00:00:00Z"><w:r><w:delText>旧号码 13700000000</w:delText></w:r></w:del>` +
		`<w:ins w:id="2" w:author="a" w:date="2020-01-01T00:00:00Z" xmlns:foo="urn:foo"><w:r><w:t foo:mark="1">新号码 13812345678</w:t></w:r></w:ins>` +
		`<w:moveFrom w:id="3" w:author="a" w:date="2020-01-01T00:00:00Z"><w:r><w:t>移动源</w:t></w:r></w:moveFrom>` +
		`<w:moveTo w:id="4" w:author="a" w:date="2020-01-01T00:00:00Z"><w:r><w:t>移动目标</w:t></w:r></w:moveTo>` +
		`<w:moveFromRangeStart w:id="5" w:author="a" w:date="2020-01-01T00:00:00Z"/><w:moveFromRangeEnd w:id="5" w:author="a" w:date="2020-01-01T00:00:00Z"/>` +
		`</w:p>`
	doc := buildRawDoc(t, inner)
	joined := strings.Join(unitTexts(doc.Units()), "|")
	if strings.Contains(joined, "旧号码") || strings.Contains(joined, "移动源") {
		t.Fatalf("旧/移动源文字应消失：%s", joined)
	}
	for _, want := range []string{"新号码 13812345678", "移动目标"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("缺少最终稿文字 %q：%s", want, joined)
		}
	}
	// 局部命名空间绑定转移后输出仍有效。
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.ValidateOutput(out); err != nil {
		t.Fatalf("命名空间绑定转移失败：%v", err)
	}
	// 外壳已消失。
	if strings.Contains(string(out), "w:ins") {
		t.Fatal("修订外壳应被移除")
	}
}

// 4.3 历史属性、trackRevisions、delText 残留；非正文部件同样检查。
func TestRevisionPropertiesAndSettings(t *testing.T) {
	b := docxtest.NewBuilder(false)
	inner := `<w:p><w:pPr><w:pStyle w:val="Heading1"/><w:pPrChange w:id="1" w:author="a" w:date="2020-01-01T00:00:00Z"><w:pPr/></w:pPrChange></w:pPr><w:r><w:rPr><w:b/><w:rPrChange w:id="2" w:author="a" w:date="2020-01-01T00:00:00Z"><w:rPr/></w:rPrChange></w:rPr><w:t>正文</w:t></w:r></w:p>`
	b.AddPart("word/document.xml", ctMain, rawMainDoc(t, inner, ""))
	b.SetMain("word/document.xml")
	b.AddPart("word/settings.xml", ctSettings, b.WMLPart("settings", `<w:trackRevisions/>`))
	b.AddRel("word/document.xml", "rIdS", b.RelNS()+"/settings", "settings.xml", false)
	doc, err := Open(b.Bytes(), DefaultLimits())
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
	if err := doc.ValidateOutput(out); err != nil {
		t.Fatalf("属性/设置清理失败：%v", err)
	}
	docPart, _ := doc.PartBytes("word/document.xml")
	s := string(docPart)
	if strings.Contains(s, "pPrChange") || strings.Contains(s, "rPrChange") {
		t.Fatal("历史属性副本应被删除")
	}
	if !strings.Contains(s, `w:val="Heading1"`) || !strings.Contains(s, "<w:b/>") {
		t.Fatal("当前属性应保留")
	}
	settings, _ := doc.PartBytes("word/settings.xml")
	if strings.Contains(string(settings), "trackRevisions") {
		t.Fatal("trackRevisions 应被移除")
	}

	// 非正文部件中的不支持修订也要拒绝。
	b = docxtest.NewBuilder(false)
	b.AddPart("word/document.xml", ctMain, rawMainDoc(t, docxtest.Para("正文"), ""))
	b.SetMain("word/document.xml")
	b.AddPart("word/numbering.xml", ctNumbering, b.WMLPart("numbering", `<w:num w:numId="1"><w:numberingChange w:id="1" w:author="a"/></w:num>`))
	b.AddRel("word/document.xml", "rIdN", b.RelNS()+"/numbering", "numbering.xml", false)
	doc, err = Open(b.Bytes(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Prepare(); err == nil {
		t.Fatal("编号部件中的不支持修订应拒绝")
	}
}

// 4.4 五类批注家族识别与未知家族拒绝；不依赖固定文件名。
func TestCommentFamilyRecognition(t *testing.T) {
	data, err := docxtest.BuildFullFixture(docxtest.FixtureOptions{
		Body:     []string{"正文"},
		Comments: &docxtest.CommentsFixture{Text: "批注值 13600000000", Extended: true, IDs: true, Extensible: true, People: true},
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
	for _, name := range []string{"word/comments.xml", "word/commentsExtended.xml", "word/commentsIds.xml", "word/commentsExtensible.xml", "word/people.xml"} {
		if doc.HasPart(name) {
			t.Errorf("批注家族部件应被删除：%s", name)
		}
	}

	// 非固定文件名：comments 部件改名为 custom-notes.xml，仍按 content type/关系识别。
	b := docxtest.NewBuilder(false)
	b.AddPart("word/document.xml", ctMain, rawMainDoc(t, `<w:p><w:commentRangeStart w:id="0"/><w:r><w:t>x</w:t></w:r><w:commentRangeEnd w:id="0"/></w:p>`, ""))
	b.SetMain("word/document.xml")
	b.AddPart("word/custom-notes.xml", ctComments, b.CommentsPart("批注值"))
	b.AddRel("word/document.xml", "rIdC", b.RelNS()+"/comments", "custom-notes.xml", false)
	doc, err = Open(b.Bytes(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Prepare(); err != nil {
		t.Fatal(err)
	}
	if doc.HasPart("word/custom-notes.xml") {
		t.Fatal("非固定文件名的批注部件也应按 content type 识别删除")
	}

	// 未知批注家族关系拒绝。
	b = docxtest.NewBuilder(false)
	b.AddPart("word/document.xml", ctMain, rawMainDoc(t, docxtest.Para("x"), ""))
	b.SetMain("word/document.xml")
	b.AddPart("word/commentsFoo.xml", "application/vnd.example.commentsFoo+xml", []byte("<x/>"))
	b.AddRel("word/document.xml", "rIdU", "http://schemas.example.com/relationships/commentsFoo", "commentsFoo.xml", false)
	if _, err := Open(b.Bytes(), DefaultLimits()); err == nil {
		t.Fatal("未知批注家族应拒绝")
	}
}

// 4.5 删除锚点/家族部件/关系/声明，批注原值不进入文字单元，摘要含数量。
func TestCommentCleanupArtifacts(t *testing.T) {
	data, err := docxtest.BuildFullFixture(docxtest.FixtureOptions{
		Body:     []string{"正文"},
		Comments: &docxtest.CommentsFixture{Text: "批注值 13600000000 与 [PHONE_1]", Extended: true, People: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Open(data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	sum, err := doc.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	if sum.CommentsDeleted == 0 || sum.PartsRemoved == 0 {
		t.Fatalf("清理摘要应有数量：%+v", sum)
	}
	joined := strings.Join(unitTexts(doc.Units()), "|")
	if strings.Contains(joined, "批注值 13600000000") || strings.Contains(joined, "[PHONE_1]") {
		t.Fatalf("批注原值不应进入文字单元：%s", joined)
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.ValidateOutput(out); err != nil {
		t.Fatal(err)
	}
	ct, _ := doc.PartBytes("[Content_Types].xml")
	if strings.Contains(string(ct), "comments") {
		t.Fatalf("content type 声明应清理：%s", ct)
	}
	rels, _ := doc.PartBytes("word/_rels/document.xml.rels")
	if strings.Contains(string(rels), "comments") || strings.Contains(string(rels), "people") {
		t.Fatalf("批注关系应清理：%s", rels)
	}
}

// 4.6 可达图：历史专属图片删除、共享/最终稿图片保留、原本未引用的非批注资源不误删；重建偏移。
func TestResourceReachability(t *testing.T) {
	b := docxtest.NewBuilder(false)
	inner := `<w:p><w:r><w:t>正文</w:t></w:r><w:r><w:drawing><wp:inline><a:graphic><a:graphicData><pic:pic><pic:blipFill><a:blip r:embed="rIdKeep"/></pic:blipFill></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r>` +
		`<w:del w:id="1" w:author="a" w:date="2020-01-01T00:00:00Z"><w:r><w:drawing><wp:inline><a:graphic><a:graphicData><pic:pic><pic:blipFill><a:blip r:embed="rIdHist"/></pic:blipFill></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:del></w:p>`
	// 使用带 DrawingML 命名空间的 raw 文档。
	ns := map[string]string{
		"w": nsWMLTransitional, "r": nsRelTransitional,
		"wp":  "http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing",
		"a":   "http://schemas.openxmlformats.org/drawingml/2006/main",
		"pic": "http://schemas.openxmlformats.org/drawingml/2006/picture",
	}
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?><w:document`)
	for _, k := range sortedKeys(ns) {
		sb.WriteString(` xmlns:` + k + `="` + ns[k] + `"`)
	}
	sb.WriteString(`><w:body>` + inner + `</w:body></w:document>`)
	b.AddPart("word/document.xml", ctMain, []byte(sb.String()))
	b.SetMain("word/document.xml")
	keep := []byte{0x89, 'P', 'N', 'G', 1, 1, 1}
	hist := []byte{0x89, 'P', 'N', 'G', 2, 2, 2}
	orphan := []byte{0x89, 'P', 'N', 'G', 3, 3, 3}
	b.AddPart("word/media/keep.png", "image/png", keep)
	b.AddPart("word/media/hist.png", "image/png", hist)
	b.AddPart("word/media/orphan.png", "image/png", orphan)
	b.AddRel("word/document.xml", "rIdKeep", b.RelNS()+"/image", "media/keep.png", false)
	b.AddRel("word/document.xml", "rIdHist", b.RelNS()+"/image", "media/hist.png", false)
	doc, err := Open(b.Bytes(), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Prepare(); err != nil {
		t.Fatal(err)
	}
	if !doc.HasPart("word/media/keep.png") {
		t.Fatal("最终稿共享图片应保留")
	}
	if doc.HasPart("word/media/hist.png") {
		t.Fatal("历史专属图片应删除")
	}
	if !doc.HasPart("word/media/orphan.png") {
		t.Fatal("原本未引用的非批注资源不应误删")
	}
	// 清理后重建索引：Apply 仍可定位。
	u := doc.Units()[0]
	if err := doc.Apply(u, []Edit{{Start: 0, End: len(u.Text), Replacement: "清理后回写"}}); err != nil {
		t.Fatalf("清理后索引应可回写：%v", err)
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.ValidateOutput(out); err != nil {
		t.Fatalf("ValidateOutput: %v", err)
	}
}
