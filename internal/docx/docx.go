package docx

import (
	"archive/zip"
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

// Document 是一个已打开、可清理与回写的 DOCX 文档。
type Document struct {
	limits   Limits
	original []byte
	entries  map[string]*zip.File

	parts map[string]*part
	order []string
	rels  map[string][]relationship // 源部件名 → 关系；包根用 ""

	ct       *contentTypes
	mainPart string
	strict   bool

	prepared bool
	cleanup  CleanupSummary
	units    []TextUnit
	expected map[int]string

	cleanedSnapshot *StructureSnapshot
	cleanedImages   map[string]string
	imageNames      []string

	patches map[string][]patch
}

// Open 读取并校验 DOCX 包。它只做结构与资源校验，不清理、不枚举文字。
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
		patches:  map[string][]patch{},
	}

	var ctPart *zipEntry
	for i := range entries {
		e := &entries[i]
		d.entries[e.name] = e.file
		part := &part{name: e.name, data: e.data, orig: e.file}
		if e.name == "[Content_Types].xml" {
			ctPart = e
		}
		if isXMLPart(e.name) {
			if err := checkXMLDeclaredEncoding(e.name, e.data); err != nil {
				return nil, err
			}
		}
		d.parts[e.name] = part
		d.order = append(d.order, e.name)
	}
	sort.Strings(d.order)
	if ctPart == nil {
		return nil, herrf("缺少 [Content_Types].xml", "这不是有效的 DOCX 包；请在 Word 中另存为新的 .docx 后重试", "docx.not_docx")
	}
	ct, err := parseContentTypes(ctPart.data)
	if err != nil {
		return nil, err
	}
	d.ct = ct

	// 解析所有 .rels 部件。
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
			return nil, herrf("重复的关系部件："+name, "文件结构异常；请在 Word 中另存为新的 .docx 后重试", "docx.relationship_invalid")
		}
		d.rels[src] = rels
	}

	// 解析每个部件的 content type，做宏/签名/未知批注家族门禁。
	for _, name := range d.order {
		if name == "[Content_Types].xml" {
			continue
		}
		resolved, ok := ct.resolve(name)
		if !ok {
			return nil, herrf("部件缺少 content type 声明："+name,
				"文件结构异常；请在 Word 中另存为新的 .docx 后重试", "docx.content_types_invalid")
		}
		d.parts[name].ct = resolved
	}

	rootRels, ok := d.rels[""]
	if !ok {
		return nil, herrf("缺少包根关系部件 _rels/.rels", "这不是有效的 DOCX 包；请在 Word 中另存为新的 .docx 后重试", "docx.not_docx")
	}
	var mainCandidates []relationship
	mainRelType := ""
	for _, r := range rootRels {
		if r.External {
			continue
		}
		if isOfficeDocumentRel(r.Type) {
			mainCandidates = append(mainCandidates, r)
			mainRelType = r.Type
		}
	}
	if len(mainCandidates) == 0 {
		return nil, herrf("包根关系未声明主文档", "这不是有效的 Word 文档；请在 Word 中另存为新的 .docx 后重试", "docx.not_docx")
	}
	if len(mainCandidates) > 1 {
		return nil, herrf("包根关系声明了多个主文档", "文件结构异常；请在 Word 中另存为新的 .docx 后重试", "docx.relationship_invalid")
	}
	d.mainPart = mainCandidates[0].Resolved
	d.strict = strings.HasPrefix(mainRelType, nsRelStrict)
	if _, ok := d.parts[d.mainPart]; !ok {
		return nil, herrf("主文档部件缺失："+d.mainPart, "文件结构损坏；请在 Word 中另存为新的 .docx 后重试", "docx.missing_part")
	}
	mainCT := d.parts[d.mainPart].ct
	if mainCT == ctMainMacro || containsASCII(lowerASCII(mainCT), "macroenabled") {
		return nil, rejectMacro()
	}
	if mainCT != ctMain {
		return nil, herrf("主文档 content type 不是 WordprocessingML： "+mainCT,
			"本工具仅支持 .docx，请在 Word 中另存为普通 .docx 后重试", "docx.not_docx")
	}

	if err := d.validatePackageGraph(); err != nil {
		return nil, err
	}
	return d, nil
}

func isOfficeDocumentRel(t string) bool {
	return t == nsRelTransitional+"/officeDocument" || t == nsRelStrict+"/officeDocument"
}

// validatePackageGraph 校验缺失内部目标、宏、数字签名与未知批注家族。
func (d *Document) validatePackageGraph() error {
	for src, rels := range d.rels {
		_ = src
		for _, r := range rels {
			if r.External {
				continue
			}
			if _, ok := d.parts[r.Resolved]; !ok {
				return herrf("内部关系目标缺失："+r.Resolved,
					"文件结构损坏；请在 Word 中打开并另存为新的 .docx 后重试", "docx.relationship_invalid")
			}
		}
	}
	for _, name := range d.order {
		part := d.parts[name]
		lct := lowerASCII(part.ct)
		if part.ct == ctVbaProject || part.ct == ctDigitalSigOrigin || part.ct == ctDigitalSigXML ||
			containsASCII(lct, "vba") || containsASCII(lct, "macroenabled") ||
			strings.HasPrefix(name, "_xmlsignatures/") || strings.HasSuffix(lowerASCII(name), "vbaproject.bin") {
			if strings.Contains(lowerASCII(part.ct), "vba") || strings.HasSuffix(lowerASCII(name), "vbaproject.bin") || containsASCII(lct, "macroenabled") {
				return rejectMacro()
			}
			return rejectSignature()
		}
		if looksLikeCommentContentType(part.ct) {
			return rejectUnknownComment("content type " + part.ct)
		}
	}
	for _, rels := range d.rels {
		for _, r := range rels {
			if looksLikeCommentRelationship(r.Type) && !isKnownCommentRel(r.Type) {
				return rejectUnknownComment("relationship type " + r.Type)
			}
		}
	}
	return nil
}

func isKnownCommentRel(t string) bool {
	for _, f := range commentFamilies {
		if f.RelTypes[t] {
			return true
		}
	}
	return false
}

func rejectMacro() error {
	return herrf("不支持宏文档（.docm / vbaProject）",
		"请在 Word 中另存为不含宏的普通 .docx 后再试", "docx.macro_unsupported")
}

func rejectSignature() error {
	return herrf("不支持含数字签名的文档包",
		"请在 Word 中移除数字签名并另存为普通 .docx 后再试", "docx.digital_signature_unsupported")
}

func rejectUnknownComment(what string) error {
	return herrfParams("docx.comment_family_unsupported", map[string]any{"what": what},
		"检测到不支持的批注家族扩展（"+what+"）",
		"请先在 Word 中删除全部批注并另存为新的 .docx 后再试")
}

// checkXMLDeclaredEncoding 拒绝 UTF-16/UTF-32 等非 UTF-8 XML 字节。
func checkXMLDeclaredEncoding(name string, data []byte) error {
	if len(data) >= 2 {
		if (data[0] == 0xFF && data[1] == 0xFE) || (data[0] == 0xFE && data[1] == 0xFF) {
			return herrf("所编辑 XML 不是 UTF-8："+name,
				"请在 Word 中另存为普通 UTF-8 .docx 后重试", "docx.xml_parse_failed")
		}
	}
	if len(data) >= 4 && data[0] == 0x00 && data[1] == 0x00 && data[2] == 0xFE && data[3] == 0xFF {
		return herrf("所编辑 XML 不是 UTF-8："+name,
			"请在 Word 中另存为普通 UTF-8 .docx 后重试", "docx.xml_parse_failed")
	}
	return nil
}

// MainPart 返回主文档部件名。
func (d *Document) MainPart() string { return d.mainPart }

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

// CleanupSummary 是本次批注/修订清理摘要（不含原文）。
type CleanupSummary struct {
	CommentsDeleted   int `json:"comments_deleted"`
	RevisionsAccepted int `json:"revisions_accepted"`
	PartsRemoved      int `json:"parts_removed"`
	ResourcesRemoved  int `json:"resources_removed"`
}

// Cleanup 返回清理摘要。
func (d *Document) Cleanup() CleanupSummary { return d.cleanup }
