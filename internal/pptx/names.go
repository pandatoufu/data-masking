// Package pptx 实现本地 PPTX 保格式脱敏适配层：
// 有界 OPC 包读取、PresentationML 关系驱动文字枚举、批注家族清理、
// 跨 run 文字原位回写、确定性打包与产物重开验证。
//
// 本包只理解容器、结构和原生文字，不包含掩码规则；规则由调用方（core）注入。
// 所有拒绝都返回带中文说明与“下一步做法”的 *loader.HumanError。
package pptx

import (
	"encoding/xml"
	"strings"

	"github.com/pandatoufu/data-masking/internal/loader"
)

// PresentationML / DrawingML 命名空间（Transitional 与 Strict）。
const (
	nsPMLTransitional = "http://schemas.openxmlformats.org/presentationml/2006/main"
	nsPMLStrict       = "http://purl.oclc.org/ooxml/presentationml/main"
	nsDrawingTrans    = "http://schemas.openxmlformats.org/drawingml/2006/main"
	nsDrawingStrict   = "http://purl.oclc.org/ooxml/drawingml/main"
)

// 关系命名空间（Transitional 与 Strict）。
const (
	nsRelTransitional = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	nsRelStrict       = "http://purl.oclc.org/ooxml/officeDocument/relationships"
)

// OPC 包级命名空间。
const (
	nsContentTypes  = "http://schemas.openxmlformats.org/package/2006/content-types"
	nsRelationships = "http://schemas.openxmlformats.org/package/2006/relationships"
	nsXML           = "http://www.w3.org/XML/1998/namespace"
)

// Markup Compatibility 命名空间（mc:AlternateContent/Choice/Fallback）。
const nsMC = "http://schemas.openxmlformats.org/markup-compatibility/2006"

// 现代批注（Office 2018/2021）根命名空间，依据 [MS-PPTX] 2.1.5/2.1.6。
const nsP188 = "http://schemas.microsoft.com/office/powerpoint/2018/8/main"

// 主演示文稿 content type。
const ctPresentation = "application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"

// 宏主演示文稿 content type（明确拒绝）。
const ctPresentationMacro = "application/vnd.ms-powerpoint.presentation.macroEnabled.main+xml"

// 已知 PresentationML 部件 content type（ECMA-376 / ISO/IEC 29500）。
const (
	ctSlide          = "application/vnd.openxmlformats-officedocument.presentationml.slide+xml"
	ctSlideLayout    = "application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"
	ctSlideMaster    = "application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"
	ctNotesSlide     = "application/vnd.openxmlformats-officedocument.presentationml.notesSlide+xml"
	ctNotesMaster    = "application/vnd.openxmlformats-officedocument.presentationml.notesMaster+xml"
	ctHandoutMaster  = "application/vnd.openxmlformats-officedocument.presentationml.handoutMaster+xml"
	ctComments       = "application/vnd.openxmlformats-officedocument.presentationml.comments+xml"
	ctCommentAuthors = "application/vnd.openxmlformats-officedocument.presentationml.commentAuthors+xml"
	ctModernComments = "application/vnd.ms-powerpoint.comments+xml"
	ctModernAuthors  = "application/vnd.ms-powerpoint.authors+xml"
	ctVbaProject     = "application/vnd.ms-office.vbaProject"
	ctDigitalSigOrig = "application/vnd.openxmlformats-package.digital-signature-origin"
	ctDigitalSigXML  = "application/vnd.openxmlformats-package.digital-signature-xmlsignature+xml"
)

// 关系类型（经典/现代批注家族）。
const (
	relOfficeDocumentTrans  = nsRelTransitional + "/officeDocument"
	relOfficeDocumentStrict = nsRelStrict + "/officeDocument"
	relSlideTrans           = nsRelTransitional + "/slide"
	relSlideStrict          = nsRelStrict + "/slide"
	relNotesSlideTrans      = nsRelTransitional + "/notesSlide"
	relNotesSlideStrict     = nsRelStrict + "/notesSlide"
	relCommentsTrans        = nsRelTransitional + "/comments"
	relCommentsStrict       = nsRelStrict + "/comments"
	relCommentAuthorsTrans  = nsRelTransitional + "/commentAuthors"
	relCommentAuthorsStrict = nsRelStrict + "/commentAuthors"
	relModernComments       = "http://schemas.microsoft.com/office/2018/10/relationships/comments"
	relModernAuthors        = "http://schemas.microsoft.com/office/2018/10/relationships/authors"
)

// commentFamily 描述一个受支持的批注家族。
// URI 依据 ECMA-376（经典）与 [MS-PPTX] 2.1.5/2.1.6（现代 2018 家族）。
type commentFamily struct {
	Name        string
	RelTypes    map[string]bool
	ContentType string
	RootLocal   string
	AuthorPart  bool
}

var commentFamilies = []commentFamily{
	{
		Name:        "legacyComments",
		RelTypes:    map[string]bool{relCommentsTrans: true, relCommentsStrict: true},
		ContentType: ctComments,
		RootLocal:   "cmLst",
	},
	{
		Name:        "legacyCommentAuthors",
		RelTypes:    map[string]bool{relCommentAuthorsTrans: true, relCommentAuthorsStrict: true},
		ContentType: ctCommentAuthors,
		RootLocal:   "cmAuthorLst",
		AuthorPart:  true,
	},
	{
		Name:        "modernComments",
		RelTypes:    map[string]bool{relModernComments: true},
		ContentType: ctModernComments,
		RootLocal:   "cmLst",
	},
	{
		Name:        "modernAuthors",
		RelTypes:    map[string]bool{relModernAuthors: true},
		ContentType: ctModernAuthors,
		RootLocal:   "authorLst",
		AuthorPart:  true,
	},
}

func isKnownCommentContentType(ct string) bool {
	for _, f := range commentFamilies {
		if f.ContentType == ct {
			return true
		}
	}
	return false
}

func isCommentAuthorContentType(ct string) bool {
	for _, f := range commentFamilies {
		if f.ContentType == ct && f.AuthorPart {
			return true
		}
	}
	return false
}

func isKnownCommentRel(t string) bool {
	for _, f := range commentFamilies {
		if f.RelTypes[t] {
			return true
		}
	}
	return false
}

// looksLikeUnknownCommentContentType 报告 content type 是否“像”批注/人员家族但不在已知表内。
func looksLikeUnknownCommentContentType(ct string) bool {
	if isKnownCommentContentType(ct) {
		return false
	}
	l := lowerASCII(ct)
	return containsASCII(l, "comment") || stringsHasSuffix(l, ".authors+xml")
}

// looksLikeUnknownCommentRel 报告关系 type 是否“像”批注/人员家族但不在已知表内。
func looksLikeUnknownCommentRel(t string) bool {
	if isKnownCommentRel(t) {
		return false
	}
	l := lowerASCII(t)
	return containsASCII(l, "comment") || stringsHasSuffix(l, "/authors") || containsASCII(l, "people")
}

func isPMLName(n xml.Name) bool {
	return n.Space == nsPMLTransitional || n.Space == nsPMLStrict
}

func isDrawingName(n xml.Name) bool {
	return n.Space == nsDrawingTrans || n.Space == nsDrawingStrict
}

func stringsHasSuffix(s, suffix string) bool { return strings.HasSuffix(s, suffix) }

// herrf 构造 HumanError，可选传入稳定 message_id。
func herrf(reason, advice string, id ...string) *loader.HumanError {
	h := &loader.HumanError{Reason: reason, Advice: advice}
	if len(id) > 0 {
		h.MessageID = id[0]
	}
	return h
}

func herrfParams(id string, params map[string]any, reason, advice string) *loader.HumanError {
	return &loader.HumanError{Reason: reason, Advice: advice, MessageID: id, Params: params}
}

func rejectMacro() error {
	return herrf("不支持宏演示文稿（.pptm / vbaProject）",
		"请在 PowerPoint 中另存为不含宏的普通 .pptx 后再试", "pptx.macro_unsupported")
}

func rejectSignature() error {
	return herrf("不支持含数字签名的演示文稿包",
		"请在 PowerPoint 中移除数字签名并另存为普通 .pptx 后再试", "pptx.digital_signature_unsupported")
}

func rejectUnknownComment(what string) error {
	return herrfParams("pptx.comment_family_unsupported", map[string]any{"what": what},
		"检测到不支持的批注家族扩展（"+what+"）",
		"请先在 PowerPoint 中删除全部批注并另存为新的 .pptx 后再试")
}
