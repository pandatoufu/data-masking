package docx

import (
	"bytes"
	"encoding/xml"
	"io"
	"sort"
	"strings"
)

// deleteSubtree 报告需要整棵删除的存活内容修订/批注锚点。
var deleteSubtreeLocals = map[string]bool{
	"del":                true,
	"moveFrom":           true,
	"moveFromRangeStart": true,
	"moveFromRangeEnd":   true,
	"moveToRangeStart":   true,
	"moveToRangeEnd":     true,
	"rPrChange":          true,
	"pPrChange":          true,
	"sectPrChange":       true,
	"tblPrChange":        true,
	"tblGridChange":      true,
	"trPrChange":         true,
	"tcPrChange":         true,
	"trackRevisions":     true,
	"commentRangeStart":  true,
	"commentRangeEnd":    true,
	"commentReference":   true,
	"delText":            true,
	"delInstrText":       true,
}

// unwrapLocals 报告需要保留子内容、去掉修订外壳的元素。
var unwrapLocals = map[string]bool{
	"ins":    true,
	"moveTo": true,
}

// rejectRevisionLocals 是明确不支持的修订（需要先接受修订）。
var rejectRevisionLocals = map[string]bool{
	"cellIns":                     true,
	"cellDel":                     true,
	"cellMerge":                   true,
	"numberingChange":             true,
	"customXmlInsRangeStart":      true,
	"customXmlInsRangeEnd":        true,
	"customXmlDelRangeStart":      true,
	"customXmlDelRangeEnd":        true,
	"customXmlMoveFromRangeStart": true,
	"customXmlMoveFromRangeEnd":   true,
	"customXmlMoveToRangeStart":   true,
	"customXmlMoveToRangeEnd":     true,
}

func isRevisionLocal(local string) bool {
	if deleteSubtreeLocals[local] || unwrapLocals[local] || rejectRevisionLocals[local] {
		return true
	}
	return strings.HasSuffix(local, "Change") ||
		strings.Contains(local, "RangeStart") || strings.Contains(local, "RangeEnd") ||
		strings.HasSuffix(local, "Ins") || strings.HasSuffix(local, "Del")
}

func rejectRevision(local string) error {
	return herrfParams("docx.unsupported_revision", map[string]any{"revision": local},
		"检测到无法可靠接受的修订："+local,
		"请先在 Word 中接受全部修订并删除全部批注，另存为新的 .docx 后再试")
}

type rewriteFrame struct {
	name    xml.Name
	unwrap  bool
	decls   map[string]string
	emitted bool
}

// rewritePart 接受支持的修订并移除批注锚点，保留其余原始 XML 字节。
// 返回新字节、接受的修订数、移除的批注锚点数。
func (d *Document) rewritePart(p *part) ([]byte, int, int, error) {
	dec := xml.NewDecoder(bytes.NewReader(p.data))
	dec.Strict = true

	var out bytes.Buffer
	var stack []rewriteFrame
	var pending []map[string]string
	var scopeStack []map[string]string
	scopeStack = append(scopeStack, map[string]string{"xml": nsXML})
	skipDepth := 0
	accepted := 0
	anchors := 0

	scopeTop := func() map[string]string { return scopeStack[len(scopeStack)-1] }

	for {
		prev := int(dec.InputOffset())
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, 0, wrapXMLParseError(p.name, err)
		}
		after := int(dec.InputOffset())
		raw := p.data[prev:after]

		switch t := tok.(type) {
		case xml.StartElement:
			if skipDepth > 0 {
				skipDepth++
				continue
			}
			name, attrNames, _, perr := parseRawStartTag(raw)
			if perr != nil {
				return nil, 0, 0, herrf("DOCX 部件 "+p.name+" 的 XML 标签异常："+perr.Error(),
					"文件结构损坏；请在 Word 中另存为新的 .docx 后重试")
			}
			_ = name
			local := t.Name.Local
			decision := "emit"
			if isWMLName(t.Name) {
				contextBad := hasAncestorName(stack, "pPr") || hasAncestorName(stack, "rPr") || hasAncestorName(stack, "trPr")
				switch {
				case local == "ins" || local == "moveTo":
					if contextBad {
						return nil, 0, 0, rejectRevision(local)
					}
					accepted++
					decision = "unwrap"
				case local == "del" || local == "moveFrom":
					if contextBad {
						return nil, 0, 0, rejectRevision(local)
					}
					accepted++
					decision = "skip"
				case rejectRevisionLocals[local]:
					return nil, 0, 0, rejectRevision(local)
				case deleteSubtreeLocals[local]:
					if local == "commentRangeStart" || local == "commentRangeEnd" || local == "commentReference" {
						anchors++
					} else {
						accepted++
					}
					decision = "skip"
				case isRevisionLocal(local):
					return nil, 0, 0, rejectRevision(local)
				}
			}

			switch decision {
			case "skip":
				skipDepth = 1
			case "unwrap":
				decls := declsOf(t.Attr)
				pending = append(pending, decls)
				stack = append(stack, rewriteFrame{name: t.Name, unwrap: true, decls: decls})
			default:
				localDecls := declsOf(t.Attr)
				emittedRaw := patchStartTag(raw, localDecls, pending, scopeTop(), attrNames)
				out.Write(emittedRaw)
				merged := map[string]string{}
				for k, v := range localDecls {
					merged[k] = v
				}
				chosen := choosePending(pending)
				for k, v := range chosen {
					if _, ok := merged[k]; !ok {
						merged[k] = v
					}
				}
				scopeStack = append(scopeStack, mergeScope(scopeTop(), merged))
				stack = append(stack, rewriteFrame{name: t.Name, emitted: true, decls: localDecls})
			}
		case xml.EndElement:
			if skipDepth > 0 {
				skipDepth--
				continue
			}
			if len(stack) == 0 {
				return nil, 0, 0, herrf("DOCX 部件 "+p.name+" 的标签不匹配",
					"文件结构损坏；请在 Word 中另存为新的 .docx 后重试")
			}
			f := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if f.unwrap {
				pending = pending[:len(pending)-1]
				continue
			}
			out.Write(raw)
			scopeStack = scopeStack[:len(scopeStack)-1]
		default:
			if skipDepth > 0 {
				continue
			}
			out.Write(raw)
		}
	}
	if skipDepth != 0 || len(stack) != 0 {
		return nil, 0, 0, herrf("DOCX 部件 "+p.name+" 的 XML 标签未闭合",
			"文件结构损坏；请在 Word 中另存为新的 .docx 后重试")
	}
	return out.Bytes(), accepted, anchors, nil
}

func hasAncestorName(stack []rewriteFrame, local string) bool {
	for _, f := range stack {
		if f.name.Local == local {
			return true
		}
	}
	return false
}

func declsOf(attrs []xml.Attr) map[string]string {
	out := map[string]string{}
	for _, a := range attrs {
		if a.Name.Space == "xmlns" {
			out[a.Name.Local] = a.Value
		} else if a.Name.Space == "" && a.Name.Local == "xmlns" {
			out[""] = a.Value
		}
	}
	return out
}

// choosePending 决定当前元素应继承的（来自被移除外壳的）命名空间绑定，内层优先。
func choosePending(pending []map[string]string) map[string]string {
	if len(pending) == 0 {
		return nil
	}
	chosen := map[string]string{}
	for i := len(pending) - 1; i >= 0; i-- {
		for p, u := range pending[i] {
			if _, ok := chosen[p]; !ok {
				chosen[p] = u
			}
		}
	}
	return chosen
}

func mergeScope(base, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// patchStartTag 把被移除外壳的局部命名空间声明转移到存活元素上，保证前缀绑定有效。
func patchStartTag(raw []byte, localDecls map[string]string, pending []map[string]string, current map[string]string, attrNames []string) []byte {
	chosen := choosePending(pending)
	if len(chosen) == 0 {
		return raw
	}
	var inject []string
	for p, u := range chosen {
		if _, local := localDecls[p]; local {
			continue
		}
		if cur, ok := current[p]; ok && cur == u {
			continue
		}
		inject = append(inject, declAttr(p, u))
	}
	if len(inject) == 0 {
		return raw
	}
	sort.Strings(inject)
	i := bytes.LastIndexByte(raw, '>')
	if i < 0 {
		return raw
	}
	j := i
	if i >= 1 && raw[i-1] == '/' {
		j = i - 1
	}
	var b bytes.Buffer
	b.Write(raw[:j])
	b.WriteByte(' ')
	b.WriteString(strings.Join(inject, " "))
	b.Write(raw[j:])
	return b.Bytes()
}

func declAttr(prefix, uri string) string {
	q := strings.ReplaceAll(uri, `"`, "&quot;")
	if prefix == "" {
		return `xmlns="` + q + `"`
	}
	return "xmlns:" + prefix + `="` + q + `"`
}
