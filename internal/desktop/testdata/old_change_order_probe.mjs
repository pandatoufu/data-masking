// 审计探针（负向证据，非产品代码；仅开发探针）：在与 ui_regression.mjs 同款的 live FileList
// 模型上，加载 HEAD（第四轮修复前）的真实生产 app.js，分派其 wireFileArea change 监听器，
// 独立验证“旧顺序：先 input.value="" 再读原 FileList 引用”在模型中不会载入任何文件。
//
// 跨平台复现（POSIX shell 与 Windows PowerShell 5.1 使用同一条命令；需 git 与 Node，均为开发
// 前置而非产品运行时依赖；不硬编码 /tmp、无 shell 重定向）：
//   node internal/desktop/testdata/old_change_order_probe.mjs
// 该命令用 child_process.execFileSync("git", ["show", "HEAD:web/static/app.js"]) 取回修复前
// 生产 app.js，经 fs.writeFileSync 写入 os.tmpdir()/path.join 下的临时文件再加载；也可显式传入
// 任一 app.js 路径：node old_change_order_probe.mjs <app.js>（用于对照当前/其它版本）。
// 期望输出 VERDICT: OLD-ORDER-FAILS-IN-MODEL，退出码 0；否则退出码 1。
// 说明：本探针只驱动真实旧监听器 + 模型边界，不复制生产处理逻辑；结论仅说明防回归逻辑成立，
// 不是原生平台（Windows/Linux 文件选择）根因确认。HEAD 需仍为第四轮修复前基线，否则用显式路径。
import { readFile, writeFile } from "node:fs/promises";
import { execFileSync } from "node:child_process";
import os from "node:os";
import path from "node:path";
import vm from "node:vm";

let appJsPath = process.argv[2];
if (!appJsPath) {
  const buf = execFileSync("git", ["show", "HEAD:web/static/app.js"]);
  appJsPath = path.join(os.tmpdir(), "data-masking-old-app-head.js");
  await writeFile(appJsPath, buf);
  console.log("generated old app ->", appJsPath);
}
const INPUTS = new Set(["#mask-file", "#custom-file", "#restore-file", "#restore-mapping"]);
function makeFakeDom() {
  const cache = new Map(); const attrs = {}; const docListeners = {};
  function makeEl(sel) {
    const listeners = {}; const classes = new Set();
    const node = { __sel: sel, hidden: false, textContent: "", innerHTML: "", value: "", disabled: false, open: false, dataset: {}, style: {}, selected: false, options: [],
      classList: { toggle(c, v) { const on = v === undefined ? !classes.has(c) : !!v; if (on) classes.add(c); else classes.delete(c); return on; }, add(c){classes.add(c);}, remove(c){classes.delete(c);}, contains(c){return classes.has(c);} },
      addEventListener(t, fn) { (listeners[t] = listeners[t] || []).push(fn); }, removeEventListener() {}, appendChild() {},
      querySelector() { return makeEl(sel + " *"); }, querySelectorAll() { return []; },
      setAttribute(k, v) { if (sel === "html") attrs[k] = v; }, getAttribute(k) { return attrs[k] == null ? null : attrs[k]; },
      focus() {}, click() {}, scrollIntoView() {},
      __fire(t, ev) { (listeners[t] || []).forEach((fn) => fn(ev || { preventDefault() {} })); } };
    if (INPUTS.has(sel)) {
      const arr = []; let value = "";
      Object.defineProperty(node, "files", { get() { return arr; }, set() { throw new Error("input.files setter unsupported"); } });
      Object.defineProperty(node, "value", { get() { return value; }, set(v) { value = v; if (v === "") arr.length = 0; } });
      node.__setFiles = (list) => { arr.length = 0; (list || []).forEach((f) => arr.push(f)); value = (list && list.length) ? (list[0].name || "file") : ""; };
    }
    return node;
  }
  function el(sel) { if (!cache.has(sel)) cache.set(sel, makeEl(sel)); return cache.get(sel); }
  const body = makeEl("body"); body.dataset.page = "mask";
  return { body, documentElement: makeEl("html"), title: "", el, querySelector(sel) { return sel === "body" ? body : el(sel); }, querySelectorAll() { return []; },
    createElement(tag) { return makeEl("<" + tag + ">"); }, addEventListener(t, fn) { (docListeners[t] = docListeners[t] || []).push(fn); }, removeEventListener() {},
    fireDocument(t, ev) { (docListeners[t] || []).forEach((fn) => fn(ev || { preventDefault() {} })); } };
}
const code = await readFile(appJsPath, "utf8");
const dom = makeFakeDom(); const hooks = {};
const sandbox = { window: { __DM_CONFIG__: { profiles: [{ name: "balanced", display_name: "balanced", rules: { phone: true, idcard: false, email: false, ip: false, api_key: true, custom: true } }], default_profile: "balanced", url: "", high_freq_threshold: 20, i18n: {}, theme: "system", language: "zh", mode: "desktop" }, __DM_TEST_HOOKS__: hooks, go: { desktop: { Bridge: null } }, confirm: () => true, matchMedia: () => ({ matches: false, addEventListener() {}, addListener() {} }), addEventListener() {}, removeEventListener() {} },
  document: dom, confirm: () => true, location: { origin: "http://localhost" }, fetch: () => Promise.resolve({ status: 200, json: () => Promise.resolve({ ok: true }) }),
  Response, Request, Headers, FormData, Blob, File, FileReader: class { readAsArrayBuffer(b) { Promise.resolve(b.arrayBuffer()).then((buf) => { this.result = buf; if (this.onload) this.onload(); }); } }, TextDecoder, TextEncoder, console, URL, setTimeout, clearTimeout,
  atob: (s) => Buffer.from(String(s), "base64").toString("binary") };
sandbox.globalThis = sandbox; vm.createContext(sandbox);
vm.runInContext(code, sandbox, { filename: appJsPath });
dom.fireDocument("DOMContentLoaded"); // 注册旧生产 wireFileArea change 监听器

// 场景1：旧监听器 + 活 FileList 模型 → 选文档
const maskInput = dom.el("#mask-file");
const doc = new File([Buffer.from("电话 13812345678", "utf8")], "notes.txt", { type: "text/plain" });
maskInput.__setFiles([doc]);
maskInput.__fire("change", {});
console.log("OLD change #mask-file loaded:", JSON.stringify(hooks.state.mask.file && hooks.state.mask.file.name) ?? "null");

// 场景2：旧监听器选 TXT 词表
const wordInput = dom.el("#custom-file");
const txt = new File([Buffer.from("内部代号\n", "utf8")], "words.txt", { type: "text/plain" });
wordInput.__setFiles([txt]);
wordInput.__fire("change", {});
await new Promise((r) => setImmediate(r)); await new Promise((r) => setImmediate(r));
console.log("OLD change #custom-file words:", JSON.stringify(hooks.state.mask.words.map((e) => e.w)));
const fails = hooks.state.mask.file == null && hooks.state.mask.words.length === 0;
console.log("VERDICT:", fails ? "OLD-ORDER-FAILS-IN-MODEL（负向证据成立）" : "OLD-ORDER-LOADS（负向证据不成立！）");
process.exit(fails ? 0 : 1);
