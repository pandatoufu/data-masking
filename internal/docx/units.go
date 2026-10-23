package docx

import (
	"encoding/xml"
	"strings"
)

// PartKind 标识文字单元所在区域。
type PartKind string

const (
	KindDocument PartKind = "document"
	KindHeader   PartKind = "header"
	KindFooter   PartKind = "footer"
	KindFootnote PartKind = "footnote"
	KindEndnote  PartKind = "endnote"
)

// unitPiece 是一个文字节点在某个文字单元中的片段，带来源 XML 字节范围。
type unitPiece struct {
	text        string
	offset      int // 在所在 unit.Text 中的字节偏移
	srcStart    int // 文字内容原始字节范围（不含标签）
	srcEnd      int
	tagStart    int // 起始标签原始范围
	tagEnd      int
	hasPreserve bool // 是否已含 xml:space="preserve"
}

// TextUnit 是一个稳定的逻辑文字单元。
type TextUnit struct {
	Index     int
	Part      string
	PartKind  PartKind
	Paragraph int // 1-based，部件内 XML 文档顺序（含空段与子文本框段落）
	Segment   int // 1-based，段内硬边界切分后的非空片段
	Text      string
	pieces    []unitPiece
}

type tInfo struct {
	tagStart, tagEnd int
	hasPreserve      bool
}

type paraCtx struct {
	para   int
	seg    int
	pieces []unitPiece
	openT  []tInfo
}

func isWMLName(n xml.Name) bool {
	return n.Space == nsWMLTransitional || n.Space == nsWMLStrict
}

// isMCName 报告元素是否为 Markup Compatibility 的兼容容器/分支。
// 冻结 D3 要求 Choice/Fallback 两个存储分支各自形成独立 unit，
// 不得跨分支拼接，也不得与前后宿主文本拼接。
func isMCName(n xml.Name) bool {
	if n.Space != nsMC {
		return false
	}
	switch n.Local {
	case "AlternateContent", "Choice", "Fallback":
		return true
	}
	return false
}

// hardBoundary 报告 WML 元素是否为片段硬边界。
func hardBoundary(local string) bool {
	switch local {
	case "tab", "br", "cr", "drawing", "object", "pict", "fldChar", "txbxContent":
		return true
	}
	return false
}

type partSpec struct {
	name string
	kind PartKind
}

// textPartSpecs 返回按规格顺序排列的文字部件：主文档，随后页眉/页脚/脚注/尾注按 URI 字典序。
func (d *Document) textPartSpecs() []partSpec {
	specs := []partSpec{{name: d.mainPart, kind: KindDocument}}
	var headers, footers, footnotes, endnotes []string
	for _, name := range d.order {
		p := d.parts[name]
		if p.removed {
			continue
		}
		switch p.ct {
		case ctHeader:
			headers = append(headers, name)
		case ctFooter:
			footers = append(footers, name)
		case ctFootnotes:
			footnotes = append(footnotes, name)
		case ctEndnotes:
			endnotes = append(endnotes, name)
		}
	}
	// d.order 已按规范化 URI 排序，过滤后保持有序。
	for _, n := range headers {
		specs = append(specs, partSpec{n, KindHeader})
	}
	for _, n := range footers {
		specs = append(specs, partSpec{n, KindFooter})
	}
	for _, n := range footnotes {
		specs = append(specs, partSpec{n, KindFootnote})
	}
	for _, n := range endnotes {
		specs = append(specs, partSpec{n, KindEndnote})
	}
	return specs
}

// rebuildUnits 枚举当前所有文字部件，赋予稳定顺序与段落/片段编号。
func (d *Document) rebuildUnits() error {
	var all []TextUnit
	for _, spec := range d.textPartSpecs() {
		p := d.parts[spec.name]
		units, err := d.enumeratePart(p, spec.kind)
		if err != nil {
			return err
		}
		all = append(all, units...)
	}
	for i := range all {
		all[i].Index = i
	}
	d.units = all
	d.expected = make(map[int]string, len(all))
	for i := range all {
		d.expected[i] = all[i].Text
	}
	return nil
}

func (d *Document) enumeratePart(p *part, kind PartKind) ([]TextUnit, error) {
	var out []TextUnit
	nextPara := 1
	var stack []*paraCtx

	flush := func(c *paraCtx) {
		if c == nil || len(c.pieces) == 0 {
			return
		}
		var b strings.Builder
		for _, pc := range c.pieces {
			b.WriteString(pc.text)
		}
		text := b.String()
		if text == "" {
			c.pieces = nil
			return
		}
		c.seg++
		pieces := make([]unitPiece, len(c.pieces))
		off := 0
		for i, pc := range c.pieces {
			pc.offset = off
			pieces[i] = pc
			off += len(pc.text)
		}
		out = append(out, TextUnit{
			Part: p.name, PartKind: kind, Paragraph: c.para, Segment: c.seg, Text: text, pieces: pieces,
		})
		c.pieces = nil
	}

	top := func() *paraCtx {
		if len(stack) == 0 {
			return nil
		}
		return stack[len(stack)-1]
	}

	err := d.walkPart(p, func(ev *event) error {
		switch ev.kind {
		case evStart:
			if isMCName(ev.name) {
				// 进入兼容容器/分支：隔离前后宿主文本与相邻分支。
				if c := top(); c != nil {
					flush(c)
				}
				return nil
			}
			if !isWMLName(ev.name) {
				return nil
			}
			switch ev.name.Local {
			case "altChunk":
				return herrf("DOCX 含 external 导入文字结构（w:altChunk），无法可靠枚举",
					"请先在 Word 中把导入内容转换为普通文字并另存为新的 .docx 后重试", "docx.alt_chunk_unsupported")
			case "p":
				stack = append(stack, &paraCtx{para: nextPara})
				nextPara++
			case "t":
				c := top()
				if c == nil {
					return herrf("DOCX 部件 "+p.name+" 含无段落归属的原生文字",
						"文件结构异常；请在 Word 中另存为新的 .docx 后重试")
				}
				c.openT = append(c.openT, tInfo{tagStart: ev.start, tagEnd: ev.end, hasPreserve: hasPreserve(ev.attrs)})
			default:
				if hardBoundary(ev.name.Local) {
					if c := top(); c != nil {
						flush(c)
					}
				}
			}
		case evEnd:
			if isMCName(ev.name) {
				// 退出兼容容器/分支：结束该分支独立片段。
				if c := top(); c != nil {
					flush(c)
				}
				return nil
			}
			if !isWMLName(ev.name) {
				return nil
			}
			switch ev.name.Local {
			case "p":
				if len(stack) == 0 {
					return herrf("DOCX 部件 "+p.name+" 的段落标签不匹配",
						"文件结构异常；请在 Word 中另存为新的 .docx 后重试")
				}
				c := stack[len(stack)-1]
				flush(c)
				stack = stack[:len(stack)-1]
			case "t":
				if c := top(); c != nil && len(c.openT) > 0 {
					c.openT = c.openT[:len(c.openT)-1]
				}
			}
		case evChar:
			if !isWMLName(ev.parent) || ev.parent.Local != "t" {
				return nil
			}
			c := top()
			if c == nil || len(c.openT) == 0 {
				return herrf("DOCX 部件 "+p.name+" 含无段落归属的原生文字",
					"文件结构异常；请在 Word 中另存为新的 .docx 后重试")
			}
			ti := c.openT[len(c.openT)-1]
			if n := len(c.pieces); n > 0 {
				last := &c.pieces[n-1]
				if last.tagStart == ti.tagStart && last.srcEnd == ev.start {
					last.text += ev.text
					last.srcEnd = ev.end
					return nil
				}
			}
			c.pieces = append(c.pieces, unitPiece{
				text: ev.text, srcStart: ev.start, srcEnd: ev.end,
				tagStart: ti.tagStart, tagEnd: ti.tagEnd, hasPreserve: ti.hasPreserve,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(stack) != 0 {
		return nil, herrf("DOCX 部件 "+p.name+" 的段落标签未闭合",
			"文件结构异常；请在 Word 中另存为新的 .docx 后重试")
	}
	return out, nil
}

func hasPreserve(attrs []xml.Attr) bool {
	for _, a := range attrs {
		if a.Name.Space == nsXML && a.Name.Local == "space" {
			return strings.TrimSpace(a.Value) == "preserve"
		}
	}
	return false
}

// Units 返回清理后按稳定顺序排列的文字单元。
func (d *Document) Units() []TextUnit { return d.units }
