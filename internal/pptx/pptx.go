package pptx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"sort"
	"strings"
)

// part 是一个已读取的 OPC 部件。
type part struct {
	name    string
	data    []byte
	ct      string
	orig    *zip.File
	dirty   bool
	removed bool
}

// Document 是一个已打开、可清理与回写的 PPTX 演示文稿。
type Document struct {
	limits   Limits
	original []byte
	entries  map[string]*zip.File

	parts map[string]*part
	order []string
	rels  map[string][]relationship

	ct       *contentTypes
	mainPart string
	strict   bool

	slideOrder []string          // 主文稿声明的幻灯片顺序
	notesOf    map[string]string // 幻灯片 → 备注页
	allSlides  []string          // 其余声明幻灯片（规范化 URI 升序）
	layouts    []string
	masters    []string
	notes      []string
	notesMast  []string
	handouts   []string

	prepared bool
	cleanup  CleanupSummary
	units    []TextUnit
	expected map[int]string

	cleanedSnapshot *StructureSnapshot
	patches         map[string][]patch
}

// Open 读取并校验 PPTX 包。它只做结构与资源校验，不清理、不枚举文字。
func Open(data []byte, limits Limits) (*Document, error) {
	lim := limits.normalize()
	entries, err := openZip(data, lim)
	if err != nil {
		return nil, err
	}
	d := &Document{
		limits:   lim,
		original: data,
		entries:  map[string]*zip.File{},
		parts:    map[string]*part{},
		rels:     map[string][]relationship{},
		notesOf:  map[string]string{},
		patches:  map[string][]patch{},
	}

	var ctPart *zipEntry
	for i := range entries {
		e := &entries[i]
		d.entries[e.name] = e.file
		raw := e.data
		if isXMLPart(e.name) {
			if err := checkXMLDeclaredEncoding(e.name, raw); err != nil {
				return nil, err
			}
			raw = stripUTF8BOM(raw)
		}
		d.parts[e.name] = &part{name: e.name, data: raw, orig: e.file}
		d.order = append(d.order, e.name)
		if e.name == "[Content_Types].xml" {
			ctPart = e
		}
	}
	sort.Strings(d.order)
	if ctPart == nil {
		return nil, herrf("缺少 [Content_Types].xml", "这不是有效的 PPTX 包；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.not_pptx")
	}
	ct, err := parseContentTypes(d.parts["[Content_Types].xml"].data)
	if err != nil {
		return nil, err
	}
	d.ct = ct

	for _, name := range d.order {
		if !strings.HasSuffix(name, ".rels") {
			continue
		}
		rels, err := parseRelationships(name, d.parts[name].data)
		if err != nil {
			return nil, err
		}
		src := relsSourceName(name)
		if _, ok := d.rels[src]; ok {
			return nil, herrf("重复的关系部件："+name,
				"文件结构异常；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.relationship_invalid")
		}
		d.rels[src] = rels
	}

	for _, name := range d.order {
		if name == "[Content_Types].xml" {
			continue
		}
		resolved, ok := ct.resolve(name)
		if !ok {
			return nil, herrf("部件缺少 content type 声明："+name,
				"文件结构异常；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.content_types_invalid")
		}
		d.parts[name].ct = resolved
	}

	rootRels, ok := d.rels[""]
	if !ok {
		return nil, herrf("缺少包根关系部件 _rels/.rels", "这不是有效的 PPTX 包；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.not_pptx")
	}
	var mainCandidates []relationship
	for _, r := range rootRels {
		if r.External {
			continue
		}
		if r.Type == relOfficeDocumentTrans || r.Type == relOfficeDocumentStrict {
			mainCandidates = append(mainCandidates, r)
		}
	}
	if len(mainCandidates) == 0 {
		return nil, herrf("包根关系未声明主演示文稿", "这不是有效的 PowerPoint 演示文稿；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.not_pptx")
	}
	if len(mainCandidates) > 1 {
		return nil, herrf("包根关系声明了多个主演示文稿",
			"文件结构异常；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.relationship_invalid")
	}
	d.mainPart = mainCandidates[0].Resolved
	d.strict = mainCandidates[0].Type == relOfficeDocumentStrict
	if _, ok := d.parts[d.mainPart]; !ok {
		return nil, herrf("主演示文稿部件缺失："+d.mainPart,
			"文件结构损坏；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.missing_part")
	}
	mainCT := d.parts[d.mainPart].ct
	if mainCT == ctPresentationMacro || containsASCII(lowerASCII(mainCT), "macroenabled") {
		return nil, rejectMacro()
	}
	if mainCT != ctPresentation {
		return nil, herrf("主演示文稿 content type 不是 PresentationML："+mainCT,
			"本工具仅支持 .pptx，请在 PowerPoint 中另存为普通 .pptx 后重试", "pptx.not_pptx")
	}

	if err := d.validateMainRoot(); err != nil {
		return nil, err
	}
	if err := d.parseSlideOrder(); err != nil {
		return nil, err
	}
	for _, name := range d.order {
		if d.parts[name].ct != ctSlide {
			continue
		}
		if err := d.resolveNotes(name); err != nil {
			return nil, err
		}
	}
	if err := d.validatePackageGraph(); err != nil {
		return nil, err
	}
	d.classifyParts()
	return d, nil
}

func stripUTF8BOM(data []byte) []byte {
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return data[3:]
	}
	return data
}

// checkXMLDeclaredEncoding 拒绝 UTF-16/UTF-32 等非 UTF-8 XML 字节。
func checkXMLDeclaredEncoding(name string, data []byte) error {
	if len(data) >= 2 {
		if (data[0] == 0xFF && data[1] == 0xFE) || (data[0] == 0xFE && data[1] == 0xFF) {
			return herrf("所编辑 XML 不是 UTF-8："+name,
				"请在 PowerPoint 中另存为普通 UTF-8 .pptx 后重试", "pptx.xml_parse_failed")
		}
	}
	if len(data) >= 4 && data[0] == 0x00 && data[1] == 0x00 && data[2] == 0xFE && data[3] == 0xFF {
		return herrf("所编辑 XML 不是 UTF-8："+name,
			"请在 PowerPoint 中另存为普通 UTF-8 .pptx 后重试", "pptx.xml_parse_failed")
	}
	return nil
}

// validateMainRoot 校验主部件根元素为 PresentationML p:presentation。
func (d *Document) validateMainRoot() error {
	seen := false
	err := d.walkPart(d.parts[d.mainPart], func(ev *event) error {
		if ev.kind != evStart {
			return nil
		}
		if !seen {
			seen = true
			if !isPMLName(ev.name) || ev.name.Local != "presentation" {
				return herrf("主部件根元素不是 PresentationML p:presentation",
					"这不是有效的 .pptx；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.not_pptx")
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !seen {
		return herrf("主演示文稿部件为空", "文件结构损坏；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.not_pptx")
	}
	return nil
}

// parseSlideOrder 从主文稿的 p:sldIdLst 解析声明顺序与关系目标。
func (d *Document) parseSlideOrder() error {
	listSeen := false
	inList := 0
	var ids []string
	err := d.walkPart(d.parts[d.mainPart], func(ev *event) error {
		if ev.kind == evStart && isPMLName(ev.name) && ev.name.Local == "sldIdLst" {
			if listSeen {
				return herrf("主演示文稿含多个幻灯片列表",
					"文件结构异常；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.relationship_invalid")
			}
			listSeen = true
			inList = 1
			return nil
		}
		if ev.kind == evStart && inList == 1 && isPMLName(ev.name) && ev.name.Local == "sldId" {
			id := relAttrValue(ev.attrs)
			if id == "" {
				return herrf("幻灯片列表项缺少关系 ID",
					"文件结构异常；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.relationship_invalid")
			}
			ids = append(ids, id)
		}
		if ev.kind == evEnd && inList == 1 && isPMLName(ev.name) && ev.name.Local == "sldIdLst" {
			inList = 0
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !listSeen {
		return herrf("主演示文稿缺少幻灯片列表（p:sldIdLst）",
			"这不是受支持的 .pptx；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.not_pptx")
	}

	byID := map[string]relationship{}
	for _, r := range d.rels[d.mainPart] {
		byID[r.ID] = r
	}
	seenTarget := map[string]bool{}
	for _, id := range ids {
		r, ok := byID[id]
		if !ok || r.External || r.Resolved == "" {
			return herrf("幻灯片关系 ID 无法解析："+id,
				"文件结构损坏；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.relationship_invalid")
		}
		if r.Type != relSlideTrans && r.Type != relSlideStrict {
			return herrf("幻灯片列表关系类型不正确："+r.Type,
				"文件结构损坏；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.relationship_invalid")
		}
		if _, ok := d.parts[r.Resolved]; !ok {
			return herrf("幻灯片部件缺失："+r.Resolved,
				"文件结构损坏；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.missing_part")
		}
		if seenTarget[r.Resolved] {
			return herrf("幻灯片列表重复引用同一部件："+r.Resolved,
				"文件结构异常；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.relationship_invalid")
		}
		seenTarget[r.Resolved] = true
		d.slideOrder = append(d.slideOrder, r.Resolved)
	}
	for _, slide := range d.slideOrder {
		if err := d.resolveNotes(slide); err != nil {
			return err
		}
	}
	return nil
}

func (d *Document) resolveNotes(slide string) error {
	notes := ""
	for _, r := range d.rels[slide] {
		if r.External || r.Resolved == "" {
			continue
		}
		if r.Type != relNotesSlideTrans && r.Type != relNotesSlideStrict {
			continue
		}
		if notes != "" && notes != r.Resolved {
			return herrf("幻灯片声明了多个备注页："+slide,
				"文件结构异常；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.relationship_invalid")
		}
		notes = r.Resolved
	}
	if notes != "" {
		if _, ok := d.parts[notes]; !ok {
			return herrf("备注页部件缺失："+notes,
				"文件结构损坏；请在 PowerPoint 中另存为新的 .pptx 后重试", "pptx.missing_part")
		}
		d.notesOf[slide] = notes
	}
	return nil
}

func relAttrValue(attrs []xml.Attr) string {
	for _, a := range attrs {
		if a.Name.Space == nsRelTransitional || a.Name.Space == nsRelStrict {
			return a.Value
		}
	}
	return ""
}

// classifyParts 收集其余文字部件类别，统一按规范化 URI 字节升序。
func (d *Document) classifyParts() {
	declaredSlides := map[string]bool{}
	for _, s := range d.slideOrder {
		declaredSlides[s] = true
	}
	usedNotes := map[string]bool{}
	for _, n := range d.notesOf {
		usedNotes[n] = true
	}
	for _, name := range d.order {
		p := d.parts[name]
		if p.removed {
			continue
		}
		switch p.ct {
		case ctSlide:
			if !declaredSlides[name] {
				d.allSlides = append(d.allSlides, name)
			}
		case ctSlideLayout:
			d.layouts = append(d.layouts, name)
		case ctSlideMaster:
			d.masters = append(d.masters, name)
		case ctNotesSlide:
			if !usedNotes[name] {
				d.notes = append(d.notes, name)
			}
		case ctNotesMaster:
			d.notesMast = append(d.notesMast, name)
		case ctHandoutMaster:
			d.handouts = append(d.handouts, name)
		}
	}
	// d.order 已按规范化 URI 排序。
}

// validatePackageGraph 校验缺失内部目标、宏、数字签名与未知批注家族。
func (d *Document) validatePackageGraph() error {
	for _, rels := range d.rels {
		for _, r := range rels {
			if r.External {
				continue
			}
			if _, ok := d.parts[r.Resolved]; !ok {
				return herrf("内部关系目标缺失："+r.Resolved,
					"文件结构损坏；请在 PowerPoint 中打开并另存为新的 .pptx 后重试", "pptx.relationship_invalid")
			}
		}
	}
	for _, name := range d.order {
		part := d.parts[name]
		lct := lowerASCII(part.ct)
		if part.ct == ctVbaProject || part.ct == ctDigitalSigOrig || part.ct == ctDigitalSigXML ||
			containsASCII(lct, "vba") || containsASCII(lct, "macroenabled") ||
			strings.HasPrefix(name, "_xmlsignatures/") || strings.HasSuffix(lowerASCII(name), "vbaproject.bin") {
			if strings.Contains(lct, "vba") || strings.HasSuffix(lowerASCII(name), "vbaproject.bin") || containsASCII(lct, "macroenabled") {
				return rejectMacro()
			}
			return rejectSignature()
		}
		if looksLikeUnknownCommentContentType(part.ct) {
			return rejectUnknownComment("content type " + part.ct)
		}
		// 现代批注锚点以内容标记保存在批注部件自身；Slide/Master/Layout 中出现
		// 2018 批注命名空间元素时无法证明可安全删除，按未知批注家族拒绝。
		if isSlideTextPart(part.ct) && part.data != nil {
			if err := d.checkModernAnchorMarkup(part); err != nil {
				return err
			}
		}
	}
	for _, rels := range d.rels {
		for _, r := range rels {
			if looksLikeUnknownCommentRel(r.Type) {
				return rejectUnknownComment("relationship type " + r.Type)
			}
		}
	}
	return nil
}

func isSlideTextPart(ct string) bool {
	switch ct {
	case ctSlide, ctSlideLayout, ctSlideMaster, ctNotesSlide, ctNotesMaster, ctHandoutMaster:
		return true
	}
	return false
}

// checkModernAnchorMarkup 允许可清理的现代批注锚点 p188:commentRel，
// 但拒绝其他 2018/8 批注命名空间元素，避免无法证明可安全删除时静默保留。
func (d *Document) checkModernAnchorMarkup(p *part) error {
	var found string
	err := d.walkPart(p, func(ev *event) error {
		if found != "" || ev.kind != evStart || ev.name.Space != nsP188 {
			return nil
		}
		if ev.name.Local != "commentRel" {
			found = ev.name.Local
		}
		return nil
	})
	if err != nil {
		return err
	}
	if found != "" {
		return rejectUnknownComment("slide anchor " + found)
	}
	return nil
}

// MainPart 返回主演示文稿部件名。
func (d *Document) MainPart() string { return d.mainPart }

// Strict 报告是否使用 Strict 关系命名空间。
func (d *Document) Strict() bool { return d.strict }

// PartNames 返回排序后的部件名副本。
func (d *Document) PartNames() []string {
	out := make([]string, len(d.order))
	copy(out, d.order)
	return out
}

// HasPart 报告部件是否存在且未被移除。
func (d *Document) HasPart(name string) bool {
	p, ok := d.parts[name]
	return ok && !p.removed
}

// PartBytes 返回部件当前字节（重写后）。
func (d *Document) PartBytes(name string) ([]byte, bool) {
	p, ok := d.parts[name]
	if !ok || p.removed {
		return nil, false
	}
	return p.data, true
}

// CleanupSummary 是本次 PPTX 批注清理摘要（不含原文）。
type CleanupSummary struct {
	CommentsDeleted    int `json:"comments_deleted"`
	PeoplePartsRemoved int `json:"people_parts_removed"`
	PartsRemoved       int `json:"parts_removed"`
	ResourcesRemoved   int `json:"resources_removed"`
}

// Cleanup 返回清理摘要。
func (d *Document) Cleanup() CleanupSummary { return d.cleanup }

func lowerASCII(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

func containsASCII(s, sub string) bool { return strings.Contains(s, sub) }

func prefixOf(name string) string {
	if i := strings.IndexByte(name, ':'); i >= 0 {
		return name[:i]
	}
	return ""
}

func bytesEqual(a, b []byte) bool { return bytes.Equal(a, b) }
