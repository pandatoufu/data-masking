// Package docxtest 提供内存 DOCX fixture 构造器，仅供测试与验收样本使用。
// 本包不导入 internal/docx，避免与 docx 的包内测试形成导入环；
// 生产代码不依赖本包。
package docxtest

import (
	"archive/zip"
	"bytes"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

// OPC / WordprocessingML 常量（与 internal/docx 保持一致）。
const (
	nsWMLTransitional = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	nsWMLStrict       = "http://purl.oclc.org/ooxml/wordprocessingml/main"
	nsRelTransitional = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	nsRelStrict       = "http://purl.oclc.org/ooxml/officeDocument/relationships"
	nsContentTypes    = "http://schemas.openxmlformats.org/package/2006/content-types"
	nsRelationships   = "http://schemas.openxmlformats.org/package/2006/relationships"

	ctMain               = "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"
	ctHeader             = "application/vnd.openxmlformats-officedocument.wordprocessingml.header+xml"
	ctFooter             = "application/vnd.openxmlformats-officedocument.wordprocessingml.footer+xml"
	ctFootnotes          = "application/vnd.openxmlformats-officedocument.wordprocessingml.footnotes+xml"
	ctEndnotes           = "application/vnd.openxmlformats-officedocument.wordprocessingml.endnotes+xml"
	ctComments           = "application/vnd.openxmlformats-officedocument.wordprocessingml.comments+xml"
	ctCommentsExtended   = "application/vnd.openxmlformats-officedocument.wordprocessingml.commentsExtended+xml"
	ctCommentsIds        = "application/vnd.openxmlformats-officedocument.wordprocessingml.commentsIds+xml"
	ctCommentsExtensible = "application/vnd.openxmlformats-officedocument.wordprocessingml.commentsExtensible+xml"
	ctPeople             = "application/vnd.openxmlformats-officedocument.wordprocessingml.people+xml"
	ctSettings           = "application/vnd.openxmlformats-officedocument.wordprocessingml.settings+xml"
	ctNumbering          = "application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml"
	ctStyles             = "application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"
	ctRelationships      = "application/vnd.openxmlformats-package.relationships+xml"
)

// relsPartName 返回某部件的 .rels 部件名（与 docx 实现一致）。
func relsPartName(source string) string {
	if source == "" {
		return "_rels/.rels"
	}
	dir := path.Dir(source)
	base := path.Base(source)
	return path.Join(dir, "_rels", base+".rels")
}

// Builder 以内存方式合成合法 DOCX 包（仅用于测试与验收样本）。
type Builder struct {
	strict   bool
	parts    map[string]fixturePart
	order    []string
	rels     map[string][]fixtureRel
	mainPart string
	mainSet  bool
}

type fixturePart struct {
	ct    string
	data  []byte
	store bool // true 时以 Store 写入（文件大小与内容长度近似 1:1，供大文件边界样本使用）
}

type fixtureRel struct {
	id       string
	relType  string
	target   string
	external bool
}

// NewBuilder 构造 fixture 构造器；strict 为 true 时使用 Strict 命名空间。
func NewBuilder(strict bool) *Builder {
	return &Builder{
		strict: strict,
		parts:  map[string]fixturePart{},
		rels:   map[string][]fixtureRel{},
	}
}

// Strict 报告构造器是否使用 Strict 命名空间。
func (b *Builder) Strict() bool { return b.strict }

// AddPart 添加部件并指定 content type。
func (b *Builder) AddPart(name, ct string, data []byte) {
	if _, ok := b.parts[name]; !ok {
		b.order = append(b.order, name)
	}
	b.parts[name] = fixturePart{ct: ct, data: data}
}

// AddStoredPart 以 ZIP Store 方法添加部件，使文件大小可精确控制（仅供大文件测试 fixture）。
func (b *Builder) AddStoredPart(name, ct string, data []byte) {
	if _, ok := b.parts[name]; !ok {
		b.order = append(b.order, name)
	}
	b.parts[name] = fixturePart{ct: ct, data: data, store: true}
}

// SetMain 设置主文档部件并登记包根关系。
func (b *Builder) SetMain(name string) {
	b.mainPart = name
	b.mainSet = true
	b.AddRel("", "rId1", b.officeDocumentRel(), name, false)
}

// MainPart 返回已设置的主文档部件名。
func (b *Builder) MainPart() string { return b.mainPart }

// AddRel 添加一条关系。
func (b *Builder) AddRel(source, id, relType, target string, external bool) {
	b.rels[source] = append(b.rels[source], fixtureRel{id: id, relType: relType, target: target, external: external})
}

func (b *Builder) wmlNS() string {
	if b.strict {
		return nsWMLStrict
	}
	return nsWMLTransitional
}

func (b *Builder) RelNS() string {
	if b.strict {
		return nsRelStrict
	}
	return nsRelTransitional
}

func (b *Builder) officeDocumentRel() string { return b.RelNS() + "/officeDocument" }

// Bytes 生成 ZIP 字节（确定性时间戳）。
func (b *Builder) Bytes() []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fixed := time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

	names := append([]string(nil), b.order...)
	names = append(names, "[Content_Types].xml")
	for src := range b.rels {
		names = append(names, relsPartName(src))
	}
	sort.Strings(names)
	seen := map[string]bool{}

	writeMethod := func(name string, data []byte, store bool) {
		if seen[name] {
			return
		}
		seen[name] = true
		method := zip.Deflate
		if store {
			method = zip.Store
		}
		fh := &zip.FileHeader{Name: name, Method: uint16(method), Modified: fixed}
		w, _ := zw.CreateHeader(fh)
		_, _ = w.Write(data)
	}
	write := func(name string, data []byte) { writeMethod(name, data, false) }

	write("[Content_Types].xml", b.contentTypesXML())
	for src, rels := range b.rels {
		write(relsPartName(src), b.relsXML(src, rels))
	}
	for _, name := range names {
		if name == "[Content_Types].xml" {
			continue
		}
		if strings.HasSuffix(name, ".rels") {
			continue
		}
		p, ok := b.parts[name]
		if !ok {
			continue
		}
		writeMethod(name, p.data, p.store)
	}
	_ = zw.Close()
	return buf.Bytes()
}

func (b *Builder) contentTypesXML() []byte {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	sb.WriteString(`<Types xmlns="` + nsContentTypes + `">`)
	sb.WriteString(`<Default Extension="rels" ContentType="` + ctRelationships + `"/>`)
	sb.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)
	var overrides []string
	for _, name := range b.order {
		p := b.parts[name]
		overrides = append(overrides, `<Override PartName="/`+xmlEscapeAttr(name)+`" ContentType="`+xmlEscapeAttr(p.ct)+`"/>`)
	}
	sort.Strings(overrides)
	for _, o := range overrides {
		sb.WriteString(o)
	}
	sb.WriteString(`</Types>`)
	return []byte(sb.String())
}

func (b *Builder) relsXML(src string, rels []fixtureRel) []byte {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	sb.WriteString(`<Relationships xmlns="` + nsRelationships + `">`)
	sorted := append([]fixtureRel(nil), rels...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].id < sorted[j].id })
	for _, r := range sorted {
		sb.WriteString(`<Relationship Id="` + xmlEscapeAttr(r.id) + `" Type="` + xmlEscapeAttr(r.relType) + `" Target="` + xmlEscapeAttr(r.target) + `"`)
		if r.external {
			sb.WriteString(` TargetMode="External"`)
		}
		sb.WriteString(`/>`)
	}
	sb.WriteString(`</Relationships>`)
	return []byte(sb.String())
}

// --- XML 文本片段辅助 ---

func xmlEsc(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// WMLDocument 包装主文档 body。
func (b *Builder) WMLDocument(inner string, extraNS map[string]string) []byte {
	ns := map[string]string{
		"w": b.wmlNS(),
		"r": b.RelNS(),
	}
	for k, v := range extraNS {
		ns[k] = v
	}
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	sb.WriteString(`<w:document`)
	for _, k := range sortedKeys(ns) {
		sb.WriteString(` xmlns:` + k + `="` + xmlEscapeAttr(ns[k]) + `"`)
	}
	sb.WriteString(`><w:body>` + inner + `</w:body></w:document>`)
	return []byte(sb.String())
}

// WMLPart 用给定根元素名与命名空间包装一个部件（页眉/页脚/脚注等）。
func (b *Builder) WMLPart(root string, inner string) []byte {
	ns := map[string]string{
		"w": b.wmlNS(),
		"r": b.RelNS(),
	}
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	sb.WriteString(`<w:` + root)
	for _, k := range sortedKeys(ns) {
		sb.WriteString(` xmlns:` + k + `="` + xmlEscapeAttr(ns[k]) + `"`)
	}
	sb.WriteString(`>` + inner + `</w:` + root + `>`)
	return []byte(sb.String())
}

// Para 生成一个简单段落。
func Para(text string) string {
	return `<w:p><w:r><w:t xml:space="preserve">` + xmlEsc(text) + `</w:t></w:r></w:p>`
}

// ParaRuns 生成一个由多个 run 组成的段落（用于跨 run 命中）。
func ParaRuns(runs ...string) string {
	var sb strings.Builder
	sb.WriteString(`<w:p>`)
	for _, r := range runs {
		sb.WriteString(`<w:r><w:t xml:space="preserve">` + xmlEsc(r) + `</w:t></w:r>`)
	}
	sb.WriteString(`</w:p>`)
	return sb.String()
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- 完整组合 fixture ---

// CommentsFixture 控制批注家族部件。
type CommentsFixture struct {
	Text               string
	Extended, IDs      bool
	Extensible, People bool
}

// FixtureOptions 描述一个合成 DOCX 的内容组合。
type FixtureOptions struct {
	Strict              bool
	Body                []string
	Table               [][]string
	Headers             []string
	Footers             []string
	Footnotes           []string
	Endnotes            []string
	DrawingTextbox      string
	VMLTextbox          string
	ChoiceText          string
	FallbackText        string
	Hidden              string
	Comments            *CommentsFixture
	InlineInsert        string
	InlineDelete        string
	ParagraphMarkDelete bool
	RowDelete           bool
	Image               []byte
	BodyRuns            []string
	// MixedFontNumber 生成一个号码跨三个不同 rPr runs 的段落（可检查首 run 样式）。
	MixedFontNumber string
	// CompatText 生成一个 mc:AlternateContent，Choice/Fallback 均存同一文字（Word 视觉兼容分支）。
	CompatText string
	// Bookmark 生成一个带书签锚点的段落。
	Bookmark string
	// TableMerge 额外生成 Word 合法的横向 gridSpan 与纵向 vMerge 表格。
	TableMerge bool
	// StoredExtra 以 Store 追加任意未引用部件（路径 → 字节），供精确文件大小边界样本使用。
	StoredExtra map[string][]byte
	// ExtraParts 以 Deflate 追加任意未引用部件（路径 → 解压字节），供解压总量样本使用。
	ExtraParts map[string][]byte
}

// BuildFullFixture 合成一个覆盖全部声明区域的 DOCX。
func BuildFullFixture(opts FixtureOptions) ([]byte, error) {
	b := NewBuilder(opts.Strict)
	extraNS := map[string]string{
		"mc":  "http://schemas.openxmlformats.org/markup-compatibility/2006",
		"wp":  "http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing",
		"a":   "http://schemas.openxmlformats.org/drawingml/2006/main",
		"pic": "http://schemas.openxmlformats.org/drawingml/2006/picture",
		"wps": "http://schemas.microsoft.com/office/word/2010/wordprocessingShape",
		"v":   "urn:schemas-microsoft-com:vml",
	}

	var body strings.Builder
	for _, p := range opts.Body {
		body.WriteString(Para(p))
	}
	if len(opts.BodyRuns) > 0 {
		body.WriteString(ParaRuns(opts.BodyRuns...))
	}
	if opts.MixedFontNumber != "" {
		n := opts.MixedFontNumber
		parts := []string{n}
		if len(n) >= 4 {
			parts = []string{n[:3], n[3 : len(n)-2], n[len(n)-2:]}
		}
		body.WriteString(`<w:p>`)
		rprs := []string{"<w:b/>", "<w:i/>", `<w:color w:val="FF0000"/>`}
		for i, p := range parts {
			rpr := ""
			if i < len(rprs) {
				rpr = rprs[i]
			}
			body.WriteString(`<w:r><w:rPr>` + rpr + `</w:rPr><w:t xml:space="preserve">` + xmlEsc(p) + `</w:t></w:r>`)
		}
		body.WriteString(`</w:p>`)
	}
	if opts.CompatText != "" {
		body.WriteString(`<w:p><w:r><w:t xml:space="preserve">兼容分支：</w:t></w:r>` +
			`<mc:AlternateContent>` +
			`<mc:Choice Requires="wps"><w:r><w:t xml:space="preserve">` + xmlEsc(opts.CompatText) + `</w:t></w:r></mc:Choice>` +
			`<mc:Fallback><w:r><w:t xml:space="preserve">` + xmlEsc(opts.CompatText) + `</w:t></w:r></mc:Fallback>` +
			`</mc:AlternateContent></w:p>`)
	}
	if opts.Bookmark != "" {
		body.WriteString(`<w:p><w:bookmarkStart w:id="90" w:name="BM"/><w:r><w:t xml:space="preserve">` + xmlEsc(opts.Bookmark) + `</w:t></w:r><w:bookmarkEnd w:id="90"/></w:p>`)
	}
	if opts.InlineInsert != "" || opts.InlineDelete != "" {
		body.WriteString(`<w:p>`)
		if opts.InlineDelete != "" {
			body.WriteString(`<w:del w:id="10" w:author="a" w:date="2020-01-01T00:00:00Z"><w:r><w:delText>` + xmlEsc(opts.InlineDelete) + `</w:delText></w:r></w:del>`)
		}
		if opts.InlineInsert != "" {
			body.WriteString(`<w:ins w:id="11" w:author="a" w:date="2020-01-01T00:00:00Z"><w:r><w:t xml:space="preserve">` + xmlEsc(opts.InlineInsert) + `</w:t></w:r></w:ins>`)
		}
		body.WriteString(`</w:p>`)
	}
	if opts.ParagraphMarkDelete {
		body.WriteString(`<w:p><w:pPr><w:rPr><w:del w:id="20" w:author="a" w:date="2020-01-01T00:00:00Z"/></w:rPr></w:pPr><w:r><w:t>` + xmlEsc("段落标记删除") + `</w:t></w:r></w:p>`)
	}
	if len(opts.Table) > 0 {
		body.WriteString(`<w:tbl><w:tblPr/><w:tblGrid>`)
		cols := 0
		for _, row := range opts.Table {
			if len(row) > cols {
				cols = len(row)
			}
		}
		for i := 0; i < cols; i++ {
			body.WriteString(`<w:gridCol w:w="2000"/>`)
		}
		body.WriteString(`</w:tblGrid>`)
		for _, row := range opts.Table {
			body.WriteString(`<w:tr>`)
			if opts.RowDelete {
				body.WriteString(`<w:trPr><w:del w:id="30" w:author="a" w:date="2020-01-01T00:00:00Z"/></w:trPr>`)
			}
			for _, cell := range row {
				body.WriteString(`<w:tc><w:tcPr><w:tcW w:w="2000" w:type="dxa"/></w:tcPr>`)
				body.WriteString(Para(cell))
				body.WriteString(`</w:tc>`)
			}
			body.WriteString(`</w:tr>`)
		}
		body.WriteString(`</w:tbl>`)
	}
	if opts.TableMerge {
		// Word 合法：横向 gridSpan 与纵向 vMerge。
		body.WriteString(`<w:tbl><w:tblPr><w:tblStyle w:val="TableGrid"/></w:tblPr>` +
			`<w:tblGrid><w:gridCol w:w="2400"/><w:gridCol w:w="2400"/></w:tblGrid>` +
			`<w:tr><w:tc><w:tcPr><w:gridSpan w:val="2"/><w:tcW w:w="4800" w:type="dxa"/></w:tcPr><w:p><w:r><w:t>横向合并单元格</w:t></w:r></w:p></w:tc></w:tr>` +
			`<w:tr><w:tc><w:tcPr><w:vMerge w:val="restart"/><w:tcW w:w="2400" w:type="dxa"/></w:tcPr><w:p><w:r><w:t>纵向合并上</w:t></w:r></w:p></w:tc>` +
			`<w:tc><w:tcPr><w:tcW w:w="2400" w:type="dxa"/></w:tcPr><w:p><w:r><w:t>右上</w:t></w:r></w:p></w:tc></w:tr>` +
			`<w:tr><w:tc><w:tcPr><w:vMerge/><w:tcW w:w="2400" w:type="dxa"/></w:tcPr><w:p><w:r><w:t>纵向合并下</w:t></w:r></w:p></w:tc>` +
			`<w:tc><w:tcPr><w:tcW w:w="2400" w:type="dxa"/></w:tcPr><w:p><w:r><w:t>右下</w:t></w:r></w:p></w:tc></w:tr>` +
			`</w:tbl>`)
	}
	if opts.DrawingTextbox != "" {
		body.WriteString(`<w:p><w:r><w:drawing><wp:inline><a:graphic><a:graphicData><wps:wsp><wps:txbx><w:txbxContent>` +
			Para(opts.DrawingTextbox) + `</w:txbxContent></wps:txbx></wps:wsp></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>`)
	}
	if opts.VMLTextbox != "" {
		body.WriteString(`<w:p><w:r><w:pict><v:shape><v:textbox><w:txbxContent>` +
			Para(opts.VMLTextbox) + `</w:txbxContent></v:textbox></v:shape></w:pict></w:r></w:p>`)
	}
	if opts.ChoiceText != "" || opts.FallbackText != "" {
		body.WriteString(`<w:p><w:r><mc:AlternateContent>`)
		body.WriteString(`<mc:Choice Requires="wps"><w:drawing><wp:inline><a:graphic><a:graphicData><wps:wsp><wps:txbx><w:txbxContent>` +
			Para(opts.ChoiceText) + `</w:txbxContent></wps:txbx></wps:wsp></a:graphicData></a:graphic></wp:inline></w:drawing></mc:Choice>`)
		body.WriteString(`<mc:Fallback><w:pict><v:shape><v:textbox><w:txbxContent>` +
			Para(opts.FallbackText) + `</w:txbxContent></v:textbox></v:shape></w:pict></mc:Fallback>`)
		body.WriteString(`</mc:AlternateContent></w:r></w:p>`)
	}
	if opts.Hidden != "" {
		body.WriteString(`<w:p><w:r><w:rPr><w:vanish/></w:rPr><w:t xml:space="preserve">` + xmlEsc(opts.Hidden) + `</w:t></w:r></w:p>`)
	}
	if opts.Comments != nil {
		body.WriteString(`<w:p><w:commentRangeStart w:id="0"/><w:r><w:t xml:space="preserve">被批注正文</w:t></w:r><w:commentRangeEnd w:id="0"/><w:r><w:commentReference w:id="0"/></w:r></w:p>`)
	}
	if len(opts.Image) > 0 {
		body.WriteString(`<w:p><w:r><w:drawing><wp:inline><a:graphic><a:graphicData><pic:pic><pic:blipFill><a:blip r:embed="rIdImg"/></pic:blipFill></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>`)
	}

	b.AddPart("word/document.xml", ctMain, b.WMLDocument(body.String(), extraNS))
	b.SetMain("word/document.xml")

	if len(opts.Image) > 0 {
		b.AddPart("word/media/image1.png", "image/png", opts.Image)
		b.AddRel("word/document.xml", "rIdImg", b.RelNS()+"/image", "media/image1.png", false)
	}
	for i, h := range opts.Headers {
		name := fmt.Sprintf("word/header%d.xml", i+1)
		b.AddPart(name, ctHeader, b.WMLPart("hdr", Para(h)))
		b.AddRel("word/document.xml", fmt.Sprintf("rIdH%d", i+1), b.RelNS()+"/header", fmt.Sprintf("header%d.xml", i+1), false)
	}
	for i, f := range opts.Footers {
		name := fmt.Sprintf("word/footer%d.xml", i+1)
		b.AddPart(name, ctFooter, b.WMLPart("ftr", Para(f)))
		b.AddRel("word/document.xml", fmt.Sprintf("rIdF%d", i+1), b.RelNS()+"/footer", fmt.Sprintf("footer%d.xml", i+1), false)
	}
	if len(opts.Footnotes) > 0 {
		var inner strings.Builder
		for i, t := range opts.Footnotes {
			inner.WriteString(fmt.Sprintf(`<w:footnote w:id="%d">`, i+1) + Para(t) + `</w:footnote>`)
		}
		b.AddPart("word/footnotes.xml", ctFootnotes, b.WMLPart("footnotes", inner.String()))
		b.AddRel("word/document.xml", "rIdFn", b.RelNS()+"/footnotes", "footnotes.xml", false)
	}
	if len(opts.Endnotes) > 0 {
		var inner strings.Builder
		for i, t := range opts.Endnotes {
			inner.WriteString(fmt.Sprintf(`<w:endnote w:id="%d">`, i+1) + Para(t) + `</w:endnote>`)
		}
		b.AddPart("word/endnotes.xml", ctEndnotes, b.WMLPart("endnotes", inner.String()))
		b.AddRel("word/document.xml", "rIdEn", b.RelNS()+"/endnotes", "endnotes.xml", false)
	}
	for name, data := range opts.ExtraParts {
		b.AddPart(name, "application/octet-stream", data)
	}
	for name, data := range opts.StoredExtra {
		b.AddStoredPart(name, "application/octet-stream", data)
	}
	if c := opts.Comments; c != nil {
		b.AddPart("word/comments.xml", ctComments, b.CommentsPart(c.Text))
		b.AddRel("word/document.xml", "rIdC", b.RelNS()+"/comments", "comments.xml", false)
		if c.Extended {
			b.AddPart("word/commentsExtended.xml", ctCommentsExtended, b.commentsExtendedPart())
			b.AddRel("word/document.xml", "rIdCE", "http://schemas.microsoft.com/office/2011/relationships/commentsExtended", "commentsExtended.xml", false)
		}
		if c.IDs {
			b.AddPart("word/commentsIds.xml", ctCommentsIds, b.commentsIdsPart())
			b.AddRel("word/document.xml", "rIdCI", "http://schemas.microsoft.com/office/2016/09/relationships/commentsIds", "commentsIds.xml", false)
		}
		if c.Extensible {
			b.AddPart("word/commentsExtensible.xml", ctCommentsExtensible, b.commentsExtensiblePart())
			b.AddRel("word/document.xml", "rIdCX", "http://schemas.microsoft.com/office/2018/08/relationships/commentsExtensible", "commentsExtensible.xml", false)
		}
		if c.People {
			b.AddPart("word/people.xml", ctPeople, b.peoplePart())
			b.AddRel("word/document.xml", "rIdP", "http://schemas.microsoft.com/office/2011/relationships/people", "people.xml", false)
		}
	}
	return b.Bytes(), nil
}

func (b *Builder) CommentsPart(text string) []byte {
	xml := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:comments xmlns:w="` + b.wmlNS() + `">` +
		`<w:comment w:id="0" w:author="审查者" w:initials="R" w:date="2020-01-01T00:00:00Z"><w:p><w:r><w:t>` + xmlEsc(text) + `</w:t></w:r></w:p></w:comment>` +
		`</w:comments>`
	return []byte(xml)
}

func (b *Builder) commentsExtendedPart() []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w15:commentsEx xmlns:w15="http://schemas.microsoft.com/office/word/2012/wordml"><w15:commentEx w15:paraId="11111111" w15:done="0"/></w15:commentsEx>`)
}

func (b *Builder) commentsIdsPart() []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w16cid:commentsIds xmlns:w16cid="http://schemas.microsoft.com/office/word/2016/wordml/cid"><w16cid:commentId w16cid:paraId="11111111" w16cid:durableId="22222222"/></w16cid:commentsIds>`)
}

func (b *Builder) commentsExtensiblePart() []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w16cex:commentsExtensible xmlns:w16cex="http://schemas.microsoft.com/office/word/2018/wordml/cex"><w16cex:commentExtensible w16cex:durableId="22222222" w16cex:dateUtc="2020-01-01T00:00:00Z"/></w16cex:commentsExtensible>`)
}

func (b *Builder) peoplePart() []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w15:people xmlns:w15="http://schemas.microsoft.com/office/word/2012/wordml"><w15:person w15:author="审查者"/></w15:people>`)
}

func xmlEscapeAttr(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
