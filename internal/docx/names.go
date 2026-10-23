// Package docx 实现本地 DOCX 保格式脱敏适配层：
// 有界包读取、OPC 校验、保留原始 XML 的文字索引、批注/修订清理、
// 跨 run 文字替换回写、确定性打包与产物重开验证。
//
// 本包只理解容器、结构和原生文字，不包含掩码规则；规则由调用方（core）注入。
// 所有拒绝都返回带中文说明与“下一步做法”的 *loader.HumanError。
package docx

import (
	"github.com/pandatoufu/data-masking/internal/loader"
)

// WordprocessingML 命名空间（Transitional / Strict）。
const (
	nsWMLTransitional = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	nsWMLStrict       = "http://purl.oclc.org/ooxml/wordprocessingml/main"
)

// 关系命名空间（Transitional / Strict）。
const (
	nsRelTransitional = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	nsRelStrict       = "http://purl.oclc.org/ooxml/officeDocument/relationships"
)

// OPC 包级命名空间。
const (
	nsContentTypes  = "http://schemas.openxmlformats.org/package/2006/content-types"
	nsRelationships = "http://schemas.openxmlformats.org/package/2006/relationships"
	nsXML           = "http://www.w3.org/XML/1998/namespace"
	nsXMLNS         = "http://www.w3.org/2000/xmlns/"
)

// Markup Compatibility 命名空间（mc:AlternateContent/Choice/Fallback）。
const nsMC = "http://schemas.openxmlformats.org/markup-compatibility/2006"

// 主文档 content type（Transitional 与 Strict 共用）。
const ctMain = "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"

// 宏主文档 content type（明确拒绝）。
const ctMainMacro = "application/vnd.ms-word.document.macroEnabled.main+xml"

// 已知 WordprocessingML 部件 content type。
const (
	ctHeader             = "application/vnd.openxmlformats-officedocument.wordprocessingml.header+xml"
	ctFooter             = "application/vnd.openxmlformats-officedocument.wordprocessingml.footer+xml"
	ctFootnotes          = "application/vnd.openxmlformats-officedocument.wordprocessingml.footnotes+xml"
	ctEndnotes           = "application/vnd.openxmlformats-officedocument.wordprocessingml.endnotes+xml"
	ctComments           = "application/vnd.openxmlformats-officedocument.wordprocessingml.comments+xml"
	ctCommentsExtended   = "application/vnd.openxmlformats-officedocument.wordprocessingml.commentsExtended+xml"
	ctCommentsIds        = "application/vnd.openxmlformats-officedocument.wordprocessingml.commentsIds+xml"
	ctCommentsExtensible = "application/vnd.openxmlformats-officedocument.wordprocessingml.commentsExtensible+xml"
	ctPeople             = "application/vnd.openxmlformats-officedocument.wordprocessingml.people+xml"
	ctStyles             = "application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"
	ctNumbering          = "application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml"
	ctSettings           = "application/vnd.openxmlformats-officedocument.wordprocessingml.settings+xml"
	ctWebSettings        = "application/vnd.openxmlformats-officedocument.wordprocessingml.webSettings+xml"
	ctFontTable          = "application/vnd.openxmlformats-officedocument.wordprocessingml.fontTable+xml"
	ctTheme              = "application/vnd.openxmlformats-officedocument.theme+xml"
	ctVbaProject         = "application/vnd.ms-office.vbaProject"
	ctDigitalSigOrigin   = "application/vnd.openxmlformats-package.digital-signature-origin"
	ctDigitalSigXML      = "application/vnd.openxmlformats-package.digital-signature-xmlsignature+xml"
	ctRelationships      = "application/vnd.openxmlformats-package.relationships+xml"
)

// wmlContentTypes 是会被检查修订/批注锚点的 WordprocessingML 部件 content type。
var wmlContentTypes = map[string]bool{
	ctMain:               true,
	ctHeader:             true,
	ctFooter:             true,
	ctFootnotes:          true,
	ctEndnotes:           true,
	ctComments:           true,
	ctCommentsExtended:   true,
	ctCommentsIds:        true,
	ctCommentsExtensible: true,
	ctPeople:             true,
	ctStyles:             true,
	ctNumbering:          true,
	ctSettings:           true,
	ctWebSettings:        true,
	ctFontTable:          true,
}

// commentFamily 描述一个批注家族（关系 type + content type）。
type commentFamily struct {
	Name        string
	RelTypes    map[string]bool
	ContentType string
	// rootParts 为该家族部件根元素 local name（用于识别非固定文件名部件）。
	RootLocal string
}

// commentFamilies 是五类批注家族的完整 URI 表。
// 关系 URI 与 content type 依据 ECMA-376 及 [MS-OI29500]；
// 经典 comments 为 Transitional/Strict 标准，其余为 Microsoft 扩展。
var commentFamilies = []commentFamily{
	{
		Name:        "comments",
		RelTypes:    map[string]bool{nsRelTransitional + "/comments": true, nsRelStrict + "/comments": true},
		ContentType: ctComments,
		RootLocal:   "comments",
	},
	{
		Name:        "commentsExtended",
		RelTypes:    map[string]bool{"http://schemas.microsoft.com/office/2011/relationships/commentsExtended": true},
		ContentType: ctCommentsExtended,
		RootLocal:   "commentsEx",
	},
	{
		Name:        "commentsIds",
		RelTypes:    map[string]bool{"http://schemas.microsoft.com/office/2016/09/relationships/commentsIds": true},
		ContentType: ctCommentsIds,
		RootLocal:   "commentsIds",
	},
	{
		Name:        "commentsExtensible",
		RelTypes:    map[string]bool{"http://schemas.microsoft.com/office/2018/08/relationships/commentsExtensible": true},
		ContentType: ctCommentsExtensible,
		RootLocal:   "commentsExtensible",
	},
	{
		Name:        "people",
		RelTypes:    map[string]bool{"http://schemas.microsoft.com/office/2011/relationships/people": true},
		ContentType: ctPeople,
		RootLocal:   "people",
	},
}

// isCommentFamilyContentType 报告 content type 是否属于已知批注家族。
func isCommentFamilyContentType(ct string) bool {
	for _, f := range commentFamilies {
		if f.ContentType == ct {
			return true
		}
	}
	return false
}

// looksLikeCommentRelationship 报告关系 type 是否“像”批注家族但不在已知表内（未知家族）。
func looksLikeCommentRelationship(relType string) bool {
	l := lowerASCII(relType)
	return containsASCII(l, "comment") || containsASCII(l, "people")
}

// looksLikeCommentContentType 报告 content type 是否“像”批注家族但不在已知表内。
func looksLikeCommentContentType(ct string) bool {
	l := lowerASCII(ct)
	return containsASCII(l, "comment") && !isCommentFamilyContentType(ct)
}

// herrf 构造 HumanError，可选传入稳定 message_id（不改变旧中文字段）。
func herrf(reason, advice string, id ...string) *loader.HumanError {
	h := &loader.HumanError{Reason: reason, Advice: advice}
	if len(id) > 0 {
		h.MessageID = id[0]
	}
	return h
}

// herrfParams 构造带 params 的 HumanError。
func herrfParams(id string, params map[string]any, reason, advice string) *loader.HumanError {
	return &loader.HumanError{Reason: reason, Advice: advice, MessageID: id, Params: params}
}
