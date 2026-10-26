package pptx

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// StructureSnapshot 是 PPTX 包的结构/资源快照（不含文字内容）。
type StructureSnapshot struct {
	Structures map[string]string   // XML 部件名 → 去文字的结构摘要
	Blobs      map[string]string   // 非 XML 部件名 → 解压字节 SHA-256
	RelTargets map[string][]string // 源部件 → 关系目标描述
}

// SnapshotDiff 是一条结构差异。
type SnapshotDiff struct {
	Part   string
	Kind   string
	Detail string
}

// Snapshot 读取包并生成结构快照。
func Snapshot(data []byte) (*StructureSnapshot, error) {
	doc, err := Open(data, DefaultLimits())
	if err != nil {
		return nil, err
	}
	return snapshotOf(doc)
}

func snapshotOf(doc *Document) (*StructureSnapshot, error) {
	s := &StructureSnapshot{
		Structures: map[string]string{},
		Blobs:      map[string]string{},
		RelTargets: map[string][]string{},
	}
	for _, name := range doc.order {
		p := doc.parts[name]
		if p.removed {
			continue
		}
		if isXMLPart(name) && isSlideTextPart(p.ct) {
			digest, err := doc.structuralDigest(p)
			if err != nil {
				return nil, err
			}
			s.Structures[name] = digest
			continue
		}
		s.Blobs[name] = hashBytes(p.data)
	}
	for src, rels := range doc.rels {
		var ts []string
		for _, r := range rels {
			mode := "internal"
			target := r.Resolved
			if r.External {
				mode = "external"
				target = r.Target
			}
			ts = append(ts, r.Type+"|"+mode+"|"+target)
		}
		sort.Strings(ts)
		s.RelTargets[src] = ts
	}
	return s, nil
}

// structuralDigest 生成忽略文字内容与 xml:space 的元素/属性摘要。
func (d *Document) structuralDigest(p *part) (string, error) {
	var b strings.Builder
	err := d.walkPart(p, func(ev *event) error {
		switch ev.kind {
		case evStart:
			b.WriteByte('<')
			b.WriteString(ev.name.Space)
			b.WriteByte('|')
			b.WriteString(ev.name.Local)
			attrs := make([]string, 0, len(ev.attrs))
			for _, a := range ev.attrs {
				if a.Name.Space == nsXML && a.Name.Local == "space" {
					continue
				}
				attrs = append(attrs, a.Name.Space+"|"+a.Name.Local+"="+a.Value)
			}
			sort.Strings(attrs)
			for _, a := range attrs {
				b.WriteByte(' ')
				b.WriteString(a)
			}
			b.WriteByte('>')
		case evEnd:
			b.WriteString("</")
			b.WriteString(ev.name.Space)
			b.WriteByte('|')
			b.WriteString(ev.name.Local)
			b.WriteByte('>')
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return b.String(), nil
}

// Diff 返回本快照相对 other 的差异。
func (s *StructureSnapshot) Diff(other *StructureSnapshot) []SnapshotDiff {
	var out []SnapshotDiff
	for name, digest := range s.Structures {
		if other.Structures[name] != digest {
			out = append(out, SnapshotDiff{Part: name, Kind: "structure", Detail: "结构/样式/属性发生变化"})
		}
	}
	for name := range other.Structures {
		if _, ok := s.Structures[name]; !ok {
			out = append(out, SnapshotDiff{Part: name, Kind: "structure", Detail: "部件被删除"})
		}
	}
	for name, h := range s.Blobs {
		if other.Blobs[name] != h {
			out = append(out, SnapshotDiff{Part: name, Kind: "resource", Detail: "资源字节发生变化或缺失"})
		}
	}
	for name := range other.Blobs {
		if _, ok := s.Blobs[name]; !ok {
			out = append(out, SnapshotDiff{Part: name, Kind: "resource", Detail: "资源部件被删除"})
		}
	}
	for src, ts := range s.RelTargets {
		if strings.Join(other.RelTargets[src], "\n") != strings.Join(ts, "\n") {
			out = append(out, SnapshotDiff{Part: src, Kind: "relationship", Detail: "关系目标发生变化"})
		}
	}
	for src := range other.RelTargets {
		if _, ok := s.RelTargets[src]; !ok {
			out = append(out, SnapshotDiff{Part: src, Kind: "relationship", Detail: "关系被删除"})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Part != out[j].Part {
			return out[i].Part < out[j].Part
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
