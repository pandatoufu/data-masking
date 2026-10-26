package pptx

import (
	"encoding/xml"
	"strings"
)

// PartKind 标识文字单元所在区域。
type PartKind string

const (
	KindSlide         PartKind = "slide"
	KindNotes         PartKind = "notes"
	KindLayout        PartKind = "layout"
	KindSlideMaster   PartKind = "slideMaster"
	KindNotesMaster   PartKind = "notesMaster"
	KindHandoutMaster PartKind = "handoutMaster"
)

// unitPiece 是一个文字节点在某个文字单元中的片段，带来源 XML 字节范围。
type unitPiece struct {
	text        string
	offset      int
	srcStart    int
	srcEnd      int
	tagStart    int
	tagEnd      int
	hasPreserve bool
}

// TextUnit 是一个稳定的逻辑文字单元。
type TextUnit struct {
	Index     int
	Part      string
	PartKind  PartKind
	Paragraph int
	Segment   int
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

// isMCName 报告元素是否为 Markup Compatibility 的兼容容器/分支。
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

// skipSubtree 报告已知不处理对象或非文字存储，应整棵跳过。
func skipSubtree(n xml.Name) bool {
	if isPMLName(n) {
		switch n.Local {
		case "pic", "oleObj", "contentPart", "control", "timing", "transition", "extLst":
			return true
		}
	}
	if isDrawingName(n) && n.Local == "extLst" {
		return true
	}
	return false
}

type partSpec struct {
	name string
	kind PartKind
}

// textPartSpecs 返回规范顺序的文字部件：主序列页及备注，其余页、备注、版式、
// 幻灯片母版、备注母版、讲义母版；同类按规范化 URI 升序，每部件仅一次。
func (d *Document) textPartSpecs() []partSpec {
	var out []partSpec
	seen := map[string]bool{}
	add := func(name string, kind PartKind) {
		if name == "" || seen[name] {
			return
		}
		p, ok := d.parts[name]
		if !ok || p.removed {
			return
		}
		seen[name] = true
		out = append(out, partSpec{name: name, kind: kind})
	}
	for _, s := range d.slideOrder {
		add(s, KindSlide)
		if n := d.notesOf[s]; n != "" {
			add(n, KindNotes)
		}
	}
	for _, s := range d.allSlides {
		add(s, KindSlide)
		if n := d.notesOf[s]; n != "" {
			add(n, KindNotes)
		}
	}
	for _, n := range d.notes {
		add(n, KindNotes)
	}
	for _, n := range d.layouts {
		add(n, KindLayout)
	}
	for _, n := range d.masters {
		add(n, KindSlideMaster)
	}
	for _, n := range d.notesMast {
		add(n, KindNotesMaster)
	}
	for _, n := range d.handouts {
		add(n, KindHandoutMaster)
	}
	return out
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
	skipDepth := 0
	txDepth := 0
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
	badText := func(what string) error {
		return herrf("PPTX 部件 "+p.name+" 含无法可靠枚举的原生文字（"+what+"）",
			"请在 PowerPoint 中将该内容转换为普通文本框或另存为新的 .pptx 后重试", "pptx.text_unsupported")
	}

	err := d.walkPart(p, func(ev *event) error {
		switch ev.kind {
		case evStart:
			if skipDepth > 0 {
				skipDepth++
				return nil
			}
			if skipSubtree(ev.name) {
				skipDepth = 1
				return nil
			}
			if isMCName(ev.name) {
				if c := top(); c != nil {
					flush(c)
				}
				return nil
			}
			if isDrawingName(ev.name) || isPMLName(ev.name) {
				switch ev.name.Local {
				case "txBody":
					txDepth++
					return nil
				case "p":
					stack = append(stack, &paraCtx{para: nextPara})
					nextPara++
					return nil
				}
			}
			if isDrawingName(ev.name) {
				switch ev.name.Local {
				case "t":
					c := top()
					if txDepth == 0 || c == nil {
						return badText("a:t 不在文本体内")
					}
					c.openT = append(c.openT, tInfo{tagStart: ev.start, tagEnd: ev.end, hasPreserve: hasPreserve(ev.attrs)})
					return nil
				case "br", "tab", "fld":
					if c := top(); c != nil {
						flush(c)
					}
					return nil
				}
			}
			return nil
		case evEnd:
			if skipDepth > 0 {
				skipDepth--
				return nil
			}
			if isMCName(ev.name) {
				if c := top(); c != nil {
					flush(c)
				}
				return nil
			}
			if isDrawingName(ev.name) || isPMLName(ev.name) {
				switch ev.name.Local {
				case "txBody":
					if txDepth > 0 {
						txDepth--
					}
					return nil
				case "p":
					if len(stack) == 0 {
						return herrf("PPTX 部件 "+p.name+" 的段落标签不匹配",
							"文件结构异常；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.xml_parse_failed")
					}
					c := stack[len(stack)-1]
					flush(c)
					stack = stack[:len(stack)-1]
					return nil
				}
			}
			if isDrawingName(ev.name) {
				switch ev.name.Local {
				case "t":
					if c := top(); c != nil && len(c.openT) > 0 {
						c.openT = c.openT[:len(c.openT)-1]
					}
					return nil
				case "fld":
					if c := top(); c != nil {
						flush(c)
					}
					return nil
				}
			}
			return nil
		case evChar:
			if skipDepth > 0 {
				return nil
			}
			if !isDrawingName(ev.parent) || ev.parent.Local != "t" {
				return nil
			}
			c := top()
			if c == nil || len(c.openT) == 0 {
				return badText("文字节点无段落归属")
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
		return nil, herrf("PPTX 部件 "+p.name+" 的段落标签未闭合",
			"文件结构异常；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.xml_parse_failed")
	}
	if txDepth != 0 || skipDepth != 0 {
		return nil, herrf("PPTX 部件 "+p.name+" 的 XML 结构未闭合",
			"文件结构异常；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.xml_parse_failed")
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
