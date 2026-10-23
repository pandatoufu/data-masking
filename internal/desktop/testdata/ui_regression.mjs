// 真实 app.js 回归：W1 重执行确认语义、W2 还原范围情境、W4 未知 ID、S1 搜索批量、
// S2 非法导入保留、S6 多文件 drop/非目标、S3 已保存另存取消、S7 会话映射 metadata。
// 用法：node ui_regression.mjs <appJsPath> <reportPath>
import { readFile, writeFile } from "node:fs/promises";
import vm from "node:vm";

const [appJsPath, reportPath] = process.argv.slice(2);
if (!appJsPath || !reportPath) {
  console.error("用法: node ui_regression.mjs <app.js> <reportPath>");
  process.exit(2);
}

const report = { cases: {}, failures: [], fatal: null };
function fail(m) { report.failures.push(m); }
async function waitFor(pred, tries = 500) {
  for (let i = 0; i < tries; i++) { if (pred()) return true; await new Promise((r) => setImmediate(r)); }
  return false;
}

// 四类真实 file input：模拟 input.value="" 同步清空同一个 live FileList（length/索引/迭代），
// 且 input.files setter 抛错（Windows/WebKit 不允许脚本赋值 files）。
const FILE_INPUTS = new Set(["#mask-file", "#custom-file", "#restore-file", "#restore-mapping"]);

function makeFakeDom(page) {
  const cache = new Map();
  const attrs = {};
  const docListeners = {};
  function makeEl(sel) {
    const listeners = {};
    const classes = new Set();
    const node = {
      __sel: sel, hidden: false, textContent: "", innerHTML: "", value: "", disabled: false, open: false,
      dataset: {}, style: {}, selected: false, options: [],
      classList: {
        toggle(c, v) { const on = (v === undefined) ? !classes.has(c) : !!v; if (on) classes.add(c); else classes.delete(c); return on; },
        add(c) { classes.add(c); }, remove(c) { classes.delete(c); }, contains(c) { return classes.has(c); },
      },
      addEventListener(t, fn) { (listeners[t] = listeners[t] || []).push(fn); },
      removeEventListener() {}, appendChild() {}, querySelector() { return makeEl(sel + " *"); },
      querySelectorAll() { return []; },
      setAttribute(k, v) { if (sel === "html") attrs[k] = v; },
      getAttribute(k) { return attrs[k] == null ? null : attrs[k]; },
      focus() {}, click() {}, scrollIntoView() {},
      __fire(t, ev) { (listeners[t] || []).forEach((fn) => fn(ev || { preventDefault() {} })); },
    };
    if (FILE_INPUTS.has(sel)) {
      const arr = []; // 同一 FileList 对象，identity 稳定
      let value = "";
      Object.defineProperty(node, "files", {
        get() { return arr; },
        set() { throw new Error("input.files setter unsupported"); },
      });
      Object.defineProperty(node, "value", {
        get() { return value; },
        set(v) { value = v; if (v === "") arr.length = 0; }, // 重置即同步清空同一 FileList
      });
      // 选择动作同时写入 value（模拟浏览器 fake path），使“重置控件”可观察。
      node.__setFiles = (list) => { arr.length = 0; (list || []).forEach((f) => arr.push(f)); value = (list && list.length) ? ("C:\\fakepath\\" + (list[0].name || "file")) : ""; };
    }
    return node;
  }
  function el(sel) { if (!cache.has(sel)) cache.set(sel, makeEl(sel)); return cache.get(sel); }
  const body = makeEl("body"); body.dataset.page = page;
  return {
    body, documentElement: makeEl("html"), title: "", el,
    querySelector(sel) { return sel === "body" ? body : el(sel); },
    querySelectorAll() { return []; },
    createElement(tag) { return makeEl("<" + tag + ">"); },
    addEventListener(t, fn) { (docListeners[t] = docListeners[t] || []).push(fn); },
    removeEventListener() {},
    fireDocument(t, ev) { (docListeners[t] || []).forEach((fn) => fn(ev || { preventDefault() {} })); },
  };
}

const plan = { saveFileResult: { status: "cancelled" }, confirmResult: true, confirmMessages: [], autoMaskResult: null, autoRestoreResult: null };

function makeConfig(opts) {
  opts = opts || {};
  const zh = {
    "error.unknown_id": "发生了未分类的问题（{id}）。请查看处理详情后重试。",
    "ui.confirm_reexecute": "重新执行将替换本视图中尚未保存的结果。确定继续吗？",
    "native.confirm_close_legacy": "存在尚未保存的结果。选择“是”将放弃这些结果并退出；选择“否”将返回应用，结果仍可保存。",
    "ui.multi_file_rejected": "一次只能选择一个文件，请重新选择一个文件。",
    "ui.location_text": "位置：{loc}",
    "ui.word_line_too_long": "导入第 {line} 行：自定义词超过 256 字符上限。整批已拒绝，请修正后重试。",
    "ui.result_processing": "正在处理，请稍候…",
    "ui.words_summary": "全局已选 {x} / 总 {y}{matchPart}",
    "ui.adjust_words": "自定义词",
    "ui.adjust_selected": "已选 {x}/{y}",
    "ui.mapping_reminder_short": "还原映射含原文，仅供本机还原；发送给模型时只使用脱敏文件。",
    "ui.mapping_source_session": "来源：本次脱敏映射（{name}）",
    "ui.mapping_source_manual": "来源：手动选择的配对文件（{name}）",
    "ui.docx_unchecked_short": "图片及属性、字段指令、绑定源、链接目标、图表/SmartArt、附件未检查。",
    "ui.cleanup_title": "本次清理结果：",
    "ui.cleanup_comments": "删除批注 {count} 处",
    "ui.cleanup_revisions": "接受修订 {count} 处",
    "ui.result_mask_title": "脱敏完成",
    "ui.input_docx": "文档格式：DOCX",
    "ui.artifact_masked": "脱敏副本",
    "ui.artifact_mapping": "还原映射",
    "ui.artifact_restored": "还原文件",
    "ui.saved_to": "{artifact}已保存到 {path}",
    "ui.partial_masked_saved": "脱敏副本已保存，但配对 mapping 尚未保存。",
    "ui.partial_mapping_saved": "配对 mapping 已保存，但脱敏副本尚未保存。",
    "ui.save": "保存",
    "ui.save_as": "另存为",
    "ui.status_unsaved": "未保存",
    "ui.status_saved": "已保存",
    "ui.status_saving": "保存中…",
    "ui.status_failed": "保存失败（可重试）",
    "save.write_failed": "写入失败，请重试保存。",
    "save.cleanup_warning": "已保存，但临时文件清理失败，请手动检查目的目录。",
    "warning.residual_forward": "检测到输出中仍有 {count} 处命中启用规则的内容（首处：第 {line} 行第 {column} 列）。",
    "ui.drop_hint": "点击选择或拖入单个文件",
    "ui.click_hint": "点击选择单个文件",
    "ui.click_to_choose": "请点击文件按钮选择文件。"
  };
  const en = {
    "error.unknown_id": "An unrecognized problem occurred ({id}). Check the details and try again.",
    "ui.confirm_reexecute": "Re-running will replace the unsaved results in this view. Continue?",
    "native.confirm_close_legacy": "There are unsaved results.",
    "ui.multi_file_rejected": "Only one file can be selected at a time.",
    "ui.location_text": "Location: {loc}",
    "ui.drop_hint": "Click to choose, or drop a single file",
    "ui.click_hint": "Click to choose a single file",
    "ui.click_to_choose": "Please click the file button to choose a file.",
    "ui.artifact_masked": "Masked copy",
    "ui.artifact_mapping": "Restore mapping",
    "ui.artifact_restored": "Restored file",
    "ui.saved_to": "{artifact} saved to {path}",
    "ui.save": "Save",
    "ui.save_as": "Save as",
    "ui.status_unsaved": "Unsaved",
    "ui.status_saved": "Saved",
    "ui.status_saving": "Saving…",
    "ui.status_failed": "Save failed (retry possible)",
    "save.cleanup_warning": "Saved, but cleaning up the temporary file failed. Check the destination folder manually.",
    "save.write_failed": "Write failed. Please retry the save.",
    "ui.partial_masked_saved": "The masked copy is saved, but the paired mapping is not.",
    "ui.mapping_source_session": "Source: this session's mask mapping ({name})",
    "ui.mapping_source_manual": "Source: manually chosen paired file ({name})"
  };
  return {
    profiles: [{ name: "balanced", display_name: "balanced", rules: { phone: true, idcard: false, email: false, ip: false, api_key: true, custom: true } }],
    default_profile: "balanced", url: "", high_freq_threshold: 20, mode: "desktop",
    initial_view: "mask", theme: "system", language: "zh", i18n: { zh, en },
    file_drop_enabled: opts.dropEnabled === true
  };
}

function makeBridge() {
  return {
    SaveFile: () => plan.saveFileResult,
    ReportUnsaved: () => {},
    SetExitConfirmed: () => {},
    SetUIPreferences: (theme, language) => ({ theme, language, persisted: true }),
    AutoSaveMask: () => plan.autoMaskResult,
    AutoSaveRestored: () => plan.autoRestoreResult,
  };
}

function loadApp(code, dom, bridge, config) {
  const hooks = {};
  const winListeners = {};
  // Node 无 FileReader：仅在沙箱内模拟平台边界，供生产 readAndDecode 使用。
  class NodeFileReader {
    readAsArrayBuffer(blob) {
      Promise.resolve(blob.arrayBuffer()).then((buf) => { this.result = buf; if (this.onload) this.onload(); })
        .catch((e) => { if (this.onerror) this.onerror(e); });
    }
  }
  const sandbox = {
    window: {
      __DM_CONFIG__: config || makeConfig(), __DM_TEST_HOOKS__: hooks,
      go: { desktop: { Bridge: bridge } },
      confirm: (m) => { plan.confirmMessages.push(m); return plan.confirmResult; },
      matchMedia: () => ({ matches: false, addEventListener() {}, addListener() {} }),
      addEventListener(t, fn) { (winListeners[t] = winListeners[t] || []).push(fn); },
      removeEventListener() {},
    },
    document: dom,
    confirm: (m) => { plan.confirmMessages.push(m); return plan.confirmResult; },
    location: { origin: "http://localhost" },
    fetch: () => Promise.resolve({ status: 200, json: () => Promise.resolve({ ok: true }) }),
    Response, Request, Headers, FormData, Blob, File, FileReader: NodeFileReader, TextDecoder, TextEncoder, console, URL, setTimeout, clearTimeout,
    atob: (s) => Buffer.from(String(s), "base64").toString("binary"),
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(code, sandbox, { filename: appJsPath });
  hooks.__fireWindow = (t, ev) => { (winListeners[t] || []).forEach((fn) => fn(ev || { preventDefault() {} })); };
  return hooks;
}

function fileOf(name, text) { return new File([Buffer.from(text, "utf8")], name, { type: "text/plain" }); }
function fileOfBytes(name, bytes) { return new File([bytes], name, { type: "text/plain" }); }

async function main() {
  const code = await readFile(appJsPath, "utf8");
  const dom = makeFakeDom("mask");
  const hooks = loadApp(code, dom, makeBridge()); // 非 Windows/未知：drop 能力保守 false
  dom.fireDocument("DOMContentLoaded"); // 触发真实 init，注册生产 wireFileArea change/drop 监听器
  // Windows 能力实例：用于真实 drop 链与 setter 抛错回归。
  const winDom = makeFakeDom("mask");
  const winHooks = loadApp(code, winDom, makeBridge(), makeConfig({ dropEnabled: true }));
  winDom.fireDocument("DOMContentLoaded");
  // 缺字段保守回退实例：pageConfig 无 file_drop_enabled。
  const missDom = makeFakeDom("mask");
  const missCfg = makeConfig();
  delete missCfg.file_drop_enabled;
  const missHooks = loadApp(code, missDom, makeBridge(), missCfg);
  missDom.fireDocument("DOMContentLoaded");

  // W1：重执行确认使用独立语义文案，不复用关闭文案。
  {
    plan.confirmMessages = [];
    hooks.state.mask.artifacts = { masked: { id: "masked", view: "mask", status: "unsaved" } };
    hooks.confirmReplace("mask");
    const msg = plan.confirmMessages[plan.confirmMessages.length - 1];
    const usesReexecute = msg && msg.indexOf("重新执行") >= 0;
    const noClose = msg && msg.indexOf("退出") < 0;
    // 无未保存时不再确认。
    hooks.state.mask.artifacts = { masked: { status: "saved" } };
    plan.confirmMessages = [];
    const okNoConfirm = hooks.confirmReplace("mask") === true && plan.confirmMessages.length === 0;
    report.cases["reexecute-confirm"] = { msg, usesReexecute, noClose, okNoConfirm };
    if (!usesReexecute) fail("reexecute-confirm: 应使用重执行语义文案");
    if (!noClose) fail("reexecute-confirm: 不应声称退出");
    if (!okNoConfirm) fail("reexecute-confirm: 已保存不应再确认");
  }

  // W4：未知 ID 在英文下给通用原因/下一步并保留 id，绝不回退中文或空串。
  {
    hooks.setPreferences("system", "en");
    await waitFor(() => true);
    const out = hooks.t("some.unknown.id.xyz", {});
    report.cases["unknown-id-en"] = { out };
    if (!out || out.indexOf("some.unknown.id.xyz") < 0) fail("unknown-id-en: 应保留未知 id");
    if (out.indexOf("未分类") >= 0) fail("unknown-id-en: 英文不得回退中文");
    if (out.indexOf("An unrecognized") < 0) fail("unknown-id-en: 应给英文通用说明");
  }

  // S1：搜索只改显示；有搜索时批量仅作用命中项；清空搜索不改其他勾选。
  {
    hooks.state.mask.profile = "balanced";
    hooks.state.mask.customRules = { phone: true, idcard: true, email: true, ip: true, api_key: true, custom: true };
    hooks.state.mask.words = [
      { w: "az", source: "text", line: 1, checked: true },
      { w: "ab", source: "text", line: 2, checked: true },
      { w: "bm", source: "text", line: 3, checked: false }
    ];
    dom.el("#word-search").value = "a";
    hooks.renderWords();
    hooks.selectMatched(false); // 仅命中 az/ab
    const afterDeselect = hooks.state.mask.words.map((e) => e.checked);
    dom.el("#word-search").value = "";
    hooks.renderWords();
    hooks.selectMatched(true); // 无搜索：全选
    const afterAll = hooks.state.mask.words.map((e) => e.checked);
    dom.el("#word-search").value = "b";
    hooks.renderWords();
    hooks.selectMatched(false); // 仅命中 bm
    const afterSearchB = hooks.state.mask.words.map((e) => e.checked);
    report.cases["search-bulk"] = { afterDeselect, afterAll, afterSearchB };
    if (afterDeselect[0] !== false || afterDeselect[1] !== false || afterDeselect[2] !== false) {
      // 命中 az/ab 取消，bm 原本 false 保持；再全选后应全 true。
    }
    if (!(afterDeselect[0] === false && afterDeselect[1] === false && afterDeselect[2] === false)) fail("search-bulk: 搜索命中批量取消错误");
    if (!(afterAll[0] && afterAll[1] && afterAll[2])) fail("search-bulk: 无搜索全选错误");
    if (!(afterSearchB[0] === true && afterSearchB[1] === false && afterSearchB[2] === false)) fail("search-bulk: 仅命中项应取消");
  }

  // S5/S2：非法导入抛错且不改词表与文件名；成功原子交换名称。
  {
    hooks.state.mask.words = [];
    hooks.state.mask.importMeta = null;
    hooks.applyImportedWords("旧词\n", { name: "A.txt", size: 4 });
    const nameA = dom.el("#word-file-name").textContent;
    const before = hooks.state.mask.words.map((e) => e.w).join(",");
    let threw = false;
    try {
      hooks.applyImportedWords("ok\n" + "x".repeat(300) + "\n", { name: "B.txt", size: 999 });
    } catch (e) { threw = true; }
    const after = hooks.state.mask.words.map((e) => e.w).join(",");
    const nameStill = dom.el("#word-file-name").textContent;
    hooks.applyImportedWords("新词\n", { name: "C.txt", size: 4 });
    const nameC = dom.el("#word-file-name").textContent;
    report.cases["illegal-import-preserve"] = { threw, before, after, nameA, nameStill, nameC };
    if (!threw) fail("illegal-import-preserve: 非法导入应抛错");
    if (before !== after) fail("illegal-import-preserve: 非法导入不应改词表");
    if (nameStill !== "A.txt") fail("illegal-import-preserve: 非法导入不应改文件名");
    if (nameC !== "C.txt") fail("illegal-import-preserve: 成功应交换文件名");
  }

  // S6：多文件 drop 给指引且不调用处理器；非目标判定。
  {
    let called = 0;
    hooks.receiveFiles([fileOf("a.txt", "a"), fileOf("b.txt", "b")], () => { called++; }, "mask");
    const multiCalled = called;
    hooks.receiveFiles([fileOf("a.txt", "a")], () => { called++; }, "mask");
    const singleCalled = called;
    // 真实 selector 语义：只有命中 .file-btn 的最近祖先才算目标。
    const seenSelectors = [];
    const fileBtnTarget = { closest: (sel) => { seenSelectors.push(sel); return sel === ".file-btn" ? {} : null; } };
    const nonFileBtnTarget = { closest: (sel) => { seenSelectors.push(sel); return null; } };
    const target = hooks.isFileAreaTarget(fileBtnTarget);
    const nonTarget = hooks.isFileAreaTarget(nonFileBtnTarget);
    const selectorIsFileBtn = seenSelectors.length > 0 && seenSelectors.every((s) => s === ".file-btn");
    report.cases["multi-file-drop"] = { multiCalled, singleCalled, nonTarget, target, selectorIsFileBtn };
    if (multiCalled !== 0) fail("multi-file-drop: 多文件不应调用处理器");
    if (singleCalled !== 1) fail("multi-file-drop: 单文件应调用处理器");
    if (nonTarget || !target) fail("multi-file-drop: 非目标判定错误");
    if (!selectorIsFileBtn) fail("multi-file-drop: 应使用 .file-btn selector 判定目标");
  }

  // S3：已保存产物另存取消仍保持 saved。
  {
    hooks.state.mask.artifacts = { masked: { id: "masked", view: "mask", label: "x", name: "a.txt", b64: "eA==", status: "saved", busy: false } };
    plan.saveFileResult = { status: "cancelled" };
    hooks.nativeSave(hooks.state.mask.artifacts.masked);
    await waitFor(() => hooks.state.mask.artifacts.masked.busy === false);
    const saved = hooks.state.mask.artifacts.masked.status;
    report.cases["save-cancel-keeps-saved"] = { saved };
    if (saved !== "saved") fail("save-cancel-keeps-saved: 取消另存不应撤销 saved");
  }

  // S3b：保存中单项按钮视觉禁用，空闲不禁用。
  {
    const busyCard = hooks.artifactCard({ id: "masked", view: "mask", label: "x", name: "a.txt", status: "unsaved", busy: true });
    const idleCard = hooks.artifactCard({ id: "masked", view: "mask", label: "x", name: "a.txt", status: "unsaved", busy: false });
    report.cases["busy-save-disabled"] = { busyDisabled: busyCard.indexOf("disabled") >= 0, idleDisabled: idleCard.indexOf("disabled") >= 0 };
    if (busyCard.indexOf("disabled") < 0) fail("busy-save-disabled: 保存中按钮应视觉禁用");
    if (idleCard.indexOf("disabled") >= 0) fail("busy-save-disabled: 空闲按钮不应禁用");
  }

  // S4：还原处理中显示可见反馈。
  {
    dom.el("#restore-result").hidden = true;
    hooks.state.restore.status = "processing";
    hooks.updateRestoreStatus();
    const html = dom.el("#restore-result").innerHTML;
    const shown = dom.el("#restore-result").hidden === false;
    report.cases["restore-processing-feedback"] = { html, shown };
    if (!shown || html.indexOf("正在处理") < 0) fail("restore-processing-feedback: 还原处理中应有可见反馈");
    hooks.state.restore.status = "idle";
  }

  // 8.4：内层“调整参与词”折叠不改变参与集/搜索，展开状态跨语言/视图保留。
  {
    hooks.state.mask.profiles = { balanced: { name: "balanced", rules: { phone: true, idcard: false, email: false, ip: false, api_key: true, custom: true } } };
    hooks.state.mask.profile = "balanced";
    hooks.state.mask.customRules = { phone: true, idcard: true, email: true, ip: true, api_key: true, custom: true };
    hooks.state.mask.file = fileOf("in.txt", "x");
    hooks.state.mask.words = [
      { w: "a", source: "text", line: 1, checked: true },
      { w: "b", source: "text", line: 2, checked: false }
    ];
    hooks.state.mask.importFile = null;
    dom.el("#custom").value = "a\nb";
    dom.el("#word-adjust").open = true;
    hooks.renderWords();
    const beforeCustom = hooks.buildMaskFormData([]).get("custom");
    const adjustCount = dom.el("#word-adjust-count").textContent;
    dom.el("#word-adjust").open = false;
    hooks.applyI18n();
    hooks.setView("restore");
    hooks.setView("mask");
    const stillClosed = dom.el("#word-adjust").open === false;
    const afterCustom = hooks.buildMaskFormData([]).get("custom");
    report.cases["adjust-details-state"] = { beforeCustom, afterCustom, stillClosed, adjustCount };
    if (beforeCustom !== afterCustom) fail("adjust-details-state: 折叠不应改变提交参与集");
    if (!stillClosed) fail("adjust-details-state: 展开状态应跨语言/视图保留");
    if (!adjustCount || adjustCount.indexOf("1/2") < 0) fail("adjust-details-state: 内层摘要应显示已选 X/Y");
  }

  // 9.1：唯一折叠收起时导入仍生效、不强制展开、摘要更新；非法导入保旧词/旧名。
  {
    hooks.state.mask.words = [];
    hooks.state.mask.importFile = null;
    hooks.state.mask.importMeta = null;
    dom.el("#word-adjust").open = false;
    hooks.applyImportedWords("新词\n", { name: "B.txt", size: 4 });
    const words = hooks.state.mask.words.map((e) => e.w);
    const closed = dom.el("#word-adjust").open === false;
    const summary = dom.el("#word-adjust-count").textContent;
    const name = dom.el("#word-file-name").textContent;
    let threw = false;
    try { hooks.applyImportedWords("ok\n" + "x".repeat(300) + "\n", { name: "C.txt", size: 999 }); } catch (e) { threw = true; }
    const afterFail = hooks.state.mask.words.map((e) => e.w).join(",");
    const nameAfterFail = dom.el("#word-file-name").textContent;
    report.cases["collapsed-import"] = { words, closed, summary, name, threw, afterFail, nameAfterFail };
    if (words.join(",") !== "新词") fail("collapsed-import: 收起时应成功导入");
    if (!closed) fail("collapsed-import: 导入不应强制展开");
    if (!summary || summary.indexOf("1/1") < 0) fail("collapsed-import: 摘要应更新");
    if (name !== "B.txt") fail("collapsed-import: 应显示导入文件名");
    if (!threw) fail("collapsed-import: 非法导入应抛错");
    if (afterFail !== "新词" || nameAfterFail !== "B.txt") fail("collapsed-import: 非法导入应保旧词/旧名");
  }

  // 8.5：无背景结果行，保存按钮最左，名称/状态在右。
  {
    const card = hooks.artifactCard({ id: "masked", view: "mask", label: "脱敏副本", name: "long-name.masked.txt", status: "unsaved", busy: false });
    const btnIdx = card.indexOf("save-btn");
    const metaIdx = card.indexOf("result-meta");
    const noCardBg = card.indexOf("artifact-card") < 0;
    report.cases["result-row-left"] = { btnIdx, metaIdx, noCardBg };
    if (btnIdx < 0 || metaIdx < btnIdx) fail("result-row-left: 保存按钮应在说明容器之前");
    if (!noCardBg) fail("result-row-left: 结果不应使用卡片背景框");
  }

  // 8.6：mapping 短提醒仅在脱敏映射行一次，重绘不累积；DOCX 结果保留实际计数+未检查短摘要。
  {
    hooks.state.mask.lastBody = { format: "text", encoding: "UTF-8", cleanup: null, warnings: [], audit_failed: false };
    hooks.state.mask.artifacts = {
      masked: { id: "masked", view: "mask", label: "脱敏副本", name: "a.masked.txt", b64: "eA==", status: "unsaved", busy: false },
      mapping: { id: "mapping", view: "mask", label: "还原映射", name: "a.mapping.json", b64: "eA==", status: "unsaved", busy: false }
    };
    hooks.renderMaskResults();
    const h1 = dom.el("#mask-result").innerHTML;
    hooks.renderMaskResults();
    const h2 = dom.el("#mask-result").innerHTML;
    const once1 = (h1.match(/还原映射含原文/g) || []).length;
    const once2 = (h2.match(/还原映射含原文/g) || []).length;

    hooks.state.mask.lastBody = { format: "docx", encoding: "", cleanup: { comments_deleted: 2, revisions_accepted: 1 }, warnings: [], audit_failed: false };
    hooks.renderMaskResults();
    const docxHtml = dom.el("#mask-result").innerHTML;
    report.cases["mapping-and-docx-summary"] = {
      once1, once2,
      docxHasCounts: docxHtml.indexOf("2") >= 0 && docxHtml.indexOf("1") >= 0,
      docxHasUnchecked: docxHtml.indexOf("未检查") >= 0,
      hasWarnTitle: docxHtml.indexOf("警告（不阻断）") >= 0
    };
    if (once1 !== 1 || once2 !== 1) fail("mapping-and-docx-summary: 映射提醒应仅一次且重绘不累积");
    if (docxHtml.indexOf("未检查") < 0) fail("mapping-and-docx-summary: DOCX 应保留未检查短摘要");
    if (docxHtml.indexOf("警告（不阻断）") >= 0) fail("mapping-and-docx-summary: 不应有统一警告标题");
  }

  // 8.7：实际反馈为就近简短列表，无统一警告标题/大框，保留计数与定位。
  {
    const list = [{ kind: "residual_forward", message_id: "warning.residual_forward", params: { count: 2, line: 3, column: 5 }, line: 3, column: 5, count: 2 }];
    const html = hooks.warningsHTML(list);
    report.cases["inline-feedback"] = { html };
    if (html.indexOf("feedback-list") < 0) fail("inline-feedback: 应为就近简短列表");
    if (html.indexOf("警告（不阻断）") >= 0) fail("inline-feedback: 不应有统一警告标题");
    if (html.indexOf("第 3 行") < 0) fail("inline-feedback: 应保留定位");
  }

  // S7：会话映射 metadata 解析真实快照（version 2 / docx），不再硬编码 version 1。
  {
    const v2 = { version: 2, source_format: "docx", source_sha256: "a", source_text_sha256: "b", placeholders: [] };
    hooks.state.mask.artifacts.mapping = { id: "mapping", view: "mask", name: "a.mapped.mapping.json", b64: Buffer.from(JSON.stringify(v2)).toString("base64") };
    hooks.useSessionMapping();
    const md = hooks.state.restore.mapping && hooks.state.restore.mapping.metadata;
    report.cases["session-mapping-metadata"] = { md };
    if (!md || md.version !== 2 || md.sourceFormat !== "docx") fail("session-mapping-metadata: 应从真实快照解析 version2/docx");
  }

  // 10.1：live FileList 模型——input.value="" 同步清空同一 FileList，独立快照保留真实字节。
  // 这证明“先重置后读原 FileList 引用”的旧顺序在模型中未载入；非原生平台根因确认。
  {
    const input = dom.el("#mask-file");
    input.__setFiles([fileOf("snap.txt", "hello")]);
    const liveRef = input.files;
    const snapshot = Array.from(input.files);
    const beforeLen = liveRef.length;
    input.value = "";
    const afterLen = liveRef.length;
    const iterated = Array.from(liveRef).length;
    const snapBytes = Buffer.from(await snapshot[0].arrayBuffer()).toString("utf8");
    report.cases["filelist-reset-model"] = { beforeLen, afterLen, iterated, snapshotLen: snapshot.length, snapName: snapshot[0].name, snapBytes };
    if (beforeLen !== 1 || afterLen !== 0 || iterated !== 0) fail("filelist-reset-model: value='' 应同步清空同一 FileList");
    if (snapshot.length !== 1 || snapBytes !== "hello" || snapshot[0].name !== "snap.txt") fail("filelist-reset-model: 独立快照应保留 File 与字节");
  }

  // 10.2/10.5：真实 change 监听器载入文档，重置控件后仍有字节与正确名称。
  {
    hooks.state.mask.file = null;
    const input = dom.el("#mask-file");
    const f = fileOf("notes.txt", "电话 13812345678");
    input.__setFiles([f]);
    input.__fire("change", {});
    const loaded = hooks.state.mask.file;
    const name = dom.el("#mask-file-name").textContent;
    const bytes = loaded ? Buffer.from(await loaded.arrayBuffer()).toString("utf8") : "";
    const cleared = input.files.length === 0;
    report.cases["change-mask-bytes"] = { name, bytes, cleared, size: loaded ? loaded.size : -1 };
    if (!loaded || name !== "notes.txt") fail("change-mask-bytes: 真实监听器应载入文件");
    if (bytes !== "电话 13812345678") fail("change-mask-bytes: 应保留真实字节而非仅名称");
    if (!cleared) fail("change-mask-bytes: 选择后应重置控件以便重复选择");
  }

  // 10.2/10.5：取消保留旧状态；可重复选择同文件；0 字节按业务校验不等同未选择。
  {
    const input = dom.el("#mask-file");
    const beforeName = hooks.state.mask.file && hooks.state.mask.file.name;
    input.__setFiles([]);
    input.__fire("change", {});
    const afterName = hooks.state.mask.file && hooks.state.mask.file.name;
    const same = fileOf("notes.txt", "电话 13812345678");
    input.__setFiles([same]);
    input.__fire("change", {});
    const repeatOk = hooks.state.mask.file === same;
    const empty = fileOfBytes("empty.txt", new Uint8Array(0));
    input.__setFiles([empty]);
    input.__fire("change", {});
    const zeroLoaded = hooks.state.mask.file === empty;
    report.cases["cancel-repeat-zero"] = { beforeName, afterName, repeatOk, zeroName: hooks.state.mask.file && hooks.state.mask.file.name, zeroSize: hooks.state.mask.file && hooks.state.mask.file.size, zeroLoaded };
    if (afterName !== "notes.txt") fail("cancel-repeat-zero: 取消不应清空旧状态");
    if (!repeatOk) fail("cancel-repeat-zero: 同文件应可重复选择并载入");
    if (!zeroLoaded || hooks.state.mask.file.size !== 0) fail("cancel-repeat-zero: 0 字节按业务校验、不等同未选择");
  }

  // 10.2/10.5：多文件 change 拒绝保旧并给指引。
  {
    const input = dom.el("#mask-file");
    const prev = hooks.state.mask.file;
    input.__setFiles([fileOf("m1.txt", "1"), fileOf("m2.txt", "2")]);
    input.__fire("change", {});
    const kept = hooks.state.mask.file === prev;
    const errShown = dom.el("#mask-error").hidden === false;
    report.cases["multi-change-keeps-old"] = { kept, errShown };
    if (!kept) fail("multi-change-keeps-old: 多文件应保旧");
    if (!errShown) fail("multi-change-keeps-old: 多文件应给单文件指引");
  }

  // 10.2/S2：禁用入口与处理忙时拒收（state 保旧），但拒收前已先快照并 reset 控件，
  // 保证解除限制后同一文件可重新选择（真实浏览器同文件不重选则不触发 change）。
  {
    const input = dom.el("#mask-file");
    const prev = hooks.state.mask.file;
    // 禁用
    const dFile = fileOf("d.txt", "d");
    input.disabled = true;
    input.__setFiles([dFile]);
    const disabledSelectedValue = input.value;
    input.__fire("change", {});
    const disabledKept = hooks.state.mask.file === prev;
    const disabledReset = input.value === "" && input.files.length === 0;
    input.disabled = false;
    input.__setFiles([dFile]);
    input.__fire("change", {});
    const disabledRetry = hooks.state.mask.file === dFile;
    // 忙
    const bFile = fileOf("b.txt", "b");
    const prev2 = hooks.state.mask.file;
    hooks.appState.busy = true;
    input.__setFiles([bFile]);
    input.__fire("change", {});
    const busyKept = hooks.state.mask.file === prev2;
    const busyReset = input.value === "" && input.files.length === 0;
    hooks.appState.busy = false;
    input.__setFiles([bFile]);
    input.__fire("change", {});
    const busyRetry = hooks.state.mask.file === bFile;
    report.cases["disabled-busy-keeps-old"] = {
      disabledSelectedValue, disabledKept, disabledReset, disabledRetry, busyKept, busyReset, busyRetry,
    };
    if (disabledSelectedValue === "") fail("disabled-busy-keeps-old: 选择动作应先写入 value 以便观察 reset");
    if (!disabledKept) fail("disabled-busy-keeps-old: 禁用入口不应载入");
    if (!disabledReset) fail("disabled-busy-keeps-old: 拒收时控件应先快照后 reset");
    if (!disabledRetry) fail("disabled-busy-keeps-old: 解除禁用后同一文件应可重选");
    if (!busyKept) fail("disabled-busy-keeps-old: 忙时不应载入");
    if (!busyReset) fail("disabled-busy-keeps-old: 忙拒收时控件应 reset");
    if (!busyRetry) fail("disabled-busy-keeps-old: 解除忙后同一文件应可重选");
  }

  // 10.5：TXT 经真实监听器导入 UTF-8/GBK，参与提交的原 File 为快照对象。
  {
    const cf = dom.el("#custom-file");
    hooks.state.mask.words = [];
    hooks.state.mask.importFile = null;
    const utf8 = fileOf("utf8.txt", "内部代号\n第二词\n");
    cf.__setFiles([utf8]);
    cf.__fire("change", {});
    const okUtf8 = await waitFor(() => hooks.state.mask.words.length >= 2);
    const wordsUtf8 = hooks.state.mask.words.map((e) => e.w);
    const importFileIsReal = hooks.state.mask.importFile === utf8;
    cf.__setFiles([fileOfBytes("gbk.txt", new Uint8Array([0xB2, 0xE2, 0xCA, 0xD4]))]);
    cf.__fire("change", {});
    const okGbk = await waitFor(() => hooks.state.mask.words.map((e) => e.w).indexOf("测试") >= 0);
    const wordsGbk = hooks.state.mask.words.map((e) => e.w);
    report.cases["txt-change-gbk-utf8"] = { okUtf8, wordsUtf8, importFileIsReal, okGbk, wordsGbk };
    if (!okUtf8) fail("txt-change-gbk-utf8: UTF-8 词文件应经真实监听器导入");
    if (wordsUtf8.join(",") !== "内部代号,第二词") fail("txt-change-gbk-utf8: UTF-8 词解析错误");
    if (!importFileIsReal) fail("txt-change-gbk-utf8: 参与提交的原 File 应为快照对象");
    if (!okGbk || wordsGbk.join(",") !== "测试") fail("txt-change-gbk-utf8: GBK 词文件应解码为 测试");
  }

  // 10.5/S4：脱敏侧真实 change 监听器 → 生产 buildMaskFormData + serializeMultipart 完整复合链：
  // 文档字节与 TXT 参与原字节均进入实际 multipart（非仅 state、非直接 helper 注入）。
  {
    const input = dom.el("#mask-file");
    const doc = fileOf("chain.txt", "电话 13812345678");
    input.__setFiles([doc]);
    input.__fire("change", {});
    const cf = dom.el("#custom-file");
    hooks.state.mask.words = [];
    hooks.state.mask.importFile = null;
    const tx = fileOf("participants.txt", "内部代号\n第二词\n");
    cf.__setFiles([tx]);
    cf.__fire("change", {});
    const wordsReady = await waitFor(() => hooks.state.mask.words.length >= 2);
    hooks.state.mask.profile = "balanced";
    const fd = hooks.buildMaskFormData([]);
    const payload = await hooks.serializeMultipart(fd);
    const text = Buffer.from(payload.buffer).toString("utf8");
    const ct = payload.contentType || "";
    const hasBoundary = /multipart\/form-data; boundary=/.test(ct);
    const hasDocPart = text.indexOf('name="file"') >= 0 && text.indexOf('filename="chain.txt"') >= 0 && text.indexOf("电话 13812345678") >= 0;
    const hasTxtPart = text.indexOf('name="custom_file"') >= 0 && text.indexOf('filename="participants.txt"') >= 0 && text.indexOf("内部代号") >= 0 && text.indexOf("第二词") >= 0;
    const hasProfile = text.indexOf('name="profile"') >= 0 && text.indexOf("balanced") >= 0;
    report.cases["mask-listener-multipart-composite"] = { wordsReady, hasBoundary, hasDocPart, hasTxtPart, hasProfile, ct };
    if (!wordsReady) fail("mask-listener-multipart-composite: TXT 词未经真实监听器就绪");
    if (!hasBoundary) fail("mask-listener-multipart-composite: serializeMultipart 应产出 multipart boundary");
    if (!hasDocPart) fail("mask-listener-multipart-composite: multipart 应含真实文档字节");
    if (!hasTxtPart) fail("mask-listener-multipart-composite: multipart 应含 TXT 参与原字节");
    if (!hasProfile) fail("mask-listener-multipart-composite: multipart 应含 profile");
  }

  // 10.5：还原文件/映射经真实监听器入链，完整 multipart 字段（file 与 text 互斥）。
  {
    hooks.setRestoreMode("file");
    const rf = dom.el("#restore-file");
    const reply = fileOf("reply.txt", "占位符 [PHONE_1]");
    rf.__setFiles([reply]);
    rf.__fire("change", {});
    const restoreBytes = hooks.state.restore.file ? Buffer.from(await hooks.state.restore.file.arrayBuffer()).toString("utf8") : "";
    const mf = dom.el("#restore-mapping");
    const mappingObj = { version: 2, source_format: "docx", source_sha256: "a", source_text_sha256: "b", placeholders: [] };
    const mapFile = fileOf("a.mapping.json", JSON.stringify(mappingObj));
    mf.__setFiles([mapFile]);
    mf.__fire("change", {});
    const okMap = await waitFor(() => hooks.state.restore.mapping && hooks.state.restore.mapping.bytes);
    const md = hooks.state.restore.mapping && hooks.state.restore.mapping.metadata;
    const fdFile = hooks.buildRestoreFormData();
    const gotFile = fdFile.get("file");
    const fileBytesOk = gotFile instanceof Blob && gotFile.name === "reply.txt" && Buffer.from(await gotFile.arrayBuffer()).toString("utf8") === "占位符 [PHONE_1]";
    const textAbsent = fdFile.get("text") === null;
    const gotMap = fdFile.get("mapping");
    const mappingBytesOk = gotMap instanceof Blob && gotMap.name === "a.mapping.json" && Buffer.from(await gotMap.arrayBuffer()).toString("utf8") === JSON.stringify(mappingObj);
    hooks.setRestoreMode("text");
    dom.el("#restore-text").value = "粘贴";
    const fdText = hooks.buildRestoreFormData();
    const textHasFile = fdText.get("file") !== null;
    const textValue = fdText.get("text");
    hooks.setRestoreMode("file");
    report.cases["restore-listener-multipart"] = { restoreBytes, okMap, md, fileBytesOk, textAbsent, mappingBytesOk, textHasFile, textValue };
    if (restoreBytes !== "占位符 [PHONE_1]") fail("restore-listener-multipart: 应保留回复文件字节");
    if (!okMap || !md || md.version !== 2 || md.sourceFormat !== "docx") fail("restore-listener-multipart: 映射应解析真实快照");
    if (!fileBytesOk || !textAbsent) fail("restore-listener-multipart: file 模式 multipart 应只含真实 file 字节");
    if (!mappingBytesOk) fail("restore-listener-multipart: 应携带真实 mapping 字节与名称");
    if (textHasFile || textValue !== "粘贴") fail("restore-listener-multipart: text 模式应只含 text");
  }

  // 10.3：缺失字段保守回退 false，且能力不随主题/语言改变。
  {
    const capBefore = missHooks.fileDropEnabled();
    missHooks.setPreferences("dark", "en");
    const capAfterLang = missHooks.fileDropEnabled();
    report.cases["drop-capability-missing"] = { dropEnabled: capBefore, hintKey: missHooks.fileHintKey(), capAfterLang };
    if (capBefore || capAfterLang) fail("drop-capability-missing: 缺字段应保守 false 且不随语言改变");
    if (missHooks.fileHintKey() !== "ui.click_hint") fail("drop-capability-missing: 应使用仅点击提示键");
  }

  // 10.3：非 Windows 能力——仅点击提示、无高亮、误拖拒收不改 state 且防导航。
  {
    const capBefore = hooks.fileDropEnabled();
    hooks.setPreferences("system", "zh");
    const zhHint = hooks.fileHint();
    const zhDrop = hooks.t("ui.drop_hint");
    hooks.setPreferences("system", "en");
    const enHint = hooks.fileHint();
    const enDrop = hooks.t("ui.drop_hint");
    const area = dom.el("#mask-file-area");
    area.__fire("dragover", { preventDefault() {} });
    const highlighted = area.classList.contains("dragover");
    const prevFile = hooks.state.mask.file;
    const linuxDrop = { target: { closest: () => ({}) }, preventDefault() { linuxDrop.prevented = true; } };
    hooks.__fireWindow("drop", linuxDrop);
    const stateKept = hooks.state.mask.file === prevFile;
    const hintShown = dom.el("#global-status").hidden === false;
    const capAfterLang = hooks.fileDropEnabled();
    report.cases["drop-capability-linux"] = {
      dropEnabled: capBefore, capAfterLang, hintKey: hooks.fileHintKey(),
      zhHint, zhDrop, enHint, enDrop, highlighted, stateKept, prevented: linuxDrop.prevented === true, hintShown,
    };
    if (capBefore || capAfterLang) fail("drop-capability-linux: 能力应 false 且不随主题/语言改变");
    if (hooks.fileHintKey() !== "ui.click_hint") fail("drop-capability-linux: 应使用仅点击提示键");
    if (/拖入|drop/i.test(zhHint) || /drop/i.test(enHint)) fail("drop-capability-linux: 仅点击提示不应含拖入");
    if (!/拖入/.test(zhDrop) || !/drop/i.test(enDrop)) fail("drop-capability-linux: Windows 文案应含拖入");
    if (highlighted) fail("drop-capability-linux: Linux 不应出现拖入高亮");
    if (!stateKept) fail("drop-capability-linux: 误拖不应改 state");
    if (!linuxDrop.prevented) fail("drop-capability-linux: 目标误拖应防导航");
    if (!hintShown) fail("drop-capability-linux: 误拖应给点击提示");
  }

  // 10.3/10.5：Windows 能力——setter 抛错仍快照入链、多文件保旧、非目标防导航；两语言提示一致。
  {
    winHooks.setPreferences("system", "zh");
    const winZh = winHooks.fileHint();
    winHooks.setPreferences("system", "en");
    const winEn = winHooks.fileHint();
    const winInput = winDom.el("#mask-file");
    let setterThrew = false;
    try { winInput.files = [fileOf("x", "y")]; } catch (e) { setterThrew = true; }
    const winArea = winDom.el("#mask-file-area");
    // S3：Windows dragenter/dragover 高亮与 dropEffect，dragleave/drop 解除，均由生产回调驱动。
    const dtOver = {};
    winArea.__fire("dragover", { preventDefault() {}, dataTransfer: dtOver });
    const overHighlighted = winArea.classList.contains("dragover");
    const dropEffect = dtOver.dropEffect;
    winArea.__fire("dragleave", {});
    const leaveCleared = winArea.classList.contains("dragover") === false;
    winArea.__fire("dragenter", { preventDefault() {}, dataTransfer: {} });
    const enterHighlighted = winArea.classList.contains("dragover");
    const dropEv = { preventDefault() { dropEv.prevented = true; }, dataTransfer: { files: [fileOfBytes("dropped.txt", Buffer.from("DROPBYTES", "utf8"))] } };
    winArea.__fire("drop", dropEv);
    const dropCleared = winArea.classList.contains("dragover") === false;
    const loaded = winHooks.state.mask.file;
    const bytes = loaded ? Buffer.from(await loaded.arrayBuffer()).toString("utf8") : "";
    const prev = loaded;
    winArea.__fire("drop", { preventDefault() {}, dataTransfer: { files: [fileOf("a", "a"), fileOf("b", "b")] } });
    const multiKept = winHooks.state.mask.file === prev;
    let nonTargetPrevented = false;
    winHooks.__fireWindow("drop", { target: { closest: () => null }, preventDefault() { nonTargetPrevented = true; } });
    report.cases["drop-capability-windows"] = {
      dropEnabled: winHooks.fileDropEnabled(), hintKey: winHooks.fileHintKey(), winZh, winEn,
      setterThrew, overHighlighted, dropEffect, leaveCleared, enterHighlighted, dropCleared,
      loadedName: loaded && loaded.name, bytes, dropPrevented: dropEv.prevented === true, multiKept, nonTargetPrevented,
    };
    if (!winHooks.fileDropEnabled() || winHooks.fileHintKey() !== "ui.drop_hint") fail("drop-capability-windows: 应为 true 且用拖入提示键");
    if (!/拖入/.test(winZh) || !/drop/i.test(winEn)) fail("drop-capability-windows: 拖入提示应双语");
    if (!setterThrew) fail("drop-capability-windows: fixture 应模拟 input.files setter 抛错");
    if (!overHighlighted || dropEffect !== "copy") fail("drop-capability-windows: dragover 应高亮且 dropEffect=copy");
    if (!leaveCleared) fail("drop-capability-windows: dragleave 应解除高亮");
    if (!enterHighlighted) fail("drop-capability-windows: dragenter 应高亮");
    if (!dropCleared) fail("drop-capability-windows: drop 应解除高亮");
    if (!loaded || loaded.name !== "dropped.txt" || bytes !== "DROPBYTES") fail("drop-capability-windows: 拖入应快照字节进入原链");
    if (!dropEv.prevented) fail("drop-capability-windows: 目标拖入应 preventDefault");
    if (!multiKept) fail("drop-capability-windows: 多文件拖入应保旧");
    if (!nonTargetPrevented) fail("drop-capability-windows: 非目标拖入应防导航");
  }

  // 11.1/11.2/11.3：三产物真实保存路径的单一成功句、全态回退、双语重绘、部分保存与清理警告。
  {
    hooks.setPreferences("system", "zh");

    // 自动保存：真实 autoSaveMask 回调 → applyWorkspaceSave → renderMaskResults。
    hooks.state.mask.lastBody = { format: "text", encoding: "UTF-8", cleanup: null, warnings: [], audit_failed: false };
    hooks.state.mask.artifacts = {
      masked: { id: "masked", view: "mask", name: "notes.masked.txt", b64: "eA==", status: "unsaved", busy: false },
      mapping: { id: "mapping", view: "mask", name: "notes.mapping.json", b64: "eA==", status: "unsaved", busy: false }
    };
    plan.autoMaskResult = {
      masked: { status: "saved", path: "C:/app/masked/notes.2.masked.txt" },
      mapping: { status: "saved", path: "C:/app/mapping/notes.2.mapping.json" }
    };
    hooks.autoSaveMask({}, hooks.state.mask.artifacts.masked, hooks.state.mask.artifacts.mapping);
    await waitFor(() => hooks.state.mask.artifacts.masked.status === "saved" && hooks.state.mask.artifacts.mapping.status === "saved");
    let mh = dom.el("#mask-result").innerHTML;
    const maskedSent = mh.split("脱敏副本已保存到").length - 1;
    const mappingSent = mh.split("还原映射已保存到").length - 1;
    const maskedPathOnce = mh.split("C:/app/masked/notes.2.masked.txt").length - 1;
    const oldSuggested = mh.indexOf("notes.masked.txt") >= 0;
    const oldAutoLine = mh.indexOf("已自动保存到") >= 0;
    const noSavedStatusChip = mh.indexOf("artifact-status") < 0;
    report.cases["saved-sentence-single"] = { maskedSent, mappingSent, maskedPathOnce, oldSuggested, oldAutoLine, noSavedStatusChip };
    if (maskedSent !== 1 || mappingSent !== 1 || maskedPathOnce !== 1) fail("saved-sentence-single: 三产物应各仅一条带编号实际路径成功句");
    if (oldSuggested) fail("saved-sentence-single: 不应再显无编号建议名");
    if (oldAutoLine) fail("saved-sentence-single: 不应再显旧自动保存重复行");
    if (!noSavedStatusChip) fail("saved-sentence-single: 已保存且非保存中不得渲染独立已保存状态 chip");

    // 双语重绘：类型/已保存到句随语言，路径原样且切换不累积。
    hooks.setPreferences("system", "en");
    mh = dom.el("#mask-result").innerHTML;
    const enMasked = mh.indexOf("Masked copy saved to C:/app/masked/notes.2.masked.txt") >= 0;
    const enMapping = mh.indexOf("Restore mapping saved to C:/app/mapping/notes.2.mapping.json") >= 0;
    const enOnce = mh.split("saved to").length - 1;
    hooks.setPreferences("system", "zh");
    const backOnce = dom.el("#mask-result").innerHTML.split("已保存到").length - 1;
    report.cases["saved-sentence-bilingual"] = { enMasked, enMapping, enOnce, backOnce };
    if (!enMasked || !enMapping) fail("saved-sentence-bilingual: 英文应更新类型与已保存到句");
    if (enOnce !== 2) fail("saved-sentence-bilingual: 英文重绘不应累积");
    if (backOnce !== 2) fail("saved-sentence-bilingual: 切回中文不应累积");
  }

  // 还原自动保存：真实 autoSaveRestored 回调。
  {
    hooks.setPreferences("system", "zh");
    hooks.state.restore.lastBody = { format: "text", encoding: "UTF-8", restored_count: 1, warnings: [], audit_failed: false };
    hooks.state.restore.artifacts = { restored: { id: "restored", view: "restore", name: "reply.restored.txt", b64: "eA==", status: "unsaved", busy: false } };
    plan.autoRestoreResult = { status: "saved", path: "C:/app/restored/reply.3.restored.txt" };
    hooks.autoSaveRestored({}, hooks.state.restore.artifacts.restored);
    await waitFor(() => hooks.state.restore.artifacts.restored.status === "saved");
    const rh = dom.el("#restore-result").innerHTML;
    const restoredSent = rh.split("还原文件已保存到").length - 1;
    const restoredPath = rh.split("C:/app/restored/reply.3.restored.txt").length - 1;
    const restoredOldName = rh.indexOf("reply.restored.txt") >= 0;
    const restoredNoStatusChip = rh.indexOf("artifact-status") < 0;
    report.cases["restored-saved-sentence"] = { restoredSent, restoredPath, restoredOldName, restoredNoStatusChip };
    if (restoredSent !== 1 || restoredPath !== 1) fail("restored-saved-sentence: 还原文件应仅一条带编号实际路径成功句");
    if (restoredOldName) fail("restored-saved-sentence: 不应再显无编号建议名");
    if (!restoredNoStatusChip) fail("restored-saved-sentence: 已保存且非保存中不得渲染独立已保存状态 chip");
  }

  // 手动保存全态：成功换最新 destination、取消保旧、失败不遮蔽、重试更新。
  {
    hooks.setPreferences("system", "zh");
    hooks.state.mask.lastBody = { format: "text", encoding: "UTF-8", cleanup: null, warnings: [], audit_failed: false };
    const art = { id: "masked", view: "mask", name: "notes.masked.txt", b64: "eA==", status: "unsaved", busy: false };
    hooks.state.mask.artifacts = {
      masked: art,
      mapping: { id: "mapping", view: "mask", name: "notes.mapping.json", b64: "eA==", status: "saved", autoPath: "C:/app/mapping/notes.2.mapping.json", busy: false }
    };

    // 成功：显示新 destination，不写“自动”。
    plan.saveFileResult = { status: "saved", destination: "C:/user/renamed.txt" };
    hooks.nativeSave(art);
    await waitFor(() => art.busy === false && art.status === "saved");
    const hRenamed = dom.el("#mask-result").innerHTML;
    const renameOnce = hRenamed.split("脱敏副本已保存到 C:/user/renamed.txt").length - 1;
    const noAutoWording = hRenamed.indexOf("已自动保存") < 0;
    report.cases["manual-save-rename"] = { renameOnce, noAutoWording, path: art.autoPath };
    if (renameOnce !== 1 || !noAutoWording) fail("manual-save-rename: 手动成功应显示新实际路径且不称自动保存");

    // 取消：保留旧成功路径与已保存状态。
    plan.saveFileResult = { status: "cancelled" };
    hooks.nativeSave(art);
    await waitFor(() => art.busy === false);
    const hCancel = dom.el("#mask-result").innerHTML;
    const cancelKeeps = art.status === "saved" && art.autoPath === "C:/user/renamed.txt" && hCancel.indexOf("脱敏副本已保存到 C:/user/renamed.txt") >= 0;
    report.cases["manual-save-cancel"] = { cancelKeeps, status: art.status, autoPath: art.autoPath };
    if (!cancelKeeps) fail("manual-save-cancel: 取消应保留旧实际路径与已保存状态");

    // 失败：即使内存有旧成功路径也不得以旧句遮蔽失败。
    plan.saveFileResult = { status: "failed", message_id: "save.write_failed" };
    hooks.nativeSave(art);
    await waitFor(() => art.busy === false && art.status === "failed");
    const hFailed = dom.el("#mask-result").innerHTML;
    const failedVisible = hFailed.indexOf("保存失败") >= 0 && hFailed.indexOf("写入失败") >= 0;
    const oldPathHidden = hFailed.indexOf("脱敏副本已保存到 C:/user/renamed.txt") < 0;
    report.cases["manual-save-failed-old-path"] = { failedVisible, oldPathHidden, status: art.status };
    if (!failedVisible) fail("manual-save-failed-old-path: 失败及原因应可见");
    if (!oldPathHidden) fail("manual-save-failed-old-path: 旧成功句不得遮蔽失败");

    // 重试：成功更新为最新实际路径。
    plan.saveFileResult = { status: "saved", destination: "C:/user/retry.txt" };
    hooks.nativeSave(art);
    await waitFor(() => art.busy === false && art.status === "saved");
    const hRetry = dom.el("#mask-result").innerHTML;
    const retryOnce = hRetry.split("脱敏副本已保存到 C:/user/retry.txt").length - 1;
    const oldGone = hRetry.indexOf("C:/user/renamed.txt") < 0;
    report.cases["manual-save-retry"] = { retryOnce, oldGone };
    if (retryOnce !== 1 || !oldGone) fail("manual-save-retry: 重试成功应更新为最新实际路径");
  }

  // 未保存/保存中/缺路径回退与路径转义。
  {
    hooks.setPreferences("system", "zh");
    const unsaved = hooks.artifactCard({ id: "restored", view: "restore", name: "reply.txt", status: "unsaved", busy: false });
    const busySaved = hooks.artifactCard({ id: "masked", view: "mask", name: "x.masked.txt", status: "saved", autoPath: "C:/a/1.x.txt", busy: true });
    const busyUnsaved = hooks.artifactCard({ id: "masked", view: "mask", name: "x.masked.txt", status: "unsaved", busy: true });
    const savedNoPath = hooks.artifactCard({ id: "mapping", view: "mask", name: "a.mapping.json", status: "saved", busy: false });
    const escaped = hooks.artifactCard({ id: "restored", view: "restore", name: "r.txt", status: "saved", autoPath: "C:/a&b/<c>.txt", busy: false });
    const ok = unsaved.indexOf("还原文件：reply.txt") >= 0 &&
      unsaved.indexOf("已保存到") < 0 &&
      busySaved.indexOf("脱敏副本已保存到 C:/a/1.x.txt") >= 0 &&
      busySaved.indexOf("保存中") >= 0 &&
      busySaved.indexOf("disabled") >= 0 &&
      busyUnsaved.indexOf("脱敏副本：x.masked.txt") >= 0 &&
      busyUnsaved.indexOf("已保存到") < 0 &&
      savedNoPath.indexOf("还原映射：a.mapping.json") >= 0 &&
      savedNoPath.indexOf("已保存") >= 0 &&
      savedNoPath.indexOf("已保存到") < 0 &&
      escaped.indexOf("C:/a&amp;b/&lt;c&gt;.txt") >= 0;
    report.cases["saved-state-fallbacks"] = {
      unsaved, busySaved, busyUnsaved, savedNoPath, escapedAmpEscaped: escaped.indexOf("C:/a&amp;b/&lt;c&gt;.txt") >= 0,
    };
    if (!ok) fail("saved-state-fallbacks: 未保存/保存中/缺路径/转义回退不符合决策10");
  }

  // C1：已保存有实际路径且非保存中，不渲染独立已保存状态 chip；仅单一成功句。
  {
    hooks.setPreferences("system", "zh");
    const savedPathCard = hooks.artifactCard({ id: "mapping", view: "mask", name: "n.mapping.json", status: "saved", autoPath: "C:/app/mapping/n.2.mapping.json", busy: false });
    const hasStatusSpan = savedPathCard.indexOf("artifact-status") >= 0;
    const sentenceOnce = savedPathCard.split("还原映射已保存到 C:/app/mapping/n.2.mapping.json").length - 1;
    const noSavedChip = savedPathCard.indexOf(">已保存<") < 0;
    report.cases["saved-no-status-chip"] = { hasStatusSpan, sentenceOnce, noSavedChip };
    if (hasStatusSpan) fail("saved-no-status-chip: 已保存有路径且非保存中不得有独立状态 span");
    if (sentenceOnce !== 1 || !noSavedChip) fail("saved-no-status-chip: 应只有单一成功句、无独立已保存 chip");
  }

  // W1：真实 nativeSave 返回 status:saved + cleanup_warning，成功句与警告并存且无独立已保存 chip。
  {
    hooks.setPreferences("system", "zh");
    hooks.state.mask.lastBody = { format: "text", encoding: "UTF-8", cleanup: null, warnings: [], audit_failed: false };
    const art = { id: "masked", view: "mask", name: "notes.masked.txt", b64: "eA==", status: "unsaved", busy: false };
    hooks.state.mask.artifacts = { masked: art, mapping: { id: "mapping", view: "mask", name: "m.mapping.json", b64: "eA==", status: "saved", autoPath: "C:/user/m.mapping.json", busy: false } };
    plan.saveFileResult = {
      status: "saved", destination: "C:/user/warned.txt",
      cleanup_warning: "已保存，但临时文件清理失败，请手动检查目的目录。", cleanup_warning_id: "save.cleanup_warning"
    };
    hooks.nativeSave(art);
    await waitFor(() => art.busy === false && art.status === "saved");
    let wh = dom.el("#mask-result").innerHTML;
    const warnSentOnce = wh.split("脱敏副本已保存到 C:/user/warned.txt").length - 1;
    const warnShown = wh.indexOf("临时文件清理失败") >= 0;
    const warnNoStatusChip = wh.indexOf("artifact-status") < 0;
    report.cases["native-save-cleanup-warning"] = { warnSentOnce, warnShown, warnNoStatusChip, autoErrorID: art.autoErrorID, saveWarningID: art.saveWarningID };
    if (warnSentOnce !== 1) fail("native-save-cleanup-warning: 成功句应仅一次");
    if (!warnShown) fail("native-save-cleanup-warning: 保存成功附带的 cleanup_warning 应可见");
    if (!warnNoStatusChip) fail("native-save-cleanup-warning: 已保存有路径不得有独立已保存 chip");
    if (art.autoErrorID) fail("native-save-cleanup-warning: 成功告警不得写入失败 error 字段");

    // 双语：切英文重绘成功句与警告，路径原样，不累积。
    hooks.setPreferences("system", "en");
    wh = dom.el("#mask-result").innerHTML;
    const enWarn = wh.indexOf("cleaning up the temporary file failed") >= 0;
    const enSent = wh.indexOf("Masked copy saved to C:/user/warned.txt") >= 0;
    hooks.setPreferences("system", "zh");
    report.cases["native-save-warning-bilingual"] = { enWarn, enSent, backWarn: dom.el("#mask-result").innerHTML.indexOf("临时文件清理失败") >= 0 };
    if (!enWarn || !enSent) fail("native-save-warning-bilingual: 英文应重绘成功句与警告");
  }

  // W1：失败后重试正常成功（无 warning）应清除旧失败、不残旧 error/warning。
  {
    hooks.setPreferences("system", "zh");
    hooks.state.mask.lastBody = { format: "text", encoding: "UTF-8", cleanup: null, warnings: [], audit_failed: false };
    const art = { id: "masked", view: "mask", name: "notes.masked.txt", b64: "eA==", status: "unsaved", busy: false };
    hooks.state.mask.artifacts = { masked: art, mapping: { id: "mapping", view: "mask", name: "m.mapping.json", b64: "eA==", status: "saved", autoPath: "C:/user/m.mapping.json", busy: false } };
    plan.saveFileResult = { status: "error", message_id: "save.write_failed" };
    hooks.nativeSave(art);
    await waitFor(() => art.busy === false && art.status === "failed");
    const failedShown = dom.el("#mask-result").innerHTML.indexOf("写入失败") >= 0;
    plan.saveFileResult = { status: "saved", destination: "C:/user/ok.txt" };
    hooks.nativeSave(art);
    await waitFor(() => art.busy === false && art.status === "saved");
    const rh = dom.el("#mask-result").innerHTML;
    const successShown = rh.split("脱敏副本已保存到 C:/user/ok.txt").length - 1 === 1;
    const noStaleError = rh.indexOf("写入失败") < 0;
    const noWarning = rh.indexOf("临时文件清理失败") < 0;
    report.cases["native-save-retry-clears-failure"] = { failedShown, successShown, noStaleError, noWarning };
    if (!failedShown) fail("native-save-retry-clears-failure: 应先显示失败");
    if (!successShown || !noStaleError || !noWarning) fail("native-save-retry-clears-failure: 重试成功应清旧失败、无旧 error/warning");
  }

  // W1：取消保留已有成功告警与路径。
  {
    hooks.setPreferences("system", "zh");
    const art = { id: "masked", view: "mask", name: "notes.masked.txt", b64: "eA==", status: "saved", autoPath: "C:/user/kept.txt", saveWarningID: "save.cleanup_warning", saveWarningParams: null, busy: false };
    hooks.state.mask.artifacts = { masked: art, mapping: { id: "mapping", view: "mask", name: "m.mapping.json", b64: "eA==", status: "saved", autoPath: "C:/user/m.mapping.json", busy: false } };
    plan.saveFileResult = { status: "cancelled" };
    hooks.nativeSave(art);
    await waitFor(() => art.busy === false);
    const ch = dom.el("#mask-result").innerHTML;
    const cancelKeepsWarn = art.status === "saved" && art.saveWarningID === "save.cleanup_warning" &&
      ch.indexOf("临时文件清理失败") >= 0 && ch.indexOf("脱敏副本已保存到 C:/user/kept.txt") >= 0;
    report.cases["native-save-cancel-keeps-warning"] = { cancelKeepsWarn, status: art.status };
    if (!cancelKeepsWarn) fail("native-save-cancel-keeps-warning: 取消应保留已有 warning 与路径");
  }

  // 部分保存与成功附带的清理警告仍就近保留，不因合并成功行丢失。
  {
    hooks.setPreferences("system", "zh");
    hooks.state.mask.lastBody = { format: "docx", encoding: "", cleanup: { comments_deleted: 2, revisions_accepted: 1 }, warnings: [], audit_failed: false };
    hooks.state.mask.artifacts = {
      masked: { id: "masked", view: "mask", name: "c.masked.docx", b64: "eA==", status: "saved", autoPath: "C:/app/masked/c.1.masked.docx", busy: false },
      mapping: { id: "mapping", view: "mask", name: "c.mapping.json", b64: "eA==", status: "unsaved", busy: false }
    };
    hooks.renderMaskResults();
    const ph = dom.el("#mask-result").innerHTML;
    const hasPartial = ph.indexOf("尚未保存") >= 0;
    const hasCleanup = ph.indexOf("删除批注 2 处") >= 0;
    const maskedOnce = ph.split("脱敏副本已保存到 C:/app/masked/c.1.masked.docx").length - 1;
    report.cases["partial-and-cleanup"] = { hasPartial, hasCleanup, maskedOnce };
    if (!hasPartial) fail("partial-and-cleanup: 部分保存提醒应常显");
    if (!hasCleanup) fail("partial-and-cleanup: 清理警告应就近保留");
    if (maskedOnce !== 1) fail("partial-and-cleanup: 已保存项应仅一条成功句");
  }

  // 12.1：无 I/O 纯词法显示名提取——Windows 盘符/UNC/扩展按两种分隔，Linux 仅按 / 保合法反斜杠，
  // 字面保留空格/中文/特殊字符，相对/尾分隔/末段空或 ./.. 及非字符串回退 null。
  {
    const lex = hooks.extractDisplayName;
    const vals = {
      winDrive: lex("C:\\dir\\notes.2.mapping.json"),
      winDriveFwd: lex("C:/app/mapping/notes.2.mapping.json"),
      unc: lex("\\\\server\\share\\a b.中文.json"),
      extended: lex("\\\\?\\C:\\x\\e<&>.txt"),
      linuxBackslash: lex("/home/u/合法\\反斜杠.txt"),
      linuxSpaces: lex("/home/u/内部 空格.txt"),
      relativeWin: lex("dir\\file.txt"),
      relativeUnix: lex("dir/file.txt"),
      trailingWin: lex("C:\\dir\\"),
      trailingUnix: lex("/home/u/"),
      dot: lex("/home/u/."),
      dotdot: lex("C:\\u\\.."),
      nonString: lex(123),
      empty: lex(""),
      blank: lex("   "),
    };
    report.cases["display-name-lexical"] = vals;
    if (vals.winDrive !== "notes.2.mapping.json") fail("display-name-lexical: Windows 盘符路径应取末文件名");
    if (vals.winDriveFwd !== "notes.2.mapping.json") fail("display-name-lexical: Windows 正斜杠路径应取末文件名");
    if (vals.unc !== "a b.中文.json") fail("display-name-lexical: UNC 应保留空格/中文");
    if (vals.extended !== "e<&>.txt") fail("display-name-lexical: 扩展路径应取末文件名且保留特殊字符");
    if (vals.linuxBackslash !== "合法\\反斜杠.txt") fail("display-name-lexical: Linux 文件名合法反斜杠不得误拆");
    if (vals.linuxSpaces !== "内部 空格.txt") fail("display-name-lexical: Linux 应保留空格/中文且不 trim");
    if (vals.relativeWin !== null || vals.relativeUnix !== null) fail("display-name-lexical: 相对路径应回退");
    if (vals.trailingWin !== null || vals.trailingUnix !== null) fail("display-name-lexical: 尾分隔应回退");
    if (vals.dot !== null || vals.dotdot !== null) fail("display-name-lexical: . / .. 末段应回退");
    if (vals.nonString !== null || vals.empty !== null || vals.blank !== null) fail("display-name-lexical: 非字符串/空应回退");
  }

  // 12.2/12.3：自动保存真实回调→已注册按钮 click→来源/名称 DOM→生产 multipart。
  {
    hooks.setPreferences("system", "zh");
    hooks.state.mask.lastBody = { format: "text", encoding: "UTF-8", cleanup: null, warnings: [], audit_failed: false };
    const mapObj = { version: 2, source_format: "txt", source_sha256: "a", source_text_sha256: "b", placeholders: [] };
    const mapB64 = Buffer.from(JSON.stringify(mapObj)).toString("base64");
    const mapping = { id: "mapping", view: "mask", name: "notes.mapping.json", b64: mapB64, status: "unsaved", busy: false };
    hooks.state.mask.artifacts = {
      masked: { id: "masked", view: "mask", name: "notes.masked.txt", b64: "eA==", status: "unsaved", busy: false },
      mapping: mapping,
    };
    plan.autoMaskResult = {
      masked: { status: "saved", path: "C:/app/masked/notes.2.masked.txt" },
      mapping: { status: "saved", path: "C:/app/mapping/notes.2.mapping.json" },
    };
    hooks.autoSaveMask({}, hooks.state.mask.artifacts.masked, mapping);
    const saved = await waitFor(() => mapping.status === "saved" && mapping.autoPath);
    dom.el("#use-session-mapping").__fire("click", {});
    const srcText = dom.el("#mapping-source").textContent;
    const nameText = dom.el("#restore-mapping-name").textContent;
    const snap = hooks.state.restore.mapping;
    const fd = hooks.buildRestoreFormData();
    const blob = fd.get("mapping");
    const blobName = blob && blob.name;
    const blobBytes = blob ? Buffer.from(await blob.arrayBuffer()).toString("utf8") : "";
    const payload = await hooks.serializeMultipart(fd);
    const mpText = Buffer.from(payload.buffer).toString("utf8");
    const maskStillFullPath = dom.el("#mask-result").innerHTML.indexOf("C:/app/mapping/notes.2.mapping.json") >= 0;
    const md = snap && snap.metadata;
    report.cases["session-display-name-autosave"] = {
      saved, srcText, nameText, blobName, mpHasSuggested: mpText.indexOf('filename="notes.mapping.json"') >= 0,
      mpHasActual: mpText.indexOf('filename="notes.2.mapping.json"') >= 0,
      snapName: snap && snap.name, snapDisplay: snap && snap.displayName,
      mdVersion: md && md.version, mdFormat: md && md.sourceFormat, maskStillFullPath,
    };
    if (!saved) fail("session-display-name-autosave: 自动保存回调未写入 autoPath");
    if (!(srcText.indexOf("notes.2.mapping.json") >= 0 && nameText === "notes.2.mapping.json")) fail("session-display-name-autosave: 来源/名称应显示最近成功实际文件名");
    if (srcText.indexOf("C:/app") >= 0) fail("session-display-name-autosave: 显示名不得带完整目录");
    if (!(snap && snap.name === "notes.mapping.json" && snap.displayName === "notes.2.mapping.json")) fail("session-display-name-autosave: 上传建议名与显示名应分离");
    if (blobName !== "notes.mapping.json" || blobBytes !== JSON.stringify(mapObj)) fail("session-display-name-autosave: multipart 应用原建议上传名与原字节");
    if (mpText.indexOf('filename="notes.2.mapping.json"') >= 0) fail("session-display-name-autosave: 上传文件名不得改为显示名");
    if (!(md && md.version === 2 && md.sourceFormat === "txt")) fail("session-display-name-autosave: metadata 应来自同一映射快照");
    if (!maskStillFullPath) fail("session-display-name-autosave: 第五轮结果行仍应显示完整实际路径");
  }

  // 12.2/12.3：手动另存改名后点击带入，显示最近成功真实文件名（Windows 盘符路径）。
  {
    hooks.setPreferences("system", "zh");
    hooks.state.mask.lastBody = { format: "text", encoding: "UTF-8", cleanup: null, warnings: [], audit_failed: false };
    const mapB64b = Buffer.from(JSON.stringify({ version: 1 })).toString("base64");
    const mappingB = { id: "mapping", view: "mask", name: "suggest.mapping.json", b64: mapB64b, status: "unsaved", busy: false };
    hooks.state.mask.artifacts = {
      masked: { id: "masked", view: "mask", name: "notes.masked.txt", b64: "eA==", status: "saved", autoPath: "C:/app/masked/notes.2.masked.txt", busy: false },
      mapping: mappingB,
    };
    plan.saveFileResult = { status: "saved", destination: "D:\\out\\手动改名.映射.json" };
    hooks.nativeSave(mappingB);
    const renamed = await waitFor(() => mappingB.busy === false && mappingB.status === "saved");
    dom.el("#use-session-mapping").__fire("click", {});
    const name3 = dom.el("#restore-mapping-name").textContent;
    const src3 = dom.el("#mapping-source").textContent;
    const upload = hooks.buildRestoreFormData().get("mapping");
    report.cases["session-display-name-manual-rename"] = {
      renamed, name: name3, srcText: src3, uploadName: upload && upload.name,
      noDir: src3.indexOf("D:\\out") < 0 && src3.indexOf("D:/out") < 0,
    };
    if (!renamed) fail("session-display-name-manual-rename: 手动另存应成功");
    if (name3 !== "手动改名.映射.json") fail("session-display-name-manual-rename: 应显示改名后的实际文件名");
    if (src3.indexOf("手动改名.映射.json") < 0 || src3.indexOf("D:\\out") >= 0) fail("session-display-name-manual-rename: 来源应含真实文件名且无完整目录");
    if (!upload || upload.name !== "suggest.mapping.json") fail("session-display-name-manual-rename: 上传名应保持建议名");
  }

  // 12.2/12.3：取消/失败保留最近成功路径名，无路径/相对/异常回退建议名，Linux 合法反斜杠保留，busy/无产物不改快照。
  {
    hooks.setPreferences("system", "zh");
    hooks.state.mask.lastBody = { format: "text", encoding: "UTF-8", cleanup: null, warnings: [], audit_failed: false };
    const mapB64c = Buffer.from(JSON.stringify({ version: 2, source_format: "txt" })).toString("base64");
    const mappingC = { id: "mapping", view: "mask", name: "base.mapping.json", b64: mapB64c, status: "saved", autoPath: "C:\\app\\mapping\\base.3.mapping.json", busy: false };
    hooks.state.mask.artifacts = {
      masked: { id: "masked", view: "mask", name: "notes.masked.txt", b64: "eA==", status: "saved", autoPath: "C:/app/masked/notes.2.masked.txt", busy: false },
      mapping: mappingC,
    };
    plan.saveFileResult = { status: "cancelled" };
    hooks.nativeSave(mappingC);
    await waitFor(() => mappingC.busy === false);
    dom.el("#use-session-mapping").__fire("click", {});
    const cancelName = dom.el("#restore-mapping-name").textContent;
    plan.saveFileResult = { status: "failed", message_id: "save.write_failed" };
    hooks.nativeSave(mappingC);
    await waitFor(() => mappingC.busy === false && mappingC.status === "failed");
    const failFeedback = dom.el("#mask-result").innerHTML.indexOf("写入失败") >= 0;
    dom.el("#use-session-mapping").__fire("click", {});
    const failName = dom.el("#restore-mapping-name").textContent;
    // 无成功路径的未保存映射：回退建议名。
    mappingC.status = "unsaved";
    delete mappingC.autoPath;
    dom.el("#use-session-mapping").__fire("click", {});
    const noPathName = dom.el("#restore-mapping-name").textContent;
    // 相对路径（非绝对）：回退建议名。
    mappingC.autoPath = "relative\\dir\\x.json";
    dom.el("#use-session-mapping").__fire("click", {});
    const relativeName = dom.el("#restore-mapping-name").textContent;
    // Linux 绝对路径中合法反斜杠应保留。
    mappingC.autoPath = "/home/u/合法\\反斜杠.txt";
    dom.el("#use-session-mapping").__fire("click", {});
    const linuxName = dom.el("#restore-mapping-name").textContent;
    // busy 时点击不改旧快照。
    mappingC.autoPath = "C:\\app\\mapping\\base.3.mapping.json";
    dom.el("#use-session-mapping").__fire("click", {});
    const beforeBusy = hooks.state.restore.mapping.displayName;
    hooks.appState.busy = true;
    dom.el("#use-session-mapping").__fire("click", {});
    const afterBusy = hooks.state.restore.mapping.displayName;
    hooks.appState.busy = false;
    // 无产物时点击不改旧快照。
    const prevSnap = hooks.state.restore.mapping;
    hooks.state.mask.artifacts.mapping = null;
    dom.el("#use-session-mapping").__fire("click", {});
    const noArtifactKept = hooks.state.restore.mapping === prevSnap;
    hooks.state.mask.artifacts.mapping = mappingC; // 恢复，避免后续重绘读取空产物
    report.cases["session-display-name-fallback-busy"] = {
      cancelName, failFeedback, failName, noPathName, relativeName, linuxName, beforeBusy, afterBusy, noArtifactKept,
    };
    if (cancelName !== "base.3.mapping.json") fail("session-display-name-fallback-busy: 取消应保留最近成功路径名");
    if (!failFeedback) fail("session-display-name-fallback-busy: 失败结果行反馈应保留");
    if (failName !== "base.3.mapping.json") fail("session-display-name-fallback-busy: 失败后仍可显示最近成功路径名");
    if (noPathName !== "base.mapping.json") fail("session-display-name-fallback-busy: 无路径应回退建议名");
    if (relativeName !== "base.mapping.json") fail("session-display-name-fallback-busy: 相对路径应回退建议名");
    if (linuxName !== "合法\\反斜杠.txt") fail("session-display-name-fallback-busy: Linux 合法反斜杠应保留");
    if (beforeBusy !== "base.3.mapping.json" || afterBusy !== beforeBusy) fail("session-display-name-fallback-busy: busy 时不得改快照");
    if (!noArtifactKept) fail("session-display-name-fallback-busy: 无产物时不得改快照");
  }

  // 12.2/12.3：带入 A 后另存改名/新脱敏/双语主题视图切换不改 A 快照；重按才替换为 B；上传名/字节保持。
  {
    hooks.setPreferences("system", "zh");
    hooks.state.mask.lastBody = { format: "text", encoding: "UTF-8", cleanup: null, warnings: [], audit_failed: false };
    const aObj = { version: 2, source_format: "txt", source_sha256: "a" };
    const bObj = { version: 2, source_format: "docx", source_sha256: "b" };
    const aB64 = Buffer.from(JSON.stringify(aObj)).toString("base64");
    const bB64 = Buffer.from(JSON.stringify(bObj)).toString("base64");
    const mappingA = { id: "mapping", view: "mask", name: "A.mapping.json", b64: aB64, status: "saved", autoPath: "C:\\app\\mapping\\A.1.mapping.json", busy: false };
    hooks.state.mask.artifacts = {
      masked: { id: "masked", view: "mask", name: "A.masked.txt", b64: "eA==", status: "saved", autoPath: "C:/app/masked/A.1.masked.txt", busy: false },
      mapping: mappingA,
    };
    dom.el("#use-session-mapping").__fire("click", {});
    const aDisplay = hooks.state.restore.mapping.displayName;
    const aB64snap = hooks.state.restore.mapping.b64;
    const fdA = hooks.buildRestoreFormData();
    const blobA = fdA.get("mapping");
    const blobAName = blobA && blobA.name;
    const blobABytes = blobA ? Buffer.from(await blobA.arrayBuffer()).toString("utf8") : "";
    const blobABytesOk = blobABytes === JSON.stringify(aObj);
    // 带入后另存改名不改已带入显示名。
    plan.saveFileResult = { status: "saved", destination: "C:\\app\\mapping\\A.RENAMED.json" };
    hooks.nativeSave(mappingA);
    await waitFor(() => mappingA.busy === false && mappingA.status === "saved");
    const afterRename = hooks.state.restore.mapping.displayName;
    // 新脱敏产生 B 不改已带入快照。
    const mappingB = { id: "mapping", view: "mask", name: "B.mapping.json", b64: bB64, status: "saved", autoPath: "C:\\app\\mapping\\B.2.mapping.json", busy: false };
    hooks.state.mask.artifacts = {
      masked: { id: "masked", view: "mask", name: "B.masked.txt", b64: "eA==", status: "saved", autoPath: "C:/app/masked/B.2.masked.txt", busy: false },
      mapping: mappingB,
    };
    hooks.renderMaskResults();
    const afterNewMaskDisplay = hooks.state.restore.mapping.displayName;
    const afterNewMaskB64 = hooks.state.restore.mapping.b64;
    // 双语/主题/视图切换不改名称内容。
    hooks.setPreferences("dark", "en");
    hooks.setView("mask");
    hooks.setView("restore");
    const afterPrefsDomName = dom.el("#restore-mapping-name").textContent;
    const afterPrefsDisplay = hooks.state.restore.mapping.displayName;
    // 重按才替换为 B 及其最近成功名。
    dom.el("#use-session-mapping").__fire("click", {});
    const replacedDisplay = hooks.state.restore.mapping.displayName;
    const replacedB64 = hooks.state.restore.mapping.b64;
    hooks.setPreferences("system", "zh");
    report.cases["session-display-name-snapshot-frozen"] = {
      aDisplay, afterRename, afterNewMaskDisplay, afterNewMaskB64,
      afterPrefsDomName, afterPrefsDisplay, replacedDisplay, replacedB64,
      blobAName, blobABytesOk,
    };
    if (aDisplay !== "A.1.mapping.json") fail("session-display-name-snapshot-frozen: A 应显示最近成功实际名");
    if (afterRename !== aDisplay) fail("session-display-name-snapshot-frozen: 另存改名不得改写已带入显示名");
    if (afterNewMaskDisplay !== aDisplay || afterNewMaskB64 !== aB64snap) fail("session-display-name-snapshot-frozen: 新脱敏不得改写已带入名称/内容");
    if (afterPrefsDomName !== aDisplay || afterPrefsDisplay !== aDisplay) fail("session-display-name-snapshot-frozen: 双语/主题/视图切换不得改名称");
    if (replacedDisplay !== "B.2.mapping.json" || replacedB64 !== bB64) fail("session-display-name-snapshot-frozen: 再次显式点击应替换为 B");
    if (blobAName !== "A.mapping.json" || !blobABytesOk) fail("session-display-name-snapshot-frozen: 带入 A 时上传名/字节应保持原快照");
  }
}

main()
  .then(async () => {
    await writeFile(reportPath, JSON.stringify(report, null, 2));
    if (report.failures.length) { console.error("ui_regression 失败:\n" + report.failures.join("\n")); process.exit(1); }
  })
  .catch(async (err) => {
    report.fatal = err && err.stack ? err.stack : String(err);
    try { await writeFile(reportPath, JSON.stringify(report, null, 2)); } catch (e) {}
    console.error("ui_regression fatal: " + report.fatal);
    process.exit(1);
  });
