// 真实 app.js 失败展示回归（S8）：业务失败/传输 reject/409 预览不得残留“处理中”，
// 旧结果须保留可见可保存；无旧结果则隐藏占位；busy 解除且下一执行可用。
// 用法：node ui_failure.mjs <appJsPath> <reportPath>
import { readFile, writeFile } from "node:fs/promises";
import vm from "node:vm";

const [appJsPath, reportPath] = process.argv.slice(2);
if (!appJsPath || !reportPath) {
  console.error("用法: node ui_failure.mjs <app.js> <reportPath>");
  process.exit(2);
}

const report = { cases: {}, failures: [], fatal: null };
function fail(m) { report.failures.push(m); }
async function waitFor(pred, tries = 500) {
  for (let i = 0; i < tries; i++) { if (pred()) return true; await new Promise((r) => setImmediate(r)); }
  return false;
}

function makeFakeDom(page) {
  const cache = new Map();
  function makeEl(sel) {
    const listeners = {};
    return {
      __sel: sel, hidden: false, textContent: "", innerHTML: "", value: "", disabled: false, files: [], open: false,
      dataset: {}, style: {}, options: [],
      classList: { toggle() {}, add() {}, remove() {}, contains() { return false; } },
      addEventListener(t, fn) { (listeners[t] = listeners[t] || []).push(fn); },
      removeEventListener() {}, appendChild() {}, querySelector() { return makeEl(sel + " *"); },
      querySelectorAll() { return []; }, setAttribute() {}, getAttribute() { return null; },
      focus() {}, click() {}, scrollIntoView() {},
      __fire(t, ev) { (listeners[t] || []).forEach((fn) => fn(ev || { preventDefault() {} })); },
    };
  }
  function el(sel) { if (!cache.has(sel)) cache.set(sel, makeEl(sel)); return cache.get(sel); }
  const body = makeEl("body"); body.dataset.page = page;
  return {
    body, documentElement: makeEl("html"), title: "", el,
    querySelector(sel) { return sel === "body" ? body : el(sel); },
    querySelectorAll() { return []; },
    createElement(tag) { return makeEl("<" + tag + ">"); },
    addEventListener() {}, removeEventListener() {},
  };
}

const plan = { mode: "ok", maskResponse: null, restoreResponse: null };

function makeConfig() {
  return {
    profiles: [{ name: "balanced", display_name: "balanced", rules: { phone: true, idcard: false, email: false, ip: false, api_key: true, custom: true } }],
    default_profile: "balanced", url: "", high_freq_threshold: 20, mode: "desktop",
    initial_view: "mask", theme: "system", language: "zh",
    i18n: {
      zh: {
        "ui.result_processing": "正在处理，请稍候…",
        "error.generic": "操作未完成，请检查输入后重试。",
        "ui.request_failed": "请求失败：{detail}",
        "ui.artifact_masked": "脱敏副本",
        "ui.artifact_mapping": "还原映射",
        "ui.artifact_restored": "还原文件",
        "ui.save": "保存", "ui.save_as": "另存为", "ui.status_unsaved": "未保存",
        "ui.status_saved": "已保存", "ui.status_failed": "保存失败（可重试）", "ui.status_saving": "保存中…",
        "ui.confirm_reexecute": "重新执行将替换本视图尚未保存的结果。确定继续吗？"
      },
      en: {}
    }
  };
}

function makeBridge() {
  const calls = { autoSaveMask: 0, autoSaveRestored: 0, reportUnsaved: [] };
  return {
    calls,
    bridge: {
      SaveFile: () => ({ status: "saved" }),
      ReportUnsaved: (gen, v) => calls.reportUnsaved.push({ gen, v }),
      SetExitConfirmed: () => {},
      SetUIPreferences: () => ({ persisted: true }),
      AutoSaveMask: () => { calls.autoSaveMask++; return null; },
      AutoSaveRestored: () => { calls.autoSaveRestored++; return null; },
    },
  };
}

function loadApp(code, dom, bridge) {
  const hooks = {};
  const sandbox = {
    window: {
      __DM_CONFIG__: makeConfig(), __DM_TEST_HOOKS__: hooks,
      go: { desktop: { Bridge: bridge } }, confirm: () => true,
      matchMedia: () => ({ matches: false, addEventListener() {}, addListener() {} }),
    },
    document: dom, confirm: () => true, location: { origin: "http://localhost" },
    fetch: (input) => {
      const p = String(input);
      if (plan.mode === "reject") return Promise.reject(new Error("network-down"));
      if (plan.mode === "409") return Promise.resolve({ status: 409, json: () => Promise.resolve({ ok: false, error: "preview_required", hits: { extra: { "内部词": 30 }, builtin: {} } }) });
      const body = p.indexOf("/api/restore") >= 0 ? plan.restoreResponse : plan.maskResponse;
      return Promise.resolve({ status: 200, json: () => Promise.resolve(body) });
    },
    Response, Request, Headers, FormData, Blob, File, TextDecoder, TextEncoder, console, URL, setTimeout, clearTimeout,
    atob: (s) => Buffer.from(String(s), "base64").toString("binary"),
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(code, sandbox, { filename: appJsPath });
  return hooks;
}

function fileOf(name, text) { return new File([Buffer.from(text, "utf8")], name, { type: "text/plain" }); }
function maskBody(name) {
  const ext = name.slice(name.lastIndexOf("."));
  return {
    ok: true, masked_name: name, mapping_name: name.slice(0, name.length - ext.length) + ".mapping.json",
    masked_b64: Buffer.from("m").toString("base64"), mapping_b64: Buffer.from("{}").toString("base64"),
    encoding: "UTF-8", format: "text", warnings: [], cleanup: null, audit_failed: false, unsaved_generation: 5,
  };
}
function restoreBody(name) {
  return { ok: true, name, restored_b64: Buffer.from("r").toString("base64"), encoding: "UTF-8", format: "text", restored_count: 1, warnings: [], cleanup: null, audit_failed: false, unsaved_generation: 6 };
}
function failBody() { return { ok: false, message: "业务失败", presentation: { message_id: "error.generic" } }; }

async function maskOnce(hooks, dom, name) {
  plan.mode = "ok";
  plan.maskResponse = maskBody(name);
  hooks.state.mask.file = fileOf("in.txt", "x");
  hooks.maskSubmit([]);
  await waitFor(() => hooks.state.mask.artifacts.masked && hooks.state.mask.artifacts.masked.name === name);
  await waitFor(() => hooks.appState.busy === false);
}

async function main() {
  const code = await readFile(appJsPath, "utf8");
  const dom = makeFakeDom("mask");
  const { calls, bridge } = makeBridge();
  const hooks = loadApp(code, dom, bridge);
  hooks.state.mask.profile = "balanced";
  hooks.state.mask.customRules = { phone: true, idcard: true, email: true, ip: true, api_key: true, custom: true };

  // 1) 业务失败保留旧结果，不残留"处理中"，busy 解除，下一执行可用。
  {
    await maskOnce(hooks, dom, "old.masked.txt");
    const oldName = hooks.state.mask.artifacts.masked.name;
    plan.mode = "ok";
    plan.maskResponse = failBody();
    hooks.state.mask.file = fileOf("in.txt", "x");
    hooks.maskSubmit([]);
    await waitFor(() => hooks.state.mask.status === "failed");
    await waitFor(() => hooks.appState.busy === false);
    const html = dom.el("#mask-result").innerHTML;
    const hidden = dom.el("#mask-result").hidden;
    const keptName = hooks.state.mask.artifacts.masked && hooks.state.mask.artifacts.masked.name;
    const hasProcessing = html.indexOf("正在处理") >= 0;
    report.cases["fail-keeps-old-mask"] = { hidden, hasProcessing, keptName, oldName, busy: hooks.appState.busy, htmlHasOld: html.indexOf("old.masked.txt") >= 0 };
    if (hidden) fail("fail-keeps-old-mask: 旧结果应可见");
    if (hasProcessing) fail("fail-keeps-old-mask: 不应残留处理中");
    if (keptName !== oldName) fail("fail-keeps-old-mask: 失败不应覆盖旧结果");
    if (!html || html.indexOf("old.masked.txt") < 0) fail("fail-keeps-old-mask: 结果区应重绘旧产物");
    if (hooks.appState.busy) fail("fail-keeps-old-mask: busy 应解除");
    // 下一执行可用。
    await maskOnce(hooks, dom, "new.masked.txt");
    report.cases["fail-keeps-old-mask"].nextOk = hooks.state.mask.artifacts.masked.name === "new.masked.txt";
    if (hooks.state.mask.artifacts.masked.name !== "new.masked.txt") fail("fail-keeps-old-mask: 失败后应可再次执行");
  }

  // 2) 无旧结果失败：隐藏结果区且无"处理中"文案。
  {
    const dom2 = makeFakeDom("mask");
    const b2 = makeBridge();
    const h2 = loadApp(code, dom2, b2.bridge);
    h2.state.mask.profile = "balanced";
    plan.mode = "ok";
    plan.maskResponse = failBody();
    h2.state.mask.file = fileOf("in.txt", "x");
    h2.maskSubmit([]);
    await waitFor(() => h2.state.mask.status === "failed");
    await waitFor(() => h2.appState.busy === false);
    const html = dom2.el("#mask-result").innerHTML;
    const hidden = dom2.el("#mask-result").hidden;
    report.cases["fail-no-old-mask"] = { hidden, html, busy: h2.appState.busy };
    if (!hidden || html.indexOf("正在处理") >= 0) fail("fail-no-old-mask: 无旧结果应隐藏且无处理中文案");
    if (h2.appState.busy) fail("fail-no-old-mask: busy 应解除");
  }

  // 3) 传输 reject：保留旧结果、清除处理中、busy 解除。
  {
    await maskOnce(hooks, dom, "ok.masked.txt");
    plan.mode = "reject";
    hooks.state.mask.file = fileOf("in.txt", "x");
    hooks.maskSubmit([]);
    await waitFor(() => hooks.state.mask.status === "failed");
    await waitFor(() => hooks.appState.busy === false);
    const html = dom.el("#mask-result").innerHTML;
    report.cases["transport-reject-mask"] = { hasProcessing: html.indexOf("正在处理") >= 0, busy: hooks.appState.busy, kept: hooks.state.mask.artifacts.masked.name };
    if (html.indexOf("正在处理") >= 0) fail("transport-reject-mask: 不应残留处理中");
    if (hooks.appState.busy) fail("transport-reject-mask: busy 应解除");
    if (hooks.state.mask.artifacts.masked.name !== "ok.masked.txt") fail("transport-reject-mask: 应保留旧结果");
  }

  // 4) 409 预览不吞旧结果、不残留处理中、busy 解除。
  {
    await maskOnce(hooks, dom, "keep.masked.txt");
    plan.mode = "409";
    hooks.state.mask.file = fileOf("in.txt", "x");
    hooks.maskSubmit([]);
    await waitFor(() => hooks.state.mask.status === "confirmation-required");
    await waitFor(() => hooks.appState.busy === false);
    const html = dom.el("#mask-result").innerHTML;
    report.cases["409-keeps-old"] = { kept: hooks.state.mask.artifacts.masked.name, hasProcessing: html.indexOf("正在处理") >= 0, busy: hooks.appState.busy, previewShown: dom.el("#mask-preview").hidden === false };
    if (hooks.state.mask.artifacts.masked.name !== "keep.masked.txt") fail("409-keeps-old: 不应吞旧结果");
    if (html.indexOf("正在处理") >= 0) fail("409-keeps-old: 不应残留处理中");
    if (hooks.appState.busy) fail("409-keeps-old: busy 应解除");
    if (dom.el("#mask-preview").hidden) fail("409-keeps-old: 应显示预览确认");
  }

  // 5) 还原业务失败保留旧结果、无处理中、busy 解除。
  {
    plan.mode = "ok";
    plan.restoreResponse = restoreBody("old.restored.txt");
    hooks.setRestoreMode("text");
    dom.el("#restore-text").value = "回复 [PHONE_1]";
    hooks.state.restore.mapping = { source: "session", name: "a.mapping.json", b64: Buffer.from("{}").toString("base64"), metadata: { version: 1 } };
    hooks.runRestore();
    await waitFor(() => hooks.state.restore.artifacts.restored);
    await waitFor(() => hooks.appState.busy === false);
    plan.mode = "ok";
    plan.restoreResponse = failBody();
    hooks.runRestore();
    await waitFor(() => hooks.state.restore.status === "failed");
    await waitFor(() => hooks.appState.busy === false);
    const html = dom.el("#restore-result").innerHTML;
    report.cases["fail-keeps-old-restore"] = { hasProcessing: html.indexOf("正在处理") >= 0, kept: hooks.state.restore.artifacts.restored.name, busy: hooks.appState.busy, visible: dom.el("#restore-result").hidden === false };
    if (html.indexOf("正在处理") >= 0) fail("fail-keeps-old-restore: 不应残留处理中");
    if (hooks.state.restore.artifacts.restored.name !== "old.restored.txt") fail("fail-keeps-old-restore: 应保留旧还原结果");
    if (hooks.appState.busy) fail("fail-keeps-old-restore: busy 应解除");
  }
}

main()
  .then(async () => {
    await writeFile(reportPath, JSON.stringify(report, null, 2));
    if (report.failures.length) { console.error("ui_failure 失败:\n" + report.failures.join("\n")); process.exit(1); }
  })
  .catch(async (err) => {
    report.fatal = err && err.stack ? err.stack : String(err);
    try { await writeFile(reportPath, JSON.stringify(report, null, 2)); } catch (e) {}
    console.error("ui_failure fatal: " + report.fatal);
    process.exit(1);
  });
