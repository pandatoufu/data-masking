package pptx

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

type eventKind int

const (
	evStart eventKind = iota
	evEnd
	evChar
	evComment
	evProcInst
	evDirective
)

// event 是遍历一个 XML 部件时产生的一个事件，带原始字节范围与作用域信息。
type event struct {
	kind      eventKind
	name      xml.Name
	parent    xml.Name
	attrs     []xml.Attr
	text      string
	start     int
	end       int
	depth     int
	stack     []xml.Name
	selfClose bool
}

// walkPart 以严格模式遍历部件，维护命名空间绑定栈、校验未声明/保留前缀与嵌套深度。
func (d *Document) walkPart(p *part, fn func(*event) error) error {
	if p.data == nil {
		return herrf("内部错误：部件未读取", "", "pptx.internal")
	}
	dec := xml.NewDecoder(bytes.NewReader(p.data))
	dec.Strict = true

	depth := 0
	var stack []xml.Name
	bindings := newBindings()

	for {
		prev := int(dec.InputOffset())
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return wrapXMLParseError(p.name, err)
		}
		after := int(dec.InputOffset())
		raw := p.data[prev:after]

		switch t := tok.(type) {
		case xml.StartElement:
			name, attrNames, selfClose, perr := parseRawStartTag(raw)
			if perr != nil {
				return herrf("PPTX 部件 "+p.name+" 的 XML 标签异常："+perr.Error(),
					"文件结构损坏；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.xml_parse_failed")
			}
			scope := bindings.push(t.Attr)
			if err := validateDeclarations(p.name, scope); err != nil {
				return err
			}
			if err := validatePrefixes(p.name, name, attrNames, scope); err != nil {
				return err
			}
			depth++
			if depth > d.limits.MaxDepth {
				return herrf("PPTX XML 嵌套深度超过上限",
					"演示文稿结构过于复杂；请在 PowerPoint 中精简后另存为新的 .pptx 再试", "pptx.zip_budget")
			}
			stack = append(stack, t.Name)
			ev := &event{
				kind: evStart, name: t.Name, attrs: t.Attr, start: prev, end: after,
				depth: depth, stack: append([]xml.Name(nil), stack...), selfClose: selfClose,
			}
			if err := fn(ev); err != nil {
				return err
			}
		case xml.EndElement:
			ev := &event{
				kind: evEnd, name: t.Name, start: prev, end: after, depth: depth,
				stack: append([]xml.Name(nil), stack...),
			}
			if err := fn(ev); err != nil {
				return err
			}
			if depth > 0 {
				depth--
			}
			if len(stack) > 0 {
				var parent xml.Name
				if len(stack) >= 2 {
					parent = stack[len(stack)-2]
				}
				ev.parent = parent
				stack = stack[:len(stack)-1]
			}
			bindings.pop()
		case xml.CharData:
			var parent xml.Name
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			ev := &event{
				kind: evChar, parent: parent, text: string(t), start: prev, end: after,
				depth: depth, stack: append([]xml.Name(nil), stack...),
			}
			if err := fn(ev); err != nil {
				return err
			}
		case xml.Comment:
			ev := &event{kind: evComment, text: string(t), start: prev, end: after, depth: depth}
			if err := fn(ev); err != nil {
				return err
			}
		case xml.ProcInst:
			ev := &event{kind: evProcInst, start: prev, end: after, depth: depth}
			if err := fn(ev); err != nil {
				return err
			}
		case xml.Directive:
			ev := &event{kind: evDirective, start: prev, end: after, depth: depth}
			if err := fn(ev); err != nil {
				return err
			}
		}
	}
	if depth != 0 {
		return herrf("PPTX 部件 "+p.name+" 的 XML 标签未闭合",
			"文件结构损坏；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.xml_parse_failed")
	}
	return nil
}

func wrapXMLParseError(part string, err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "CharsetReader is nil"):
		return herrf("PPTX 部件 "+part+" 使用了非 UTF-8 编码的 XML",
			"所编辑 XML 必须是 UTF-8；请在 PowerPoint 中另存为普通 .pptx 后重试", "pptx.xml_parse_failed")
	case strings.Contains(msg, "invalid UTF-8"):
		return herrf("PPTX 部件 "+part+" 含非法 UTF-8 字节",
			"文件内容损坏或编码不符；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.xml_parse_failed")
	default:
		return herrf("PPTX 部件 "+part+" 的 XML 解析失败："+msg,
			"文件结构损坏；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.xml_parse_failed")
	}
}

// bindings 是命名空间绑定栈，支持局部重绑定。
type bindings struct {
	stack []map[string]string
}

func newBindings() *bindings {
	return &bindings{stack: []map[string]string{{
		"xml": nsXML,
		"":    "",
	}}}
}

func (b *bindings) push(attrs []xml.Attr) map[string]string {
	top := b.stack[len(b.stack)-1]
	next := make(map[string]string, len(top)+2)
	for k, v := range top {
		next[k] = v
	}
	for _, a := range attrs {
		if a.Name.Space == "xmlns" {
			next[a.Name.Local] = a.Value
		} else if a.Name.Space == "" && a.Name.Local == "xmlns" {
			next[""] = a.Value
		}
	}
	b.stack = append(b.stack, next)
	return next
}

func (b *bindings) pop() {
	if len(b.stack) > 1 {
		b.stack = b.stack[:len(b.stack)-1]
	}
}

// validateDeclarations 校验保留前缀声明：xml 必须绑定到 XML 命名空间，xmlns 不可被声明。
func validateDeclarations(part string, scope map[string]string) error {
	if _, ok := scope["xmlns"]; ok {
		return herrf("PPTX 部件 "+part+" 非法声明保留前缀 xmlns",
			"文件结构异常；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.xml_parse_failed")
	}
	if uri := scope["xml"]; uri != nsXML {
		return herrf("PPTX 部件 "+part+" 非法重绑定保留前缀 xml",
			"文件结构异常；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.xml_parse_failed")
	}
	return nil
}

// validatePrefixes 校验元素名与属性名的前缀已绑定，并拒绝保留前缀误用。
func validatePrefixes(part, elem string, attrNames []string, scope map[string]string) error {
	if err := checkPrefix(part, elem, scope); err != nil {
		return err
	}
	for _, a := range attrNames {
		if a == "xmlns" || strings.HasPrefix(a, "xmlns:") {
			if strings.HasPrefix(a, "xmlns:") && a[len("xmlns:"):] == "xmlns" {
				return herrf("PPTX 部件 "+part+" 非法声明保留前缀 xmlns:xmlns",
					"文件结构异常；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.xml_parse_failed")
			}
			continue
		}
		if err := checkPrefix(part, a, scope); err != nil {
			return err
		}
	}
	return nil
}

func checkPrefix(part, qualified string, scope map[string]string) error {
	prefix := prefixOf(qualified)
	if prefix == "" {
		return nil
	}
	if prefix == "xmlns" {
		return herrf("PPTX 部件 "+part+" 非法使用保留前缀 xmlns",
			"文件结构异常；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.xml_parse_failed")
	}
	if prefix == "xml" {
		return nil
	}
	if _, ok := scope[prefix]; !ok {
		return herrf(fmt.Sprintf("PPTX 部件 %s 使用了未声明的命名空间前缀 %q", part, prefix),
			"文件命名空间不完整；请在 PowerPoint 中打开并另存为新的 .pptx 后重试", "pptx.xml_parse_failed")
	}
	return nil
}

// parseRawStartTag 从原始起始标签中提取限定元素名、属性名与是否自闭合。
func parseRawStartTag(raw []byte) (name string, attrNames []string, selfClose bool, err error) {
	i := 0
	if i >= len(raw) || raw[i] != '<' {
		return "", nil, false, fmt.Errorf("标签不以 < 开头")
	}
	i++
	if i < len(raw) && raw[i] == '/' {
		return "", nil, false, fmt.Errorf("意外的结束标签")
	}
	start := i
	for i < len(raw) && !isXMLSpace(raw[i]) && raw[i] != '>' && raw[i] != '/' {
		i++
	}
	name = string(raw[start:i])
	if name == "" {
		return "", nil, false, fmt.Errorf("缺少元素名")
	}
	for i < len(raw) {
		for i < len(raw) && isXMLSpace(raw[i]) {
			i++
		}
		if i >= len(raw) {
			break
		}
		if raw[i] == '>' {
			return name, attrNames, false, nil
		}
		if raw[i] == '/' {
			if i+1 < len(raw) && raw[i+1] == '>' {
				return name, attrNames, true, nil
			}
			return "", nil, false, fmt.Errorf("非法的 / 位置")
		}
		as := i
		for i < len(raw) && !isXMLSpace(raw[i]) && raw[i] != '=' && raw[i] != '>' && raw[i] != '/' {
			i++
		}
		attr := string(raw[as:i])
		if attr == "" {
			return "", nil, false, fmt.Errorf("缺少属性名")
		}
		attrNames = append(attrNames, attr)
		for i < len(raw) && isXMLSpace(raw[i]) {
			i++
		}
		if i >= len(raw) || raw[i] != '=' {
			return "", nil, false, fmt.Errorf("属性 %s 缺少 =", attr)
		}
		i++
		for i < len(raw) && isXMLSpace(raw[i]) {
			i++
		}
		if i >= len(raw) || (raw[i] != '"' && raw[i] != '\'') {
			return "", nil, false, fmt.Errorf("属性 %s 缺少引号", attr)
		}
		q := raw[i]
		i++
		for i < len(raw) && raw[i] != q {
			i++
		}
		if i >= len(raw) {
			return "", nil, false, fmt.Errorf("属性 %s 引号未闭合", attr)
		}
		i++
	}
	return name, attrNames, false, fmt.Errorf("标签未闭合")
}

func isXMLSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n'
}
