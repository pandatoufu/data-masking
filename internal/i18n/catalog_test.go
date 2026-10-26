package i18n

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

var paramNameRe = regexp.MustCompile(`\{([a-zA-Z0-9_.]+)\}`)

func keys(m map[Lang]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, string(k))
	}
	sort.Strings(out)
	return out
}

// 键集合与参数占位在 zh/en 间必须一致：避免某语言缺文案或参数漂移。
func TestCatalogBilingualKeysAndParamsConsistent(t *testing.T) {
	for id, m := range catalog {
		if _, ok := m[ZH]; !ok {
			t.Errorf("%s 缺少 zh 文案", id)
			continue
		}
		if _, ok := m[EN]; !ok {
			t.Errorf("%s 缺少 en 文案", id)
			continue
		}
		zh := paramNames(m[ZH])
		en := paramNames(m[EN])
		if strings.Join(zh, ",") != strings.Join(en, ",") {
			t.Errorf("%s 参数占位不一致：zh=%v en=%v", id, zh, en)
		}
	}
}

func paramNames(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range paramNameRe.FindAllStringSubmatch(s, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return out
}

// 通用失败与未知 id 必须给出所选语言的可读说明，不能回退中文。
func TestUnknownIDFallsBackToSelectedLanguage(t *testing.T) {
	en := T(EN, "totally.unknown.id", map[string]any{"id": "totally.unknown.id"})
	if strings.ContainsAny(en, "请操作失败未分类") || !strings.Contains(en, "totally.unknown.id") {
		t.Fatalf("英文未知 id 回退应为英文并保留 id：%q", en)
	}
	zh := T(ZH, "totally.unknown.id", map[string]any{"id": "totally.unknown.id"})
	if !strings.Contains(zh, "totally.unknown.id") {
		t.Fatalf("中文未知 id 回退应保留 id：%q", zh)
	}
	if got := T(EN, IDErrorGeneric, nil); got == "" || strings.ContainsAny(got, "请") {
		t.Fatalf("英文通用失败应为英文：%q", got)
	}
}

// 参数插值：普通值原样、缺失占位保留、嵌套子消息按当前语言解析。
func TestInterpolationAndNested(t *testing.T) {
	got := T(ZH, "profile.unknown", map[string]any{"profile": `b"ad`, "options": "a、b"})
	if !strings.Contains(got, `b"ad`) {
		t.Fatalf("参数应原样插值（Go 侧不做 HTML 转义，由前端渲染层负责）：%q", got)
	}
	// 缺失参数：未知 id 模板 {id} 由调用方提供；显式测缺失时保留占位。
	got = T(ZH, "file.too_large", map[string]any{"size": "11MB"})
	if !strings.Contains(got, "{limit}") {
		t.Fatalf("缺失参数应保留占位以便诊断：%q", got)
	}
	// 嵌套参数：words.line_invalid 的 detail 为子消息。
	nested := T(EN, "words.line_invalid", map[string]any{
		"source": "word file",
		"line":   3,
		"detail": N("word.too_long", map[string]any{"max": 256}),
	})
	if !strings.Contains(nested, "line 3") || !strings.Contains(nested, "256-character limit") {
		t.Fatalf("嵌套子消息应按英文解析：%q", nested)
	}
}

// 每种语言都能拿到非空 Messages；Manifest 覆盖所有键。
func TestMessagesAndManifest(t *testing.T) {
	zh := Messages(ZH)
	en := Messages(EN)
	if len(zh) == 0 || len(en) == 0 {
		t.Fatal("Messages 不应为空")
	}
	if len(Manifest()) != len(catalog) {
		t.Fatalf("Manifest 应覆盖全部 catalog：%d vs %d", len(Manifest()), len(catalog))
	}
	// 通用回退必须注册。
	for _, id := range []string{IDErrorGeneric, IDErrorUnknownID, IDWarningGeneric} {
		if !Has(id, ZH) || !Has(id, EN) {
			t.Fatalf("通用回退 %s 必须双语注册", id)
		}
	}
}

// 资源必须完全嵌入：catalog 不得包含 URL/在线引用。
func TestCatalogHasNoOnlineReferences(t *testing.T) {
	for id, m := range catalog {
		for _, s := range m {
			if strings.Contains(s, "http://") || strings.Contains(s, "https://") {
				t.Errorf("%s 文案含在线引用：%q", id, s)
			}
		}
	}
}

// 键集合快照：新增 message_id 必须显式出现在此清单，防止静默漂移。
func TestManifestStable(t *testing.T) {
	want := map[string]bool{
		IDErrorGeneric: true, IDErrorUnknownID: true, IDWarningGeneric: true,
		"file.unsupported_format": true, "file.too_large": true, "file.too_large_stream": true,
		"decode.binary_or_utf16": true, "decode.unsupported_encoding": true, "encode.gbk_failed": true,
		"word.empty": true, "word.whitespace_only": true, "word.contains_newline": true, "word.too_long": true,
		"words.file_too_large": true, "words.line_invalid": true, "words.too_many": true,
		"words.merge_too_many": true, "words.none": true,
		"words.source_text": true, "words.source_file": true,
		"collision.grammar_text": true, "collision.grammar_docx": true, "collision.grammar_pptx": true,
		"preview.required": true, "preview.hit": true, "preview.empty_group": true,
		"profile.unknown": true, "rule.only_custom": true, "rule.unknown_field": true,
		"rule.need_single_bool": true, "rule.bad_bool": true, "http.method_post_only": true,
		"http.form_parse_failed": true, "http.mask_file_missing": true, "http.file_read_failed": true,
		"http.form_cleanup_failed": true,
		"http.operation_busy":      true,
		"http.mapping_missing":     true, "http.mapping_read_failed": true, "http.restore_input_missing": true,
		"exit.ok": true, "exit.blocked": true,
		"mapping.json_invalid": true, "mapping.version_unsupported": true, "mapping.v2_format_invalid": true,
		"mapping.v2_encoding_invalid": true, "mapping.v2_source_sha_invalid": true, "mapping.v2_text_sha_invalid": true,
		"mapping.v3_format_invalid": true, "mapping.v3_encoding_invalid": true,
		"mapping.v3_source_sha_invalid": true, "mapping.v3_text_sha_invalid": true,
		"mapping.v3_placeholders_missing": true,
		"mapping.generic_invalid":         true,
		"docx.not_docx":                   true, "docx.encrypted_ole": true, "docx.zip_budget": true,
		"docx.part_name_invalid": true, "docx.content_types_invalid": true, "docx.relationship_invalid": true,
		"docx.missing_part": true, "docx.macro_unsupported": true, "docx.digital_signature_unsupported": true,
		"docx.comment_family_unsupported": true, "docx.unsupported_revision": true,
		"docx.xml_parse_failed": true, "docx.alt_chunk_unsupported": true, "docx.output_validation_failed": true,
		"pptx.not_pptx": true, "pptx.encrypted_ole": true, "pptx.zip_budget": true,
		"pptx.part_name_invalid": true, "pptx.content_types_invalid": true, "pptx.relationship_invalid": true,
		"pptx.missing_part": true, "pptx.macro_unsupported": true, "pptx.digital_signature_unsupported": true,
		"pptx.comment_family_unsupported": true, "pptx.xml_parse_failed": true, "pptx.text_unsupported": true,
		"pptx.output_validation_failed": true, "pptx.combination_unsupported": true, "pptx.internal": true,
		"warning.residual_forward": true, "warning.residual_forward_docx": true, "warning.residual_forward_pptx": true,
		"warning.residual_reverse": true, "warning.residual_reverse_docx": true, "warning.residual_reverse_pptx": true,
		"warning.sha_mismatch": true, "warning.cross_format": true, "warning.docx_history": true,
		"warning.text_mismatch": true, "warning.pptx_history": true, "warning.pptx_text_mismatch": true,
		"feedback.audit_failed": true,
		"save.in_progress":      true, "save.bad_base64": true, "save.no_dialog": true, "save.dialog_failed": true,
		"save.cancelled": true, "save.dir_missing": true, "save.dir_unreadable": true, "save.parent_not_dir": true,
		"save.target_exists": true, "save.target_uncheckable": true, "save.temp_failed": true,
		"save.write_failed": true, "save.sync_failed": true, "save.close_failed": true,
		"save.race_exists": true, "save.link_unsupported": true, "save.link_failed": true,
		"save.saved": true, "save.cleanup_warning": true, "save.name_missing": true,
		"save.name_has_separator": true, "save.name_invalid": true,
		"workspace.unavailable": true, "workspace.unresolved_root": true, "workspace.bad_name": true,
		"workspace.bad_base64": true, "workspace.target_check_failed": true, "workspace.temp_failed": true,
		"workspace.too_many": true, "workspace.no_derived_name": true, "workspace.link_unsupported": true,
		"workspace.saved": true, "workspace.busy": true,
		"input.bad_kind": true, "input.root_unavailable": true, "input.dir_unavailable": true,
		"input.list_failed": true, "input.bad_name": true, "input.unsupported_format": true,
		"input.file_missing": true, "input.not_regular": true, "input.changed": true,
		"input.read_failed": true, "input.too_large": true, "input.busy": true,
		"native.app_title": true, "native.startup_failed_title": true, "native.session_bus_unavailable": true,
		"native.workspace_unavailable_title": true, "native.close_blocked_title": true,
		"native.close_blocked_body": true, "native.confirm_exit_title": true,
		"native.confirm_close_legacy": true, "native.confirm_close_workspace": true,
		"native.save_dialog_title": true, "native.linux_webkit_required": true,
		"native.webview2_installation_required": true, "native.webview2_update_required": true,
		"native.webview2_missing_requirements": true, "native.webview2_not_installed": true,
		"native.webview2_error": true, "native.webview2_failed_install": true,
		"native.webview2_download_page": true, "native.webview2_press_ok": true,
		"native.webview2_contact_admin": true, "native.webview2_invalid_fixed": true,
		"native.webview2_process_crash": true, "native.launch_failed": true, "native.desktop_hint": true,
		"prefs.save_failed": true, "prefs.invalid": true,
	}
	for id := range want {
		if !Has(id, ZH) || !Has(id, EN) {
			t.Errorf("期望的 message_id %s 未双语注册", id)
		}
	}
	for id := range catalog {
		if strings.HasPrefix(id, "ui.") {
			continue // 前端静态 UI 字典由 TestStaticUIDictionaryPresent 覆盖
		}
		if !want[id] {
			t.Errorf("catalog 出现未在快照登记的 message_id %s", id)
		}
	}
}

// 第三轮 9.2：标题精确双语，无旧二级标题键。
func TestThirdRoundTitlesExact(t *testing.T) {
	exact := map[string][2]string{
		"ui.adjust_words":        {"自定义词", "Custom words"},
		"ui.rule_details":        {"规则详情", "Rule details"},
		"ui.use_current_mapping": {"使用当前映射", "Use current mapping"},
		"ui.pick_mapping":        {"选择映射", "Choose mapping"},
	}
	for id, pair := range exact {
		if got := catalog[id][ZH]; got != pair[0] {
			t.Errorf("%s zh=%q want %q", id, got, pair[0])
		}
		if got := catalog[id][EN]; got != pair[1] {
			t.Errorf("%s en=%q want %q", id, got, pair[1])
		}
	}
	for _, gone := range []string{"ui.words_title", "ui.details_toggle", "ui.mapping_use_session"} {
		if _, ok := catalog[gone]; ok {
			t.Errorf("旧标题键 %s 应已移除", gone)
		}
	}
}

// 第五轮决策10：三产物类型与单一成功句精确双语；旧“已自动保存到”重复行键移除。
func TestFifthRoundArtifactTypeAndSavedSentenceExact(t *testing.T) {
	exact := map[string][2]string{
		"ui.artifact_masked":   {"脱敏副本", "Masked copy"},
		"ui.artifact_mapping":  {"还原映射", "Restore mapping"},
		"ui.artifact_restored": {"还原文件", "Restored file"},
		"ui.saved_to":          {"{artifact}已保存到 {path}", "{artifact} saved to {path}"},
	}
	for id, pair := range exact {
		m, ok := catalog[id]
		if !ok {
			t.Errorf("缺少 message_id %s", id)
			continue
		}
		if m[ZH] != pair[0] {
			t.Errorf("%s zh=%q want %q", id, m[ZH], pair[0])
		}
		if m[EN] != pair[1] {
			t.Errorf("%s en=%q want %q", id, m[EN], pair[1])
		}
	}
	if _, ok := catalog["ui.auto_saved_to"]; ok {
		t.Error("旧 ui.auto_saved_to 键应移除，不再显示自动保存重复行")
	}
}

// 本轮：还原文件按钮中文精确“选择还原文件”，英文精确 “choose file to restore”。
func TestPickReplyFileExactWording(t *testing.T) {
	if got := catalog["ui.pick_reply_file"][ZH]; got != "选择还原文件" {
		t.Errorf("ui.pick_reply_file zh=%q want %q", got, "选择还原文件")
	}
	if got := catalog["ui.pick_reply_file"][EN]; got != "choose file to restore" {
		t.Errorf("ui.pick_reply_file en=%q want %q", got, "choose file to restore")
	}
}

// 确保 ui.* 静态文案键存在（供前端首帧与语言切换）。
func TestStaticUIDictionaryPresent(t *testing.T) {
	for _, id := range []string{
		"ui.app_title", "ui.view_mask", "ui.view_restore", "ui.theme_label", "ui.theme_system",
		"ui.theme_light", "ui.theme_dark", "ui.language_label", "ui.execute_mask", "ui.execute_restore",
		"ui.words_select_all", "ui.words_deselect_all", "ui.words_select_search", "ui.words_deselect_search",
		"ui.save", "ui.save_as", "ui.status_saved", "ui.status_unsaved", "ui.status_saving", "ui.status_processing",
		"ui.preview_continue", "ui.use_current_mapping", "ui.exit", "ui.exited_title",
		"ui.rule_details", "ui.adjust_words", "ui.pick_mapping", "ui.pick_file", "ui.pick_word_file", "ui.pick_reply_file",
		"ui.drop_hint", "ui.click_hint", "ui.click_to_choose",
	} {
		if !Has(id, ZH) || !Has(id, EN) {
			t.Errorf("静态 UI 文案 %s 未双语注册", id)
		}
	}
}
