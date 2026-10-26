package i18n

// catalog 是嵌入双语 catalog：message_id -> 语言 -> 模板。
// 键集合与参数占位在 zh/en 间保持一致（由 catalog_test.go 强制）。
// 模板中的 {param} 由 T 插值；用户内容、文件名、路径、占位符不翻译。
var catalog = map[string]map[Lang]string{
	// ---- 通用 ----
	IDErrorGeneric: {
		ZH: "操作未完成，请检查输入后重试。",
		EN: "The operation did not complete. Check your input and try again.",
	},
	IDErrorUnknownID: {
		ZH: "发生了未分类的问题（{id}）。请查看处理详情后重试。",
		EN: "An unrecognized problem occurred ({id}). Check the details and try again.",
	},
	IDWarningGeneric: {
		ZH: "存在需要确认的提示，请查看处理详情。",
		EN: "There is a notice requiring your attention; see the details.",
	},

	// ---- loader：文件门禁与解码 ----
	"file.unsupported_format": {
		ZH: "不支持的文件格式 {ext}。本工具只做纯文本替换、.docx 与 .pptx 处理；请用原程序转换为纯文本（UTF-8/GBK）或另存为 .docx/.pptx 后再试。",
		EN: "Unsupported file format {ext}. This tool only performs plain-text replacement, .docx and .pptx processing; convert it to plain text (UTF-8/GBK) with the original program or save it as .docx/.pptx and try again.",
	},
	"file.too_large": {
		ZH: "文件大小 {size} 超过上限 {limit}。请拆分文件，或在配置中调高大小上限后重试。",
		EN: "File size {size} exceeds the limit {limit}. Split the file or raise the size limit in the configuration, then retry.",
	},
	"file.too_large_stream": {
		ZH: "文件大小超过上限 {limit}。请拆分文件，或在配置中调高大小上限后重试。",
		EN: "The file size exceeds the limit {limit}. Split the file or raise the size limit in the configuration, then retry.",
	},
	"decode.binary_or_utf16": {
		ZH: "检测到二进制或 UTF-16 内容，不是受支持的纯文本。请用纯文本编辑器另存为 UTF-8 或 GBK 后再试。",
		EN: "Binary or UTF-16 content detected; this is not supported plain text. Save it as UTF-8 or GBK with a plain-text editor and try again.",
	},
	"decode.unsupported_encoding": {
		ZH: "无法按 UTF-8 或 GBK 解码文件内容。文件可能是其他编码或已损坏，请转码为 UTF-8/GBK 后重试。",
		EN: "The file content could not be decoded as UTF-8 or GBK. It may use another encoding or be damaged; transcode it to UTF-8/GBK and retry.",
	},
	"encode.gbk_failed": {
		ZH: "按 GBK 写回失败。请检查文件内容是否包含 GBK 无法表示的字符。",
		EN: "Writing back as GBK failed. Check whether the content contains characters GBK cannot represent.",
	},

	// ---- custom 词校验与解析 ----
	"word.empty": {
		ZH: "自定义词不能为空。",
		EN: "A custom word cannot be empty.",
	},
	"word.whitespace_only": {
		ZH: "自定义词不能只由空白字符组成。",
		EN: "A custom word cannot consist only of whitespace.",
	},
	"word.contains_newline": {
		ZH: "自定义词不能包含换行符。",
		EN: "A custom word cannot contain line breaks.",
	},
	"word.too_long": {
		ZH: "自定义词超过 {max} 字符上限。",
		EN: "The custom word exceeds the {max}-character limit.",
	},
	"words.file_too_large": {
		ZH: "词文件大小 {size} 字节，超过 1MB 上限。请拆分或精简词文件后整批重试。",
		EN: "The word file is {size} bytes, over the 1MB limit. Split or trim it and retry the whole batch.",
	},
	"words.line_invalid": {
		ZH: "{source}第 {line} 行无效：{detail} 请修正该行后整批重试（导入为整批原子操作）。",
		EN: "Invalid entry in {source} at line {line}: {detail} Fix that line and retry the whole batch (imports are atomic).",
	},
	"words.too_many": {
		ZH: "本次自定义词（去重后）超过 {max} 个上限。请删减本次词条后重试。",
		EN: "This session's custom words (after de-duplication) exceed the {max}-word limit. Remove some words and retry.",
	},
	"words.merge_too_many": {
		ZH: "本次参与的自定义词（本次词与常备词去重后）共 {count} 个，超过 {max} 个上限。请删减本次词或常备词后重试。",
		EN: "The participating custom words (session + built-in, de-duplicated) total {count}, over the {max}-word limit. Remove some session or built-in words and retry.",
	},
	"words.none": {
		ZH: "未解析到任何有效自定义词。请输入至少一个非空词条，或检查导入文件是否只含空行。",
		EN: "No valid custom words were parsed. Enter at least one non-empty word, or check whether the imported file contains only blank lines.",
	},
	"words.source_text": {
		ZH: "文本框",
		EN: "text box",
	},
	"words.source_file": {
		ZH: "词文件",
		EN: "word file",
	},

	// ---- 碰撞与高频确认 ----
	"collision.grammar_text": {
		ZH: "原文已含有占位符文法（如 {hit}），与脱敏占位符冲突。请先移除或替换原文中的占位符样式文本（形如 [PHONE_1]）后再试。",
		EN: "The source text already contains placeholder syntax (e.g. {hit}), which conflicts with masking placeholders. Remove or replace placeholder-like text (such as [PHONE_1]) and retry.",
	},
	"collision.grammar_docx": {
		ZH: "DOCX 原文已含占位符文法（如 {hit}，位于 {part} 第 {paragraph} 段第 {segment} 片段），与脱敏占位符冲突。请先移除或替换原文中的占位符样式文本（形如 [PHONE_1]）后再试。",
		EN: "The DOCX source contains placeholder syntax (e.g. {hit} in {part}, paragraph {paragraph}, segment {segment}), which conflicts with masking placeholders. Remove or replace placeholder-like text (such as [PHONE_1]) and retry.",
	},
	"collision.grammar_pptx": {
		ZH: "PPTX 原文已含占位符文法（如 {hit}，位于 {part} 第 {paragraph} 段第 {segment} 片段），与脱敏占位符冲突。请先移除或替换原文中的占位符样式文本（形如 [PHONE_1]）后再试。",
		EN: "The PPTX source contains placeholder syntax (e.g. {hit} in {part}, paragraph {paragraph}, segment {segment}), which conflicts with masking placeholders. Remove or replace placeholder-like text (such as [PHONE_1]) and retry.",
	},
	"preview.required": {
		ZH: "自定义词命中次数过高，需先预览确认。本次词：{extra}；常备词：{builtin}。",
		EN: "Some custom words appear too often and need confirmation. Session words: {extra}; built-in words: {builtin}.",
	},
	"preview.hit": {
		ZH: "{word} 命中 {count} 次",
		EN: "{word} matched {count} times",
	},
	"preview.empty_group": {
		ZH: "（无）",
		EN: "(none)",
	},

	// ---- server 表单/API 门禁 ----
	"profile.unknown": {
		ZH: "未知 profile {profile}。可用选项：{options}。",
		EN: "Unknown profile {profile}. Available options: {options}.",
	},
	"rule.only_custom": {
		ZH: "规则覆盖字段仅 custom 档可用。请切换到 custom 档，或移除 rule_* 字段。",
		EN: "Rule override fields are only available for the custom profile. Switch to custom, or remove the rule_* fields.",
	},
	"rule.unknown_field": {
		ZH: "未知规则覆盖字段 {field}。可用字段：rule_phone、rule_idcard、rule_email、rule_ip、rule_api_key、rule_custom。",
		EN: "Unknown rule override field {field}. Available fields: rule_phone, rule_idcard, rule_email, rule_ip, rule_api_key, rule_custom.",
	},
	"rule.need_single_bool": {
		ZH: "规则覆盖字段 {field} 须为布尔单值。请只提交一次 true 或 false。",
		EN: "Rule override field {field} must be a single boolean. Submit true or false exactly once.",
	},
	"rule.bad_bool": {
		ZH: "规则覆盖字段 {field} 的值 {value} 不是布尔值。规则覆盖字段须为布尔值：true 或 false。",
		EN: "The value {value} for rule override field {field} is not boolean. Rule override fields must be true or false.",
	},
	"http.method_post_only": {
		ZH: "{scope}只支持 POST。请从应用界面执行操作。",
		EN: "{scope} only accepts POST. Perform the action from the app UI.",
	},
	"http.form_parse_failed": {
		ZH: "表单解析失败。请重新选择文件后再试。",
		EN: "Form parsing failed. Choose the file again and retry.",
	},
	"http.operation_busy": {
		ZH: "有操作正在进行。请等待当前处理或保存完成后再试。",
		EN: "An operation is already in progress. Wait for the current processing or save to finish and try again.",
	},
	"http.mask_file_missing": {
		ZH: "缺少待脱敏文件。请在脱敏页选择纯文本、.docx 或 .pptx 文件。",
		EN: "The file to mask is missing. Choose a plain-text, .docx or .pptx file on the mask view.",
	},
	"http.file_read_failed": {
		ZH: "读取文件失败。请重试。",
		EN: "Reading the file failed. Please retry.",
	},
	"http.form_cleanup_failed": {
		ZH: "处理完成后未能清理表单临时资源。请重试；如问题持续，请重启应用后再试。",
		EN: "Temporary form resources could not be cleaned up after processing. Retry; if the problem persists, restart the app and try again.",
	},
	"http.mapping_missing": {
		ZH: "缺少 mapping 文件。请选择本机上次脱敏后下载的 .mapping.json 文件。",
		EN: "The mapping file is missing. Choose the .mapping.json downloaded after the last mask on this machine.",
	},
	"http.mapping_read_failed": {
		ZH: "读取 mapping 失败。请重试。",
		EN: "Reading the mapping failed. Please retry.",
	},
	"http.restore_input_missing": {
		ZH: "缺少待还原内容。请上传 LLM 成品文件，或在文本框中粘贴内容。",
		EN: "Content to restore is missing. Upload the LLM output file, or paste the content into the text box.",
	},
	"exit.ok": {
		ZH: "应用正在退出。",
		EN: "The application is exiting.",
	},
	"exit.blocked": {
		ZH: "保存或处理正在进行，暂时无法退出。请等待当前操作完成后再退出。",
		EN: "A save or processing operation is in progress, so exit is temporarily blocked. Wait for it to finish and try again.",
	},

	// ---- mapping 门禁 ----
	"mapping.json_invalid": {
		ZH: "mapping 文件不是合法 JSON。请选择本机上次脱敏后下载的 .mapping.json 文件。",
		EN: "The mapping file is not valid JSON. Choose the .mapping.json downloaded after the last mask on this machine.",
	},
	"mapping.version_unsupported": {
		ZH: "不支持的 mapping version {version}。该 mapping 可能来自更新的工具版本；请使用对应版本，或重新脱敏生成新 mapping。",
		EN: "Unsupported mapping version {version}. It may come from a newer tool version; use the matching version, or re-mask to generate a new mapping.",
	},
	"mapping.v2_format_invalid": {
		ZH: "version 2 mapping 的 source_format 必须为 docx。请选择由本工具对 DOCX 脱敏后下载的 version 2 mapping 文件。",
		EN: "The source_format of a version 2 mapping must be docx. Choose the version 2 mapping downloaded after masking a DOCX with this tool.",
	},
	"mapping.v2_encoding_invalid": {
		ZH: "version 2 mapping 的 source_encoding 必须为空字符串。DOCX 不为整个容器赋予文本编码；请重新对 DOCX 脱敏并下载配对 mapping。",
		EN: "The source_encoding of a version 2 mapping must be an empty string. DOCX has no single text encoding; re-mask the DOCX and download its paired mapping.",
	},
	"mapping.v2_source_sha_invalid": {
		ZH: "version 2 mapping 的 source_sha256 缺失或格式非法。请重新对 DOCX 脱敏并下载配对 mapping 文件。",
		EN: "The source_sha256 of a version 2 mapping is missing or malformed. Re-mask the DOCX and download its paired mapping file.",
	},
	"mapping.v2_text_sha_invalid": {
		ZH: "version 2 mapping 的 source_text_sha256 缺失或格式非法。请重新对 DOCX 脱敏并下载配对 mapping 文件。",
		EN: "The source_text_sha256 of a version 2 mapping is missing or malformed. Re-mask the DOCX and download its paired mapping file.",
	},
	"mapping.v3_format_invalid": {
		ZH: "version 3 mapping 的 source_format 必须为 pptx。请选择由本工具对 PPTX 脱敏后下载的 version 3 mapping 文件。",
		EN: "The source_format of a version 3 mapping must be pptx. Choose the version 3 mapping downloaded after masking a PPTX with this tool.",
	},
	"mapping.v3_encoding_invalid": {
		ZH: "version 3 mapping 的 source_encoding 必须为空字符串。PPTX 不为整个容器赋予文本编码；请重新对 PPTX 脱敏并下载配对 mapping。",
		EN: "The source_encoding of a version 3 mapping must be an empty string. PPTX has no single text encoding; re-mask the PPTX and download its paired mapping.",
	},
	"mapping.v3_source_sha_invalid": {
		ZH: "version 3 mapping 的 source_sha256 缺失或格式非法。请重新对 PPTX 脱敏并下载配对 mapping 文件。",
		EN: "The source_sha256 of a version 3 mapping is missing or malformed. Re-mask the PPTX and download its paired mapping file.",
	},
	"mapping.v3_text_sha_invalid": {
		ZH: "version 3 mapping 的 source_text_sha256 缺失或格式非法。请重新对 PPTX 脱敏并下载配对 mapping 文件。",
		EN: "The source_text_sha256 of a version 3 mapping is missing or malformed. Re-mask the PPTX and download its paired mapping file.",
	},
	"mapping.v3_placeholders_missing": {
		ZH: "version 3 mapping 缺少 placeholders 条目。请重新对 PPTX 脱敏并下载配对 mapping 文件。",
		EN: "The version 3 mapping is missing its placeholders list. Re-mask the PPTX and download its paired mapping file.",
	},
	"mapping.generic_invalid": {
		ZH: "mapping 文件无法解析。请选择本机上次脱敏后下载的 .mapping.json 文件。",
		EN: "The mapping file could not be parsed. Choose the .mapping.json downloaded after the last mask on this machine.",
	},

	// ---- DOCX 包/关系/XML 门禁与输出结构 ----
	"docx.not_docx": {
		ZH: "文件不是有效的 DOCX 包。请在 Word 中另存为新的 .docx 后重试。",
		EN: "The file is not a valid DOCX package. Re-save it as a new .docx in Word and retry.",
	},
	"docx.encrypted_ole": {
		ZH: "检测到加密的 Word 文档（OLE 复合文件），无法处理。请先解除加密并另存为 .docx 后重试。",
		EN: "An encrypted Word document (OLE compound file) was detected and cannot be processed. Remove encryption, save as .docx, and retry.",
	},
	"docx.zip_budget": {
		ZH: "DOCX 包超出安全预算（条目数量/单部件大小/解压总量）。请精简文档后另存为新的 .docx 再试。",
		EN: "The DOCX package exceeds the safety budget (entry count / single-part size / total decompressed size). Trim the document, save as a new .docx, and retry.",
	},
	"docx.part_name_invalid": {
		ZH: "DOCX 含非法部件名。ZIP 部件名必须为包内相对路径且不含冒号、反斜杠或 . / ..。请重新另存 .docx。",
		EN: "The DOCX contains an invalid part name. ZIP part names must be package-relative and contain no colon, backslash, or . / .. segments. Re-save the .docx.",
	},
	"docx.content_types_invalid": {
		ZH: "[Content_Types].xml 声明不完整或重复。这不是有效的 DOCX 包；请在 Word 中另存为新的 .docx 后重试。",
		EN: "[Content_Types].xml is incomplete or duplicated. This is not a valid DOCX package; re-save it as a new .docx in Word and retry.",
	},
	"docx.relationship_invalid": {
		ZH: "DOCX 关系部件命名空间/声明/ID 不正确。文件结构异常；请在 Word 中另存为新的 .docx 后重试。",
		EN: "The DOCX relationships part has an invalid namespace, declaration, or ID. Re-save it as a new .docx in Word and retry.",
	},
	"docx.missing_part": {
		ZH: "DOCX 缺少必要部件（[Content_Types].xml、包根关系或主文档）。文件结构损坏；请在 Word 中另存为新的 .docx 后重试。",
		EN: "The DOCX is missing a required part ([Content_Types].xml, package root relationships, or the main document). Re-save it as a new .docx in Word and retry.",
	},
	"docx.macro_unsupported": {
		ZH: "不支持宏文档（.docm / vbaProject）。请另存为不含宏的 .docx 后重试。",
		EN: "Macro-enabled documents (.docm / vbaProject) are not supported. Save as a macro-free .docx and retry.",
	},
	"docx.digital_signature_unsupported": {
		ZH: "不支持含数字签名的文档包。请移除签名后另存为 .docx 再试。",
		EN: "Documents with digital signatures are not supported. Remove the signature, save as .docx, and retry.",
	},
	"docx.comment_family_unsupported": {
		ZH: "检测到不支持的批注家族扩展（{what}）。请在 Word 中另存为新的 .docx 后重试。",
		EN: "An unsupported comment-family extension was detected ({what}). Re-save as a new .docx in Word and retry.",
	},
	"docx.unsupported_revision": {
		ZH: "检测到无法可靠接受的修订：{revision}。请在 Word 中接受或拒绝该修订后另存为新的 .docx 再试。",
		EN: "A revision that cannot be reliably accepted was detected: {revision}. Accept or reject it in Word, save as a new .docx, and retry.",
	},
	"docx.xml_parse_failed": {
		ZH: "DOCX 部件 XML 解析失败。文件可能损坏；请在 Word 中另存为新的 .docx 后重试。",
		EN: "Parsing a DOCX part's XML failed. The file may be damaged; re-save it as a new .docx in Word and retry.",
	},
	"docx.alt_chunk_unsupported": {
		ZH: "DOCX 含 external 导入文字结构（w:altChunk），无法可靠枚举文字。请移除该结构或另存为新的 .docx 后重试。",
		EN: "The DOCX contains an external imported-text structure (w:altChunk), so text cannot be reliably enumerated. Remove it or re-save as a new .docx and retry.",
	},
	"docx.output_validation_failed": {
		ZH: "输出包结构/资源校验未通过，未交付文件。请重试，或改用其他文件。",
		EN: "The output package structure/asset validation failed, so no file was delivered. Retry, or use another file.",
	},

	// ---- PPTX 包门禁与文字处理 ----
	"pptx.not_pptx": {
		ZH: "文件不是有效的 PPTX 演示文稿。请在 PowerPoint 中另存为普通、未加密的 .pptx 后重试。",
		EN: "The file is not a valid PPTX presentation. Save it as a normal, unencrypted .pptx in PowerPoint and retry.",
	},
	"pptx.encrypted_ole": {
		ZH: "检测到加密的演示文稿（OLE 复合文件），无法处理。请先解除加密并另存为普通 .pptx 后重试。",
		EN: "An encrypted presentation (OLE compound file) was detected and cannot be processed. Remove encryption, save as a normal .pptx, and retry.",
	},
	"pptx.zip_budget": {
		ZH: "PPTX 包超出安全预算（条目数量/单部件大小/解压总量/压缩方式）。请精简演示文稿后另存为新的 .pptx 再试。",
		EN: "The PPTX package exceeds the safety budget (entry count, single-part size, total decompressed size, or compression method). Trim the presentation, save as a new .pptx, and retry.",
	},
	"pptx.part_name_invalid": {
		ZH: "PPTX 含非法部件名。ZIP 部件名必须为包内相对路径且不含冒号、反斜杠或 . / ..。请重新另存 .pptx。",
		EN: "The PPTX contains an invalid part name. ZIP part names must be package-relative and contain no colon, backslash, or . / .. segments. Re-save the .pptx.",
	},
	"pptx.content_types_invalid": {
		ZH: "[Content_Types].xml 声明不完整或重复。这不是有效的 PPTX 包；请在 PowerPoint 中另存为新的 .pptx 后重试。",
		EN: "[Content_Types].xml is incomplete or duplicated. This is not a valid PPTX package; re-save it as a new .pptx in PowerPoint and retry.",
	},
	"pptx.relationship_invalid": {
		ZH: "PPTX 关系部件命名空间/声明/ID/主列表不正确。文件结构异常；请在 PowerPoint 中另存为新的 .pptx 后重试。",
		EN: "The PPTX relationships part has an invalid namespace, declaration, ID, or slide list. Re-save it as a new .pptx in PowerPoint and retry.",
	},
	"pptx.missing_part": {
		ZH: "PPTX 缺少必要部件或内部关系目标。文件结构损坏；请在 PowerPoint 中另存为新的 .pptx 后重试。",
		EN: "The PPTX is missing a required part or internal relationship target. Re-save it as a new .pptx in PowerPoint and retry.",
	},
	"pptx.macro_unsupported": {
		ZH: "不支持宏演示文稿（.pptm / vbaProject）。请另存为不含宏的普通 .pptx 后重试。",
		EN: "Macro-enabled presentations (.pptm / vbaProject) are not supported. Save as a macro-free .pptx and retry.",
	},
	"pptx.digital_signature_unsupported": {
		ZH: "不支持含数字签名的演示文稿包。请移除签名后另存为 .pptx 再试。",
		EN: "Presentations with digital signatures are not supported. Remove the signature, save as .pptx, and retry.",
	},
	"pptx.comment_family_unsupported": {
		ZH: "检测到不支持的批注家族扩展（{what}）。请先在 PowerPoint 中删除全部批注并另存为新的 .pptx 后再试。",
		EN: "An unsupported comment-family extension was detected ({what}). Delete all comments in PowerPoint, save as a new .pptx, and retry.",
	},
	"pptx.xml_parse_failed": {
		ZH: "PPTX 部件 XML 解析失败。文件可能损坏；请在 PowerPoint 中另存为新的 .pptx 后重试。",
		EN: "Parsing a PPTX part's XML failed. The file may be damaged; re-save it as a new .pptx in PowerPoint and retry.",
	},
	"pptx.text_unsupported": {
		ZH: "PPTX 含无法可靠枚举的原生文字结构，未执行脱敏。请在 PowerPoint 中把该内容转换为普通文本框或删除后另存为新的 .pptx 再试。",
		EN: "The PPTX contains native text that cannot be reliably enumerated, so masking was not performed. Convert it to a normal text box or remove it in PowerPoint, save as a new .pptx, and retry.",
	},
	"pptx.output_validation_failed": {
		ZH: "PPTX 输出包结构/资源/文字校验未通过，未交付文件。请重试，或改用其他文件。",
		EN: "The PPTX output package failed structure/asset/text validation, so no file was delivered. Retry, or use another file.",
	},
	"pptx.combination_unsupported": {
		ZH: "不支持的格式组合：本期 PPTX 仅支持 PPTX 原件与 PPTX 回复的同格式闭环。请勿用 PPTX mapping 还原文本/DOCX，也不要用旧文本/DOCX mapping 还原 PPTX。",
		EN: "Unsupported format combination: PPTX currently supports only a same-format PPTX source and PPTX reply. Do not restore text/DOCX with a PPTX mapping, or restore PPTX with an older text/DOCX mapping.",
	},
	"pptx.internal": {
		ZH: "PPTX 处理内部状态异常。请重试；若持续出现请报告此问题。",
		EN: "An internal PPTX processing state is invalid. Retry; report the problem if it persists.",
	},

	// ---- 警告（非阻断）----
	"warning.residual_forward": {
		ZH: "检测到输出中仍有 {count} 处命中启用规则的内容（首处：第 {line} 行第 {column} 列）。注意：该校验仅验证规则覆盖率，不构成语义级无泄露保证。如仍需交付，可强制导出；建议检查输出中的残留内容。",
		EN: "{count} places matching enabled rules remain in the output (first at line {line}, column {column}). Note: this check only verifies rule coverage and is not a semantic no-leak guarantee. If you still need to deliver, you may export anyway; review the remaining content.",
	},
	"warning.residual_forward_docx": {
		ZH: "检测到输出中仍有 {count} 处命中启用规则的内容（首处：{part} 第 {paragraph} 段第 {segment} 片段第 {column} 字符）。注意：该校验仅验证规则覆盖率，不构成语义级无泄露保证。如仍需交付，可强制导出；建议检查输出中的残留内容。",
		EN: "{count} places matching enabled rules remain in the output (first at {part}, paragraph {paragraph}, segment {segment}, character {column}). Note: this check only verifies rule coverage and is not a semantic no-leak guarantee. If you still need to deliver, you may export anyway; review the remaining content.",
	},
	"warning.residual_reverse": {
		ZH: "还原后仍存在占位符残留，疑似使用了与文本不配对的 mapping 文件（共 {count} 处，首处：第 {line} 行 {text}）。本工具只做大小写/全角的 best-effort 匹配，无法找回被 LLM 改写或翻译的占位符；请确认 mapping 来源为本机上次下载的配对文件。",
		EN: "Placeholders remain after restore; the mapping may not match the text ({count} places; first at line {line}: {text}). This tool only does case/full-width best-effort matching and cannot recover placeholders rewritten or translated by the LLM; confirm the mapping came from the paired file downloaded on this machine.",
	},
	"warning.residual_reverse_docx": {
		ZH: "还原后仍存在占位符残留，疑似使用了与文本不配对的 mapping 文件（共 {count} 处，首处：{part} 第 {paragraph} 段第 {segment} 片段第 {column} 字符 {text}）。本工具只做大小写/全角的 best-effort 匹配，无法找回被 LLM 改写或翻译的占位符；请确认 mapping 来源为本机上次下载的配对文件。",
		EN: "Placeholders remain after restore; the mapping may not match the text ({count} places; first at {part}, paragraph {paragraph}, segment {segment}, character {column}: {text}). This tool only does case/full-width best-effort matching and cannot recover placeholders rewritten or translated by the LLM; confirm the mapping came from the paired file downloaded on this machine.",
	},
	"warning.sha_mismatch": {
		ZH: "无法确证一致：还原结果与 mapping 记录的原文文件指纹不一致。该提示仅供参考，不构成拿错 mapping 的断言，也不阻断导出。",
		EN: "Consistency cannot be confirmed: the restored result's fingerprint differs from the source fingerprint recorded in the mapping. This is advisory only, is not an assertion that the wrong mapping was used, and does not block export.",
	},
	"warning.cross_format": {
		ZH: "无法确证文本一致：本次回程为跨格式（缺少与输入同格式的文字基线）。该提示仅供参考，不构成拿错 mapping 的断言，也不阻断导出。",
		EN: "Text consistency cannot be confirmed: this restore is cross-format (no same-format text baseline). This is advisory only, is not an assertion that the wrong mapping was used, and does not block export.",
	},
	"warning.docx_history": {
		ZH: "注意：本工具已清除批注并接受修订；回程只恢复映射文字，不能恢复已清除的批注或修订历史。",
		EN: "Note: this tool removed comments and accepted revisions; restore only recovers mapped text and cannot restore the cleared comments or revision history.",
	},
	"warning.text_mismatch": {
		ZH: "无法确证文本一致：还原后 DOCX 的清理后覆盖文字与 mapping 记录的文字参考不一致。该提示仅供参考，不阻断导出。",
		EN: "Text consistency cannot be confirmed: the restored DOCX's cleaned covered text differs from the text reference recorded in the mapping. This is advisory only and does not block export.",
	},
	"warning.residual_forward_pptx": {
		ZH: "检测到输出中仍有 {count} 处命中启用规则的内容（首处：{part} 第 {paragraph} 段第 {segment} 片段第 {column} 字符）。注意：该校验仅验证规则覆盖率，不构成语义级无泄露保证。如仍需交付，可强制导出；建议检查输出中的残留内容。",
		EN: "{count} places matching enabled rules remain in the output (first at {part}, paragraph {paragraph}, segment {segment}, character {column}). Note: this check only verifies rule coverage and is not a semantic no-leak guarantee. If you still need to deliver, you may export anyway; review the remaining content.",
	},
	"warning.residual_reverse_pptx": {
		ZH: "还原后仍存在占位符残留，疑似使用了与文本不配对的 mapping 文件（共 {count} 处，首处：{part} 第 {paragraph} 段第 {segment} 片段第 {column} 字符 {text}）。本工具只做大小写/全角的 best-effort 匹配，无法找回被 LLM 改写或翻译的占位符；请确认 mapping 来源为本机上次下载的配对文件。",
		EN: "Placeholders remain after restore; the mapping may not match the text ({count} places; first at {part}, paragraph {paragraph}, segment {segment}, character {column}: {text}). This tool only does case/full-width best-effort matching and cannot recover placeholders rewritten or translated by the LLM; confirm the mapping came from the paired file downloaded on this machine.",
	},
	"warning.pptx_history": {
		ZH: "注意：本工具已按支持范围清除 PPTX 批注及相关人员信息；回程只恢复映射文字，不能恢复已清除的批注或原始排版。",
		EN: "Note: this tool removed PPTX comments and related people information within the supported scope; restore only recovers mapped text and cannot restore the cleared comments or original layout.",
	},
	"warning.pptx_text_mismatch": {
		ZH: "无法确证文本一致：还原后 PPTX 的清理后覆盖文字与 mapping 记录的文字参考不一致。该提示仅供参考，不阻断导出。",
		EN: "Text consistency cannot be confirmed: the restored PPTX's cleaned covered text differs from the text reference recorded in the mapping. This is advisory only and does not block export.",
	},

	// ---- 审计 ----
	"feedback.audit_failed": {
		ZH: "脱敏已完成并交付，但审计记录写入失败（不影响本次结果）。",
		EN: "Masking completed and was delivered, but the audit record could not be written (this does not affect the result).",
	},

	// ---- 设置错误：SaveCoordinator ----
	"save.in_progress": {
		ZH: "保存进行中或正在退出，请稍后再试。",
		EN: "A save is in progress or the app is exiting. Try again later.",
	},
	"save.bad_base64": {
		ZH: "保存失败：产物数据不是有效的 base64，请重试。",
		EN: "Save failed: the artifact data is not valid base64. Please retry.",
	},
	"save.no_dialog": {
		ZH: "保存失败：当前环境没有可用的另存对话框。",
		EN: "Save failed: no save dialog is available in this environment.",
	},
	"save.dialog_failed": {
		ZH: "保存失败：无法打开另存对话框，请重试。",
		EN: "Save failed: the save dialog could not be opened. Please retry.",
	},
	"save.cancelled": {
		ZH: "已取消保存，结果仍可再次保存。",
		EN: "Save cancelled; the result can still be saved again.",
	},
	"save.dir_missing": {
		ZH: "保存失败：目的目录不存在，请另选一个本地目录。",
		EN: "Save failed: the destination folder does not exist. Choose another local folder.",
	},
	"save.dir_unreadable": {
		ZH: "保存失败：无法访问目的目录，请重试或另选目录。",
		EN: "Save failed: the destination folder is not accessible. Retry or choose another folder.",
	},
	"save.parent_not_dir": {
		ZH: "保存失败：目的路径的父级不是目录。",
		EN: "Save failed: the parent of the destination path is not a folder.",
	},
	"save.target_exists": {
		ZH: "已存在同名文件，未覆盖。请换一个文件名。",
		EN: "A file with the same name already exists; it was not overwritten. Choose another file name.",
	},
	"save.target_uncheckable": {
		ZH: "保存失败：无法检查目标文件，请重试或另选文件名。",
		EN: "Save failed: the target file could not be checked. Retry or choose another file name.",
	},
	"save.temp_failed": {
		ZH: "保存失败：无法在目的目录创建临时文件，请检查权限或另选目录。",
		EN: "Save failed: a temporary file could not be created in the destination folder. Check permissions or choose another folder.",
	},
	"save.write_failed": {
		ZH: "保存失败：写入临时文件出错，已清理，请重试。",
		EN: "Save failed: writing the temporary file failed; it was cleaned up. Please retry.",
	},
	"save.sync_failed": {
		ZH: "保存失败：同步数据出错，已清理，请重试。",
		EN: "Save failed: syncing data failed; it was cleaned up. Please retry.",
	},
	"save.close_failed": {
		ZH: "保存失败：关闭临时文件出错，已清理，请重试。",
		EN: "Save failed: closing the temporary file failed; it was cleaned up. Please retry.",
	},
	"save.race_exists": {
		ZH: "目标文件已被其他程序创建，未覆盖。请换一个文件名重试。",
		EN: "The target file was created by another program; it was not overwritten. Choose another file name and retry.",
	},
	"save.link_unsupported": {
		ZH: "保存失败：该目录不支持安全的无覆盖发布，请另选本地支持的目录。",
		EN: "Save failed: this folder does not support safe no-overwrite publishing. Choose a supported local folder.",
	},
	"save.link_failed": {
		ZH: "保存失败：无法在该目录安全发布文件，请另选本地支持的目录。",
		EN: "Save failed: the file could not be safely published in this folder. Choose a supported local folder.",
	},
	"save.saved": {
		ZH: "已保存。",
		EN: "Saved.",
	},
	"save.cleanup_warning": {
		ZH: "已保存，但临时文件清理失败，请手动检查目的目录。",
		EN: "Saved, but cleaning up the temporary file failed. Check the destination folder manually.",
	},
	"save.name_missing": {
		ZH: "保存失败：缺少建议文件名。",
		EN: "Save failed: the suggested file name is missing.",
	},
	"save.name_has_separator": {
		ZH: "保存失败：建议文件名不能包含路径分隔符。",
		EN: "Save failed: the suggested file name cannot contain path separators.",
	},
	"save.name_invalid": {
		ZH: "保存失败：建议文件名无效。",
		EN: "Save failed: the suggested file name is invalid.",
	},

	// ---- 工作区自动落盘 ----
	"workspace.unavailable": {
		ZH: "自动落盘不可用：无法在程序所在目录创建工作区目录。仍可使用“保存”按钮手动另存产物。",
		EN: "Auto-save is unavailable: the workspace folders could not be created next to the program. You can still save artifacts manually with the Save button.",
	},
	"workspace.unresolved_root": {
		ZH: "自动落盘不可用：无法确定程序所在目录。仍可使用“保存”按钮手动另存产物。",
		EN: "Auto-save is unavailable: the program folder could not be determined. You can still save artifacts manually with the Save button.",
	},
	"workspace.bad_name": {
		ZH: "自动落盘失败：产物命名不符合约定，请手动另存。",
		EN: "Auto-save failed: the artifact name does not follow the convention. Save it manually.",
	},
	"workspace.bad_base64": {
		ZH: "自动落盘失败：产物数据不是有效的 base64。",
		EN: "Auto-save failed: the artifact data is not valid base64.",
	},
	"workspace.target_check_failed": {
		ZH: "自动落盘失败：无法检查目标文件，请手动另存。",
		EN: "Auto-save failed: the target file could not be checked. Save it manually.",
	},
	"workspace.temp_failed": {
		ZH: "自动落盘失败：无法在工作区创建临时文件，请检查权限或手动另存。",
		EN: "Auto-save failed: a temporary file could not be created in the workspace. Check permissions or save manually.",
	},
	"workspace.too_many": {
		ZH: "自动落盘失败：目标目录同名文件过多，已超过 1000 次尝试，请手动另存。",
		EN: "Auto-save failed: too many same-named files in the target folder (over 1000 attempts). Save it manually.",
	},
	"workspace.no_derived_name": {
		ZH: "自动落盘失败：脱敏产物命名不符合约定，无法生成不覆盖的新名，请手动另存。",
		EN: "Auto-save failed: the masked artifact name does not follow the convention, so a non-overwriting name could not be generated. Save it manually.",
	},
	"workspace.link_unsupported": {
		ZH: "自动落盘失败：目标目录不支持安全的无覆盖发布，请手动另存到受支持的本地目录。",
		EN: "Auto-save failed: the target folder does not support safe no-overwrite publishing. Save manually to a supported local folder.",
	},
	"workspace.saved": {
		ZH: "已自动保存。",
		EN: "Auto-saved.",
	},
	"workspace.busy": {
		ZH: "自动落盘暂不可用：工作区正被其他程序使用。本次未覆盖任何文件，请稍后重试或手动另存。",
		EN: "Auto-save is temporarily unavailable: the workspace is in use by another program. Nothing was overwritten; retry later or save manually.",
	},

	// ---- GUI 固定目录快捷输入（original/cards/pending/mapping）----
	"input.bad_kind": {
		ZH: "不支持的目录类型（{kind}）。仅支持 original、cards、pending、mapping。",
		EN: "Unsupported directory kind ({kind}). Only original, cards, pending and mapping are supported.",
	},
	"input.root_unavailable": {
		ZH: "无法确定程序所在目录，固定目录快捷选择不可用。请使用手动选择。",
		EN: "The program folder could not be determined, so quick selection is unavailable. Use manual selection instead.",
	},
	"input.dir_unavailable": {
		ZH: "{dir} 不可用：它可能是普通文件、符号链接或当前不可访问。请修复后点击刷新，或使用手动选择。",
		EN: "{dir} is unavailable: it may be a regular file, a link, or currently inaccessible. Fix it and refresh, or use manual selection.",
	},
	"input.list_failed": {
		ZH: "无法列举 {dir} 的文件，请检查权限后点击刷新，或使用手动选择。",
		EN: "Could not list files in {dir}. Check permissions and refresh, or use manual selection.",
	},
	"input.bad_name": {
		ZH: "文件名无效，已拒绝读取。请刷新列表或使用手动选择。",
		EN: "The file name is invalid, so reading was refused. Refresh the list or use manual selection.",
	},
	"input.unsupported_format": {
		ZH: "该文件格式不适用于此快捷入口，已拒绝读取。请刷新列表或使用手动选择。",
		EN: "This file format does not belong to this quick-selection entry, so reading was refused. Refresh the list or use manual selection.",
	},
	"input.file_missing": {
		ZH: "所选文件已不存在或不可访问。请刷新列表或使用手动选择。",
		EN: "The selected file no longer exists or is inaccessible. Refresh the list or use manual selection.",
	},
	"input.not_regular": {
		ZH: "所选项目不是普通文件，已拒绝读取。请刷新列表或重新选择。",
		EN: "The selected item is not a regular file, so reading was refused. Refresh the list or choose again.",
	},
	"input.changed": {
		ZH: "文件在读取前发生变化或已被替换，已拒绝读取。请刷新列表或重新选择。",
		EN: "The file changed or was replaced before reading, so reading was refused. Refresh the list or choose again.",
	},
	"input.read_failed": {
		ZH: "读取文件失败，请检查权限后重试或使用手动选择。",
		EN: "Reading the file failed. Check permissions and retry, or use manual selection.",
	},
	"input.too_large": {
		ZH: "所选文件为 {size}，超过该快捷入口 {limit} 的读取上限。请使用手动选择该文件。",
		EN: "The selected file is {size}, over this quick-selection entry's {limit} read limit. Use manual selection for this file.",
	},
	"input.busy": {
		ZH: "当前有操作或退出正在进行，快捷选择暂不可用。请稍后再试。",
		EN: "An operation or exit is in progress, so quick selection is temporarily unavailable. Try again shortly.",
	},

	// ---- 原生提示与应用级 ----
	"native.app_title": {
		ZH: "本地脱敏工具",
		EN: "Local Masking Tool",
	},
	"native.startup_failed_title": {
		ZH: "启动失败",
		EN: "Startup failed",
	},
	"native.session_bus_unavailable": {
		ZH: "无法连接到桌面图形会话（session bus），应用无法保持单实例。请在正常的图形登录会话中运行后重试。",
		EN: "Could not connect to the desktop graphical session (session bus), so the app cannot keep a single instance. Run it in a normal graphical login session and retry.",
	},
	"native.workspace_unavailable_title": {
		ZH: "自动落盘不可用",
		EN: "Auto-save unavailable",
	},
	"native.close_blocked_title": {
		ZH: "暂时无法退出",
		EN: "Cannot exit yet",
	},
	"native.close_blocked_body": {
		ZH: "保存或处理正在进行，请等待完成后再退出。",
		EN: "A save or processing operation is in progress. Wait for it to finish, then exit.",
	},
	"native.confirm_exit_title": {
		ZH: "退出应用",
		EN: "Exit application",
	},
	"native.confirm_close_legacy": {
		ZH: "存在尚未保存的结果。选择“是”将放弃这些结果并退出；选择“否”将返回应用，结果仍可保存。",
		EN: "There are unsaved results. Choose Yes to discard them and exit; choose No to return to the app so they can still be saved.",
	},
	"native.confirm_close_workspace": {
		ZH: "有产物未落盘，关闭将丢失，请确认已保存。",
		EN: "Some artifacts were not saved to disk; closing will lose them. Confirm they are saved before closing.",
	},
	"native.save_dialog_title": {
		ZH: "另存为",
		EN: "Save as",
	},
	"native.linux_webkit_required": {
		ZH: "本应用需要 WebKit2GTK {version}；请先安装系统组件（如 webkit2gtk4.1）后重新启动。",
		EN: "This app requires WebKit2GTK {version}; install the system component (for example webkit2gtk4.1) and restart.",
	},
	"native.webview2_installation_required": {
		ZH: "本应用需要 Microsoft Edge WebView2 运行时。点击“确定”将打开微软官方下载页面；安装完成后请重新启动应用。",
		EN: "This app requires the Microsoft Edge WebView2 runtime. Clicking OK opens the official Microsoft download page; install it and restart the app.",
	},
	"native.webview2_update_required": {
		ZH: "本机的 Microsoft Edge WebView2 运行时版本过旧。点击“确定”将打开微软官方下载页面；更新后请重新启动应用。",
		EN: "The installed Microsoft Edge WebView2 runtime is outdated. Clicking OK opens the official Microsoft download page; update it and restart the app.",
	},
	"native.webview2_missing_requirements": {
		ZH: "缺少运行环境",
		EN: "Missing runtime",
	},
	"native.webview2_not_installed": {
		ZH: "未安装 WebView2 运行时",
		EN: "WebView2 runtime not installed",
	},
	"native.webview2_error": {
		ZH: "错误",
		EN: "Error",
	},
	"native.webview2_failed_install": {
		ZH: "WebView2 运行时安装未完成，请重试或联系管理员。",
		EN: "The WebView2 runtime installation did not complete. Retry or contact your administrator.",
	},
	"native.webview2_download_page": {
		ZH: "本应用需要 WebView2 运行时。点击“确定”打开微软官方安装页面，安装后重新启动。最低版本：",
		EN: "This app requires the WebView2 runtime. Clicking OK opens the official Microsoft installation page; install it and restart. Minimum version:",
	},
	"native.webview2_press_ok": {
		ZH: "点击“确定”开始安装。",
		EN: "Click OK to start installation.",
	},
	"native.webview2_contact_admin": {
		ZH: "运行本应用需要 WebView2 运行时，请联系系统管理员安装。",
		EN: "Running this app requires the WebView2 runtime; contact your system administrator to install it.",
	},
	"native.webview2_invalid_fixed": {
		ZH: "指定的 WebView2 运行时不可用，请检查最低版本与路径。",
		EN: "The specified WebView2 runtime is unavailable; check the minimum version and path.",
	},
	"native.webview2_process_crash": {
		ZH: "WebView2 进程已崩溃，应用需要重新启动。",
		EN: "The WebView2 process crashed; the app must be restarted.",
	},
	"native.launch_failed": {
		ZH: "桌面启动失败",
		EN: "Desktop startup failed",
	},
	"native.desktop_hint": {
		ZH: "应用已退出，可关闭本窗口。再次双击应用即可重新启动。",
		EN: "The app has exited; you can close this window. Double-click the app again to restart.",
	},

	// ---- 偏好持久化 ----
	"prefs.save_failed": {
		ZH: "偏好未保存，重启后可能恢复默认。",
		EN: "Preferences were not saved; defaults may return after restart.",
	},
	"prefs.invalid": {
		ZH: "无效的偏好值，已忽略。",
		EN: "Invalid preference value; it was ignored.",
	},

	// ---- 前端静态文案（页面按钮/标签/状态，随语言即时切换）----
	"ui.app_title": {
		ZH: "本地脱敏工具",
		EN: "Local Masking Tool",
	},
	"ui.view_mask": {
		ZH: "脱敏",
		EN: "Mask",
	},
	"ui.view_restore": {
		ZH: "还原",
		EN: "Restore",
	},
	"ui.theme_label": {
		ZH: "主题",
		EN: "Theme",
	},
	"ui.theme_system": {
		ZH: "跟随系统",
		EN: "System",
	},
	"ui.theme_light": {
		ZH: "浅色",
		EN: "Light",
	},
	"ui.theme_dark": {
		ZH: "暗色",
		EN: "Dark",
	},
	"ui.language_label": {
		ZH: "语言",
		EN: "Language",
	},
	"ui.select_file": {
		ZH: "选择文件（纯文本不限后缀，或 .docx/.pptx）",
		EN: "Choose a file (any plain-text extension or none, or .docx/.pptx)",
	},
	"ui.pick_file": {
		ZH: "选择文件",
		EN: "Choose file",
	},
	"ui.doc_formats_text": {
		ZH: "支持纯文本（不限后缀，可无后缀；UTF-8/GBK）与 Word .docx、PowerPoint .pptx。",
		EN: "Accepts plain text (any extension or none; UTF-8/GBK), Word .docx, and PowerPoint .pptx.",
	},
	"ui.doc_formats_examples": {
		ZH: "纯文本示例：TXT、Markdown、CSV、JSON、YAML、代码、日志；不支持 PDF、ODT、RTF、XLSX、PPT/PPTM 等格式。PPTX 仅处理原生文字并保留可编辑结构，不保证视觉排版不变。",
		EN: "Plain-text examples: TXT, Markdown, CSV, JSON, YAML, code, logs. PDF, ODT, RTF, XLSX, PPT/PPTM and similar formats are not supported. PPTX handles native text and keeps editable structure, without promising identical visual layout.",
	},
	"ui.doc_formats_syntax": {
		ZH: "按纯文本替换，不保证代码或结构化数据替换后的语法有效。",
		EN: "Replacement is plain text; code or structured data is not guaranteed to remain syntactically valid.",
	},
	"ui.pick_word_file": {
		ZH: "导入 TXT",
		EN: "Import TXT",
	},
	"ui.pick_reply_file": {
		ZH: "选择还原文件",
		EN: "choose file to restore",
	},
	"ui.pick_mapping": {
		ZH: "选择映射",
		EN: "Choose mapping",
	},
	"ui.adjust_words": {
		ZH: "自定义词",
		EN: "Custom words",
	},
	"ui.adjust_selected": {
		ZH: "已选 {x}/{y}",
		EN: "{x}/{y} selected",
	},
	"ui.mapping_reminder_short": {
		ZH: "还原映射含原文，仅供本机还原；发送给模型时只使用脱敏文件。",
		EN: "The restore mapping contains original text and is for local restore only; send only the masked file to the model.",
	},
	"ui.docx_unchecked_short": {
		ZH: "图片及属性、字段指令、绑定源、链接目标、图表/SmartArt、附件未检查。",
		EN: "Images and document properties, field codes, binding sources, link targets, charts/SmartArt, and attachments are not checked.",
	},
	"ui.pptx_unchecked_short": {
		ZH: "图表及数据源、SmartArt、嵌入对象、图片文字、属性、字段指令与链接目标未检查；刷新可能重新引入未处理值。",
		EN: "Charts and data sources, SmartArt, embedded objects, text in images, properties, field codes, and link targets are not checked; refreshing may reintroduce unprocessed values.",
	},
	"ui.drop_hint": {
		ZH: "点击选择或拖入单个文件",
		EN: "Click to choose, or drop a single file",
	},
	"ui.click_hint": {
		ZH: "点击选择单个文件",
		EN: "Click to choose a single file",
	},
	"ui.click_to_choose": {
		ZH: "请点击文件按钮选择文件。",
		EN: "Please click the file button to choose a file.",
	},
	"ui.workspace_label_original": {
		ZH: "original/ 文件",
		EN: "original/ file",
	},
	"ui.workspace_label_cards": {
		ZH: "cards/ 词卡",
		EN: "cards/ word card",
	},
	"ui.workspace_label_pending": {
		ZH: "pending/ 文件",
		EN: "pending/ file",
	},
	"ui.workspace_label_mapping": {
		ZH: "mapping/ 映射",
		EN: "mapping/ mapping",
	},
	"ui.workspace_choose_original": {
		ZH: "从 original/ 选择文件",
		EN: "Choose a file from original/",
	},
	"ui.workspace_choose_cards": {
		ZH: "从 cards/ 选择词卡",
		EN: "Choose a word card from cards/",
	},
	"ui.workspace_choose_pending": {
		ZH: "从 pending/ 选择文件",
		EN: "Choose a file from pending/",
	},
	"ui.workspace_choose_mapping": {
		ZH: "从 mapping/ 选择映射",
		EN: "Choose a mapping from mapping/",
	},
	"ui.workspace_refresh": {
		ZH: "刷新",
		EN: "Refresh",
	},
	"ui.workspace_loading": {
		ZH: "列表加载中…",
		EN: "Loading list…",
	},
	"ui.workspace_empty": {
		ZH: "没有符合条件的直属文件。",
		EN: "No matching direct files.",
	},
	"ui.workspace_count": {
		ZH: "{count} 个直属文件",
		EN: "{count} direct files",
	},
	"ui.workspace_origin": {
		ZH: "来源：{dir}/",
		EN: "Source: {dir}/",
	},
	"ui.mapping_source_workspace": {
		ZH: "来源：mapping/ 目录文件（{name}）",
		EN: "Source: file in mapping/ ({name})",
	},
	"ui.execute_mask": {
		ZH: "执行脱敏",
		EN: "Mask",
	},
	"ui.execute_restore": {
		ZH: "执行还原",
		EN: "Restore",
	},
	"ui.profile_label": {
		ZH: "方案",
		EN: "Profile",
	},
	"ui.profile_readonly": {
		ZH: "内置方案只读；需要逐项调整请选“自定义”",
		EN: "Built-in profiles are read-only; choose Custom to adjust rules",
	},
	"ui.rules_legend": {
		ZH: "规则开关矩阵（内置方案只读，自定义可调）",
		EN: "Rule matrix (built-in read-only, Custom editable)",
	},
	"ui.summary_enabled": {
		ZH: "启用规则：{enabled}",
		EN: "Enabled rules: {enabled}",
	},
	"ui.summary_none": {
		ZH: "无启用规则",
		EN: "No rules enabled",
	},
	"ui.rule_details": {
		ZH: "规则详情",
		EN: "Rule details",
	},
	"ui.words_hint": {
		ZH: "按字面量精确匹配（大小写敏感）；本次词与常备词共同参与本次脱敏，不勾选的不参与。",
		EN: "Literal exact match (case-sensitive). Session words join built-in words; unchecked words do not participate.",
	},
	"ui.word_text_label": {
		ZH: "文本框手输（每行一个）",
		EN: "Type words (one per line)",
	},
	"ui.word_text_placeholder": {
		ZH: "例如：内部项目代号",
		EN: "e.g. internal project codename",
	},
	"ui.word_file_label": {
		ZH: "导入 txt 词文件（回车分隔，导入会替换上次导入词并保留手输词）",
		EN: "Import a txt word file (newline-separated; replaces the last imported words, keeps typed words)",
	},
	"ui.words_search_placeholder": {
		ZH: "搜索词（仅过滤显示）",
		EN: "Search words (display filter only)",
	},
	"ui.words_select_all": {
		ZH: "全选",
		EN: "Select all",
	},
	"ui.words_deselect_all": {
		ZH: "取消全选",
		EN: "Deselect all",
	},
	"ui.words_select_search": {
		ZH: "选择搜索结果",
		EN: "Select search results",
	},
	"ui.words_deselect_search": {
		ZH: "取消选择搜索结果",
		EN: "Deselect search results",
	},
	"ui.words_summary": {
		ZH: "全局已选 {x} / 总 {y}{matchPart}",
		EN: "Selected {x} / {y}{matchPart}",
	},
	"ui.words_match_suffix": {
		ZH: "；匹配 {z}",
		EN: "; {z} matching",
	},
	"ui.words_import_meta": {
		ZH: "导入 {n} 行，去重后 {m} 词；",
		EN: "Imported {n} lines, {m} unique words; ",
	},
	"ui.words_disabled": {
		ZH: "当前方案未启用自定义词：填写与导入均不生效，提交时不附带自定义词。",
		EN: "The current profile does not enable custom words: typing and importing have no effect, and no word fields are submitted.",
	},
	"ui.words_empty": {
		ZH: "（暂无本次词；可在上方输入或导入）",
		EN: "(No session words yet; type or import above)",
	},
	"ui.word_imported_tag": {
		ZH: "（导入）",
		EN: "(imported)",
	},
	"ui.preview_title": {
		ZH: "高频命中预览确认",
		EN: "High-frequency match confirmation",
	},
	"ui.preview_desc": {
		ZH: "以下自定义词命中次数超过阈值，确认后才执行替换。",
		EN: "The following custom words appear above the threshold; replacement runs after you confirm.",
	},
	"ui.preview_extra_group": {
		ZH: "本次词（可取消勾选，取消即不参与本次）",
		EN: "Session words (uncheck to exclude from this run)",
	},
	"ui.preview_builtin_group": {
		ZH: "常备词（不可在此排除）",
		EN: "Built-in words (cannot be excluded here)",
	},
	"ui.preview_builtin_hint": {
		ZH: "如需排除常备词，请编辑 config.json 中的 custom_words 后重启应用。",
		EN: "To exclude built-in words, edit custom_words in config.json and restart the app.",
	},
	"ui.preview_continue": {
		ZH: "确认并继续",
		EN: "Confirm and continue",
	},
	"ui.preview_cancel": {
		ZH: "取消",
		EN: "Cancel",
	},
	"ui.restore_input_file": {
		ZH: "上传 LLM 成品文件",
		EN: "Upload the LLM output file",
	},
	"ui.restore_input_text": {
		ZH: "粘贴 LLM 回复文本",
		EN: "Paste the LLM reply text",
	},
	"ui.restore_mode_file": {
		ZH: "文件",
		EN: "File",
	},
	"ui.restore_mode_text": {
		ZH: "粘贴文本",
		EN: "Paste text",
	},
	"ui.restore_text_placeholder": {
		ZH: "可在此粘贴内容",
		EN: "Paste content here",
	},
	"ui.mapping_label": {
		ZH: "选择本机 mapping 文件（.mapping.json）",
		EN: "Choose a local mapping file (.mapping.json)",
	},
	"ui.use_current_mapping": {
		ZH: "使用当前映射",
		EN: "Use current mapping",
	},
	"ui.mapping_source_session": {
		ZH: "来源：本次脱敏映射（{name}）",
		EN: "Source: this session's mask mapping ({name})",
	},
	"ui.mapping_source_manual": {
		ZH: "来源：手动选择的配对文件（{name}）",
		EN: "Source: manually chosen paired file ({name})",
	},
	"ui.mapping_confirm_manual": {
		ZH: "请确认所选 mapping 来自本机上次脱敏后保存的配对文件，且与待还原文本匹配。继续？",
		EN: "Confirm the chosen mapping came from the paired file saved after the last mask on this machine and matches the text. Continue?",
	},
	"ui.mapping_required": {
		ZH: "请先选择 mapping 文件，或点击“使用本次映射还原”。",
		EN: "Choose a mapping file first, or click \"Restore with this session's mapping\".",
	},
	"ui.result_mask_title": {
		ZH: "脱敏完成",
		EN: "Masking complete",
	},
	"ui.result_restore_title": {
		ZH: "还原完成",
		EN: "Restore complete",
	},
	"ui.artifact_masked": {
		ZH: "脱敏副本",
		EN: "Masked copy",
	},
	"ui.artifact_mapping": {
		ZH: "还原映射",
		EN: "Restore mapping",
	},
	"ui.artifact_restored": {
		ZH: "还原文件",
		EN: "Restored file",
	},
	"ui.save": {
		ZH: "保存",
		EN: "Save",
	},
	"ui.save_as": {
		ZH: "另存为",
		EN: "Save as",
	},
	"ui.status_unsaved": {
		ZH: "未保存",
		EN: "Unsaved",
	},
	"ui.status_saved": {
		ZH: "已保存",
		EN: "Saved",
	},
	"ui.status_failed": {
		ZH: "保存失败（可重试）",
		EN: "Save failed (retry possible)",
	},
	"ui.status_saving": {
		ZH: "保存中…",
		EN: "Saving…",
	},
	"ui.status_processing": {
		ZH: "处理中…",
		EN: "Processing…",
	},
	"ui.result_processing": {
		ZH: "正在处理，请稍候…",
		EN: "Processing, please wait…",
	},
	"ui.result_done_other_view": {
		ZH: "另一视图已完成，点击查看结果。",
		EN: "The other view finished; click to view the result.",
	},
	"ui.saved_to": {
		ZH: "{artifact}已保存到 {path}",
		EN: "{artifact} saved to {path}",
	},
	"ui.auto_save_failed": {
		ZH: "自动落盘失败：{message}，可手动另存。",
		EN: "Auto-save failed: {message}. You can save manually.",
	},
	"ui.partial_masked_saved": {
		ZH: "脱敏副本已保存，但配对 mapping 尚未保存。",
		EN: "The masked copy is saved, but the paired mapping is not.",
	},
	"ui.partial_mapping_saved": {
		ZH: "配对 mapping 已保存，但脱敏副本尚未保存。",
		EN: "The paired mapping is saved, but the masked copy is not.",
	},
	"ui.output_docx": {
		ZH: "输出格式：DOCX（不对整个容器显示文本编码）",
		EN: "Output format: DOCX (no single text encoding for the container)",
	},
	"ui.output_pptx": {
		ZH: "输出格式：PPTX（不对整个容器显示文本编码）",
		EN: "Output format: PPTX (no single text encoding for the container)",
	},
	"ui.output_text": {
		ZH: "输出格式：文本（{encoding}）",
		EN: "Output format: text ({encoding})",
	},
	"ui.input_docx": {
		ZH: "文档格式：DOCX（不对整个容器显示文本编码）",
		EN: "Document format: DOCX (no single text encoding for the container)",
	},
	"ui.input_pptx": {
		ZH: "文档格式：PPTX（保留可编辑结构；不保证视觉排版不变）",
		EN: "Document format: PPTX (keeps editable structure; visual layout is not guaranteed to stay identical)",
	},
	"ui.detected_encoding": {
		ZH: "检出编码：{encoding}",
		EN: "Detected encoding: {encoding}",
	},
	"ui.restored_count": {
		ZH: "还原占位符 {count} 处",
		EN: "Restored {count} placeholders",
	},
	"ui.cleanup_title": {
		ZH: "本次清理结果：",
		EN: "Cleanup result:",
	},
	"ui.cleanup_comments": {
		ZH: "删除批注 {count} 处",
		EN: "Deleted {count} comments",
	},
	"ui.cleanup_revisions": {
		ZH: "接受修订 {count} 处",
		EN: "Accepted {count} revisions",
	},
	"ui.cleanup_parts": {
		ZH: "移除部件 {count} 个",
		EN: "Removed {count} parts",
	},
	"ui.cleanup_resources": {
		ZH: "清理历史专属资源 {count} 个",
		EN: "Cleaned {count} history-only resources",
	},
	"ui.cleanup_pptx_title": {
		ZH: "本次 PPTX 清理结果：",
		EN: "PPTX cleanup result:",
	},
	"ui.cleanup_pptx_comments": {
		ZH: "删除批注 {count} 处",
		EN: "Deleted {count} comments",
	},
	"ui.cleanup_pptx_people": {
		ZH: "移除批注人员部件 {count} 个",
		EN: "Removed {count} comment-people parts",
	},
	"ui.cleanup_pptx_parts": {
		ZH: "移除批注家族部件 {count} 个",
		EN: "Removed {count} comment-family parts",
	},
	"ui.cleanup_pptx_resources": {
		ZH: "清理批注专属资源 {count} 个",
		EN: "Cleaned {count} comment-only resources",
	},
	"ui.narrow_result_hint": {
		ZH: "结果已更新，请查看结果区。",
		EN: "The result is updated; see the result area.",
	},
	"ui.busy_execute": {
		ZH: "有操作正在进行，请等待完成。",
		EN: "An operation is in progress; wait for it to finish.",
	},
	"ui.confirm_reexecute": {
		ZH: "重新执行将替换本视图中尚未保存的结果。确定继续吗？",
		EN: "Re-running will replace the unsaved results in this view. Continue?",
	},
	"ui.location_text": {
		ZH: "位置：{loc}",
		EN: "Location: {loc}",
	},
	"ui.multi_file_rejected": {
		ZH: "一次只能选择一个文件，请重新选择一个文件。",
		EN: "Only one file can be selected at a time; choose a single file.",
	},
	"ui.unsupported_word_file": {
		ZH: "无法按 UTF-8 或 GBK 解码词文件，请转码后重试。",
		EN: "The word file could not be decoded as UTF-8 or GBK; transcode it and retry.",
	},
	"ui.word_file_too_large": {
		ZH: "词文件超过 1MB 上限，请拆分或精简后重试。",
		EN: "The word file exceeds the 1MB limit; split or trim it and retry.",
	},
	"ui.word_file_binary": {
		ZH: "检测到二进制或 UTF-16 内容，不是受支持的纯文本，请另存为 UTF-8 或 GBK 后重试。",
		EN: "Binary or UTF-16 content detected; this is not supported plain text. Save it as UTF-8 or GBK and retry.",
	},
	"ui.word_line_too_long": {
		ZH: "导入第 {line} 行：自定义词超过 256 字符上限。整批已拒绝，请修正后重试。",
		EN: "Imported line {line}: the custom word exceeds the 256-character limit. The whole batch was rejected; fix it and retry.",
	},
	"ui.word_file_empty": {
		ZH: "未解析到任何有效自定义词（空文件或全为空行），请检查后重试。",
		EN: "No valid custom words were parsed (empty file or only blank lines); check it and retry.",
	},
	"ui.word_file_too_many": {
		ZH: "导入词数（去重后）超过 1000 个上限，请删减后重试。",
		EN: "The imported word count (after de-duplication) exceeds the 1000-word limit; remove some and retry.",
	},
	"ui.word_selected_too_many": {
		ZH: "本次勾选的自定义词超过 1000 个上限，请取消部分勾选后重试。",
		EN: "The checked session custom words exceed the 1000-word limit; uncheck some and retry.",
	},
	"ui.exit": {
		ZH: "退出应用",
		EN: "Exit app",
	},
	"ui.exit_busy": {
		ZH: "处理或保存正在进行，请等待完成后再退出。",
		EN: "Processing or saving is in progress; wait for it to finish before exiting.",
	},
	"ui.exited_title": {
		ZH: "应用已退出",
		EN: "Application exited",
	},
	"ui.request_failed": {
		ZH: "请求失败：{detail}",
		EN: "Request failed: {detail}",
	},
	"ui.offline_unknown": {
		ZH: "操作未完成，请检查输入后重试。",
		EN: "The operation did not complete. Check your input and try again.",
	},
	"ui.copy_suffix": {
		ZH: "副本",
		EN: "copy",
	},
}
