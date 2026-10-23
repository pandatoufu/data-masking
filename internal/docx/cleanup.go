package docx

import (
	"bytes"
	"encoding/xml"
	"io"
	"sort"
	"strings"
)

// Prepare 清理批注与修订、重建索引，并产出清理摘要。可重复调用（幂等）。
func (d *Document) Prepare() (*CleanupSummary, error) {
	if d.prepared {
		s := d.cleanup
		return &s, nil
	}

	reachBefore := d.reachable()

	// 清理前，记录存活 WML 部件引用的关系 ID。
	beforeRefs := map[string]map[string]bool{}
	for _, name := range d.order {
		p := d.parts[name]
		if p.removed || !isSurvivingWML(p) {
			continue
		}
		refs, err := d.relationshipRefs(p)
		if err != nil {
			return nil, err
		}
		beforeRefs[name] = refs
	}

	// 1) 删除批注家族部件及其关系/声明。
	commentParts := d.commentPartSet()
	removed := map[string]bool{}
	for name := range commentParts {
		removed[name] = true
	}
	for name := range commentParts {
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

	// 2) 接受修订、移除批注锚点（存活 WML 部件）。
	acceptedTotal, anchorTotal := 0, 0
	for _, name := range d.order {
		p := d.parts[name]
		if p.removed || !isSurvivingWML(p) {
			continue
		}
		newData, acc, anchors, err := d.rewritePart(p)
		if err != nil {
			return nil, err
		}
		acceptedTotal += acc
		anchorTotal += anchors
		if !bytes.Equal(newData, p.data) {
			p.data = newData
			p.dirty = true
		}
	}

	// 3) 关系引用图：删除因清理失去引用的关系。
	afterRefs := map[string]map[string]bool{}
	for _, name := range d.order {
		p := d.parts[name]
		if p.removed || !isSurvivingWML(p) {
			continue
		}
		refs, err := d.relationshipRefs(p)
		if err != nil {
			return nil, err
		}
		afterRefs[name] = refs
	}
	for source, before := range beforeRefs {
		after := afterRefs[source]
		lost := map[string]bool{}
		for id := range before {
			if after == nil || !after[id] {
				lost[id] = true
			}
		}
		if len(lost) == 0 {
			continue
		}
		d.dropRelationships(source, lost)
	}

	// 4) 删除本次清理导致不可达的历史专属资源。
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

	// 5) 清理 [Content_Types].xml 中已删除部件的 Override。
	if err := d.pruneContentTypeOverrides(removed); err != nil {
		return nil, err
	}

	// 6) 重建索引与文字单元。
	if err := d.rebuildUnits(); err != nil {
		return nil, err
	}
	d.prepared = true
	d.cleanup = CleanupSummary{
		CommentsDeleted:   anchorTotal,
		RevisionsAccepted: acceptedTotal,
		PartsRemoved:      len(removed),
		ResourcesRemoved:  resourcesRemoved,
	}

	// 7) 生成清理后快照与图片基线，供输出验证使用。
	cleaned, err := d.Bytes()
	if err != nil {
		return nil, err
	}
	snap, err := Snapshot(cleaned)
	if err != nil {
		return nil, err
	}
	d.cleanedSnapshot = snap
	d.cleanedImages = map[string]string{}
	d.imageNames = nil
	for _, name := range d.order {
		p := d.parts[name]
		if p.removed {
			continue
		}
		if strings.HasPrefix(p.ct, "image/") {
			d.imageNames = append(d.imageNames, name)
			d.cleanedImages[name] = hashBytes(p.data)
		}
	}
	sort.Strings(d.imageNames)
	s := d.cleanup
	return &s, nil
}

func isSurvivingWML(p *part) bool {
	return wmlContentTypes[p.ct] && !p.removed
}

// commentPartSet 返回应删除的批注家族部件集合。
func (d *Document) commentPartSet() map[string]bool {
	out := map[string]bool{}
	for _, name := range d.order {
		p := d.parts[name]
		if p.removed {
			continue
		}
		if isCommentFamilyContentType(p.ct) {
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

// relationshipRefs 收集部件 XML 中引用的关系 ID（关系命名空间属性值）。
func (d *Document) relationshipRefs(p *part) (map[string]bool, error) {
	refs := map[string]bool{}
	err := d.walkPart(p, func(ev *event) error {
		if ev.kind != evStart {
			return nil
		}
		for _, a := range ev.attrs {
			if a.Name.Space == nsRelTransitional || a.Name.Space == nsRelStrict {
				if a.Value != "" {
					refs[a.Value] = true
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return refs, nil
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

// dropRelationships 删除某源部件中指定 ID 的关系。
func (d *Document) dropRelationships(source string, ids map[string]bool) {
	rels, ok := d.rels[source]
	if !ok {
		return
	}
	kept := rels[:0]
	changed := false
	for _, r := range rels {
		if ids[r.ID] {
			changed = true
			continue
		}
		kept = append(kept, r)
	}
	if changed {
		d.rels[source] = kept
		d.markRelsDirty(source)
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
