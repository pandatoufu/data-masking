// 真实 app.js 固定目录快捷选择回归 harness（Node v22；非 app 运行/build 依赖）。
//
// 加载真实 `web/static/app.js`，用轻量 DOM fake + 测试钩子驱动真实 initWorkspacePickers /
// refreshWorkspaceList / onWorkspaceSelect / buildMaskFormData / buildRestoreFormData /
// runRestore / applyImportedWords，注入 fake Wails Bridge，断言只列举名称、显式读取后复用
// 原 multipart/解析链、快照与列表解耦、过期响应与忙态保护。fetch 为桩，不发起真实网络。
//
// 用法：node workspace_selectors.mjs <appJsPath> <reportPath>
import { readFile, writeFile } from "node:fs/promises";
import vm from "node:vm";

const [appJsPath, reportPath] = process.argv.slice(2);
if (!appJsPath || !reportPath) {
  console.error("用法: node workspace_selectors.mjs <app.js> <reportPath>");
  process.exit(2);
}

const report = { cases: {}, failures: [], fatal: null };
function fail(msg) { report.failures.push(msg); }
async function flush(n = 120) {
  for (let i = 0; i < n; i++) await new Promise((r) => setImmediate(r));
}
async function waitFor(pred, tries = 800) {
  for (let i = 0; i < tries; i++) {
    if (pred()) return true;
    await new Promise((r) => setImmediate(r));
  }
  return false;
}
function deferred() {
  let resolve;
  const promise = new Promise((r) => { resolve = r; });
  return { promise, resolve };
}

function makeFakeDom(page) {
  const cache = new Map();
  function makeEl(sel) {
    const listeners = {};
    return {
      __sel: sel,
      hidden: false,
      textContent: "",
      innerHTML: "",
      value: "",
      disabled: false,
      open: false,
      files: [],
      dataset: {},
      style: {},
      selected: false,
      options: [],
      className: "",
      classList: {
        _set: new Set(),
        toggle(c, on) { if (on === undefined) on = !this._set.has(c); on ? this._set.add(c) : this._set.delete(c); },
        add(c) { this._set.add(c); },
        remove(c) { this._set.delete(c); },
        contains(c) { return this._set.has(c); }
      },
      addEventListener(t, fn) { (listeners[t] = listeners[t] || []).push(fn); },
      removeEventListener() {},
      appendChild(child) { this.options.push(child); },
      removeChild(child) { this.options = this.options.filter((o) => o !== child); },
      querySelector() { return makeEl(sel + " *"); },
      querySelectorAll() { return []; },
      setAttribute() {},
      getAttribute() { return null; },
      focus() {},
      click() {},
      scrollIntoView() {},
      __fire(t, ev) { (listeners[t] || []).forEach((fn) => fn(ev || { preventDefault() {} })); }
    };
  }
  function el(sel) {
    if (!cache.has(sel)) cache.set(sel, makeEl(sel));
    return cache.get(sel);
  }
  const body = makeEl("body");
  body.dataset.page = page;
  return {
    body,
    documentElement: makeEl("html"),
    title: "",
    el,
    querySelector(sel) { return sel === "body" ? body : el(sel); },
    querySelectorAll() { return []; },
    createElement(tag) { return makeEl("<" + tag + ">"); },
    addEventListener() {},
    removeEventListener() {}
  };
}

function makeI18n() {
  return {
    zh: {
      "ui.pick_reply_file": "选择还原文件",
      "ui.workspace_refresh": "刷新",
      "ui.workspace_loading": "列表加载中…",
      "ui.workspace_empty": "没有符合条件的直属文件。",
      "ui.workspace_count": "{count} 个直属文件",
      "ui.workspace_origin": "来源：{dir}/",
      "ui.mapping_source_workspace": "来源：mapping/ 目录文件（{name}）",
      "ui.mapping_source_manual": "来源：手动选择的配对文件（{name}）",
      "ui.mapping_source_session": "来源：本次脱敏映射（{name}）",
      "ui.workspace_choose_original": "从 original/ 选择文件",
      "ui.workspace_choose_cards": "从 cards/ 选择词卡",
      "ui.workspace_choose_pending": "从 pending/ 选择文件",
      "ui.workspace_choose_mapping": "从 mapping/ 选择映射",
      "ui.word_file_too_large": "词文件超过 1MB",
      "ui.unsupported_word_file": "词文件格式不支持",
      "ui.word_file_binary": "词文件为二进制",
      "ui.word_file_empty": "词文件没有有效词",
      "ui.word_line_too_long": "第 {line} 行过长",
      "ui.word_file_too_many": "词过多",
      "ui.mapping_confirm_manual": "确认配对？",
      "ui.confirm_reexecute": "重新执行？",
      "ui.request_failed": "请求失败：{detail}",
      "ui.busy_execute": "正在处理",
      "error.unknown_id": "未知问题（{id}）",
      "error.generic": "操作未完成",
      "input.busy": "当前有操作或退出正在进行",
      "input.root_unavailable": "无法确定程序所在目录",
      "input.read_failed": "读取文件失败",
      "input.dir_unavailable": "{dir} 不可用",
      "input.list_failed": "无法列举 {dir}",
      "input.too_large": "所选文件为 {size}，超过该快捷入口 {limit} 的读取上限。",
      "native.confirm_close_workspace": "有产物未落盘",
      "native.confirm_close_legacy": "存在尚未保存的结果",
      "ui.mapping_required": "请选择 mapping"
    },
    en: {
      "ui.pick_reply_file": "choose file to restore",
      "ui.workspace_refresh": "Refresh",
      "ui.workspace_loading": "Loading list…",
      "ui.workspace_empty": "No matching direct files.",
      "ui.workspace_count": "{count} direct files",
      "ui.workspace_origin": "Source: {dir}/",
      "ui.workspace_choose_original": "Choose a file from original/",
      "ui.workspace_choose_cards": "Choose a word card from cards/",
      "ui.workspace_choose_pending": "Choose a file from pending/",
      "ui.workspace_choose_mapping": "Choose a mapping from mapping/",
      "ui.mapping_source_workspace": "Source: file in mapping/ ({name})",
      "input.busy": "An operation or exit is in progress",
      "input.root_unavailable": "The program folder could not be determined",
      "input.read_failed": "Reading the file failed",
      "input.dir_unavailable": "{dir} is unavailable",
      "input.list_failed": "Could not list files in {dir}",
      "error.unknown_id": "Unrecognized problem ({id})",
      "error.generic": "The operation did not complete"
    }
  };
}

const DEFAULT_PROFILES = [
  { name: "balanced", display_name: "日常脱敏", rules: { phone: true, idcard: true, email: true, ip: true, api_key: true, custom: true } },
  { name: "minimal", display_name: "仅密钥", rules: { phone: false, idcard: false, email: false, ip: false, api_key: true, custom: false } },
  { name: "__custom", display_name: "自定义", rules: { phone: true, idcard: true, email: true, ip: true, api_key: true, custom: true } }
];

// 每个用例可替换的实现：返回普通值或 Promise 均可。
const plan = {
  listImpl: () => ({ status: "ok", files: [] }),
  readImpl: () => ({ status: "failed", message_id: "input.read_failed" }),
  maskResponse: null,
  restoreResponse: null,
  confirmImpl: () => true
};

function makeBridge(mode) {
  const calls = { list: [], read: [], autoSaveMask: 0, autoSaveRestored: 0, saveFile: 0 };
  const bridge = {
    SaveFile: () => { calls.saveFile++; return { status: "saved", destination: "C:/app/out.txt" }; },
    ReportUnsaved: () => {},
    SetExitConfirmed: () => {},
    SetUIPreferences: () => ({ persisted: true }),
    AutoSaveMask: () => { calls.autoSaveMask++; return { masked: { status: "saved", path: "x" }, mapping: { status: "saved", path: "y" } }; },
    AutoSaveRestored: () => { calls.autoSaveRestored++; return { status: "saved", path: "z" }; },
    ListWorkspaceFiles: (kind) => { calls.list.push(kind); return plan.listImpl(kind); },
    ReadWorkspaceFile: (kind, name) => { calls.read.push({ kind: kind, name: name }); return plan.readImpl(kind, name); }
  };
  if (mode === "no-binding") return { bridge: null, calls };
  return { bridge, calls };
}

// 轻量 FileReader：仅实现 harness 用到的 readAsArrayBuffer/onload/onerror。
class FakeFileReader {
  constructor() { this.onload = null; this.onerror = null; this.result = null; }
  readAsArrayBuffer(blob) {
    Promise.resolve(blob.arrayBuffer()).then((buf) => {
      this.result = buf;
      if (this.onload) this.onload({ target: this });
    }, (err) => { if (this.onerror) this.onerror(err); });
  }
}

function loadApp(code, dom, bridge, opts) {
  opts = opts || {};
  const hooks = {};
  let fetchCount = 0;
  const sandbox = {
    window: {
      __DM_CONFIG__: {
        profiles: opts.profiles || DEFAULT_PROFILES,
        default_profile: "balanced",
        url: "",
        high_freq_threshold: 20,
        mode: "desktop",
        file_drop_enabled: false,
        initial_view: opts.page || "mask",
        theme: "system",
        language: opts.language || "zh",
        i18n: makeI18n()
      },
      __DM_TEST_HOOKS__: hooks,
      go: bridge ? { desktop: { Bridge: bridge } } : undefined,
      confirm: (m) => plan.confirmImpl(m),
      matchMedia: undefined
    },
    document: dom,
    confirm: (m) => plan.confirmImpl(m),
    location: { origin: "http://localhost" },
    fetch: (input) => {
      fetchCount++;
      const p = String(input);
      const body = p.indexOf("/api/restore") >= 0 ? plan.restoreResponse : plan.maskResponse;
      return Promise.resolve({ status: 200, json: () => Promise.resolve(body) });
    },
    Response,
    Request,
    Headers,
    FormData,
    Blob,
    File,
    FileReader: FakeFileReader,
    TextDecoder,
    TextEncoder,
    Uint8Array,
    console,
    URL,
    setTimeout,
    clearTimeout,
    atob: (s) => Buffer.from(String(s), "base64").toString("binary"),
    btoa: (s) => Buffer.from(String(s), "binary").toString("base64")
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(code, sandbox, { filename: appJsPath });
  return { hooks, sandbox, fetchCount: () => fetchCount };
}

function b64(s) { return Buffer.from(s, "binary").toString("base64"); }
function fileOf(name, text) { return new File([Buffer.from(text, "utf8")], name, { type: "text/plain" }); }

async function entries(fd) {
  const out = {};
  for (const [k, v] of fd.entries()) out[k] = v;
  return out;
}
async function bytesOf(v) {
  if (v && typeof v.arrayBuffer === "function") return new Uint8Array(await v.arrayBuffer());
  if (v instanceof Uint8Array) return v;
  return new Uint8Array(0);
}

function maskBody(name) {
  const ext = name.slice(name.lastIndexOf("."));
  const stem = name.slice(0, name.length - ext.length);
  return {
    ok: true, masked_name: name, mapping_name: stem + ".mapping.json",
    masked_b64: b64("masked"), mapping_b64: b64("{}"),
    encoding: "UTF-8", format: "text", warnings: [], cleanup: null, audit_failed: false, unsaved_generation: 3
  };
}
function restoreBody(name) {
  return {
    ok: true, name: name, restored_b64: b64("restored"),
    encoding: "UTF-8", format: "text", restored_count: 1, warnings: [], cleanup: null, audit_failed: false, unsaved_generation: 4
  };
}

async function main() {
  const code = await readFile(appJsPath, "utf8");

  // 1) 初始只列举四类名称，不自动读取、不默认选择文件。
  {
    plan.listImpl = (kind) => ({ status: "ok", files: [kind + "-a.txt", kind + "-b.txt"] });
    const dom = makeFakeDom("mask");
    const { bridge, calls } = makeBridge();
    const { hooks } = loadApp(code, dom, bridge);
    hooks.initWorkspacePickers();
    await waitFor(() => calls.list.length === 4 && hooks.workspaceListState("mapping").status === "ok");
    await flush();
    report.cases["init-lists"] = {
      kinds: calls.list.slice(), reads: calls.read.length,
      originalFiles: hooks.workspaceListState("original").files.slice(),
      maskFile: hooks.state.mask.file, busy: hooks.appState.busy
    };
    if (calls.list.length !== 4) fail("init-lists: 应请求四类列表");
    if (calls.read.length !== 0) fail("init-lists: 列表不得自动读取正文");
    if (hooks.state.mask.file) fail("init-lists: 不得自动选择文件");
    if (hooks.workspaceListState("original").files.join(",") !== "original-a.txt,original-b.txt") fail("init-lists: 列表名称应按后端返回保存");
  }

  // 2) 刷新只更新对应列表，不重读当前快照；移除已选文件不清空输入。
  {
    let files = ["one.txt", "two.txt"];
    plan.listImpl = () => ({ status: "ok", files: files.slice() });
    plan.readImpl = () => ({ status: "ok", kind: "original", name: "one.txt", data_b64: Buffer.from("first").toString("base64") });
    const dom = makeFakeDom("mask");
    const { bridge, calls } = makeBridge();
    const { hooks } = loadApp(code, dom, bridge);
    hooks.initWorkspacePickers();
    await waitFor(() => hooks.workspaceListState("original").status === "ok");
    hooks.loadWorkspaceFile("original", "one.txt");
    await waitFor(() => hooks.state.mask.file && hooks.state.mask.file.name === "one.txt");
    const readsAfterLoad = calls.read.length;
    files = ["two.txt", "three.txt"];
    hooks.refreshWorkspaceList("original");
    await waitFor(() => hooks.workspaceListState("original").files.join(",") === "two.txt,three.txt");
    report.cases["refresh-decoupled"] = {
      files: hooks.workspaceListState("original").files.slice(),
      snapshot: hooks.state.mask.file && hooks.state.mask.file.name,
      reads: calls.read.length, readsAfterLoad: readsAfterLoad,
      otherKinds: calls.list.length
    };
    if (calls.read.length !== readsAfterLoad) fail("refresh-decoupled: 刷新不得重读当前文件");
    if (!hooks.state.mask.file || hooks.state.mask.file.name !== "one.txt") fail("refresh-decoupled: 列表移除后当前快照应保留");
    if (hooks.workspaceListState("original").files.indexOf("one.txt") >= 0) fail("refresh-decoupled: 列表应更新");
  }

  // 3) 空目录、不可用与无 binding 可区分；手动入口不受影响。
  {
    plan.listImpl = (kind) => (kind === "cards"
      ? { status: "empty", files: [] }
      : { status: "unavailable", message_id: "input.dir_unavailable", params: { dir: kind + "/" } });
    const dom = makeFakeDom("mask");
    const { bridge } = makeBridge();
    const { hooks } = loadApp(code, dom, bridge);
    hooks.initWorkspacePickers();
    await waitFor(() => hooks.workspaceListState("cards").status === "empty" && hooks.workspaceListState("pending").status === "unavailable");
    const cardsStatus = dom.el("#cards-list-status").textContent;
    const pendingStatus = dom.el("#pending-list-status").textContent;
    // 手动入口仍可用：直接设置手动文件并提交。
    hooks.onMaskFile(fileOf("manual.txt", "manual"), "manual");
    report.cases["empty-unavailable"] = {
      cards: hooks.workspaceListState("cards").status,
      pending: hooks.workspaceListState("pending").status,
      cardsStatus: cardsStatus, pendingStatus: pendingStatus,
      manualFile: hooks.state.mask.file && hooks.state.mask.file.name,
      originText: dom.el("#mask-file-origin").textContent
    };
    if (hooks.workspaceListState("cards").status !== "empty") fail("empty-unavailable: 空目录应为 empty");
    if (hooks.workspaceListState("pending").status !== "unavailable") fail("empty-unavailable: 失败/不可用应为 unavailable");
    if (cardsStatus.indexOf("没有符合") < 0) fail("empty-unavailable: 空状态应有本地化文案");
    if (pendingStatus.indexOf("不可用") < 0) fail("empty-unavailable: 失败应显示原因与下一步");
    if (!hooks.state.mask.file || hooks.state.mask.file.name !== "manual.txt") fail("empty-unavailable: 手动入口应仍可用");
    if (dom.el("#mask-file-origin").textContent !== "") fail("empty-unavailable: 手动来源不应误示目录来源");
  }

  // 4) 旧列表响应晚到不得覆盖更新的列表。
  {
    const first = deferred();
    let round = 0;
    plan.listImpl = () => { round++; return round === 1 ? first.promise : { status: "ok", files: ["new.txt"] }; };
    const dom = makeFakeDom("mask");
    const { bridge } = makeBridge();
    const { hooks } = loadApp(code, dom, bridge);
    hooks.refreshWorkspaceList("original");
    await flush(5);
    hooks.refreshWorkspaceList("original");
    await waitFor(() => hooks.workspaceListState("original").files.join(",") === "new.txt");
    first.resolve({ status: "ok", files: ["old.txt"] });
    await flush();
    report.cases["stale-list"] = { files: hooks.workspaceListState("original").files.slice() };
    if (hooks.workspaceListState("original").files.join(",") !== "new.txt") fail("stale-list: 旧响应不得覆盖新列表");
  }

  // 5) 无 binding：隐藏快捷组、给出不可用反馈，手动入口不受影响。
  {
    const dom = makeFakeDom("mask");
    const { bridge } = makeBridge("no-binding");
    const { hooks } = loadApp(code, dom, bridge);
    hooks.initWorkspacePickers();
    hooks.onMaskFile(fileOf("manual.txt", "manual"), "manual");
    report.cases["no-binding"] = {
      hidden: dom.el("#original-picker").hidden,
      status: hooks.workspaceListState("original").status,
      errorID: hooks.workspaceListState("original").errorID,
      manualFile: hooks.state.mask.file && hooks.state.mask.file.name
    };
    if (!dom.el("#original-picker").hidden) fail("no-binding: 快捷组应隐藏并回退手动入口");
    if (hooks.workspaceListState("original").status !== "unavailable") fail("no-binding: 应标记不可用");
    if (!hooks.state.mask.file) fail("no-binding: 手动入口不得受影响");
  }

  // 6) original 读取：真实文件名/原始字节进入原 multipart，不复制到目录。
  {
    const gbk = Buffer.from([0xd6, 0xd0, 0xce, 0xc4]);
    plan.listImpl = () => ({ status: "ok", files: ["合同 副本.txt"] });
    plan.readImpl = () => ({ status: "ok", kind: "original", name: "合同 副本.txt", data_b64: gbk.toString("base64") });
    const dom = makeFakeDom("mask");
    const { bridge, calls } = makeBridge();
    const { hooks } = loadApp(code, dom, bridge);
    hooks.state.mask.profiles = {};
    hooks.state.mask.profile = "balanced";
    hooks.initWorkspacePickers();
    await flush(5);
    hooks.onWorkspaceSelect("original");
    dom.el("#original-select").value = "合同 副本.txt";
    hooks.onWorkspaceSelect("original");
    await waitFor(() => hooks.state.mask.file && hooks.state.mask.file.name === "合同 副本.txt");
    const fd = hooks.buildMaskFormData([]);
    const parts = await entries(fd);
    const bytes = parts.file ? await bytesOf(parts.file) : new Uint8Array(0);
    report.cases["original-read"] = {
      fileName: parts.file && parts.file.name,
      size: bytes.length,
      gbkMatches: bytes.length === gbk.length && bytes[0] === gbk[0],
      origin: dom.el("#mask-file-origin").textContent,
      autoSaveCalls: calls.autoSaveMask, reads: calls.read.length
    };
    if (!parts.file || parts.file.name !== "合同 副本.txt") fail("original-read: multipart 应使用真实文件名");
    if (bytes.length !== gbk.length) fail("original-read: 应提交原始字节");
    if (dom.el("#mask-file-origin").textContent.indexOf("original") < 0) fail("original-read: 应显示目录来源");
    if (calls.autoSaveMask !== 0) fail("original-read: 不得自动复制/保存到目录");
    // 手动选择覆盖目录来源，不继续误示旧文件已激活。
    hooks.onMaskFile(fileOf("manual.txt", "m"), "manual");
    const originAfterManual = dom.el("#mask-file-origin").textContent;
    report.cases["original-read"].originAfterManual = originAfterManual;
    if (originAfterManual !== "") fail("original-read: 手动选择应清除目录来源");
  }

  // 7) pending 读取进入还原 multipart；粘贴模式不提交隐藏文件。
  {
    plan.readImpl = () => ({ status: "ok", kind: "pending", name: "reply.txt", data_b64: b64("reply-bytes") });
    const dom = makeFakeDom("restore");
    const { bridge } = makeBridge();
    const { hooks } = loadApp(code, dom, bridge, { page: "restore" });
    hooks.initRestore();
    hooks.initWorkspacePickers();
    hooks.applyWorkspacePayload("pending", "reply.txt", new Uint8Array(Buffer.from("reply-bytes")));
    hooks.state.restore.mapping = { source: "session", name: "s.mapping.json", b64: b64("{}") };
    const fileFd = hooks.buildRestoreFormData();
    const fileParts = await entries(fileFd);
    const fileBytes = fileParts.file ? await bytesOf(fileParts.file) : new Uint8Array(0);
    hooks.setRestoreMode("text");
    dom.el("#restore-text").value = "pasted";
    const textFd = hooks.buildRestoreFormData();
    const textParts = await entries(textFd);
    report.cases["pending-read"] = {
      name: fileParts.file && fileParts.file.name,
      size: fileBytes.length,
      textHasFile: Object.prototype.hasOwnProperty.call(textParts, "file"),
      pendingHidden: dom.el("#pending-picker").hidden,
      origin: dom.el("#restore-file-origin").textContent
    };
    if (!fileParts.file || fileParts.file.name !== "reply.txt" || fileBytes.length !== 11) fail("pending-read: 应提交真实文件名与字节");
    if (Object.prototype.hasOwnProperty.call(textParts, "file")) fail("pending-read: 粘贴模式不得提交隐藏文件");
    if (!dom.el("#pending-picker").hidden) fail("pending-read: 粘贴模式应隐藏 pending 入口");
    if (dom.el("#restore-file-origin").textContent.indexOf("pending") < 0) fail("pending-read: 应显示目录来源");
  }

  // 8) mapping 目录来源：与手动来源同等的配对确认、真实名称与字节，不冒充当前映射。
  {
    plan.restoreResponse = restoreBody("notes.restored.txt");
    plan.readImpl = () => ({ status: "ok", kind: "mapping", name: "pair.mapping.json", data_b64: b64('{"version":2}') });
    let confirms = 0;
    plan.confirmImpl = () => { confirms++; return true; };
    const dom = makeFakeDom("restore");
    const { bridge, calls } = makeBridge();
    const { hooks } = loadApp(code, dom, bridge, { page: "restore" });
    hooks.initRestore();
    hooks.initWorkspacePickers();
    hooks.setRestoreMode("text");
    dom.el("#restore-text").value = "回复 [PHONE_1]";
    hooks.state.mask.artifacts = {
      mapping: { id: "mapping", view: "mask", name: "session.mapping.json", b64: b64("{}"), status: "saved", autoPath: "C:/app/mapping/session.mapping.json" }
    };
    hooks.applyWorkspacePayload("mapping", "pair.mapping.json", new Uint8Array(Buffer.from('{"version":2}')));
    const fd = hooks.buildRestoreFormData();
    const parts = await entries(fd);
    const mapBytes = parts.mapping ? await bytesOf(parts.mapping) : new Uint8Array(0);
    hooks.runRestore();
    await waitFor(() => calls.autoSaveRestored === 1);
    report.cases["mapping-workspace"] = {
      source: hooks.state.restore.mapping && hooks.state.restore.mapping.source,
      name: parts.mapping && parts.mapping.name,
      bytes: mapBytes.length,
      confirms: confirms,
      sourceText: dom.el("#mapping-source").textContent,
      sessionName: hooks.state.mask.artifacts.mapping && hooks.state.mask.artifacts.mapping.name,
      restored: hooks.state.restore.artifacts.restored && hooks.state.restore.artifacts.restored.status
    };
    if (!hooks.state.restore.mapping || hooks.state.restore.mapping.source !== "workspace") fail("mapping-workspace: 应标记 workspace 来源");
    if (hooks.state.mask.artifacts.mapping.name !== "session.mapping.json") fail("mapping-workspace: 本次脱敏 mapping 快照应保持不变");
    if (!parts.mapping || parts.mapping.name !== "pair.mapping.json") fail("mapping-workspace: multipart 应使用真实文件名");
    if (confirms < 1) fail("mapping-workspace: 目录来源必须要求配对确认");
    if (dom.el("#mapping-source").textContent.indexOf("mapping/") < 0) fail("mapping-workspace: 来源应指向 mapping/");
  }

  // 9) cards：合法词卡替换文件词、保留手输词；非法词卡保旧并反馈；custom 关闭禁用入口。
  {
    plan.readImpl = () => ({ status: "ok", kind: "cards", name: "A.txt", data_b64: b64("甲\n乙\n") });
    const dom = makeFakeDom("mask");
    const { bridge } = makeBridge();
    const { hooks } = loadApp(code, dom, bridge);
    hooks.initMask();
    hooks.initWorkspacePickers();
    // 已有手输词 + 旧文件词。
    hooks.state.mask.words = [
      { w: "手输词", source: "text", line: 1, checked: true },
      { w: "旧卡词", source: "file", checked: true }
    ];
    hooks.state.mask.importFile = fileOf("old.txt", "旧卡词\n");
    hooks.applyWorkspacePayload("cards", "A.txt", new Uint8Array(Buffer.from("甲\n乙\n")));
    await waitFor(() => hooks.state.mask.words.some((e) => e.w === "甲"));
    const wordsAfter = hooks.state.mask.words.map((e) => e.w).sort();
    const fileName = dom.el("#word-file-name").textContent;
    const origin = dom.el("#word-file-origin").textContent;
    // 非法词卡（二进制）保旧。
    const before = hooks.state.mask.words.map((e) => e.w).sort().join(",");
    hooks.state.mask.words = hooks.state.mask.words.map((e) => ({ w: e.w, source: e.source, checked: e.checked, line: e.line }));
    hooks.applyWorkspacePayload("cards", "bad.txt", new Uint8Array([0x00, 0x01, 0xff]));
    await flush();
    const after = hooks.state.mask.words.map((e) => e.w).sort().join(",");
    const feedback = dom.el("#word-feedback").textContent;
    // custom 关闭：入口禁用。
    hooks.state.mask.profile = "minimal";
    hooks.state.mask.profiles = {};
    DEFAULT_PROFILES.forEach((p) => { hooks.state.mask.profiles[p.name] = p; });
    hooks.renderWords();
    report.cases["cards-import"] = {
      words: wordsAfter, fileName: fileName, origin: origin,
      preserved: after, feedback: feedback,
      disabled: dom.el("#cards-select").disabled,
      refreshDisabled: dom.el("#cards-refresh").disabled
    };
    if (wordsAfter.join(",") !== "乙,手输词,甲") fail("cards-import: 应替换文件词并保留手输词：" + wordsAfter);
    if (fileName !== "A.txt") fail("cards-import: 应显示真实文件名");
    if (origin.indexOf("cards") < 0) fail("cards-import: 应显示目录来源");
    if (after !== before) fail("cards-import: 非法词卡应保旧");
    if (!feedback) fail("cards-import: 非法词卡应有反馈");
    if (!dom.el("#cards-select").disabled) fail("cards-import: custom 关闭应从词卡入口禁用");
  }

  // 10) 读取忙态：处理中重复选择被拒、完成后释放；过期读取不覆盖新选择；generation 不变。
  {
    const slow = deferred();
    let readRound = 0;
    plan.readImpl = () => { readRound++; return readRound === 1 ? slow.promise : { status: "ok", kind: "original", name: "new.txt", data_b64: b64("new") }; };
    const dom = makeFakeDom("mask");
    const { bridge } = makeBridge();
    const { hooks } = loadApp(code, dom, bridge);
    hooks.initWorkspacePickers();
    hooks.loadWorkspaceFile("original", "old.txt");
    await flush(3);
    const busyDuring = hooks.appState.busy;
    hooks.loadWorkspaceFile("original", "new.txt"); // 忙态拒绝
    slow.resolve({ status: "ok", kind: "original", name: "old.txt", data_b64: b64("old") });
    await flush(10);
    hooks.loadWorkspaceFile("original", "new.txt");
    await waitFor(() => hooks.state.mask.file && hooks.state.mask.file.name === "new.txt");
    report.cases["busy-and-stale"] = {
      busyDuring: busyDuring, busyAfter: hooks.appState.busy,
      file: hooks.state.mask.file && hooks.state.mask.file.name,
      reads: readRound, gen: hooks.appState.reportGen
    };
    if (!busyDuring) fail("busy-and-stale: 读取期间应处于忙态");
    if (hooks.appState.busy) fail("busy-and-stale: 读取完成后应释放忙态");
    if (hooks.state.mask.file.name !== "new.txt") fail("busy-and-stale: 最终应为较新的选择");
  }

  // 11) 视图/偏好切换与刷新保持快照；选择只在会话内存中。
  {
    plan.readImpl = () => ({ status: "ok", kind: "original", name: "snap.txt", data_b64: b64("snap") });
    const dom = makeFakeDom("mask");
    const { bridge } = makeBridge();
    const { hooks } = loadApp(code, dom, bridge);
    hooks.initWorkspacePickers();
    hooks.loadWorkspaceFile("original", "snap.txt");
    await waitFor(() => hooks.state.mask.file && hooks.state.mask.file.name === "snap.txt");
    hooks.setView("restore");
    hooks.setPreferences("dark", "en");
    await flush(5);
    hooks.setView("mask");
    hooks.refreshWorkspaceList("original");
    await flush(5);
    report.cases["session-scoped"] = {
      file: hooks.state.mask.file && hooks.state.mask.file.name,
      view: hooks.appState.activeView,
      language: hooks.appState.prefs.language,
      list: hooks.workspaceListState("original").status
    };
    if (!hooks.state.mask.file || hooks.state.mask.file.name !== "snap.txt") fail("session-scoped: 切换视图/偏好不得丢失快照");
    if (hooks.appState.prefs.language !== "en") fail("session-scoped: 语言偏好应生效");
  }

  // 12) 语言切换不丢输入/快照；快捷文案与状态按当前语言渲染。
  {
    plan.listImpl = () => ({ status: "ok", files: ["notes.txt"] });
    plan.readImpl = () => ({ status: "ok", kind: "original", name: "notes.txt", data_b64: b64("x") });
    const dom = makeFakeDom("mask");
    const { bridge } = makeBridge();
    const { hooks } = loadApp(code, dom, bridge);
    hooks.initWorkspacePickers();
    await flush(5);
    hooks.loadWorkspaceFile("original", "notes.txt");
    await waitFor(() => hooks.state.mask.file && hooks.state.mask.file.name === "notes.txt");
    hooks.setPreferences("dark", "en");
    await flush(8);
    const optionText = dom.el("#original-select").options.map((o) => o.textContent).join("|");
    report.cases["language-switch"] = {
      file: hooks.state.mask.file && hooks.state.mask.file.name,
      origin: dom.el("#mask-file-origin").textContent,
      status: dom.el("#original-list-status").textContent,
      optionText: optionText,
      language: hooks.appState.prefs.language
    };
    if (!hooks.state.mask.file || hooks.state.mask.file.name !== "notes.txt") fail("language-switch: 切换语言不得丢失输入快照");
    if (dom.el("#mask-file-origin").textContent.indexOf("Source: original/") < 0) fail("language-switch: 来源应按英文渲染");
    if (optionText.indexOf("Choose a file from original/") < 0) fail("language-switch: 占位应按英文渲染");
  }

  // 13) 读取期间执行/保存/退出冲突被阻止；完成后释放，generation 与已有产物不被清除。
  {
    const slow = deferred();
    plan.readImpl = () => slow.promise;
    const dom = makeFakeDom("mask");
    const { bridge, calls } = makeBridge();
    const { hooks, fetchCount } = loadApp(code, dom, bridge);
    hooks.state.mask.profiles = {};
    hooks.state.mask.profile = "balanced";
    hooks.state.mask.artifacts = {
      masked: { id: "masked", view: "mask", name: "old.masked.txt", b64: b64("old"), status: "unsaved", busy: false }
    };
    hooks.initWorkspacePickers();
    hooks.loadWorkspaceFile("original", "slow.txt");
    await flush(3);
    const fetchBefore = fetchCount();
    const savesBefore = calls.saveFile;
    hooks.requestExit();
    hooks.nativeSave(hooks.state.mask.artifacts.masked);
    const artifactsDuring = Object.keys(hooks.state.mask.artifacts).length;
    slow.resolve({ status: "ok", kind: "original", name: "slow.txt", data_b64: b64("x") });
    await waitFor(() => hooks.state.mask.file && hooks.state.mask.file.name === "slow.txt");
    await flush();
    report.cases["conflict-guards"] = {
      busyDuring: true,
      fetchDelta: fetchCount() - fetchBefore,
      savesDelta: calls.saveFile - savesBefore,
      artifactsDuring: artifactsDuring,
      artifactsAfter: Object.keys(hooks.state.mask.artifacts).length,
      generation: hooks.appState.reportGen,
      busyAfter: hooks.appState.busy
    };
    if (fetchCount() - fetchBefore !== 0) fail("conflict-guards: 忙时退出不得发起 /api/exit");
    if (calls.saveFile - savesBefore !== 0) fail("conflict-guards: 忙时另存不得开始");
    if (Object.keys(hooks.state.mask.artifacts).length !== 1) fail("conflict-guards: 读取不得清除已有产物");
    if (hooks.appState.reportGen !== 0) fail("conflict-guards: 读取不得推进 generation");
    if (hooks.appState.busy) fail("conflict-guards: 读取完成后应释放忙态");
  }

  // 14) 读取失败保留上次有效快照并就近反馈，不进入业务处理。
  {
    let round = 0;
    plan.readImpl = () => {
      round++;
      return round === 1
        ? { status: "ok", kind: "original", name: "good.txt", data_b64: b64("good") }
        : { status: "failed", message_id: "input.too_large", params: { size: "11 MiB", limit: "10 MiB" } };
    };
    const dom = makeFakeDom("mask");
    const { bridge, calls } = makeBridge();
    const { hooks } = loadApp(code, dom, bridge);
    hooks.initWorkspacePickers();
    hooks.loadWorkspaceFile("original", "good.txt");
    await waitFor(() => hooks.state.mask.file && hooks.state.mask.file.name === "good.txt");
    hooks.loadWorkspaceFile("original", "bad.txt");
    await flush(15);
    report.cases["read-failure-preserves"] = {
      file: hooks.state.mask.file && hooks.state.mask.file.name,
      status: dom.el("#original-list-status").textContent,
      reads: calls.read.length,
      autoSave: calls.autoSaveMask,
      busyAfter: hooks.appState.busy
    };
    if (!hooks.state.mask.file || hooks.state.mask.file.name !== "good.txt") fail("read-failure-preserves: 失败应保留旧快照");
    if (dom.el("#original-list-status").textContent.indexOf("上限") < 0) fail("read-failure-preserves: 应就近显示失败原因");
    if (calls.autoSaveMask !== 0) fail("read-failure-preserves: 读取失败不得进入保存链");
    if (hooks.appState.busy) fail("read-failure-preserves: 读取失败也必须释放忙态");
  }
}

main()
  .then(async () => {
    await writeFile(reportPath, JSON.stringify(report, null, 2));
    if (report.failures.length) {
      console.error("workspace_selectors 失败:\n" + report.failures.join("\n"));
      process.exit(1);
    }
  })
  .catch(async (err) => {
    report.fatal = err && err.stack ? err.stack : String(err);
    try { await writeFile(reportPath, JSON.stringify(report, null, 2)); } catch (e) { /* ignore */ }
    console.error("workspace_selectors fatal: " + report.fatal);
    process.exit(1);
  });
