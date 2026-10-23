package desktop

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pandatoufu/data-masking/internal/i18n"
)

func repoFile(t *testing.T, parts ...string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	b, err := os.ReadFile(filepath.Join(append([]string{root}, parts...)...))
	if err != nil {
		t.Fatalf("读取 %v 失败：%v", parts, err)
	}
	return string(b)
}

// 8.1/8.6/8.7 静态 DOM/CSS/JS 契约：紧凑布局作用域、移除常驻块与统一警告框、二层词折叠默认收起。
func TestCompactLayoutStaticContract(t *testing.T) {
	html := repoFile(t, "web", "static", "index.html")
	css := repoFile(t, "web", "static", "style.css")
	js := repoFile(t, "web", "static", "app.js")

	// 移除常驻块（不是隐藏占位）。
	for _, banned := range []string{
		`id="docx-scope"`, `id="docx-restore-scope"`,
		`data-i18n="ui.restore_intro"`, `data-i18n="ui.mapping_local_warning"`,
	} {
		if strings.Contains(html, banned) {
			t.Errorf("index.html 不应再包含常驻块 %q", banned)
		}
	}
	// 四类紧凑文件按钮与原生 input。
	for _, id := range []string{`id="mask-file-area"`, `id="word-file-area"`, `id="restore-file-area"`, `id="restore-mapping-area"`} {
		if !strings.Contains(html, id) {
			t.Errorf("缺少紧凑文件按钮 %q", id)
		}
	}
	for _, inp := range []string{`id="mask-file"`, `id="custom-file"`, `id="restore-file"`, `id="restore-mapping"`} {
		if !strings.Contains(html, inp) {
			t.Errorf("缺少原生 file input %q", inp)
		}
	}
	// 内层“调整参与词”默认收起。
	if !strings.Contains(html, `id="word-adjust"`) {
		t.Error("缺少内层调整参与词 details")
	}
	if strings.Contains(html, `id="word-adjust" class="word-adjust" open`) || strings.Contains(html, `id="word-adjust" open`) {
		t.Error("内层调整参与词应默认收起（无 open）")
	}

	// 顶部偏好作用域：标签不缩、select 内容宽度、组间距；不再全局污染。
	if !strings.Contains(css, ".pref-label > span { white-space: nowrap") {
		t.Error("偏好标签应 nowrap 不缩")
	}
	if !strings.Contains(css, ".pref-label select { width: auto") {
		t.Error("偏好 select 应为内容宽度")
	}
	if !strings.Contains(css, ".prefs { display: flex; flex-wrap: wrap; align-items: center; gap: 16px; }") {
		t.Error("偏好组间应约 16px 且整组换行")
	}
	if strings.Contains(css, "select,\n  textarea") || strings.Contains(css, "select,\r\n  textarea") {
		t.Error("不应有裸 select 全局 width:100% 规则")
	}
	// 结果无背景行与绿色文件操作。
	if !strings.Contains(css, ".result-row {") {
		t.Error("结果应为无背景 result-row")
	}
	if !strings.Contains(css, ".file-btn {") || !strings.Contains(css, "var(--ok-bg)") {
		t.Error("文件按钮应使用绿色 token")
	}

	// JS：无统一警告标题/旧常驻提醒；有单次短提醒与 DOCX 未检查短摘要。
	for _, banned := range []string{"ui.mapping_local_warning", "ui.warnings_title", "docxScopeHTML"} {
		if strings.Contains(js, banned) {
			t.Errorf("app.js 不应再引用 %q", banned)
		}
	}
	for _, want := range []string{"ui.mapping_reminder_short", "ui.docx_unchecked_short", "feedback-list"} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js 应包含 %q", want)
		}
	}
	// 拖入目标判定必须用实际按钮 selector .file-btn，而非旧 .file-area。
	if !strings.Contains(js, `closest(".file-btn")`) {
		t.Error("isFileAreaTarget 应使用 .file-btn selector")
	}
	if strings.Contains(js, `closest(".file-area")`) {
		t.Error("不应再使用旧 .file-area selector")
	}
}

// 9.1–9.4 第三轮静态契约：导入在唯一折叠外、summary marker、统一控制尺寸、映射同行右对齐。
func TestThirdRoundStaticContract(t *testing.T) {
	html := repoFile(t, "web", "static", "index.html")
	css := repoFile(t, "web", "static", "style.css")

	// 词区为非 details；导入/文件名/失活/失败在唯一折叠外。
	if strings.Contains(html, `<details id="word-area"`) {
		t.Error("词区不应再是 details")
	}
	importIdx := strings.Index(html, `id="word-file-area"`)
	disabledIdx := strings.Index(html, `id="word-disabled-hint"`)
	feedbackIdx := strings.Index(html, `id="word-feedback"`)
	adjustIdx := strings.Index(html, `<details id="word-adjust"`)
	if importIdx < 0 || disabledIdx < 0 || feedbackIdx < 0 || adjustIdx < 0 {
		t.Fatalf("缺少词区元素：import=%d disabled=%d feedback=%d adjust=%d", importIdx, disabledIdx, feedbackIdx, adjustIdx)
	}
	if !(importIdx < adjustIdx && disabledIdx < adjustIdx && feedbackIdx < adjustIdx) {
		t.Error("导入/失活/失败反馈应在唯一折叠 details 之前（折叠外）")
	}
	if !strings.Contains(html, `data-i18n="ui.adjust_words"`) || strings.Contains(html, `ui.words_title`) {
		t.Error("唯一自定义词标题应为 ui.adjust_words，且无旧二级标题")
	}
	if !strings.Contains(html, `id="word-adjust-count"`) {
		t.Error("summary 应含已选/总数摘要")
	}
	// summary marker：不得用 display:flex / list-style:none 吞掉原生三角。
	if i := strings.Index(css, ".word-adjust > summary"); i >= 0 {
		rule := css[i:]
		if j := strings.Index(rule, "}"); j >= 0 {
			rule = rule[:j]
		}
		if strings.Contains(rule, "display: flex") || strings.Contains(rule, "list-style: none") {
			t.Errorf("summary 规则不应吞掉原生展开三角：%q", rule)
		}
	} else {
		t.Error("缺少 .word-adjust > summary 规则")
	}
	// 统一控制尺寸：primary 与 file-btn 共享 38px/字号/内边距/border-box。
	if !strings.Contains(css, "button.primary, .file-btn {") {
		t.Error("缺少 primary 与 file-btn 共享尺寸规则")
	}
	if !strings.Contains(css, "height: 38px") || !strings.Contains(css, "box-sizing: border-box") {
		t.Error("共享控制高度应为 38px 且 border-box")
	}
	// 映射同行：选择映射→使用当前映射→来源/名称在第二行；拖入说明不在动作行。
	mIdx := strings.Index(html, `id="restore-mapping-area"`)
	useIdx := strings.Index(html, `id="use-session-mapping"`)
	srcIdx := strings.Index(html, `id="mapping-source"`)
	if !(mIdx >= 0 && useIdx > mIdx && srcIdx > useIdx) {
		t.Errorf("映射操作顺序应选择映射→使用当前映射→来源下方：m=%d use=%d src=%d", mIdx, useIdx, srcIdx)
	}
	actionsStart := strings.Index(html, `class="mapping-actions"`)
	actionsEnd := strings.Index(html, `class="mapping-meta"`)
	if actionsStart < 0 || actionsEnd < actionsStart {
		t.Fatal("缺少 mapping-actions/mapping-meta")
	}
	if strings.Contains(html[actionsStart:actionsEnd], `ui.drop_hint`) {
		t.Error("拖入说明应在第二行 mapping-meta，不挤动作行")
	}
	if !strings.Contains(css, ".mapping-use { flex: 0 0 auto; margin-left: auto; }") {
		t.Error("使用当前映射应右对齐")
	}
	for _, want := range []string{"ui.rule_details", "ui.use_current_mapping", "ui.adjust_words"} {
		if !strings.Contains(html, want) {
			t.Errorf("页面应使用键 %q", want)
		}
	}
}

// 10.3/10.4 第四轮静态契约：平台能力提示、正常字重 summary、区域分隔与快照式选择。
func TestFourthRoundStaticContract(t *testing.T) {
	html := repoFile(t, "web", "static", "index.html")
	css := repoFile(t, "web", "static", "style.css")
	js := repoFile(t, "web", "static", "app.js")

	// 四入口提示为能力驱动标记，初始为中性点击文案；不再静态绑定 Windows 拖入键。
	if got := strings.Count(html, `data-i18n-file-hint`); got != 4 {
		t.Errorf("四入口应使用 data-i18n-file-hint，got %d", got)
	}
	if strings.Contains(html, `data-i18n="ui.drop_hint"`) {
		t.Error("提示不应静态绑定 ui.drop_hint，应由能力决定")
	}
	if !strings.Contains(js, `cfg.file_drop_enabled === true`) || !strings.Contains(js, "FILE_DROP_ENABLED") {
		t.Error("app.js 应按只读 file_drop_enabled 保守取值")
	}
	if !strings.Contains(js, "ui.click_to_choose") || !strings.Contains(js, "ui.click_hint") {
		t.Error("app.js 应含仅点击提示与误拖提示键")
	}
	// 选择先快照再重置，且不依赖 input.files setter。
	if !strings.Contains(js, "Array.from(files)") || !strings.Contains(js, "var picked = Array.from(files)") {
		t.Error("change 应先复制独立 File 快照再重置控件")
	}
	if strings.Contains(js, "input.files =") {
		t.Error("不应再依赖 input.files setter（Windows 不支持）")
	}

	// 规则详情/自定义词 summary 正常字重 400 + 手形指针（保留原生 marker）。
	if !strings.Contains(css, ".profile-details > summary, .word-adjust > summary { cursor: pointer; font-weight: 400; }") {
		t.Error("两个折叠标题应为 font-weight:400 + cursor:pointer")
	}
	// 分隔：word-adjust 内部无虚线，word-block 顶部一条；两执行区各上方一条且不叠双线。
	if strings.Contains(css, ".word-adjust { margin-top: 0.5rem; padding-top: 0.4rem; border-top: 1px dashed") {
		t.Error("word-adjust 顶部虚线应移除（分隔上移到词区整体之前）")
	}
	if !strings.Contains(css, ".word-block { border-top: 1px solid var(--border); border-bottom: 0; }") {
		t.Error("word-block 顶部应有一条分隔且不复画底边")
	}
	if !strings.Contains(css, ".execute-actions { margin-top: 0.5rem; padding-top: 0.6rem; border-top: 1px solid var(--border); }") {
		t.Error("两执行区上方应有唯一分隔")
	}
	if got := strings.Count(html, `class="execute-actions"`); got != 2 {
		t.Errorf("脱敏/还原各应有一个 execute-actions，got %d", got)
	}
	maskActions := strings.Index(html, `class="execute-actions"`)
	maskSubmit := strings.Index(html, `id="mask-submit"`)
	restoreActions := strings.LastIndex(html, `class="execute-actions"`)
	restoreSubmit := strings.Index(html, `id="restore-submit"`)
	if !(0 <= maskActions && maskActions < maskSubmit && maskSubmit < restoreActions && restoreActions < restoreSubmit) {
		t.Errorf("execute-actions 应包住各自主执行按钮：%d %d %d %d", maskActions, maskSubmit, restoreActions, restoreSubmit)
	}
}

// 纵向分区静态契约：文件区独立在上，方案区在其下纵向排列，分隔线归属唯一，且不使用同排规则。
// 静态断言只证明 DOM/CSS 结构，不代表像素目检通过。
func TestVerticalFileProfileLayoutStaticContract(t *testing.T) {
	html := repoFile(t, "web", "static", "index.html")
	css := repoFile(t, "web", "static", "style.css")

	fileIdx := strings.Index(html, `class="block file-block"`)
	profileIdx := strings.Index(html, `class="block profile-block"`)
	wordIdx := strings.Index(html, `class="block word-block"`)
	if fileIdx < 0 || profileIdx < 0 || wordIdx < 0 {
		t.Fatalf("缺少纵向块：file=%d profile=%d word=%d", fileIdx, profileIdx, wordIdx)
	}
	if !(fileIdx < profileIdx && profileIdx < wordIdx) {
		t.Errorf("块顺序应为文件→方案→词区：file=%d profile=%d word=%d", fileIdx, profileIdx, wordIdx)
	}

	// 文件块不含任何方案控件，也不与方案同排。
	fileChunk := html[fileIdx:profileIdx]
	for _, banned := range []string{`id="profile-select"`, `id="profile-summary"`, `id="profile-details"`, `class="field-label profile-field"`} {
		if strings.Contains(fileChunk, banned) {
			t.Errorf("文件块不应包含方案控件 %q", banned)
		}
	}
	if !strings.Contains(fileChunk, `id="mask-file-area"`) || !strings.Contains(fileChunk, `id="mask-file-name"`) {
		t.Error("文件块应保留文件按钮与名称")
	}

	// 方案块：标签/下拉 → 生效摘要 → 规则详情，依次纵向。
	profileChunk := html[profileIdx:wordIdx]
	selIdx := strings.Index(profileChunk, `id="profile-select"`)
	sumIdx := strings.Index(profileChunk, `id="profile-summary"`)
	detIdx := strings.Index(profileChunk, `id="profile-details"`)
	if !(selIdx >= 0 && sumIdx > selIdx && detIdx > sumIdx) {
		t.Errorf("方案块顺序应为 select→摘要→详情：%d %d %d", selIdx, sumIdx, detIdx)
	}
	if !strings.Contains(profileChunk, `class="field-label profile-field"`) {
		t.Error("方案块应包含方案标签")
	}
	if !strings.Contains(profileChunk, `id="profile-matrix-body"`) {
		t.Error("方案块应保留规则矩阵")
	}

	// ID 不得重复：脚本按 ID 读取状态，重复会让重排丢状态。
	seen := map[string]bool{}
	for i := 0; ; {
		j := strings.Index(html[i:], `id="`)
		if j < 0 {
			break
		}
		start := i + j + len(`id="`)
		end := strings.Index(html[start:], `"`)
		if end < 0 {
			break
		}
		id := html[start : start+end]
		if seen[id] {
			t.Errorf("ID 重复：%q", id)
		}
		seen[id] = true
		i = start + end
	}

	// CSS：文件区无底边、方案区唯一顶线且无底边，词区/执行区原顶线保持。
	if strings.Contains(css, ".mask-top") {
		t.Error("不应保留 mask-top 横排规则及其窄窗覆盖")
	}
	if !strings.Contains(css, ".file-block { border-bottom: 0; }") {
		t.Error("文件区应无底边（分隔线归方案区）")
	}
	if !strings.Contains(css, ".profile-block { border-top: 1px solid var(--border); border-bottom: 0; }") {
		t.Error("方案区应有唯一顶线且无底边")
	}
	if !strings.Contains(css, ".word-block { border-top: 1px solid var(--border); border-bottom: 0; }") {
		t.Error("词区原有顶线应保持")
	}
	if !strings.Contains(css, ".execute-actions { margin-top: 0.5rem; padding-top: 0.6rem; border-top: 1px solid var(--border); }") {
		t.Error("执行区原有顶线应保持")
	}
	// 方案下拉按内容宽度，宽窗也不横排；文件名称仍可换行。
	if !strings.Contains(css, ".profile-field select { width: auto") {
		t.Error("方案下拉应为内容宽度")
	}
	if strings.Contains(css, ".profile-field { flex-basis: 100%; }") {
		t.Error("不应保留方案字段的窄窗横排覆盖")
	}
	if !strings.Contains(css, ".file-name { font-size: 0.82rem; color: var(--text); overflow-wrap: anywhere; }") {
		t.Error("长文件名应可完整换行")
	}
}

// 11.1 静态契约：单一成功句用稳定键、类型按 art.id、长路径可换行、旧自动保存行不再引用。
func TestFifthRoundSavedSentenceStaticContract(t *testing.T) {
	css := repoFile(t, "web", "static", "style.css")
	js := repoFile(t, "web", "static", "app.js")

	if !strings.Contains(js, `t("ui.saved_to"`) {
		t.Error("app.js 应使用 ui.saved_to 单一成功句")
	}
	for _, banned := range []string{"ui.auto_saved_to", "esc(art.label)"} {
		if strings.Contains(js, banned) {
			t.Errorf("app.js 不应再引用 %q", banned)
		}
	}
	if !strings.Contains(js, "ARTIFACT_TYPE_KEYS") || !strings.Contains(js, "artifactTypeLabel") {
		t.Error("app.js 应按 art.id 稳定取产物类型而非缓存 art.label")
	}
	if !strings.Contains(css, ".artifact-path {") || !strings.Contains(css, "overflow-wrap: anywhere") {
		t.Error("长实际路径应可换行而非裁切")
	}
}

// 本轮 3.1/4.2 静态契约：四类固定目录下拉 + 中性刷新 + 状态行；纵向布局与焦点可达，
// 长名称安全换行，不新增分隔线或挤压映射动作行。
func TestWorkspacePickerStaticContract(t *testing.T) {
	html := repoFile(t, "web", "static", "index.html")
	css := repoFile(t, "web", "static", "style.css")
	js := repoFile(t, "web", "static", "app.js")

	for _, kind := range []string{"original", "cards", "pending", "mapping"} {
		for _, id := range []string{kind + "-picker", kind + "-select", kind + "-refresh", kind + "-list-status"} {
			if !strings.Contains(html, `id="`+id+`"`) {
				t.Errorf("缺少固定目录快捷元素 %q", id)
			}
		}
	}
	for _, want := range []string{
		`data-i18n="ui.workspace_label_original"`, `data-i18n="ui.workspace_label_cards"`,
		`data-i18n="ui.workspace_label_pending"`, `data-i18n="ui.workspace_label_mapping"`,
		`data-i18n="ui.workspace_choose_original"`, `data-i18n="ui.workspace_choose_cards"`,
		`data-i18n="ui.workspace_choose_pending"`, `data-i18n="ui.workspace_choose_mapping"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("页面应使用键 %q", want)
		}
	}
	// 刷新按钮为中性 chip，且有用途标签；不引入 tabindex=-1 等焦点击穿。
	if got := strings.Count(html, `class="chip picker-refresh" data-i18n="ui.workspace_refresh"`); got != 4 {
		t.Errorf("四个刷新按钮应使用中性 chip 与统一文案，got %d", got)
	}
	if strings.Contains(html, `tabindex="-1"`) {
		t.Error("快捷入口不应移除键盘焦点")
	}
	// 长名称完整显示仍由 file-name/file-origin 承载，pick-status 可换行。
	if !strings.Contains(css, ".file-picker { display: flex; flex-wrap: wrap;") {
		t.Error("快捷组应整组换行")
	}
	if !strings.Contains(css, ".picker-field select {") || !strings.Contains(css, "max-width: 100%") {
		t.Error("目录下拉应按内容宽度且不溢出窄窗")
	}
	if !strings.Contains(css, ".picker-status { flex: 1 1 100%;") {
		t.Error("状态行应独占一行并可换行")
	}
	if !strings.Contains(css, ".file-origin {") || !strings.Contains(css, "overflow-wrap: anywhere") {
		t.Error("目录来源应可换行呈现")
	}
	// 映射动作行保持原顺序与右对齐，快捷行不插进动作行。
	actions := strings.Index(html, `class="mapping-actions"`)
	meta := strings.Index(html, `class="mapping-meta"`)
	picker := strings.Index(html, `id="mapping-picker"`)
	if !(actions >= 0 && meta > actions && picker > meta) {
		t.Errorf("映射动作/说明/快捷顺序应保持：actions=%d meta=%d picker=%d", actions, meta, picker)
	}
	if !strings.Contains(css, ".mapping-use { flex: 0 0 auto; margin-left: auto; }") {
		t.Error("使用当前映射应保持右对齐")
	}
	// JS：初始无默认文件、显式刷新/读取、来源与序号保护。
	for _, want := range []string{
		"initWorkspacePickers", "refreshWorkspaceList", "loadWorkspaceFile",
		"workspaceReadSeq", "seq !== st.seq", "seq !== appState.workspaceReadSeq[kind]",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js 应包含 %q", want)
		}
	}
	if strings.Contains(js, "setInterval") {
		t.Error("不得监听文件系统或轮询目录")
	}
	// 还原按钮文案精确（4.1）：HTML 回退与 catalog 一致。
	if !strings.Contains(html, `<span data-i18n="ui.pick_reply_file">选择还原文件</span>`) {
		t.Error("HTML 回退文字应为“选择还原文件”")
	}
	if strings.Contains(html, "选择回复文件") {
		t.Error("不应残留旧“选择回复文件”文案")
	}
}

// 8.8 README 中英文完整记录 DOCX/还原边界，并互相链接。
func TestReadmeBoundaryContract(t *testing.T) {
	zh := repoFile(t, "README.md")
	en := repoFile(t, "README.en.md")

	for _, w := range []string{
		"正文", "表格", "页眉页脚", "脚注尾注", "文本框",
		"字段指令", "绑定源", "链接目标", "SmartArt", "附件",
		"不可恢复", "best-effort", "Word 排版",
	} {
		if !strings.Contains(zh, w) {
			t.Errorf("README 中文边界应含 %q", w)
		}
	}
	for _, w := range []string{
		"headers/footers", "footnotes/endnotes", "native text boxes",
		"field codes", "binding sources", "hyperlink targets", "charts/SmartArt",
		"cannot be recovered", "best-effort", "does not restore Word layout",
	} {
		if !strings.Contains(en, w) {
			t.Errorf("README English boundaries should contain %q", w)
		}
	}
	if !strings.Contains(zh, "](README.en.md)") {
		t.Error("README 中文版应链接英文版")
	}
	if !strings.Contains(en, "](README.md)") {
		t.Error("README English version should link the Chinese version")
	}
}

// 4.1/4.2 文档入口格式说明：两个文档 input 不再窄过滤；脱敏页与还原文件模式
// 常显同义双语说明，静态中文回退与 catalog 一致；词卡 TXT 与 mapping JSON 用途不变。
func TestDocumentFormatNotesContract(t *testing.T) {
	html := repoFile(t, "web", "static", "index.html")
	css := repoFile(t, "web", "static", "style.css")
	js := repoFile(t, "web", "static", "app.js")

	if strings.Contains(html, `accept=".txt,.md,.log,.py,.js,.docx"`) {
		t.Error("两个文档 input 不应再有旧后缀 accept 过滤")
	}
	for _, id := range []string{"ui.doc_formats_text", "ui.doc_formats_examples", "ui.doc_formats_syntax"} {
		if got := strings.Count(html, `data-i18n="`+id+`"`); got != 2 {
			t.Errorf("%s 应在脱敏与还原文件入口各出现一次，got %d", id, got)
		}
		zh := i18n.T(i18n.ZH, id, nil)
		en := i18n.T(i18n.EN, id, nil)
		if zh == "" || en == "" || zh == en {
			t.Errorf("%s 应双语注册且中英文不同：%q / %q", id, zh, en)
		}
		if !strings.Contains(html, ">"+zh+"</span>") {
			t.Errorf("%s 静态中文回退应与 catalog 一致：%q", id, zh)
		}
	}

	if !strings.Contains(html, `id="mask-format-note"`) {
		t.Error("脱敏文件区应常显格式说明")
	}
	start := strings.Index(html, `id="restore-file-op"`)
	end := strings.Index(html, `id="pending-picker"`)
	if start < 0 || end <= start || !strings.Contains(html[start:end], `id="restore-format-note"`) {
		t.Error("还原格式说明应在文件模式容器内（粘贴模式不重复）")
	}
	if !strings.Contains(js, `setHidden("#restore-format-note", mode !== "file")`) {
		t.Error("粘贴模式应隐藏还原文件格式说明")
	}
	if !strings.Contains(css, ".format-note") {
		t.Error("格式说明应有轻量行样式")
	}
	if !strings.Contains(html, `id="custom-file" accept=".txt"`) || !strings.Contains(html, `id="restore-mapping" accept=".json"`) {
		t.Error("词卡仍应仅 TXT、mapping 仍应仅 JSON")
	}
}
