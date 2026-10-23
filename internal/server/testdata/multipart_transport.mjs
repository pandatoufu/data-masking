// 真实 app.js 文件上传回归 harness（Node v22；非 app 运行/build 依赖）。
//
// 加载真实 `web/static/app.js`，以轻量 DOM fake + 测试钩子驱动真实 `maskSubmit` /
// `runRestore` / `postMultipart`，请求发往 Go 测试启动的真实 handler（httptest）。
// Go 侧捕获原始 multipart 字节与边界，业务结果由 Go 侧真实 handler 返回并校验。
//
// 用法：node multipart_transport.mjs <appJsPath> <serverURL> <fixturesDir> <reportPath>
import { readFile, writeFile } from "node:fs/promises";
import vm from "node:vm";
import path from "node:path";

const [appJsPath, serverURL, fixturesDir, reportPath] = process.argv.slice(2);
if (!appJsPath || !serverURL || !fixturesDir || !reportPath) {
  console.error("用法: node multipart_transport.mjs <app.js> <serverURL> <fixturesDir> <reportPath>");
  process.exit(2);
}

const report = { cases: [], fatal: null };
const failures = [];
function fail(msg) {
  failures.push(msg);
}

async function flush(times = 40) {
  for (let i = 0; i < times; i++) {
    await new Promise((r) => setImmediate(r));
  }
}
async function waitFor(pred, tries = 400) {
  for (let i = 0; i < tries; i++) {
    if (pred()) return true;
    await new Promise((r) => setImmediate(r));
  }
  return false;
}

// 读取页面真实下发的配置，避免在测试里硬编码 profile。
async function loadInjectedConfig() {
  const resp = await fetch(new URL("/", serverURL));
  const html = await resp.text();
  const marker = "window.__DM_CONFIG__ = ";
  const i = html.indexOf(marker);
  if (i < 0) throw new Error("页面未找到 __DM_CONFIG__ 注入");
  const rest = html.slice(i + marker.length);
  const end = rest.indexOf(";</" + "script>");
  return JSON.parse(rest.slice(0, end));
}

// ---- 轻量 DOM fake（仅覆盖被测入口所需的最小行为）----
function makeFakeDom(page) {
  const cache = new Map();
  function makeEl(sel) {
    const listeners = {};
    const el = {
      __sel: sel,
      __listeners: listeners,
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
      addEventListener(type, fn) {
        (listeners[type] = listeners[type] || []).push(fn);
      },
      removeEventListener() {},
      appendChild() {},
      querySelector() { return makeEl(sel + " *"); },
      querySelectorAll() { return []; },
      setAttribute() {},
      getAttribute() { return null; },
      focus() {},
      click() {},
      scrollIntoView() {},
      __fire(type, ev) {
        (listeners[type] || []).forEach((fn) => fn(ev || { preventDefault() {} }));
      },
    };
    return el;
  }
  function el(sel) {
    if (!cache.has(sel)) cache.set(sel, makeEl(sel));
    return cache.get(sel);
  }
  const body = makeEl("body");
  body.dataset.page = page;
  const documentElement = makeEl("html");
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

class FakeFormData extends FormData {
  constructor(form) {
    super();
    const entries = (form && form.__entries) || [];
    for (const e of entries) {
      if (e.file) this.append(e.name, e.file, e.filename || e.file.name);
      else this.append(e.name, String(e.value));
    }
  }
}

function makeThrowingFormData() {
  const fd = new FormData();
  fd.append("file", new File([new Uint8Array([1, 2, 3])], "a.bin"));
  return new Proxy(fd, {
    get() {
      throw new Error("simulated-serialization-failure");
    },
  });
}

function loadApp(code, config, dom) {
  const hooks = {};
  const fetchCalls = [];
  const sandbox = {
    window: { __DM_CONFIG__: config, __DM_TEST_HOOKS__: hooks, confirm: () => true },
    document: dom,
    confirm: () => true,
    location: { origin: serverURL },
    fetch: (input, init) => {
      const reqPath = String(input);
      const url = new URL(reqPath, serverURL).toString();
      const rec = {
        path: reqPath,
        contentType: init && init.headers && init.headers["Content-Type"] ? init.headers["Content-Type"] : null,
        bodyIsArrayBuffer: init && init.body instanceof ArrayBuffer,
        bodyIsArrayBufferView: !!(init && init.body && ArrayBuffer.isView(init.body)),
        boundaryPresent: false,
        status: null,
        response: null,
        rejected: null,
      };
      let bodyBuf = null;
      if (rec.bodyIsArrayBuffer) bodyBuf = Buffer.from(init.body);
      else if (rec.bodyIsArrayBufferView) bodyBuf = Buffer.from(init.body.buffer, init.body.byteOffset, init.body.byteLength);
      if (bodyBuf && rec.contentType) {
        const m = /boundary=([^;]+)/.exec(rec.contentType);
        rec.boundaryPresent = m ? bodyBuf.toString("latin1").includes(m[1]) : false;
      }
      rec.bodyByteLength = bodyBuf ? bodyBuf.length : null;
      fetchCalls.push(rec);
      const p = fetch(url, init).then(async (resp) => {
        rec.status = resp.status;
        try {
          rec.response = await resp.clone().json();
        } catch (e) {
          rec.response = null;
        }
        return resp;
      });
      rec.promise = p;
      return p;
    },
    Response, Request, Headers, FormData: FakeFormData, Blob, File,
    TextDecoder, TextEncoder, console, URL, setTimeout,
    atob: (s) => Buffer.from(String(s), "base64").toString("binary"),
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(code, sandbox, { filename: appJsPath });
  return { hooks, fetchCalls, dom };
}

function fileOf(buf, name, type) {
  return new File([buf], name, { type: type || "application/octet-stream" });
}

async function readFixture(name) {
  return readFile(path.join(fixturesDir, name));
}

function record(name, pathValue, rec, js) {
  report.cases.push({
    name,
    path: pathValue,
    status: rec ? rec.status : null,
    response: rec ? rec.response : null,
    js: js || (rec ? {
      fetchCalled: true,
      bodyIsArrayBuffer: rec.bodyIsArrayBuffer,
      contentType: rec.contentType,
      boundaryPresent: rec.boundaryPresent,
      bodyByteLength: rec.bodyByteLength,
    } : { fetchCalled: false }),
  });
}

async function main() {
  const config = await loadInjectedConfig();
  const code = await readFile(appJsPath, "utf8");
  const dom = makeFakeDom("mask");
  const ctx = loadApp(code, config, dom);
  const { hooks, fetchCalls } = ctx;
  if (!hooks.postMultipart || !hooks.serializeMultipart || !hooks.maskSubmit || !hooks.runRestore || !hooks.state || !hooks.appState) {
    throw new Error("真实 app.js 未导出测试钩子（postMultipart/serializeMultipart/maskSubmit/runRestore/state/appState）");
  }

  const profiles = {};
  for (const p of config.profiles || []) profiles[p.name] = p;

  function resetMaskState(profile, file) {
    hooks.state.mask.profiles = profiles;
    hooks.state.mask.profile = profile || "balanced";
    hooks.state.mask.customRules = { phone: true, idcard: true, email: true, ip: true, api_key: true, custom: true };
    hooks.state.mask.words = [];
    hooks.state.mask.importMeta = null;
    hooks.state.mask.importFile = null;
    hooks.state.mask.lastBody = null;
    hooks.state.mask.artifacts = {};
    hooks.state.mask.preview = null;
    hooks.state.mask.status = "idle";
    hooks.state.mask.file = file || null;
    hooks.appState.busy = false;
    dom.el("#custom").value = "";
    dom.el("#mask-error").hidden = true;
    dom.el("#mask-error").textContent = "";
  }

  async function nextRecord(calls, before) {
    const ok = await waitFor(() => calls.length > before);
    if (!ok) return null;
    const rec = calls[calls.length - 1];
    if (rec && rec.promise) {
      try {
        await rec.promise;
      } catch (e) {
        // 请求失败时状态/响应保持原样，交由断言处理。
      }
    }
    await flush();
    return rec;
  }

  const docxBuf = await readFixture("合同.docx");
  const notesBuf = await readFixture("notes.txt");
  const highBuf = await readFixture("highfreq.txt");
  const utf8Words = await readFixture("words-utf8.txt");
  const gbkWords = await readFixture("words-gbk.txt");

  // 1) 二进制 DOCX + 中文文件名 + 手输词 + 词文件 + 重复 confirmed。
  resetMaskState("balanced", fileOf(docxBuf, "合同.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"));
  dom.el("#custom").value = "手输词";
  hooks.state.mask.words = [
    { w: "手输词", source: "text", line: 1, checked: true },
    { w: "绝密词", source: "file", checked: true },
  ];
  hooks.state.mask.importFile = fileOf(utf8Words, "词表.txt", "text/plain");
  const beforeDocx = fetchCalls.length;
  hooks.maskSubmit(["绝密词", "绝密词"]);
  {
    const rec = await nextRecord(fetchCalls, beforeDocx);
    record("docx-binary-mask", rec.path, rec);
    if (rec.status !== 200) fail("docx-binary-mask: 期望 200，实际 " + rec.status);
  }
  const maskResp = fetchCalls[fetchCalls.length - 1].response;

  // 2) GBK 词文件原始字节保真。
  resetMaskState("balanced", fileOf(notesBuf, "notes.txt", "text/plain"));
  hooks.state.mask.words = [{ w: "绝密词", source: "file", checked: true }];
  hooks.state.mask.importFile = fileOf(gbkWords, "词表.txt", "text/plain");
  {
    const before = fetchCalls.length;
    hooks.maskSubmit([]);
    const rec = await nextRecord(fetchCalls, before);
    record("wordfile-gbk", rec.path, rec);
    if (rec.status !== 200) fail("wordfile-gbk: 期望 200，实际 " + rec.status);
  }

  // 3) 词的部分勾选 → 生成 words-selected.txt Blob。
  resetMaskState("balanced", fileOf(notesBuf, "notes.txt", "text/plain"));
  hooks.state.mask.words = [
    { w: "绝密词", source: "file", checked: true },
    { w: "内部词", source: "file", checked: false },
  ];
  hooks.state.mask.importFile = fileOf(utf8Words, "词表.txt", "text/plain");
  {
    const before = fetchCalls.length;
    hooks.maskSubmit([]);
    const rec = await nextRecord(fetchCalls, before);
    record("partial-checked-words", rec.path, rec);
    if (rec.status !== 200) fail("partial-checked-words: 期望 200，实际 " + rec.status);
  }

  // 4) 409 preview_required → 面板“确认并继续”走同一发送函数。
  resetMaskState("balanced", fileOf(highBuf, "高频.txt", "text/plain"));
  dom.el("#custom").value = "内部词";
  hooks.state.mask.words = [{ w: "内部词", source: "text", line: 1, checked: true }];
  {
    const beforeRetry = fetchCalls.length;
    hooks.maskSubmit([]);
    const first = await nextRecord(fetchCalls, beforeRetry);
    record("mask-409-first", first.path, first);
    if (first.status !== 409) fail("mask-409-first: 期望 409，实际 " + first.status);
    // 触发真实“确认并继续”按钮回调。
    const retryBefore = fetchCalls.length;
    dom.el("#pv-continue").__fire("click");
    const second = await nextRecord(fetchCalls, retryBefore);
    record("mask-409-retry", second.path, second);
    if (second.status !== 200) fail("mask-409-retry: 期望 200，实际 " + second.status);
  }

  // 5) 还原 DOCX：真实 runRestore + 原生 file/mapping 上传。
  if (!maskResp || !maskResp.masked_b64 || !maskResp.mapping_b64) {
    fail("restore-docx: 缺少 mask 产物，无法构造还原输入");
  } else {
    const masked = Buffer.from(maskResp.masked_b64, "base64");
    const mapping = Buffer.from(maskResp.mapping_b64, "base64");
    const domR = makeFakeDom("restore");
    const ctxR = loadApp(code, config, domR);
    ctxR.hooks.setRestoreMode("file");
    ctxR.hooks.state.restore.file = fileOf(masked, maskResp.masked_name || "a.masked.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document");
    ctxR.hooks.state.restore.mapping = { source: "manual", name: maskResp.mapping_name || "a.mapping.json", file: fileOf(mapping, maskResp.mapping_name || "a.mapping.json", "application/json") };
    const before = ctxR.fetchCalls.length;
    ctxR.hooks.runRestore();
    const rec = await nextRecord(ctxR.fetchCalls, before);
    record("restore-docx", rec.path, rec);
    if (rec.status !== 200) fail("restore-docx: 期望 200，实际 " + rec.status);

    // 6) 还原粘贴文本（同一 v2 mapping 跨格式回程）。
    const domP = makeFakeDom("restore");
    const ctxP = loadApp(code, config, domP);
    ctxP.hooks.setRestoreMode("text");
    domP.el("#restore-text").value = "回复 [PHONE_1] 与 [CUSTOM_1]";
    ctxP.hooks.state.restore.mapping = { source: "manual", name: maskResp.mapping_name || "a.mapping.json", file: fileOf(mapping, maskResp.mapping_name || "a.mapping.json", "application/json") };
    const pBefore = ctxP.fetchCalls.length;
    ctxP.hooks.runRestore();
    const prec = await nextRecord(ctxP.fetchCalls, pBefore);
    record("restore-paste", prec.path, prec);
    if (prec.status !== 200) fail("restore-paste: 期望 200，实际 " + prec.status);
  }

  // 7) 序列化失败：不得发出任何请求。
  {
    const before = fetchCalls.length;
    let rejected = false;
    let message = null;
    try {
      await hooks.postMultipart("/api/mask", makeThrowingFormData());
    } catch (e) {
      rejected = true;
      message = e && e.message ? e.message : String(e);
    }
    const fired = fetchCalls.length - before;
    record("serialization-failure", null, null, {
      fetchCalled: fired > 0,
      rejected,
      message,
      fired,
    });
    if (!rejected) fail("serialization-failure: 序列化失败应 reject");
    if (fired > 0) fail("serialization-failure: 序列化失败不得发出请求（实际 " + fired + "）");
  }
}

main()
  .then(async () => {
    report.failures = failures;
    await writeFile(reportPath, JSON.stringify(report, null, 2));
    if (failures.length) {
      console.error("multipart_transport 失败:\n" + failures.join("\n"));
      process.exit(1);
    }
  })
  .catch(async (err) => {
    report.fatal = err && err.stack ? err.stack : String(err);
    try {
      await writeFile(reportPath, JSON.stringify(report, null, 2));
    } catch (e) {
      // ignore
    }
    console.error("multipart_transport fatal: " + (err && err.stack ? err.stack : err));
    process.exit(1);
  });
