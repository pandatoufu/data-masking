// 真实 app.js 双视图/语言/主题/映射快照/未保存聚合回归（Node v22；非 app 运行/build 依赖）。
//
// 用法：node ui_dual_view.mjs <appJsPath> <reportPath>
import { readFile, writeFile } from "node:fs/promises";
import vm from "node:vm";

const [appJsPath, reportPath] = process.argv.slice(2);
if (!appJsPath || !reportPath) {
  console.error("用法: node ui_dual_view.mjs <app.js> <reportPath>");
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
  const attrs = {};
  function makeEl(sel) {
    const listeners = {};
    return {
      __sel: sel, hidden: false, textContent: "", innerHTML: "", value: "", disabled: false, open: false,
      files: [], dataset: {}, style: {}, selected: false, options: [],
      classList: { toggle() {}, add() {}, remove() {}, contains() { return false; } },
      addEventListener(t, fn) { (listeners[t] = listeners[t] || []).push(fn); },
      removeEventListener() {}, appendChild() {}, querySelector() { return makeEl(sel + " *"); },
      querySelectorAll() { return []; },
      setAttribute(k, v) { if (sel === "html") attrs[k] = v; },
      getAttribute(k) { return attrs[k] == null ? null : attrs[k]; },
      focus() {}, click() {}, scrollIntoView() {},
      __fire(t, ev) { (listeners[t] || []).forEach((fn) => fn(ev || { preventDefault() {} })); },
    };
  }
  function el(sel) { if (!cache.has(sel)) cache.set(sel, makeEl(sel)); return cache.get(sel); }
  const body = makeEl("body"); body.dataset.page = page;
  const documentElement = makeEl("html");
  return {
    body, documentElement, title: "", el,
    querySelector(sel) { return sel === "body" ? body : el(sel); },
    querySelectorAll() { return []; },
    createElement(tag) { return makeEl("<" + tag + ">"); },
    addEventListener() {}, removeEventListener() {},
  };
}

const plan = { maskResponse: null, restoreResponse: null, autoSaveMaskResult: null, autoSaveRestoredResult: null };

function makeBridge() {
  const calls = { prefs: [], reportUnsaved: [] };
  return {
    calls,
    bridge: {
      SaveFile: () => ({ status: "saved" }),
      ReportUnsaved: (gen, v) => calls.reportUnsaved.push({ gen, v }),
      SetExitConfirmed: () => {},
      SetUIPreferences: (theme, language) => { calls.prefs.push({ theme, language }); return { theme, language, persisted: true }; },
      AutoSaveMask: () => plan.autoSaveMaskResult,
      AutoSaveRestored: () => plan.autoSaveRestoredResult,
    },
  };
}

function makeConfig() {
  return {
    profiles: [{ name: "balanced", display_name: "balanced", rules: { phone: true, idcard: false, email: false, ip: false, api_key: true, custom: true } }],
    default_profile: "balanced", url: "", high_freq_threshold: 20, mode: "desktop",
    initial_view: "mask", theme: "system", language: "zh",
    i18n: {
      zh: { "ui.view_mask": "脱敏", "ui.view_restore": "还原", "ui.app_title": "本地脱敏工具" },
      en: { "ui.view_mask": "Mask", "ui.view_restore": "Restore", "ui.app_title": "Local Masking Tool" }
    }
  };
}

function loadApp(code, dom, bridge) {
  const hooks = {};
  const fetchCalls = [];
  const sandbox = {
    window: {
      __DM_CONFIG__: makeConfig(), __DM_TEST_HOOKS__: hooks,
      go: { desktop: { Bridge: bridge } }, confirm: () => true,
      matchMedia: () => ({ matches: false, addEventListener() {}, addListener() {} }),
    },
    document: dom, confirm: () => true, location: { origin: "http://localhost" },
    fetch: (input) => {
      const p = String(input);
      const body = p.indexOf("/api/restore") >= 0 ? plan.restoreResponse : plan.maskResponse;
      fetchCalls.push(p);
      return Promise.resolve({ status: 200, json: () => Promise.resolve(body) });
    },
    Response, Request, Headers, FormData, Blob, File, TextDecoder, TextEncoder, console, URL, setTimeout, clearTimeout,
    atob: (s) => Buffer.from(String(s), "base64").toString("binary"),
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(code, sandbox, { filename: appJsPath });
  return { hooks, fetchCalls };
}

function fileOf(name, text) { return new File([Buffer.from(text, "utf8")], name, { type: "text/plain" }); }
function maskBody(name, gen) {
  const ext = name.slice(name.lastIndexOf("."));
  return {
    ok: true, masked_name: name, mapping_name: name.slice(0, name.length - ext.length) + ".mapping.json",
    masked_b64: Buffer.from("m").toString("base64"), mapping_b64: Buffer.from("{}").toString("base64"),
    encoding: "UTF-8", format: "text", warnings: [], cleanup: null, audit_failed: false, unsaved_generation: gen,
  };
}
function restoreBody(name, gen) {
  return { ok: true, name, restored_b64: Buffer.from("r").toString("base64"), encoding: "UTF-8", format: "text", restored_count: 1, warnings: [], cleanup: null, audit_failed: false, unsaved_generation: gen };
}

async function main() {
  const code = await readFile(appJsPath, "utf8");
  const dom = makeFakeDom("mask");
  const { calls, bridge } = makeBridge();
  const { hooks, fetchCalls } = loadApp(code, dom, bridge);

  if (!hooks.setView || !hooks.setPreferences || !hooks.useSessionMapping || !hooks.maskSubmit || !hooks.runRestore) {
    throw new Error("缺少双视图/偏好测试钩子");
  }

  // 1) 双视图切换保留各自内存状态，仅隐藏 DOM。
  hooks.state.mask.profile = "balanced";
  hooks.state.mask.words = [{ w: "甲", source: "text", line: 1, checked: false }];
  hooks.state.mask.customRules = { phone: true, idcard: false, email: false, ip: false, api_key: true, custom: false };
  hooks.setRestoreMode("text");
  dom.el("#restore-text").value = "粘贴内容";
  hooks.state.restore.mapping = { source: "manual", name: "m.json", bytes: new Uint8Array([1]) };
  hooks.setView("restore");
  hooks.setView("mask");
  const preserved = hooks.state.mask.words[0].checked === false &&
    hooks.state.mask.customRules.custom === false &&
    dom.el("#restore-text").value === "粘贴内容" &&
    hooks.state.restore.mapping.name === "m.json";
  report.cases["dual-view-preserve"] = { preserved, page: dom.body.dataset.page };
  if (!preserved) fail("dual-view-preserve: 切视图不应丢失状态");
  if (dom.body.dataset.page !== "mask") fail("dual-view-preserve: 应回到 mask");

  // 2) 即时语言/主题：无需刷新，且偏好经 binding 串行提交最后值。
  hooks.setPreferences("dark", "en");
  await waitFor(() => calls.prefs.length >= 1);
  const themeAttr = dom.documentElement.getAttribute("data-theme");
  const enText = hooks.t("ui.view_mask");
  report.cases["prefs-live"] = { themeAttr, enText, lastPref: calls.prefs[calls.prefs.length - 1] };
  if (themeAttr !== "dark") fail("prefs-live: 明确暗色应即时应用");
  if (enText !== "Mask") fail("prefs-live: 英文文案应即时生效");
  if (calls.prefs[calls.prefs.length - 1].language !== "en") fail("prefs-live: 偏好应以最后值提交");

  // 回到中文以便文案断言。
  hooks.setPreferences("system", "zh");
  await waitFor(() => calls.prefs.length >= 2);

  // 3) 会话映射快照独立：新脱敏 B 不覆盖已选 A。
  plan.maskResponse = maskBody("A.masked.txt", 7);
  plan.autoSaveMaskResult = null;
  hooks.state.mask.file = fileOf("A.txt", "secret");
  hooks.maskSubmit([]);
  await waitFor(() => hooks.state.mask.artifacts.masked);
  hooks.useSessionMapping();
  const snapshotName = hooks.state.restore.mapping && hooks.state.restore.mapping.name;
  plan.maskResponse = maskBody("B.masked.txt", 9);
  hooks.state.mask.file = fileOf("B.txt", "secret");
  hooks.maskSubmit([]);
  await waitFor(() => hooks.state.mask.artifacts.masked && hooks.state.mask.artifacts.masked.name === "B.masked.txt");
  const stillA = hooks.state.restore.mapping && hooks.state.restore.mapping.name === snapshotName && snapshotName.indexOf("A") === 0;
  report.cases["mapping-snapshot"] = { snapshotName, afterNewMask: hooks.state.restore.mapping.name };
  if (!stillA) fail("mapping-snapshot: 新脱敏不应覆盖已选会话映射");

  // 3b) busy 拒绝重复执行；失败保留旧结果。
  {
    plan.maskResponse = maskBody("C.masked.txt", 10);
    plan.autoSaveMaskResult = null;
    hooks.state.mask.file = fileOf("C.txt", "x");
    const before = fetchCalls.length;
    hooks.maskSubmit([]);
    hooks.maskSubmit([]); // 处理中，应被拒绝
    await waitFor(() => hooks.state.mask.artifacts.masked && hooks.state.mask.artifacts.masked.name === "C.masked.txt");
    const dupFetches = fetchCalls.length - before;
    const prevStatus = hooks.state.mask.artifacts.masked.status;
    plan.maskResponse = { ok: false, error: "x", message: "失败" };
    hooks.state.mask.file = fileOf("D.txt", "x");
    hooks.maskSubmit([]);
    await waitFor(() => hooks.state.mask.status === "failed");
    const kept = hooks.state.mask.artifacts.masked.status === prevStatus;
    report.cases["busy-reject"] = { dupFetches, kept };
    if (dupFetches !== 1) fail("busy-reject: 重复执行应被拒绝（只发 1 次请求）");
    if (!kept) fail("busy-reject: 失败应保留旧结果");
  }

  // 4) 未保存聚合与最大 generation：两视图都有未保存，最后一次 ReportUnsaved 用最大 gen。
  plan.restoreResponse = restoreBody("restored.txt", 12);
  plan.autoSaveRestoredResult = null;
  hooks.setRestoreMode("text");
  dom.el("#restore-text").value = "回复 [PHONE_1]";
  hooks.runRestore();
  await waitFor(() => hooks.state.restore.artifacts.restored);
  await waitFor(() => calls.reportUnsaved.length > 0);
  const last = calls.reportUnsaved[calls.reportUnsaved.length - 1];
  const bothUnsaved = hooks.state.mask.artifacts.masked.status === "unsaved" && hooks.state.restore.artifacts.restored.status === "unsaved";
  report.cases["generation-aggregate"] = { last, bothUnsaved };
  if (!last || last.gen !== 12) fail("generation-aggregate: 应使用最大 generation");
  if (!last.v) fail("generation-aggregate: 两视图未保存应聚合为 true");
  if (!bothUnsaved) fail("generation-aggregate: 两视图应各自保持未保存");
}

main()
  .then(async () => {
    await writeFile(reportPath, JSON.stringify(report, null, 2));
    if (report.failures.length) { console.error("ui_dual_view 失败:\n" + report.failures.join("\n")); process.exit(1); }
  })
  .catch(async (err) => {
    report.fatal = err && err.stack ? err.stack : String(err);
    try { await writeFile(reportPath, JSON.stringify(report, null, 2)); } catch (e) {}
    console.error("ui_dual_view fatal: " + report.fatal);
    process.exit(1);
  });
