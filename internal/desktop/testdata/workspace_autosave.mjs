// 真实 app.js 工作区自动落盘回归 harness（Node v22；非 app 运行/build 依赖）。
//
// 加载真实 `web/static/app.js`，用轻量 DOM fake + 测试钩子驱动真实 `maskSubmit` /
// `runRestore` / `confirmDiscard`，注入 fake Wails Bridge，断言自动落盘调用次数、
// 产物状态、ReportUnsaved 上报值与退出确认文案分支。不发起真实网络请求（fetch 为桩）。
//
// 用法：node workspace_autosave.mjs <appJsPath> <reportPath>
import { readFile, writeFile } from "node:fs/promises";
import vm from "node:vm";

const [appJsPath, reportPath] = process.argv.slice(2);
if (!appJsPath || !reportPath) {
  console.error("用法: node workspace_autosave.mjs <app.js> <reportPath>");
  process.exit(2);
}

const report = { cases: {}, failures: [], fatal: null };
function fail(msg) {
  report.failures.push(msg);
}
async function flush(n = 80) {
  for (let i = 0; i < n; i++) await new Promise((r) => setImmediate(r));
}
async function waitFor(pred, tries = 500) {
  for (let i = 0; i < tries; i++) {
    if (pred()) return true;
    await new Promise((r) => setImmediate(r));
  }
  return false;
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
      classList: { toggle() {}, add() {}, remove() {}, contains() { return false; } },
      addEventListener(t, fn) { (listeners[t] = listeners[t] || []).push(fn); },
      removeEventListener() {},
      appendChild() {},
      querySelector() { return makeEl(sel + " *"); },
      querySelectorAll() { return []; },
      setAttribute() {},
      getAttribute() { return null; },
      focus() {},
      click() {},
      scrollIntoView() {},
      __fire(t, ev) { (listeners[t] || []).forEach((fn) => fn(ev || { preventDefault() {} })); },
    };
  }
  function el(sel) {
    if (!cache.has(sel)) cache.set(sel, makeEl(sel));
    return cache.get(sel);
  }
  const body = makeEl("body");
  body.dataset.page = page;
  const documentElement = makeEl("html");
  el("#mask-error").hidden = true;
  return {
    body,
    documentElement,
    title: "",
    el,
    querySelector(sel) { return sel === "body" ? body : el(sel); },
    querySelectorAll() { return []; },
    createElement(tag) { return makeEl("<" + tag + ">"); },
    addEventListener() {},
    removeEventListener() {},
  };
}

function makeI18n() {
  return {
    zh: {
      "native.confirm_close_workspace": "有产物未落盘，关闭将丢失，请确认已保存。",
      "native.confirm_close_legacy": "存在尚未保存的结果。选择“是”将放弃这些结果并退出；选择“否”将返回应用，结果仍可保存。"
    },
    en: {}
  };
}

// 可变计划：每个用例设置响应体、自动落盘结果与 confirm 行为。
const plan = {
  maskResponse: null,
  restoreResponse: null,
  autoSaveMaskResult: null,
  autoSaveRestoredResult: null,
  confirmImpl: () => true,
};

function makeBridge() {
  const calls = { autoSaveMask: 0, autoSaveRestored: 0, reportUnsaved: [], setExitConfirmed: 0 };
  const bridge = {
    SaveFile: () => ({ status: "saved", destination: "unused" }),
    ReportUnsaved: (gen, v) => { calls.reportUnsaved.push({ gen: gen, v: v }); },
    SetExitConfirmed: () => { calls.setExitConfirmed++; },
    SetUIPreferences: () => ({ persisted: true }),
    AutoSaveMask: () => { calls.autoSaveMask++; return plan.autoSaveMaskResult; },
    AutoSaveRestored: () => { calls.autoSaveRestored++; return plan.autoSaveRestoredResult; },
  };
  return { bridge, calls };
}

function loadApp(code, dom, bridge) {
  const hooks = {};
  const sandbox = {
    window: {
      __DM_CONFIG__: {
        profiles: [], default_profile: "balanced", url: "", high_freq_threshold: 20, mode: "desktop",
        initial_view: "mask", theme: "system", language: "zh", i18n: makeI18n()
      },
      __DM_TEST_HOOKS__: hooks,
      go: { desktop: { Bridge: bridge } },
      confirm: (m) => plan.confirmImpl(m),
    },
    document: dom,
    confirm: (m) => plan.confirmImpl(m),
    location: { origin: "http://localhost" },
    fetch: (input) => {
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
    TextDecoder,
    TextEncoder,
    console,
    URL,
    setTimeout,
    atob: (s) => Buffer.from(String(s), "base64").toString("binary"),
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(code, sandbox, { filename: appJsPath });
  return { hooks };
}

function fileOf(name, text) {
  return new File([Buffer.from(text, "utf8")], name, { type: "text/plain" });
}

function maskBody(name) {
  const ext = name.slice(name.lastIndexOf("."));
  const stem = name.slice(0, name.length - ext.length);
  return {
    ok: true,
    masked_name: name,
    mapping_name: stem + ".mapping.json",
    masked_b64: Buffer.from("masked").toString("base64"),
    mapping_b64: Buffer.from("{}").toString("base64"),
    encoding: "UTF-8",
    format: "text",
    warnings: [],
    cleanup: null,
    audit_failed: false,
    unsaved_generation: 7,
  };
}

function restoreBody(name) {
  return {
    ok: true,
    name: name,
    restored_b64: Buffer.from("restored").toString("base64"),
    encoding: "UTF-8",
    format: "text",
    restored_count: 1,
    warnings: [],
    cleanup: null,
    audit_failed: false,
    unsaved_generation: 8,
  };
}

async function main() {
  const code = await readFile(appJsPath, "utf8");

  // 1) 脱敏自动落盘成功：成对 saved、ReportUnsaved(false)。
  {
    plan.maskResponse = maskBody("notes.masked.txt");
    plan.autoSaveMaskResult = {
      masked: { status: "saved", path: "C:/app/masked/notes.masked.txt" },
      mapping: { status: "saved", path: "C:/app/mapping/notes.masked.mapping.json" },
    };
    const dom = makeFakeDom("mask");
    const { bridge, calls } = makeBridge();
    const { hooks } = loadApp(code, dom, bridge);
    hooks.state.mask.profiles = {};
    hooks.state.mask.profile = "balanced";
    hooks.state.mask.file = fileOf("notes.txt", "secret");
    hooks.maskSubmit([]);
    await waitFor(() => calls.autoSaveMask === 1 && hooks.state.mask.artifacts.masked && hooks.state.mask.artifacts.masked.status === "saved");
    await flush();
    const last = calls.reportUnsaved[calls.reportUnsaved.length - 1];
    const a = hooks.state.mask.artifacts;
    report.cases["mask-saved"] = {
      maskedStatus: a.masked && a.masked.status,
      mappingStatus: a.mapping && a.mapping.status,
      maskedPath: a.masked && a.masked.autoPath,
      workspaceEnabled: hooks.appState.workspaceEnabled,
      lastReport: last,
    };
    if (!a.masked || a.masked.status !== "saved" || a.mapping.status !== "saved") fail("mask-saved: 应成对 saved");
    if (!a.masked.autoPath || a.masked.autoPath.indexOf("masked") < 0) fail("mask-saved: 应显示落盘路径");
    if (!last || last.v !== false) fail("mask-saved: 落盘成功后应上报未保存=false");
    if (!hooks.appState.workspaceEnabled) fail("mask-saved: workspaceEnabled 应为 true");
  }

  // 2) 脱敏自动落盘失败：保持 failed、ReportUnsaved(true)、可手动另存。
  {
    plan.maskResponse = maskBody("notes.masked.txt");
    plan.autoSaveMaskResult = {
      masked: { status: "failed", message: "磁盘已满" },
      mapping: { status: "failed", message: "磁盘已满" },
    };
    const dom = makeFakeDom("mask");
    const { bridge, calls } = makeBridge();
    const { hooks } = loadApp(code, dom, bridge);
    hooks.state.mask.profiles = {};
    hooks.state.mask.profile = "balanced";
    hooks.state.mask.file = fileOf("notes.txt", "secret");
    hooks.maskSubmit([]);
    await waitFor(() => calls.autoSaveMask === 1 && hooks.state.mask.artifacts.masked && hooks.state.mask.artifacts.masked.status === "failed");
    await flush();
    const last = calls.reportUnsaved[calls.reportUnsaved.length - 1];
    report.cases["mask-failed"] = {
      maskedStatus: hooks.state.mask.artifacts.masked.status,
      lastReport: last,
      errorHidden: dom.el("#mask-error").hidden,
    };
    if (hooks.state.mask.artifacts.masked.status !== "failed") fail("mask-failed: 应保持 failed");
    if (!last || last.v !== true) fail("mask-failed: 失败后应上报未保存=true");
    if (!dom.el("#mask-error").hidden) fail("mask-failed: 不应弹全局错误（结果区提示即可）");
  }

  // 3) 非 Windows / 降级：disabled 静默回退手动保存、保持未保存、不报错。
  {
    plan.maskResponse = maskBody("notes.masked.txt");
    plan.autoSaveMaskResult = { masked: { status: "disabled" }, mapping: { status: "disabled" } };
    const dom = makeFakeDom("mask");
    const { bridge, calls } = makeBridge();
    const { hooks } = loadApp(code, dom, bridge);
    hooks.state.mask.profiles = {};
    hooks.state.mask.profile = "balanced";
    hooks.state.mask.file = fileOf("notes.txt", "secret");
    hooks.maskSubmit([]);
    await waitFor(() => calls.autoSaveMask === 1 && hooks.state.mask.artifacts.masked);
    await flush();
    const last = calls.reportUnsaved[calls.reportUnsaved.length - 1];
    report.cases["mask-disabled"] = {
      maskedStatus: hooks.state.mask.artifacts.masked.status,
      lastReport: last,
      workspaceEnabled: hooks.appState.workspaceEnabled,
      errorHidden: dom.el("#mask-error").hidden,
    };
    if (hooks.state.mask.artifacts.masked.status !== "unsaved") fail("mask-disabled: disabled 应保持未保存");
    if (!last || last.v !== true) fail("mask-disabled: disabled 后应上报未保存=true");
    if (hooks.appState.workspaceEnabled) fail("mask-disabled: workspaceEnabled 应保持 false");
    if (!dom.el("#mask-error").hidden) fail("mask-disabled: disabled 不得报错");
  }

  // 4) 还原自动落盘成功。
  {
    plan.restoreResponse = restoreBody("notes.restored.txt");
    plan.autoSaveRestoredResult = { status: "saved", path: "C:/app/restored/notes.restored.txt" };
    const dom = makeFakeDom("restore");
    const { bridge, calls } = makeBridge();
    const { hooks } = loadApp(code, dom, bridge);
    hooks.setRestoreMode("text");
    dom.el("#restore-text").value = "回复 [PHONE_1]";
    hooks.state.restore.mapping = { source: "session", name: "a.mapping.json", b64: Buffer.from("{}").toString("base64") };
    hooks.runRestore();
    await waitFor(() => calls.autoSaveRestored === 1 && hooks.state.restore.artifacts.restored && hooks.state.restore.artifacts.restored.status === "saved");
    await flush();
    const last = calls.reportUnsaved[calls.reportUnsaved.length - 1];
    report.cases["restore-saved"] = {
      restoredStatus: hooks.state.restore.artifacts.restored.status,
      restoredPath: hooks.state.restore.artifacts.restored.autoPath,
      lastReport: last,
    };
    if (hooks.state.restore.artifacts.restored.status !== "saved") fail("restore-saved: 应 saved");
    if (!last || last.v !== false) fail("restore-saved: 落盘成功后应上报未保存=false");
  }

  // 5) 退出文案分支：选择器与文案随当前语言；工作区启用→未落盘，未启用→既有文案。
  {
    const dom = makeFakeDom("mask");
    const { bridge } = makeBridge();
    const { hooks } = loadApp(code, dom, bridge);
    let captured = null;
    plan.confirmImpl = (m) => { captured = m; return false; };
    hooks.state.restore.artifacts = { restored: { id: "restored", view: "restore", status: "unsaved" } };
    hooks.appState.workspaceEnabled = true;
    hooks.confirmDiscard("exit", true);
    const enabledMsg = captured;
    hooks.appState.workspaceEnabled = false;
    hooks.confirmDiscard("exit", true);
    const disabledMsg = captured;
    report.cases["exit-text"] = { enabledMsg: enabledMsg, disabledMsg: disabledMsg };
    if (!enabledMsg || enabledMsg.indexOf("未落盘") < 0) fail("exit-text: 工作区启用应用未落盘文案");
    if (!disabledMsg || disabledMsg.indexOf("尚未保存") < 0) fail("exit-text: 未启用应保留既有文案");
  }
}

main()
  .then(async () => {
    await writeFile(reportPath, JSON.stringify(report, null, 2));
    if (report.failures.length) {
      console.error("workspace_autosave 失败:\n" + report.failures.join("\n"));
      process.exit(1);
    }
  })
  .catch(async (err) => {
    report.fatal = err && err.stack ? err.stack : String(err);
    try { await writeFile(reportPath, JSON.stringify(report, null, 2)); } catch (e) { /* ignore */ }
    console.error("workspace_autosave fatal: " + report.fatal);
    process.exit(1);
  });
