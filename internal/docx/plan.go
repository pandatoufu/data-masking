package docx

import (
	"fmt"
	"sort"
)

// Edit 是作用于已解码 UTF-8 文字的一段互不重叠替换区间。
type Edit struct {
	Start       int
	End         int
	Replacement string
}

// localEdit 是映射到单个文字节点内的局部替换。
type localEdit struct {
	start, end int
	repl       string
}

// Apply 把某文字单元上的 edits 规划并写入对应 XML 节点。
// edits 偏移作用于该单元的已解码 UTF-8 文字；跨节点命中时完整替换写入首个命中节点。
func (d *Document) Apply(unit TextUnit, edits []Edit) error {
	if !d.prepared {
		return fmt.Errorf("docx: 必须先 Prepare 才能 Apply")
	}
	if unit.Index < 0 || unit.Index >= len(d.units) {
		return fmt.Errorf("docx: 文字单元索引越界")
	}
	u := d.units[unit.Index]
	if len(edits) == 0 {
		return nil
	}
	es := make([]Edit, len(edits))
	copy(es, edits)
	sort.Slice(es, func(i, j int) bool { return es[i].Start < es[j].Start })
	for i := 1; i < len(es); i++ {
		if es[i].Start < es[i-1].End {
			return fmt.Errorf("docx: edits 必须互不重叠")
		}
	}
	if es[0].Start < 0 || es[len(es)-1].End > len(u.Text) {
		return fmt.Errorf("docx: edit 超出文字单元范围")
	}

	pieceEdits := make([][]localEdit, len(u.pieces))
	for _, e := range es {
		placed := false
		for i := range u.pieces {
			pc := u.pieces[i]
			pStart := pc.offset
			pEnd := pc.offset + len(pc.text)
			if e.End <= pStart || e.Start >= pEnd {
				continue
			}
			ls := maxInt(e.Start, pStart) - pStart
			le := minInt(e.End, pEnd) - pStart
			repl := ""
			if !placed {
				repl = e.Replacement
				placed = true
			}
			pieceEdits[i] = append(pieceEdits[i], localEdit{start: ls, end: le, repl: repl})
		}
		if !placed {
			return fmt.Errorf("docx: edit 无法映射到文字节点")
		}
	}

	p := d.parts[u.Part]
	if p == nil || p.removed {
		return fmt.Errorf("docx: 文字单元所属部件已移除")
	}
	for i := range u.pieces {
		if len(pieceEdits[i]) == 0 {
			continue
		}
		pc := u.pieces[i]
		sort.Slice(pieceEdits[i], func(a, b int) bool { return pieceEdits[i][a].start < pieceEdits[i][b].start })
		newText := spliceText(pc.text, pieceEdits[i])
		if newText == pc.text {
			continue
		}
		d.addPatch(u.Part, patch{start: pc.srcStart, end: pc.srcEnd, text: escapeText(newText)})
		if needsPreserve(newText) && !pc.hasPreserve {
			j := insertionPoint(p.data, pc.tagStart, pc.tagEnd)
			d.addPatch(u.Part, patch{start: j, end: j, text: ` xml:space="preserve"`})
		}
	}
	p.dirty = true
	d.expected[u.Index] = applyEditsToText(u.Text, es)
	return nil
}

func spliceText(text string, edits []localEdit) string {
	var b []byte
	prev := 0
	for _, e := range edits {
		if e.start < prev {
			continue
		}
		b = append(b, text[prev:e.start]...)
		b = append(b, e.repl...)
		prev = e.end
	}
	b = append(b, text[prev:]...)
	return string(b)
}

func applyEditsToText(text string, edits []Edit) string {
	var b []byte
	prev := 0
	for _, e := range edits {
		if e.Start < prev {
			continue
		}
		b = append(b, text[prev:e.Start]...)
		b = append(b, e.Replacement...)
		prev = e.End
	}
	b = append(b, text[prev:]...)
	return string(b)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ExpectedText 返回某单元在已应用 edits 后的预期文字（用于输出验证）。
func (d *Document) ExpectedText(index int) (string, bool) {
	t, ok := d.expected[index]
	return t, ok
}
