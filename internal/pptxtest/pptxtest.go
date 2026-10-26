// Package pptxtest 构造无私人信息、确定性的 PPTX 包，供 pptx/core/agent/server 测试使用。
// 它不使用 Office、LibreOffice 或网络，也不写入任何真实用户数据。
package pptxtest

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

// Options 控制生成的演示文稿特性。
type Options struct {
	Strict         bool
	Prefix         string // PresentationML 前缀，默认 p；DrawingML 固定使用 a
	MainPath       string // 主部件路径，默认 ppt/presentation.xml
	Slides         []Slide
	LegacyComments bool
	ModernComments bool
	CommentText    string
	ChartText      string
	SmartArtText   string
	EmbeddedText   string
	MediaBytes     []byte
	// ExtraParts 覆盖或追加任意部件（路径 → 解压字节）。
	ExtraParts map[string][]byte
	// StoreParts 覆盖或追加任意部件并以 Store 写入（供精确文件大小边界样本使用）。
	// 同时出现在 ExtraParts 与 StoreParts 时以 StoreParts 为准。
	StoreParts map[string][]byte
}

// Slide 描述一页的内容与结构特性。
type Slide struct {
	Title      string
	Body       []string
	Notes      []string
	Hidden     bool
	GroupText  string
	Table      [][]string
	TableMerge bool
	Choice     string
	Fallback   string
	Field      string
	SplitRuns  []string
	Picture    bool
	Chart      bool
	SmartArt   bool
	Embedded   bool
	Animation  bool
}

// Minimal 返回单页、无批注的最小有效包。
func Minimal(title string) []byte {
	return Build(Options{Slides: []Slide{{Title: title}}})
}

// Rich 返回覆盖全部支持区域、批注与未检查对象的组合包。
func Rich() []byte {
	return Build(Options{
		Slides: []Slide{
			{
				Title:      "季度报告 13800138000",
				Body:       []string{"联系人 13800138000", "邮箱 test@example.com"},
				Notes:      []string{"备注也含 13800138000"},
				GroupText:  "组合内 13800138000",
				Table:      [][]string{{"单元格 13800138000", "无关"}, {"合并", "内容"}},
				TableMerge: true,
				Choice:     "Choice 13800138000",
				Fallback:   "Fallback 13800138000",
				Field:      "5",
				SplitRuns:  []string{"跨 run 1380", "0138000"},
				Picture:    true,
				Chart:      true,
				SmartArt:   true,
				Embedded:   true,
				Animation:  true,
			},
			{
				Title:  "隐藏页 13900139000",
				Body:   []string{"第二页"},
				Notes:  []string{"第二页备注"},
				Hidden: true,
			},
		},
		LegacyComments: true,
		ModernComments: true,
		CommentText:    "批注旧值 13700137000 [PHONE_1]",
		ChartText:      "图表内 13600136000",
		SmartArtText:   "SmartArt 内 13500135000",
		EmbeddedText:   "嵌入对象内 13400134000",
		MediaBytes:     []byte("png-bytes-not-a-real-image"),
	})
}

// StrictRich 返回使用 Strict 命名空间的组合包。
func StrictRich() []byte {
	opts := richOptions()
	opts.Strict = true
	return Build(opts)
}

func richOptions() Options {
	return Options{
		Slides: []Slide{
			{Title: "Strict 13800138000", Body: []string{"正文 13800138000"}, Notes: []string{"备注 13800138000"}, GroupText: "组合 13800138000",
				Table: [][]string{{"表格 13800138000"}}, Choice: "Choice 13800138000", Fallback: "Fallback 13800138000", Field: "3",
				SplitRuns: []string{"拆分 1380", "0138000"}},
		},
		LegacyComments: true,
		ModernComments: true,
		CommentText:    "批注 13700137000",
		MediaBytes:     []byte("strict-media"),
	}
}

// 命名空间常量（与 internal/pptx 的冻结表保持一致）。
const (
	nsPMLTrans    = "http://schemas.openxmlformats.org/presentationml/2006/main"
	nsPMLStrict   = "http://purl.oclc.org/ooxml/presentationml/main"
	nsDrawTrans   = "http://schemas.openxmlformats.org/drawingml/2006/main"
	nsDrawStrict  = "http://purl.oclc.org/ooxml/drawingml/main"
	nsRelTrans    = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	nsRelStrict   = "http://purl.oclc.org/ooxml/officeDocument/relationships"
	nsContent     = "http://schemas.openxmlformats.org/package/2006/content-types"
	nsPackageRels = "http://schemas.openxmlformats.org/package/2006/relationships"
	nsMC          = "http://schemas.openxmlformats.org/markup-compatibility/2006"
	nsP188        = "http://schemas.microsoft.com/office/powerpoint/2018/8/main"
	nsChart       = "http://schemas.openxmlformats.org/drawingml/2006/chart"
	nsDgm         = "http://schemas.openxmlformats.org/drawingml/2006/diagram"
)

const (
	ctPresentation  = "application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"
	ctSlide         = "application/vnd.openxmlformats-officedocument.presentationml.slide+xml"
	ctLayout        = "application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"
	ctMaster        = "application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"
	ctNotes         = "application/vnd.openxmlformats-officedocument.presentationml.notesSlide+xml"
	ctNotesMaster   = "application/vnd.openxmlformats-officedocument.presentationml.notesMaster+xml"
	ctHandoutMaster = "application/vnd.openxmlformats-officedocument.presentationml.handoutMaster+xml"
	ctComments      = "application/vnd.openxmlformats-officedocument.presentationml.comments+xml"
	ctCommentAuthor = "application/vnd.openxmlformats-officedocument.presentationml.commentAuthors+xml"
	ctModernComment = "application/vnd.ms-powerpoint.comments+xml"
	ctModernAuthor  = "application/vnd.ms-powerpoint.authors+xml"
	ctTheme         = "application/vnd.openxmlformats-officedocument.theme+xml"
	ctChart         = "application/vnd.openxmlformats-officedocument.drawingml.chart+xml"
	ctDiagram       = "application/vnd.openxmlformats-officedocument.drawingml.diagramData+xml"
)

type generated struct {
	parts  map[string][]byte
	ctypes map[string]string
	rels   map[string][]rel
}

type rel struct {
	id, typ, target string
	external        bool
}

var fixedTime = time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)

// Build 生成一个确定性 ZIP 包。
func Build(opts Options) []byte {
	opts = normalize(opts)
	g := &generated{parts: map[string][]byte{}, ctypes: map[string]string{}, rels: map[string][]rel{}}

	pml, draw := nsPMLTrans, nsDrawTrans
	relNS := nsRelTrans
	if opts.Strict {
		pml, draw = nsPMLStrict, nsDrawStrict
		relNS = nsRelStrict
	}
	nsdecl := fmt.Sprintf(`xmlns:%s=%q xmlns:%s=%q xmlns:r=%q xmlns:mc=%q xmlns:p14=%q xmlns:p188=%q`,
		opts.Prefix, pml, "a", draw, relNS, nsMC, "http://schemas.microsoft.com/office/powerpoint/2010/main", nsP188)

	// 主演示文稿。
	main := opts.MainPath
	g.ctypes["/"+main] = ctPresentation
	var sldIDs strings.Builder
	for i := range opts.Slides {
		fmt.Fprintf(&sldIDs, `<%s:sldId id="%d" r:id="rId%d"/>`, opts.Prefix, 256+i, i+1)
	}
	g.parts[main] = []byte(xmlHeader() + fmt.Sprintf(`<%[1]s:presentation %[2]s><%[1]s:sldMasterIdLst><%[1]s:sldMasterId id="2147483648" r:id="rIdM"/></%[1]s:sldMasterIdLst><%[1]s:notesMasterIdLst><%[1]s:notesMasterId r:id="rIdNM"/></%[1]s:notesMasterIdLst><%[1]s:handoutMasterIdLst><%[1]s:handoutMasterId r:id="rIdHM"/></%[1]s:handoutMasterIdLst><%[1]s:sldIdLst>%[3]s</%[1]s:sldIdLst><%[1]s:sldSz cx="12192000" cy="6858000"/><%[1]s:notesSz cx="6858000" cy="9144000"/></%[1]s:presentation>`,
		opts.Prefix, nsdecl, sldIDs.String()))

	addRel := func(src, id, typ, target string) {
		g.rels[src] = append(g.rels[src], rel{id: id, typ: typ, target: target})
	}

	addRel(main, "rIdM", relNS+"/slideMaster", "/ppt/slideMasters/slideMaster1.xml")
	addRel(main, "rIdNM", relNS+"/notesMaster", "/ppt/notesMasters/notesMaster1.xml")
	addRel(main, "rIdHM", relNS+"/handoutMaster", "/ppt/handoutMasters/handoutMaster1.xml")
	for i := range opts.Slides {
		slidePath := fmt.Sprintf("ppt/slides/slide%d.xml", i+1)
		addRel(main, fmt.Sprintf("rId%d", i+1), relNS+"/slide", "/"+slidePath)
		g.ctypes["/"+slidePath] = ctSlide
		g.parts[slidePath] = []byte(slideXML(opts, i+1, opts.Slides[i], nsdecl))
		if len(opts.Slides[i].Notes) > 0 {
			notesPath := fmt.Sprintf("ppt/notesSlides/notesSlide%d.xml", i+1)
			g.ctypes["/"+notesPath] = ctNotes
			g.parts[notesPath] = []byte(notesXML(opts, i+1, opts.Slides[i].Notes, nsdecl))
			addRel(slidePath, "rIdNotes", relNS+"/notesSlide", "/"+notesPath)
			addRel(notesPath, "rIdNM", relNS+"/notesMaster", "/ppt/notesMasters/notesMaster1.xml")
		}
		if opts.Slides[i].Picture {
			addRel(slidePath, "rIdImg", relNS+"/image", "/ppt/media/image1.png")
			g.ctypes["/ppt/media/image1.png"] = "" // 走 Default png
		}
		if opts.Slides[i].Chart {
			addRel(slidePath, "rIdChart", relNS+"/chart", "/ppt/charts/chart1.xml")
			g.ctypes["/ppt/charts/chart1.xml"] = ctChart
			g.parts["ppt/charts/chart1.xml"] = []byte(chartXML(opts))
		}
		if opts.Slides[i].SmartArt {
			for _, r := range []struct{ id, suffix string }{{"rIdDm", "diagramData"}, {"rIdLo", "diagramLayout"}, {"rIdQs", "diagramQuickStyle"}, {"rIdCs", "diagramColors"}} {
				addRel(slidePath, r.id, relNS+"/"+r.suffix, "/ppt/diagrams/"+strings.TrimPrefix(r.id, "rId")+"1.xml")
			}
			g.ctypes["/ppt/diagrams/Dm1.xml"] = ctDiagram
			for _, n := range []string{"Lo", "Qs", "Cs"} {
				g.ctypes["/ppt/diagrams/"+n+"1.xml"] = ctDiagram
				g.parts["ppt/diagrams/"+n+"1.xml"] = []byte(xmlHeader() + `<dgm:dataModel xmlns:dgm="` + nsDgm + `"/>`)
			}
			g.parts["ppt/diagrams/Dm1.xml"] = []byte(diagramXML(opts))
		}
		if opts.Slides[i].Embedded {
			addRel(slidePath, "rIdOle", relNS+"/oleObject", "/ppt/embeddings/oleObject1.bin")
			g.ctypes["/ppt/embeddings/oleObject1.bin"] = ""
		}
	}

	// 版式 / 母版 / 备注母版 / 讲义母版 / 主题。
	g.ctypes["/ppt/slideLayouts/slideLayout1.xml"] = ctLayout
	g.parts["ppt/slideLayouts/slideLayout1.xml"] = []byte(spTreeDoc(opts, "layout", "版式 LAYOUT 13800138000", nsdecl))
	addRel("ppt/slideLayouts/slideLayout1.xml", "rIdM", relNS+"/slideMaster", "/ppt/slideMasters/slideMaster1.xml")

	g.ctypes["/ppt/slideMasters/slideMaster1.xml"] = ctMaster
	g.parts["ppt/slideMasters/slideMaster1.xml"] = []byte(spTreeDoc(opts, "master", "母版 MASTER 13800138000", nsdecl))
	addRel("ppt/slideMasters/slideMaster1.xml", "rIdL", relNS+"/slideLayout", "/ppt/slideLayouts/slideLayout1.xml")
	addRel("ppt/slideMasters/slideMaster1.xml", "rIdT", relNS+"/theme", "/ppt/theme/theme1.xml")

	g.ctypes["/ppt/notesMasters/notesMaster1.xml"] = ctNotesMaster
	g.parts["ppt/notesMasters/notesMaster1.xml"] = []byte(spTreeDoc(opts, "notesMaster", "备注母版 NOTESMASTER 13800138000", nsdecl))
	addRel("ppt/notesMasters/notesMaster1.xml", "rIdT", relNS+"/theme", "/ppt/theme/theme1.xml")

	g.ctypes["/ppt/handoutMasters/handoutMaster1.xml"] = ctHandoutMaster
	g.parts["ppt/handoutMasters/handoutMaster1.xml"] = []byte(spTreeDoc(opts, "handout", "讲义母版 HANDOUT 13800138000", nsdecl))

	g.ctypes["/ppt/theme/theme1.xml"] = ctTheme
	g.parts["ppt/theme/theme1.xml"] = []byte(xmlHeader() + `<a:theme xmlns:a="` + draw + `" name="Office"><a:themeElements><a:clrScheme name="Office"/><a:fontScheme name="Office"/><a:fmtScheme name="Office"/></a:themeElements></a:theme>`)

	if opts.LegacyComments {
		g.ctypes["/ppt/commentAuthors.xml"] = ctCommentAuthor
		g.parts["ppt/commentAuthors.xml"] = []byte(xmlHeader() + `<p:cmAuthorLst xmlns:p="` + pml + `"><p:cmAuthor id="0" name="Alice" initials="A" lastIdx="1" clrIdx="0"/></p:cmAuthorLst>`)
		addRel(main, "rIdCA", relNS+"/commentAuthors", "/ppt/commentAuthors.xml")
		for i := range opts.Slides {
			commentPath := fmt.Sprintf("ppt/comments/comment%d.xml", i+1)
			g.ctypes["/"+commentPath] = ctComments
			g.parts[commentPath] = []byte(legacyCommentXML(pml, opts.CommentText))
			addRel(fmt.Sprintf("ppt/slides/slide%d.xml", i+1), "rIdC", relNS+"/comments", "/"+commentPath)
		}
	}
	if opts.ModernComments {
		g.ctypes["/ppt/authors.xml"] = ctModernAuthor
		g.parts["ppt/authors.xml"] = []byte(xmlHeader() + `<p188:authorLst xmlns:p188="` + nsP188 + `"><p188:author id="{11111111-1111-1111-1111-111111111111}" name="Bob" initials="B" userId="bob@example.com" providerId="Windows Live"/></p188:authorLst>`)
		addRel(main, "rIdMA", "http://schemas.microsoft.com/office/2018/10/relationships/authors", "/ppt/authors.xml")
		for i := range opts.Slides {
			commentPath := fmt.Sprintf("ppt/comments/modernComment%d.xml", i+1)
			g.ctypes["/"+commentPath] = ctModernComment
			g.parts[commentPath] = []byte(modernCommentXML(draw, opts.CommentText))
			addRel(fmt.Sprintf("ppt/slides/slide%d.xml", i+1), "rIdMC", "http://schemas.microsoft.com/office/2018/10/relationships/comments", "/"+commentPath)
		}
	}

	if opts.MediaBytes != nil {
		g.parts["ppt/media/image1.png"] = opts.MediaBytes
		g.ctypes["/ppt/media/image1.png"] = ""
	}
	if hasEmbedded(opts) {
		g.parts["ppt/embeddings/oleObject1.bin"] = []byte(opts.EmbeddedText)
		g.ctypes["/ppt/embeddings/oleObject1.bin"] = ""
	}

	for name, data := range opts.ExtraParts {
		g.parts[name] = data
		if _, ok := g.ctypes["/"+name]; !ok {
			g.ctypes["/"+name] = ""
		}
	}

	g.parts["[Content_Types].xml"] = []byte(contentTypesXML(g.ctypes))
	g.parts["_rels/.rels"] = []byte(relsXML([]rel{{id: "rId1", typ: relNS + "/officeDocument", target: "/" + main}}))
	for src, rs := range g.rels {
		g.parts[relsPath(src)] = []byte(relsXML(rs))
	}

	store := map[string]bool{}
	for name, data := range opts.StoreParts {
		g.parts[name] = data
		store[name] = true
	}
	return zipParts(g.parts, store)
}

func hasEmbedded(opts Options) bool {
	for _, s := range opts.Slides {
		if s.Embedded {
			return true
		}
	}
	return false
}

func normalize(o Options) Options {
	if o.Prefix == "" {
		o.Prefix = "p"
	}
	if o.MainPath == "" {
		o.MainPath = "ppt/presentation.xml"
	}
	if o.CommentText == "" {
		o.CommentText = "批注旧值 13700137000 [PHONE_1]"
	}
	if o.ChartText == "" {
		o.ChartText = "图表内 13600136000"
	}
	if o.SmartArtText == "" {
		o.SmartArtText = "SmartArt 内 13500135000"
	}
	if o.EmbeddedText == "" {
		o.EmbeddedText = "嵌入对象内 13400134000"
	}
	if o.MediaBytes == nil {
		o.MediaBytes = []byte("media-bytes")
	}
	return o
}

func xmlHeader() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`
}

func esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func paragraph(text string) string {
	return `<a:p><a:r><a:rPr lang="en-US"/><a:t>` + esc(text) + `</a:t></a:r></a:p>`
}

func shapeXML(p, draw string, id int, name string, hidden bool, body string) string {
	h := ""
	if hidden {
		h = ` hidden="1"`
	}
	return fmt.Sprintf(`<%[1]s:sp><%[1]s:nvSpPr><%[1]s:cNvPr id="%[2]d" name="%[4]s"%[6]s/><%[1]s:cNvSpPr><%[3]s:spLocks noGrp="1"/></%[1]s:cNvSpPr><%[1]s:nvPr/></%[1]s:nvSpPr><%[1]s:spPr><%[3]s:xfrm><%[3]s:off x="0" y="0"/><%[3]s:ext cx="100" cy="100"/></%[3]s:xfrm><%[3]s:prstGeom prst="rect"><%[3]s:avLst/></%[3]s:prstGeom></%[1]s:spPr><%[1]s:txBody><%[3]s:bodyPr/><%[3]s:lstStyle/>%[5]s</%[1]s:txBody></%[1]s:sp>`,
		p, id, draw, esc(name), body, h)
}

func slideXML(opts Options, n int, s Slide, nsdecl string) string {
	p, draw := opts.Prefix, "a"
	var tree strings.Builder
	id := 2
	if s.Title != "" {
		tree.WriteString(shapeXML(p, draw, id, "Title", false, paragraph(s.Title)))
		id++
	}
	for _, line := range s.Body {
		tree.WriteString(shapeXML(p, draw, id, "Body", false, paragraph(line)))
		id++
	}
	if len(s.SplitRuns) > 0 {
		var runs strings.Builder
		for _, part := range s.SplitRuns {
			runs.WriteString(`<a:r><a:rPr lang="en-US"/><a:t>` + esc(part) + `</a:t></a:r>`)
		}
		tree.WriteString(shapeXML(p, draw, id, "Split", false, `<a:p>`+runs.String()+`</a:p>`))
		id++
	}
	if s.Field != "" {
		body := `<a:p><a:r><a:rPr lang="en-US"/><a:t>页码 </a:t></a:r><a:fld id="{22222222-2222-2222-2222-222222222222}" type="slidenum"><a:rPr lang="en-US"/><a:t>` + esc(s.Field) + `</a:t></a:fld><a:r><a:rPr lang="en-US"/><a:t> 结束</a:t></a:r></a:p>`
		tree.WriteString(shapeXML(p, draw, id, "Field", false, body))
		id++
	}
	if s.GroupText != "" {
		tree.WriteString(fmt.Sprintf(`<%[1]s:grpSp><%[1]s:nvGrpSpPr><%[1]s:cNvPr id="%[2]d" name="Group"/><%[1]s:cNvGrpSpPr/><%[1]s:nvPr/></%[1]s:nvGrpSpPr><%[1]s:grpSpPr/>%[3]s</%[1]s:grpSp>`,
			p, id, shapeXML(p, draw, id+1, "GroupChild", false, paragraph(s.GroupText))))
		id += 2
	}
	if len(s.Table) > 0 {
		tree.WriteString(tableXML(p, draw, id, s.Table, s.TableMerge))
		id++
	}
	if s.Choice != "" || s.Fallback != "" {
		tree.WriteString(fmt.Sprintf(`<mc:AlternateContent><mc:Choice Requires="p14">%s</mc:Choice><mc:Fallback>%s</mc:Fallback></mc:AlternateContent>`,
			shapeXML(p, draw, id, "Choice", false, paragraph(s.Choice)),
			shapeXML(p, draw, id+1, "Fallback", false, paragraph(s.Fallback))))
		id += 2
	}
	if s.Picture {
		tree.WriteString(fmt.Sprintf(`<%[1]s:pic><%[1]s:nvPicPr><%[1]s:cNvPr id="%[2]d" name="Picture"/><%[1]s:cNvPicPr/><%[1]s:nvPr/></%[1]s:nvPicPr><%[1]s:blipFill><%[3]s:blip r:embed="rIdImg"/></%[1]s:blipFill><%[1]s:spPr/></%[1]s:pic>`, p, id, draw))
		id++
	}
	if s.Chart {
		tree.WriteString(fmt.Sprintf(`<%[1]s:graphicFrame><%[1]s:nvGraphicFramePr><%[1]s:cNvPr id="%[2]d" name="Chart"/><%[1]s:cNvGraphicFramePr/><%[1]s:nvPr/></%[1]s:nvGraphicFramePr><%[1]s:xfrm><%[3]s:off x="0" y="0"/><%[3]s:ext cx="10" cy="10"/></%[1]s:xfrm><%[3]s:graphic><%[3]s:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/chart"><c:chart xmlns:c="%[4]s" r:id="rIdChart"/></%[3]s:graphicData></%[3]s:graphic></%[1]s:graphicFrame>`, p, id, draw, nsChart))
		id++
	}
	if s.SmartArt {
		tree.WriteString(fmt.Sprintf(`<%[1]s:graphicFrame><%[1]s:nvGraphicFramePr><%[1]s:cNvPr id="%[2]d" name="SmartArt"/><%[1]s:cNvGraphicFramePr/><%[1]s:nvPr/></%[1]s:nvGraphicFramePr><%[1]s:xfrm><%[3]s:off x="0" y="0"/><%[3]s:ext cx="10" cy="10"/></%[1]s:xfrm><%[3]s:graphic><%[3]s:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/diagram"><dgm:relIds xmlns:dgm="%[4]s" r:dm="rIdDm" r:lo="rIdLo" r:qs="rIdQs" r:cs="rIdCs"/></%[3]s:graphicData></%[3]s:graphic></%[1]s:graphicFrame>`, p, id, draw, nsDgm))
		id++
	}
	if s.Embedded {
		tree.WriteString(fmt.Sprintf(`<%[1]s:oleObj spid="_x0000_s1026" name="Object 1" r:id="rIdOle" imgW="10" imgH="10"><%[1]s:embed/></%[1]s:oleObj>`, p))
	}
	if s.Animation {
		tree.WriteString(fmt.Sprintf(`<%[1]s:timing><%[1]s:tnLst><%[1]s:par><%[1]s:cTn id="1" dur="indefinite" restart="never" nodeType="tmRoot"><%[1]s:childTnLst><%[1]s:seq><%[1]s:cTn id="2" dur="indefinite" nodeType="mainSeq"><%[1]s:childTnLst><%[1]s:par><%[1]s:cTn id="3" fill="hold"><%[1]s:stCondLst><%[1]s:cond delay="0"/></%[1]s:stCondLst><%[1]s:childTnLst><%[1]s:set><%[1]s:cBhvr><%[1]s:cTn id="4" dur="1" fill="hold"/><%[1]s:tgtEl><%[1]s:spTgt spid="2"/></%[1]s:tgtEl></%[1]s:cBhvr><%[1]s:to><%[3]s:strVal val="visible"/></%[1]s:to></%[1]s:set></%[1]s:childTnLst></%[1]s:cTn></%[1]s:par></%[1]s:childTnLst></%[1]s:cTn></%[1]s:seq></%[1]s:childTnLst></%[1]s:cTn></%[1]s:par></%[1]s:tnLst></%[1]s:timing>`, p, id, draw))
	}

	hidden := ""
	if s.Hidden {
		hidden = ` show="0"`
	}
	ext := ""
	if opts.ModernComments {
		ext = `<p:extLst><p:ext uri="{6950BFC3-B14F-4119-B1C7-8E2F5F9F6F7A}"><p188:commentRel r:id="rIdMC"/></p:ext></p:extLst>`
	}
	return xmlHeader() + fmt.Sprintf(`<%[1]s:sld %[2]s%[3]s><%[1]s:cSld><%[1]s:spTree><%[1]s:nvGrpSpPr><%[1]s:cNvPr id="1" name=""/><%[1]s:cNvGrpSpPr/><%[1]s:nvPr/></%[1]s:nvGrpSpPr><%[1]s:grpSpPr/>%[4]s</%[1]s:spTree></%[1]s:cSld>%[5]s</%[1]s:sld>`,
		p, nsdecl, hidden, tree.String(), ext)
}

func tableXML(p, draw string, id int, rows [][]string, merged bool) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`<%[1]s:graphicFrame><%[1]s:nvGraphicFramePr><%[1]s:cNvPr id="%[2]d" name="Table"/><%[1]s:cNvGraphicFramePr/><%[1]s:nvPr/></%[1]s:nvGraphicFramePr><%[1]s:xfrm><%[3]s:off x="0" y="0"/><%[3]s:ext cx="10" cy="10"/></%[1]s:xfrm><%[3]s:graphic><%[3]s:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/table"><%[3]s:tbl><%[3]s:tblPr firstRow="1"/><%[3]s:tblGrid>`, p, id, draw))
	for range rows[0] {
		b.WriteString(`<a:gridCol w="100"/>`)
	}
	b.WriteString(`</a:tblGrid>`)
	for ri, row := range rows {
		b.WriteString(`<a:tr h="100">`)
		for ci, cell := range row {
			merge := ""
			if merged && ri == 0 && ci == 0 {
				merge = ` gridSpan="2" rowSpan="2"`
			}
			if merged && (ri == 0 && ci == 1 || ri == 1 && ci == 0) {
				merge = ` hMerge="1"`
			}
			b.WriteString(`<a:tc` + merge + `><a:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:rPr lang="en-US"/><a:t>` + esc(cell) + `</a:t></a:r></a:p></a:txBody><a:tcPr/></a:tc>`)
		}
		b.WriteString(`</a:tr>`)
	}
	b.WriteString(`</a:tbl></a:graphicData></a:graphic></` + p + `:graphicFrame>`)
	return b.String()
}

func notesXML(opts Options, n int, notes []string, nsdecl string) string {
	p, draw := opts.Prefix, "a"
	var body strings.Builder
	for i, line := range notes {
		body.WriteString(shapeXML(p, draw, 2+i, "Notes", false, paragraph(line)))
	}
	body.WriteString(shapeXML(p, draw, 90, "SlideNumber", false, `<a:p><a:fld id="{33333333-3333-3333-3333-333333333333}" type="slidenum"><a:rPr lang="en-US"/><a:t>`+fmt.Sprint(n)+`</a:t></a:fld></a:p>`))
	return xmlHeader() + fmt.Sprintf(`<%[1]s:notes %[2]s><%[1]s:cSld><%[1]s:spTree><%[1]s:nvGrpSpPr><%[1]s:cNvPr id="1" name=""/><%[1]s:cNvGrpSpPr/><%[1]s:nvPr/></%[1]s:nvGrpSpPr><%[1]s:grpSpPr/>%[3]s</%[1]s:spTree></%[1]s:cSld></%[1]s:notes>`,
		p, nsdecl, body.String())
}

func spTreeDoc(opts Options, name, text, nsdecl string) string {
	p, draw := opts.Prefix, "a"
	return xmlHeader() + fmt.Sprintf(`<%[1]s:%[4]s %[2]s><%[1]s:cSld><%[1]s:spTree><%[1]s:nvGrpSpPr><%[1]s:cNvPr id="1" name=""/><%[1]s:cNvGrpSpPr/><%[1]s:nvPr/></%[1]s:nvGrpSpPr><%[1]s:grpSpPr/>%[3]s</%[1]s:spTree></%[1]s:cSld></%[1]s:%[4]s>`,
		p, nsdecl, shapeXML(p, draw, 2, name, false, paragraph(text)), name)
}

func legacyCommentXML(pml, text string) string {
	return xmlHeader() + `<p:cmLst xmlns:p="` + pml + `"><p:cm authorId="0" dt="2026-01-01T00:00:00Z" idx="1"><p:pos x="10" y="20"/><p:text>` + esc(text) + `</p:text></p:cm></p:cmLst>`
}

func modernCommentXML(draw, text string) string {
	return xmlHeader() + `<p188:cmLst xmlns:p188="` + nsP188 + `" xmlns:a="` + draw + `"><p188:cm id="{44444444-4444-4444-4444-444444444444}" authorId="{11111111-1111-1111-1111-111111111111}" created="2026-01-01T00:00:00Z"><p188:unknownAnchor/><p188:pos x="10" y="20"/><p188:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:rPr lang="en-US"/><a:t>` + esc(text) + `</a:t></a:r></a:p></p188:txBody></p188:cm></p188:cmLst>`
}

func chartXML(opts Options) string {
	return xmlHeader() + `<c:chartSpace xmlns:c="` + nsChart + `" xmlns:a="` + nsDrawTrans + `"><c:chart><c:title><c:tx><c:rich><a:bodyPr/><a:p><a:r><a:t>` + esc(opts.ChartText) + `</a:t></a:r></a:p></c:rich></c:tx></c:title></c:chart></c:chartSpace>`
}

func diagramXML(opts Options) string {
	return xmlHeader() + `<dgm:dataModel xmlns:dgm="` + nsDgm + `" xmlns:a="` + nsDrawTrans + `"><dgm:ptLst><dgm:pt modelId="1"><dgm:t><a:bodyPr/><a:p><a:r><a:t>` + esc(opts.SmartArtText) + `</a:t></a:r></a:p></dgm:t></dgm:pt></dgm:ptLst></dgm:dataModel>`
}

func relsPath(src string) string {
	if src == "" {
		return "_rels/.rels"
	}
	return path.Join(path.Dir(src), "_rels", path.Base(src)+".rels")
}

func relsXML(rs []rel) string {
	var b strings.Builder
	b.WriteString(xmlHeader())
	b.WriteString(`<Relationships xmlns="` + nsPackageRels + `">`)
	sorted := make([]rel, len(rs))
	copy(sorted, rs)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].id < sorted[j].id })
	for _, r := range sorted {
		fmt.Fprintf(&b, `<Relationship Id="%s" Type="%s" Target="%s"`, esc(r.id), esc(r.typ), esc(r.target))
		if r.external {
			b.WriteString(` TargetMode="External"`)
		}
		b.WriteString(`/>`)
	}
	b.WriteString(`</Relationships>`)
	return b.String()
}

func contentTypesXML(overrides map[string]string) string {
	var b strings.Builder
	b.WriteString(xmlHeader())
	b.WriteString(`<Types xmlns="` + nsContent + `">`)
	b.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	b.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)
	b.WriteString(`<Default Extension="png" ContentType="image/png"/>`)
	b.WriteString(`<Default Extension="bin" ContentType="application/vnd.openxmlformats-officedocument.oleObject"/>`)
	names := make([]string, 0, len(overrides))
	for n, ct := range overrides {
		if ct == "" {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&b, `<Override PartName="%s" ContentType="%s"/>`, esc(n), esc(overrides[n]))
	}
	b.WriteString(`</Types>`)
	return b.String()
}

func zipParts(parts map[string][]byte, store map[string]bool) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(parts))
	for n := range parts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		method := zip.Deflate
		if store[n] {
			method = zip.Store
		}
		fh := &zip.FileHeader{Name: n, Method: uint16(method), Modified: fixedTime}
		w, err := zw.CreateHeader(fh)
		if err != nil {
			panic(err)
		}
		if _, err := w.Write(parts[n]); err != nil {
			panic(err)
		}
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}
