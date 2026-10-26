package pptx

import (
	"bytes"
	"encoding/xml"
	"io"
	"sort"
	"strings"
)

// Prepare 清理批注家族与专用人员信息、重建索引，并产出清理摘要。可重复调用（幂等）。
func (d *Document) Prepare() (*CleanupSummary, error) {
	if d.prepared {
		s := d.cleanup
		return &s, nil
	}

	reachBefore := d.reachable()
	commentParts := d.commentPartSet()

	// 0) 移除现代批注在 Slide 上的 p188:commentRel 锚点及其专属 p:ext。
	for _, name := range d.order {
		p := d.parts[name]
		if p.removed || !isSlideTextPart(p.ct) {
			continue
		}
		stripped, removedAnchors, err := d.stripCommentAnchors(p)
		if err != nil {
			return nil, err
		}
		if removedAnchors > 0 {
			p.data = stripped
			p.dirty = true
		}
	}

	commentsDeleted := 0
	for name := range commentParts {
		p := d.parts[name]
		if p == nil || p.removed || isCommentAuthorContentType(p.ct) {
			continue
		}
		n, err := d.countComments(p)
		if err != nil {
			return nil, err
		}
		commentsDeleted += n
	}

	removed := map[string]bool{}
	peopleRemoved := 0
	for name := range commentParts {
		removed[name] = true
		if p := d.parts[name]; p != nil && isCommentAuthorContentType(p.ct) {
			peopleRemoved++
		}
		relsName := relsPartName(name)
		if rp, ok := d.parts[relsName]; ok && !rp.removed {
			removed[relsName] = true
		}
	}
	for name := range removed {
		if p, ok := d.parts[name]; ok {
			p.removed = true
		}
	}
	d.dropRelationshipsTo(removed)

	// 删除本次清理导致不可达的历史专属资源（共享图片和正文资源保留）。
	reachAfter := d.reachable()
	resourcesRemoved := 0
	for name := range reachBefore {
		if reachAfter[name] {
			continue
		}
		p, ok := d.parts[name]
		if !ok || p.removed {
			continue
		}
		p.removed = true
		resourcesRemoved++
		removed[name] = true
	}

	if err := d.pruneContentTypeOverrides(removed); err != nil {
		return nil, err
	}

	if err := d.rebuildUnits(); err != nil {
		return nil, err
	}
	d.prepared = true
	d.cleanup = CleanupSummary{
		CommentsDeleted:    commentsDeleted,
		PeoplePartsRemoved: peopleRemoved,
		PartsRemoved:       len(removed),
		ResourcesRemoved:   resourcesRemoved,
	}

	cleaned, err := d.Bytes()
	if err != nil {
		return nil, err
	}
	snap, err := snapshotFromBytes(cleaned, d.limits)
	if err != nil {
		return nil, err
	}
	d.cleanedSnapshot = snap
	s := d.cleanup
	return &s, nil
}

func snapshotFromBytes(data []byte, limits Limits) (*StructureSnapshot, error) {
	doc, err := Open(data, limits)
	if err != nil {
		return nil, err
	}
	return snapshotOf(doc)
}

// stripCommentAnchors 删除 Slide/Master/Layout 上承载现代批注的 p:ext
// （其唯一子元素为 p188:commentRel）。若该 p:ext 还含其他扩展，拒绝而不丢弃无关数据。
func (d *Document) stripCommentAnchors(p *part) ([]byte, int, error) {
	type extFrame struct {
		start  int
		end    int
		hasRel bool
		other  bool
	}
	var stack []*extFrame
	var cuts [][2]int
	err := d.walkPart(p, func(ev *event) error {
		switch ev.kind {
		case evStart:
			if isPMLName(ev.name) && ev.name.Local == "ext" {
				stack = append(stack, &extFrame{start: ev.start})
				return nil
			}
			if len(stack) > 0 {
				top := stack[len(stack)-1]
				if ev.name.Space == nsP188 && ev.name.Local == "commentRel" {
					top.hasRel = true
				} else {
					top.other = true
				}
			}
		case evEnd:
			if isPMLName(ev.name) && ev.name.Local == "ext" && len(stack) > 0 {
				f := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				f.end = ev.end
				if f.hasRel {
					if f.other {
						return rejectUnknownComment("mixed comment anchor extension")
					}
					cuts = append(cuts, [2]int{f.start, f.end})
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	if len(cuts) == 0 {
		return p.data, 0, nil
	}
	sort.Slice(cuts, func(i, j int) bool { return cuts[i][0] > cuts[j][0] })
	data := append([]byte(nil), p.data...)
	for _, c := range cuts {
		data = append(data[:c[0]], data[c[1]:]...)
	}
	return data, len(cuts), nil
}

// countComments 统计批注部件中的批注条目（不含回复）。
func (d *Document) countComments(p *part) (int, error) {
	n := 0
	err := d.walkPart(p, func(ev *event) error {
		if ev.kind != evStart {
			return nil
		}
		if ev.name.Local != "cm" {
			return nil
		}
		if isPMLName(ev.name) || ev.name.Space == nsP188 {
			n++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// commentPartSet 返回应删除的批注家族部件集合。
func (d *Document) commentPartSet() map[string]bool {
	out := map[string]bool{}
	for _, name := range d.order {
		p := d.parts[name]
		if p.removed {
			continue
		}
		if isKnownCommentContentType(p.ct) {
			out[name] = true
		}
	}
	for _, rels := range d.rels {
		for _, r := range rels {
			if r.External || r.Resolved == "" {
				continue
			}
			if isKnownCommentRel(r.Type) {
				if _, ok := d.parts[r.Resolved]; ok {
					out[r.Resolved] = true
				}
			}
		}
	}
	return out
}

// reachable 从包根出发，沿内部关系计算可达部件集合。
func (d *Document) reachable() map[string]bool {
	seen := map[string]bool{}
	queue := []string{""}
	queued := map[string]bool{"": true}
	for len(queue) > 0 {
		src := queue[0]
		queue = queue[1:]
		for _, r := range d.rels[src] {
			if r.External || r.Resolved == "" {
				continue
			}
			p, ok := d.parts[r.Resolved]
			if !ok || p.removed {
				continue
			}
			if !seen[r.Resolved] {
				seen[r.Resolved] = true
			}
			if !queued[r.Resolved] {
				queued[r.Resolved] = true
				queue = append(queue, r.Resolved)
			}
		}
	}
	return seen
}

// dropRelationshipsTo 删除所有指向给定部件的关系。
func (d *Document) dropRelationshipsTo(removed map[string]bool) {
	for src, rels := range d.rels {
		kept := rels[:0]
		changed := false
		for _, r := range rels {
			if !r.External && removed[r.Resolved] {
				changed = true
				continue
			}
			kept = append(kept, r)
		}
		if changed {
			d.rels[src] = kept
			d.markRelsDirty(src)
		}
	}
}

// markRelsDirty 重新序列化某源部件的 .rels 部件（保留确定性顺序）。
func (d *Document) markRelsDirty(source string) {
	name := relsPartName(source)
	p, ok := d.parts[name]
	if !ok || p.removed {
		return
	}
	data, err := marshalRelationships(name, d.rels[source])
	if err != nil {
		return
	}
	p.data = data
	p.dirty = true
}

func marshalRelationships(partName string, rels []relationship) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<Relationships xmlns="` + nsRelationships + `">`)
	sorted := make([]relationship, len(rels))
	copy(sorted, rels)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for _, r := range sorted {
		b.WriteString(`<Relationship Id="` + xmlEscapeAttr(r.ID) + `" Type="` + xmlEscapeAttr(r.Type) + `" Target="` + xmlEscapeAttr(r.Target) + `"`)
		if r.External {
			b.WriteString(` TargetMode="External"`)
		}
		b.WriteString(`/>`)
	}
	b.WriteString(`</Relationships>`)
	return b.Bytes(), nil
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

// pruneContentTypeOverrides 删除已移除部件的 Override 声明。
func (d *Document) pruneContentTypeOverrides(removed map[string]bool) error {
	if len(removed) == 0 {
		return nil
	}
	p, ok := d.parts["[Content_Types].xml"]
	if !ok {
		return nil
	}
	dec := xml.NewDecoder(bytes.NewReader(p.data))
	dec.Strict = true
	var cuts [][2]int
	for {
		prev := int(dec.InputOffset())
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return wrapXMLParseError("[Content_Types].xml", err)
		}
		after := int(dec.InputOffset())
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "Override" {
			continue
		}
		partName := attrValue(se.Attr, "PartName")
		if !strings.HasPrefix(partName, "/") {
			continue
		}
		if removed[partName[1:]] {
			cuts = append(cuts, [2]int{prev, after})
		}
	}
	if len(cuts) == 0 {
		return nil
	}
	sort.Slice(cuts, func(i, j int) bool { return cuts[i][0] > cuts[j][0] })
	data := append([]byte(nil), p.data...)
	for _, c := range cuts {
		data = append(data[:c[0]], data[c[1]:]...)
	}
	p.data = data
	p.dirty = true
	return nil
}
