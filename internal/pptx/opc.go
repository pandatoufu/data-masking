package pptx

import (
	"bytes"
	"encoding/xml"
	"io"
	"path"
	"strings"
)

// contentTypes 是 [Content_Types].xml 的解析结果。
type contentTypes struct {
	defaults  map[string]string
	overrides map[string]string
}

func parseContentTypes(data []byte) (*contentTypes, error) {
	ct := &contentTypes{defaults: map[string]string{}, overrides: map[string]string{}}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	rootSeen := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, wrapXMLParseError("[Content_Types].xml", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "Types":
				if t.Name.Space != nsContentTypes {
					return nil, herrf("[Content_Types].xml 根命名空间不正确",
						"请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.content_types_invalid")
				}
				rootSeen = true
			case "Default":
				ext := attrValue(t.Attr, "Extension")
				ctVal := attrValue(t.Attr, "ContentType")
				if ext == "" || ctVal == "" {
					return nil, herrf("[Content_Types].xml 含不完整 Default 声明",
						"文件结构异常；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.content_types_invalid")
				}
				if _, dup := ct.defaults[lowerASCII(ext)]; dup {
					return nil, herrf("[Content_Types].xml 含重复 Default："+ext,
						"文件结构异常；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.content_types_invalid")
				}
				ct.defaults[lowerASCII(ext)] = ctVal
			case "Override":
				part := attrValue(t.Attr, "PartName")
				ctVal := attrValue(t.Attr, "ContentType")
				if !strings.HasPrefix(part, "/") || ctVal == "" {
					return nil, herrf("[Content_Types].xml 含不完整 Override 声明",
						"文件结构异常；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.content_types_invalid")
				}
				if _, dup := ct.overrides[part]; dup {
					return nil, herrf("[Content_Types].xml 含重复 Override："+part,
						"文件结构异常；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.content_types_invalid")
				}
				ct.overrides[part] = ctVal
			}
		}
	}
	if !rootSeen {
		return nil, herrf("缺少 [Content_Types].xml 根元素",
			"这不是有效的 PPTX 包；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.content_types_invalid")
	}
	return ct, nil
}

func (c *contentTypes) resolve(partName string) (string, bool) {
	if ct, ok := c.overrides["/"+partName]; ok {
		return ct, true
	}
	base := path.Base(partName)
	if i := strings.LastIndexByte(base, '.'); i >= 0 {
		ext := lowerASCII(base[i+1:])
		if ct, ok := c.defaults[ext]; ok {
			return ct, true
		}
	}
	return "", false
}

// relationship 是一条 OPC 关系。
type relationship struct {
	ID       string
	Type     string
	Target   string
	External bool
	Resolved string
}

func parseRelationships(relsPart string, data []byte) ([]relationship, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	var out []relationship
	seen := map[string]bool{}
	rootSeen := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, wrapXMLParseError(relsPart, err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "Relationships":
				if t.Name.Space != nsRelationships {
					return nil, herrf("关系部件 "+relsPart+" 命名空间不正确",
						"文件结构异常；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.relationship_invalid")
				}
				rootSeen = true
			case "Relationship":
				id := attrValue(t.Attr, "Id")
				relType := attrValue(t.Attr, "Type")
				target := attrValue(t.Attr, "Target")
				mode := attrValue(t.Attr, "TargetMode")
				if id == "" || relType == "" || target == "" {
					return nil, herrf("关系部件 "+relsPart+" 含不完整 Relationship",
						"文件结构异常；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.relationship_invalid")
				}
				if seen[id] {
					return nil, herrf("关系部件 "+relsPart+" 含重复关系 ID "+id,
						"文件结构异常；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.relationship_invalid")
				}
				seen[id] = true
				rel := relationship{ID: id, Type: relType, Target: target, External: mode == "External"}
				if rel.External || hasURIScheme(target) {
					rel.External = true
				} else {
					resolved, err := resolveTarget(relsSourceName(relsPart), target)
					if err != nil {
						return nil, err
					}
					rel.Resolved = resolved
				}
				out = append(out, rel)
			}
		}
	}
	if !rootSeen {
		return nil, herrf("关系部件 "+relsPart+" 缺少根元素",
			"文件结构异常；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.relationship_invalid")
	}
	return out, nil
}

func attrValue(attrs []xml.Attr, local string) string {
	for _, a := range attrs {
		if a.Name.Space == "" && a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}

func hasURIScheme(target string) bool {
	i := strings.Index(target, ":")
	if i <= 0 {
		return false
	}
	for _, r := range target[:i] {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

// resolveTarget 把关系目标解析为包内部件名；越界或非法返回错误。
func resolveTarget(source, target string) (string, error) {
	if strings.Contains(target, "\\") {
		return "", herrf("关系目标含反斜杠："+target, "文件结构异常；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.relationship_invalid")
	}
	var joined string
	if strings.HasPrefix(target, "/") {
		joined = path.Clean(target[1:])
	} else {
		base := path.Dir(source)
		joined = path.Clean(path.Join(base, target))
	}
	if joined == "." || joined == ".." || strings.HasPrefix(joined, "../") || joined == "" {
		return "", herrf("关系目标越出包边界："+target, "文件结构异常；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.relationship_invalid")
	}
	return joined, nil
}

// relsPartName 返回某部件的 .rels 部件名。
func relsPartName(source string) string {
	if source == "" {
		return "_rels/.rels"
	}
	dir := path.Dir(source)
	base := path.Base(source)
	return path.Join(dir, "_rels", base+".rels")
}

// relsSourceName 返回 .rels 部件对应的源部件名（包根为 ""）。
func relsSourceName(relsPart string) string {
	if relsPart == "_rels/.rels" {
		return ""
	}
	if !strings.HasSuffix(relsPart, ".rels") {
		return ""
	}
	dir := path.Dir(relsPart)
	if path.Base(dir) != "_rels" {
		return ""
	}
	parent := path.Dir(dir)
	base := strings.TrimSuffix(path.Base(relsPart), ".rels")
	if base == "" {
		return ""
	}
	return path.Join(parent, base)
}
