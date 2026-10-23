(function () {
  "use strict";

  var cfg = window.__DM_CONFIG__ || {
    profiles: [], default_profile: "balanced", url: "", high_freq_threshold: 20,
    i18n: {}, theme: "system", language: "zh"
  };
  var IS_DESKTOP = cfg.mode === "desktop";
  // 只读平台能力：仅桌面 Windows 支持按钮拖入；Linux/非桌面/缺字段保守 false。
  // 由 Go 按运行时 GOOS 下发，前端不按浏览器 UA 猜测，切换主题/语言不改变该值。
  var FILE_DROP_ENABLED = cfg.file_drop_enabled === true;
  function currentFileHintKey() { return FILE_DROP_ENABLED ? "ui.drop_hint" : "ui.click_hint"; }

  var CUSTOM_TOKEN = "__custom";
  var MAX_WORD_FILE = 1024 * 1024; // 词文件 ≤1MB
  var MAX_WORD_LEN = 256;          // 单词 ≤256 字符
  var MAX_WORD_COUNT = 1000;       // 本次词表/合并参与集 ≤1000
  var RULE_ORDER = ["phone", "idcard", "email", "ip", "api_key", "custom"];

  // 内置名称表（design 冻结）：六类规则与四档方案的中英文名。用户自建方案原样显示。
  var RULE_NAMES = {
    phone: { zh: "手机号", en: "Mainland China mobile" },
    idcard: { zh: "身份证号", en: "Chinese ID number" },
    email: { zh: "邮箱", en: "Email" },
    ip: { zh: "IP", en: "IP" },
    api_key: { zh: "API 密钥", en: "API key" },
    custom: { zh: "自定义词", en: "Custom words" }
  };
  var PROFILE_NAMES = {
    strict: { zh: "全规则脱敏", en: "All rules" },
    balanced: { zh: "日常脱敏", en: "Balanced" },
    minimal: { zh: "仅密钥", en: "API keys only" },
    __custom: { zh: "自定义", en: "Custom" }
  };

  // ---- i18n（资源随页面注入，无在线引用）----
  var I18N = cfg.i18n || {};
  var lang = (cfg.language === "en") ? "en" : "zh";
  function raw(id) {
    if (I18N[lang] && I18N[lang][id] != null) return I18N[lang][id];
    if (I18N.zh && I18N.zh[id] != null) return I18N.zh[id];
    return null;
  }
  function resolveParam(v) {
    if (v == null) return "";
    if (typeof v === "object") {
      if (Array.isArray(v)) return v.map(resolveParam).join("；");
      if (v.message_id) return t(v.message_id, v.params);
    }
    return String(v);
  }
  function format(s, params) {
    if (!s) return "";
    if (!params) return s;
    return s.replace(/\{([a-zA-Z0-9_.]+)\}/g, function (tok, key) {
      if (!Object.prototype.hasOwnProperty.call(params, key)) return tok;
      return resolveParam(params[key]);
    });
  }
  function t(id, params) {
    var s = raw(id);
    if (s == null) {
      // 未知 message_id：按所选语言给通用原因/下一步，绝不回退原始中文或空串。
      var fb = raw("error.unknown_id") || "An unrecognized problem occurred ({id}).";
      var merged = {};
      if (params) Object.keys(params).forEach(function (k) { merged[k] = params[k]; });
      merged.id = id;
      return format(fb, merged);
    }
    return format(s, params);
  }
  function isKnownID(id) { return raw(id) != null; }
  function esc(s) {
    return String(s == null ? "" : s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }
  function th(id, params) { return esc(t(id, params)); }
  function langName(map, name) {
    var m = map[name];
    if (m) return m[lang] || m.zh || map[name];
    return null;
  }
  function profileDisplay(p) {
    var builtin = langName(PROFILE_NAMES, p.name);
    if (builtin) return builtin;
    return p.display_name || p.name;
  }
  function ruleName(id) { return (RULE_NAMES[id] || {})[lang] || id; }

  // ---- DOM 帮助 ----
  function $(sel) { return document.querySelector(sel); }
  function $$(sel) { return Array.prototype.slice.call(document.querySelectorAll(sel)); }
  function setText(sel, text) { var el = $(sel); if (el) el.textContent = text; }
  function setHidden(sel, hidden) { var el = $(sel); if (el) el.hidden = !!hidden; }
  function showViewError(view, msg) {
    var el = $(view === "restore" ? "#restore-error" : "#mask-error");
    if (!el) return;
    el.textContent = msg;
    el.hidden = false;
  }
  function clearViewError(view) {
    var el = $(view === "restore" ? "#restore-error" : "#mask-error");
    if (el) { el.hidden = true; el.textContent = ""; }
  }
  function runeLen(s) { return Array.from(s).length; }
  function bindingApp() { return (window.go && window.go.desktop && window.go.desktop.Bridge) || null; }
  function b64ToBytes(b64) {
    var bin = atob(b64), out = new Uint8Array(bin.length);
    for (var i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
    return out;
  }

  // ---- 全局与两视图内存状态 ----
  function seedCustomRules() {
    var rules = { phone: true, idcard: true, email: true, ip: true, api_key: true, custom: true };
    var cu = (cfg.profiles || []).filter(function (p) { return p.name === CUSTOM_TOKEN; })[0];
    if (cu && cu.rules) RULE_ORDER.forEach(function (id) { rules[id] = !!cu.rules[id]; });
    return rules;
  }
  function newMaskState() {
    var profiles = {};
    (cfg.profiles || []).forEach(function (p) { profiles[p.name] = p; });
    return {
      profiles: profiles,
      profile: cfg.default_profile || "balanced",
      customRules: seedCustomRules(),
      words: [], importMeta: null, importFile: null, file: null,
      preview: null, lastBody: null, artifacts: {}, status: "idle"
    };
  }
  var appState = {
    activeView: (cfg.initial_view === "restore") ? "restore" : "mask",
    prefs: { theme: cfg.theme || "system", language: (cfg.language === "en") ? "en" : "zh" },
    busy: false,
    reportGen: 0,
    workspaceEnabled: false,
    opSeq: 0,
    // 四类固定目录列表只保存名称与本地状态，与当前输入/产物快照解耦。
    workspaceLists: {
      original: { seq: 0, status: "idle", files: [], errorID: null, errorParams: null },
      cards: { seq: 0, status: "idle", files: [], errorID: null, errorParams: null },
      pending: { seq: 0, status: "idle", files: [], errorID: null, errorParams: null },
      mapping: { seq: 0, status: "idle", files: [], errorID: null, errorParams: null }
    },
    // 读取请求序号：旧回调不得覆盖较新的选择/视图。
    workspaceReadSeq: { original: 0, cards: 0, pending: 0, mapping: 0 }
  };
  // 敏感会话仅内存，MUST NOT 写 localStorage/sessionStorage/磁盘。
  var state = {
    mask: newMaskState(),
    restore: { inputMode: "file", mapping: null, lastBody: null, artifacts: {}, status: "idle" }
  };
  lang = appState.prefs.language;

  function anyUnsaved() {
    return [state.mask.artifacts, state.restore.artifacts].some(function (arts) {
      return Object.keys(arts).some(function (k) { return arts[k].status !== "saved"; });
    });
  }
  function anyArtifactBusy() {
    return [state.mask.artifacts, state.restore.artifacts].some(function (arts) {
      return Object.keys(arts).some(function (k) { return !!arts[k].busy; });
    });
  }
  function artBusy(view) {
    var arts = state[view].artifacts;
    return Object.keys(arts).some(function (k) { return !!arts[k].busy; });
  }
  function updateUnsavedBinding() {
    var app = bindingApp();
    if (app && app.ReportUnsaved) {
      try { app.ReportUnsaved(appState.reportGen, anyUnsaved()); } catch (e) { /* 非桌面环境忽略 */ }
    }
  }
  // 每次成功响应取最大 generation；旧回调用最新 token 重新聚合，不带旧 false 快照。
  function noteGeneration(gen) {
    if (typeof gen === "number" && gen > appState.reportGen) appState.reportGen = gen;
  }

  // ---- 主题 ----
  function resolvedTheme() {
    if (appState.prefs.theme === "system") {
      try {
        if (window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches) return "dark";
      } catch (e) { /* ignore */ }
      return "light";
    }
    return appState.prefs.theme === "dark" ? "dark" : "light";
  }
  function applyTheme() {
    var root = document.documentElement;
    if (root && root.setAttribute) root.setAttribute("data-theme", resolvedTheme());
  }
  function initTheme() {
    applyTheme();
    try {
      if (window.matchMedia) {
        var mq = window.matchMedia("(prefers-color-scheme: dark)");
        var listener = function () { if (appState.prefs.theme === "system") applyTheme(); };
        if (mq.addEventListener) mq.addEventListener("change", listener);
        else if (mq.addListener) mq.addListener(listener);
      }
    } catch (e) { /* ignore */ }
  }

  // ---- 偏好串行提交（最后选择收敛，旧返回不回滚）----
  var prefsQueue = Promise.resolve();
  function setPreferences(theme, language) {
    appState.prefs.theme = theme;
    appState.prefs.language = (language === "en") ? "en" : "zh";
    lang = appState.prefs.language;
    applyTheme();
    applyI18n();
    prefsQueue = prefsQueue.then(function () {
      var app = bindingApp();
      if (!app || !app.SetUIPreferences) return null;
      return Promise.resolve(app.SetUIPreferences(theme, lang)).then(function (r) {
        if (r && r.message_id) showGlobalStatus(t(r.message_id), 5000);
      });
    }).catch(function () { /* 偏好失败不阻断业务 */ });
  }

  function initPrefs() {
    var ts = $("#theme-select");
    if (ts) { ts.value = appState.prefs.theme; ts.addEventListener("change", function () { setPreferences(ts.value, lang); }); }
    var ls = $("#language-select");
    if (ls) { ls.value = lang; ls.addEventListener("change", function () { setPreferences(appState.prefs.theme, ls.value); }); }
  }

  // ---- 视图切换（仅隐藏 DOM，不销毁输入）----
  function setView(v) {
    appState.activeView = v;
    renderVisibility();
    if (v === "restore") renderRestoreResults(); else renderMaskResults();
  }
  function renderVisibility() {
    var m = $("#mask-panel"), r = $("#restore-panel");
    if (m) m.hidden = appState.activeView !== "mask";
    if (r) r.hidden = appState.activeView !== "restore";
    if (document.body && document.body.dataset) document.body.dataset.page = appState.activeView;
    var mb = $("#view-mask"), rb = $("#view-restore");
    if (mb && mb.classList) { mb.classList.toggle("active", appState.activeView === "mask"); }
    if (rb && rb.classList) { rb.classList.toggle("active", appState.activeView === "restore"); }
  }
  function initViewSwitch() {
    var mb = $("#view-mask"), rb = $("#view-restore");
    if (mb) mb.addEventListener("click", function () { setView("mask"); });
    if (rb) rb.addEventListener("click", function () { setView("restore"); });
  }

  // ---- 全局状态（隐藏视图完成通知）----
  var statusTimer = null;
  function showGlobalStatus(message, ms, actionLabel, action) {
    var el = $("#global-status");
    if (!el) return;
    el.innerHTML = "";
    var span = document.createElement("span");
    span.textContent = message;
    el.appendChild(span);
    if (actionLabel && action) {
      var btn = document.createElement("button");
      btn.type = "button";
      btn.textContent = actionLabel;
      btn.addEventListener("click", action);
      el.appendChild(btn);
    }
    el.hidden = false;
    if (statusTimer) clearTimeout(statusTimer);
    if (ms) statusTimer = setTimeout(function () { el.hidden = true; }, ms);
  }

  // ---- 静态文案/i18n 应用 ----
  function applyI18n() {
    var root = document.documentElement;
    if (root && root.setAttribute) root.setAttribute("lang", lang === "en" ? "en" : "zh-CN");
    if (document.title !== undefined) document.title = t("ui.app_title");
    $$("[data-i18n]").forEach(function (el) { el.textContent = t(el.getAttribute("data-i18n")); });
    $$("[data-i18n-placeholder]").forEach(function (el) { el.setAttribute("placeholder", t(el.getAttribute("data-i18n-placeholder"))); });
    // 四入口提示按平台能力选择点击/点击或拖入，切换语言只换文案、不改能力。
    $$("[data-i18n-file-hint]").forEach(function (el) { el.textContent = t(currentFileHintKey()); });
    renderProfile();
    renderWords();
    renderPreview();
    renderMaskResults();
    renderRestoreResults();
    renderMappingSource();
    renderWordFeedback();
    renderAllWorkspaceLists();
    renderFileOrigins();
    updateActionState();
  }

  // ---- 方案渲染（实际配置驱动，不硬编码 coverage）----
  function profileByName(name) { return state.mask.profiles[name]; }
  function effectiveCustomRule() {
    if (state.mask.profile === CUSTOM_TOKEN) return !!state.mask.customRules.custom;
    var p = profileByName(state.mask.profile);
    return !!(p && p.rules && p.rules.custom);
  }
  function initProfiles() {
    var sel = $("#profile-select");
    if (!sel) return;
    (cfg.profiles || []).forEach(function (p) {
      var o = document.createElement("option");
      o.value = p.name;
      o.textContent = profileDisplay(p);
      if (p.name === state.mask.profile) o.selected = true;
      sel.appendChild(o);
    });
    if (!state.mask.profiles[state.mask.profile] && (cfg.profiles || []).length) {
      state.mask.profile = cfg.profiles[0].name;
      sel.value = state.mask.profile;
    }
    sel.addEventListener("change", function () {
      state.mask.profile = sel.value;
      renderProfile();
      renderWords();
    });
  }
  function currentRules() {
    var p = profileByName(state.mask.profile);
    if (state.mask.profile === CUSTOM_TOKEN) return state.mask.customRules;
    return (p && p.rules) || {};
  }
  function renderProfile() {
    var sel = $("#profile-select");
    if (sel && (cfg.profiles || []).length) {
      // 语言切换后刷新选项显示名（保留选中）。
      Array.prototype.forEach.call(sel.options || [], function (o) {
        var p = profileByName(o.value);
        if (p) o.textContent = profileDisplay(p);
      });
    }
    var p = profileByName(state.mask.profile);
    if (p) {
      var desc = $("#profile-desc");
      if (desc) { desc.textContent = ""; desc.hidden = true; }
      var summary = $("#profile-summary");
      if (summary) {
        var on = RULE_ORDER.filter(function (id) { return !!currentRules()[id]; }).map(ruleName);
        summary.textContent = on.length ? t("ui.summary_enabled", { enabled: on.join("、") }) : t("ui.summary_none");
        summary.hidden = false;
      }
    }
    var body = $("#profile-matrix-body");
    if (!body) return;
    var editable = state.mask.profile === CUSTOM_TOKEN;
    var rules = currentRules();
    body.innerHTML = "";
    RULE_ORDER.forEach(function (id) {
      var wrap = document.createElement("label");
      wrap.className = "matrix-item";
      var cb = document.createElement("input");
      cb.type = "checkbox";
      cb.checked = !!rules[id];
      cb.disabled = !editable;
      if (editable) {
        cb.addEventListener("change", function () {
          state.mask.customRules[id] = cb.checked;
          renderProfile();
          renderWords();
        });
      }
      var span = document.createElement("span");
      span.textContent = ruleName(id);
      wrap.appendChild(cb);
      wrap.appendChild(span);
      body.appendChild(wrap);
    });
    if (editable && $("#profile-details")) $("#profile-details").open = true;
  }

  // ---- 本次词：解析、导入、勾选、搜索、批量（5.2/5.3）----
  function dedup(list) {
    var seen = {}, out = [];
    list.forEach(function (w) { if (!seen[w]) { seen[w] = true; out.push(w); } });
    return out;
  }
  function wordMap() { var m = {}; state.mask.words.forEach(function (e) { m[e.w] = e.checked; }); return m; }
  function hasNULOrUTF16BOM(bytes) {
    if (bytes.length >= 2 && ((bytes[0] === 0xFF && bytes[1] === 0xFE) || (bytes[0] === 0xFE && bytes[1] === 0xFF))) return true;
    for (var i = 0; i < bytes.length; i++) if (bytes[i] === 0x00) return true;
    return false;
  }
  function readAndDecode(file) {
    return new Promise(function (resolve, reject) {
      var fr = new FileReader();
      fr.onerror = function () { reject({ message_id: "ui.unsupported_word_file" }); };
      fr.onload = function () {
        var bytes = new Uint8Array(fr.result);
        if (hasNULOrUTF16BOM(bytes)) { reject({ message_id: "ui.word_file_binary" }); return; }
        try { resolve(new TextDecoder("utf-8", { fatal: true }).decode(bytes)); return; } catch (e) {}
        try { resolve(new TextDecoder("gbk").decode(bytes)); return; } catch (e) {}
        reject({ message_id: "ui.unsupported_word_file" });
      };
      fr.readAsArrayBuffer(file);
    });
  }
  function onTextInput() {
    var prev = wordMap();
    var fileEntries = state.mask.words.filter(function (e) { return e.source === "file"; });
    var fileSet = {};
    fileEntries.forEach(function (e) { fileSet[e.w] = true; });
    var rawLines = String($("#custom").value).split(/\r\n|\n/);
    var seen = {};
    var textEntries = [];
    rawLines.forEach(function (raw, i) {
      var w = raw.trim();
      if (!w || seen[w] || fileSet[w]) return;
      seen[w] = true;
      textEntries.push({ w: w, source: "text", line: i + 1, checked: prev.hasOwnProperty(w) ? prev[w] : true });
    });
    state.mask.words = textEntries.concat(fileEntries);
    renderWords();
  }
  // applyImportedWords 解析并“成功才原子交换”词表：非法导入抛错且不改动旧词表与旧文件名。
  function applyImportedWords(text, file) {
    var lines = String(text).split(/\r\n|\n/);
    var words = [];
    for (var i = 0; i < lines.length; i++) {
      var w = lines[i].trim();
      if (!w) continue;
      if (runeLen(w) > MAX_WORD_LEN) throw { message_id: "ui.word_line_too_long", params: { line: i + 1 } };
      words.push(w);
    }
    if (words.length === 0) throw { message_id: "ui.word_file_empty" };
    var uniq = dedup(words);
    if (uniq.length > MAX_WORD_COUNT) throw { message_id: "ui.word_file_too_many" };
    var prev = wordMap();
    var textEntries = state.mask.words.filter(function (e) { return e.source === "text"; });
    var textSet = {};
    textEntries.forEach(function (e) { textSet[e.w] = true; });
    var fileEntries = uniq.filter(function (w) { return !textSet[w]; }).map(function (w) {
      return { w: w, source: "file", checked: prev.hasOwnProperty(w) ? prev[w] : true };
    });
    state.mask.words = textEntries.concat(fileEntries);
    state.mask.importMeta = { n: lines.length, m: uniq.length };
    state.mask.importFile = file;
    setText("#word-file-name", file && file.name ? file.name : "");
    clearWordFeedback();
    renderWords();
  }
  // 导入失败反馈位于唯一折叠外（导入入口旁），不强制展开自定义词。
  function showWordFeedback(msg, id, params) {
    var el = $("#word-feedback");
    if (el) { el.textContent = msg; el.hidden = false; }
    state.mask.wordFeedback = { id: id || null, params: params || null };
  }
  function clearWordFeedback() {
    var el = $("#word-feedback");
    if (el) { el.hidden = true; el.textContent = ""; }
    state.mask.wordFeedback = null;
  }
  function renderWordFeedback() {
    var fb = state.mask.wordFeedback;
    if (!fb || !fb.id) return;
    setText("#word-feedback", t(fb.id, fb.params));
    var el = $("#word-feedback");
    if (el) el.hidden = false;
  }
  function onImportWordFile(file, origin) {
    if (!file) return Promise.resolve();
    clearViewError("mask");
    if (file.size > MAX_WORD_FILE) {
      showWordFeedback(t("ui.word_file_too_large"), "ui.word_file_too_large", null);
      return Promise.resolve();
    }
    return readAndDecode(file).then(function (text) {
      applyImportedWords(text, file);
      state.mask.wordOrigin = origin || "manual";
      renderFileOrigins();
    }).catch(function (err) {
      // 非法导入保留上一份有效词表与旧文件名；不交换状态、不强制展开。
      var id = (err && err.message_id) ? err.message_id : "ui.unsupported_word_file";
      showWordFeedback(t(id, err && err.params), id, err && err.params);
    });
  }
  function searchTerm() { var el = $("#word-search"); return el ? String(el.value).trim() : ""; }
  function matchedWords() {
    var q = searchTerm();
    if (!q) return state.mask.words.slice();
    var low = q.toLowerCase();
    return state.mask.words.filter(function (e) { return e.w.toLowerCase().indexOf(low) >= 0; });
  }
  function renderWords() {
    var enabled = effectiveCustomRule();
    var area = $("#word-area");
    if (area && area.classList) area.classList.toggle("disabled", !enabled);
    var ta = $("#custom"); if (ta) ta.disabled = !enabled;
    var fi = $("#custom-file"); if (fi) fi.disabled = !enabled;
    var cardsSel = $("#cards-select"); if (cardsSel) cardsSel.disabled = !enabled || appState.busy;
    var cardsBtn = $("#cards-refresh"); if (cardsBtn) cardsBtn.disabled = !enabled || appState.busy;
    var cardsGroup = $("#cards-picker"); if (cardsGroup && cardsGroup.classList) cardsGroup.classList.toggle("disabled", !enabled);
    var hint = $("#word-disabled-hint");
    if (hint) { hint.hidden = enabled; hint.textContent = t("ui.words_disabled"); }

    var all = state.mask.words;
    var selected = all.filter(function (e) { return e.checked; }).length;
    var matched = matchedWords();
    // 唯一折叠 summary 的已选/总数摘要（折叠时也可见）；搜索时附匹配 Z。
    var adjustCount = $("#word-adjust-count");
    if (adjustCount) {
      var matchPart = searchTerm() ? t("ui.words_match_suffix", { z: matched.length }) : "";
      adjustCount.textContent = t("ui.adjust_selected", { x: selected, y: all.length }) + matchPart;
    }
    var selAll = $("#word-select-all"), deAll = $("#word-deselect-all");
    if (selAll) selAll.textContent = searchTerm() ? t("ui.words_select_search") : t("ui.words_select_all");
    if (deAll) deAll.textContent = searchTerm() ? t("ui.words_deselect_search") : t("ui.words_deselect_all");

    var list = $("#word-list");
    if (!list) return;
    list.innerHTML = "";
    var show = searchTerm() ? matched : all;
    show.forEach(function (e) {
      var wrap = document.createElement("label");
      wrap.className = "word-item" + (e.checked ? "" : " unchecked");
      var cb = document.createElement("input");
      cb.type = "checkbox";
      cb.checked = e.checked;
      cb.disabled = !enabled;
      cb.addEventListener("change", function () {
        e.checked = cb.checked;
        if (wrap.classList) wrap.classList.toggle("unchecked", !cb.checked);
        renderWords();
      });
      var span = document.createElement("span");
      span.textContent = e.w + (e.source === "file" ? t("ui.word_imported_tag") : "");
      wrap.appendChild(cb);
      wrap.appendChild(span);
      list.appendChild(wrap);
    });
    if (!show.length) {
      var empty = document.createElement("p");
      empty.className = "hint";
      empty.textContent = t("ui.words_empty");
      list.appendChild(empty);
    }
  }
  function setMatchedChecked(value) {
    var target = searchTerm() ? matchedWords() : state.mask.words;
    target.forEach(function (e) { e.checked = value; });
    renderWords();
  }
  function initWords() {
    var ta = $("#custom"); if (ta) ta.addEventListener("input", onTextInput);
    var fi = $("#custom-file");
    if (fi) {
      wireFileArea("#word-file-area", fi, function (file) { onImportWordFile(file); }, "mask");
    }
    var search = $("#word-search"); if (search) search.addEventListener("input", renderWords);
    var sa = $("#word-select-all"); if (sa) sa.addEventListener("click", function () { setMatchedChecked(true); });
    var da = $("#word-deselect-all"); if (da) da.addEventListener("click", function () { setMatchedChecked(false); });
    renderWords();
  }

  // ---- 文件区域：点击/拖入同一选择链（5.1）----
  function isFileAreaTarget(target) {
    return !!(target && target.closest && target.closest(".file-btn"));
  }
  // receiveFiles 统一多文件拒绝：>1 给单文件指引且不调用选择处理器；=1 走同一处理器。
  function receiveFiles(files, onFile, view) {
    if (!files || files.length === 0) return;
    if (files.length > 1) { showViewError(view || currentErrorView(), t("ui.multi_file_rejected")); return; }
    onFile(files[0]);
  }
  function wireFileArea(areaSel, input, onFile, view) {
    var area = $(areaSel);
    if (!area) return;
    if (input) {
      input.addEventListener("change", function () {
        var files = input.files;
        if (!files || files.length === 0) return; // 取消：保留先前文件/名称/词/映射
        // 先复制独立 File 对象，再重置控件；不得清空后仍读取原 FileList 引用。
        var picked = Array.from(files);
        input.value = ""; // 无条件重置：忙/禁用被拒后解除限制仍可重选同一文件
        // 禁用（规则关闭）或处理/保存忙时按既有保护拒绝选择，不因事件绕过；控件已 reset。
        if (input.disabled || appState.busy) return;
        receiveFiles(picked, onFile, view);
      });
    }
    // Linux/非桌面/未知：不注册拖入接收与高亮，仅由全局防导航拒收。
    if (!FILE_DROP_ENABLED) return;
    ["dragenter", "dragover"].forEach(function (ev) {
      area.addEventListener(ev, function (e) { e.preventDefault(); if (e.dataTransfer) e.dataTransfer.dropEffect = "copy"; if (area.classList) area.classList.add("dragover"); });
    });
    ["dragleave", "dragend"].forEach(function (ev) {
      area.addEventListener(ev, function () { if (area.classList) area.classList.remove("dragover"); });
    });
    area.addEventListener("drop", function (e) {
      e.preventDefault();
      if (area.classList) area.classList.remove("dragover");
      if (input && (input.disabled || appState.busy)) return; // 不绕过禁用/忙保护
      // 事件内先快照真实 File 字节，直接进入原状态/提交链；不依赖 input.files setter。
      var files = (e.dataTransfer && e.dataTransfer.files) ? Array.from(e.dataTransfer.files) : [];
      receiveFiles(files, onFile, view);
    });
  }
  function currentErrorView() { return appState.activeView === "restore" ? "restore" : "mask"; }
  function preventNonTargetDrop() {
    if (typeof window.addEventListener !== "function") return;
    window.addEventListener("dragover", function (e) { e.preventDefault(); });
    window.addEventListener("drop", function (e) {
      var onTarget = isFileAreaTarget(e.target);
      if (FILE_DROP_ENABLED && onTarget) return; // Windows 目标区域由区域 drop 处理
      // 非目标区域、或平台不支持拖入：阻止默认导航且不改变任何 state/产物。
      e.preventDefault();
      if (!FILE_DROP_ENABLED && onTarget) showGlobalStatus(t("ui.click_to_choose"), 4000);
    });
  }

  // ---- 固定目录快捷选择（original/cards/pending/mapping）----
  var WORKSPACE_KINDS = ["original", "cards", "pending", "mapping"];
  var WORKSPACE_PLACEHOLDER = {
    original: "ui.workspace_choose_original",
    cards: "ui.workspace_choose_cards",
    pending: "ui.workspace_choose_pending",
    mapping: "ui.workspace_choose_mapping"
  };
  function workspaceListState(kind) { return appState.workspaceLists[kind]; }
  function workspaceSelect(kind) { return $("#" + kind + "-select"); }
  function workspaceStatusEl(kind) { return $("#" + kind + "-list-status"); }
  function workspaceRefreshEl(kind) { return $("#" + kind + "-refresh"); }
  function workspaceGroupEl(kind) { return $("#" + kind + "-picker"); }
  function workspaceBinding() {
    var app = bindingApp();
    return (app && app.ListWorkspaceFiles && app.ReadWorkspaceFile) ? app : null;
  }

  // 列表只重建名称选项；已选中的名称若不在新列表中则清空选择，但当前快照保持不变。
  function renderWorkspaceList(kind) {
    var st = workspaceListState(kind);
    var sel = workspaceSelect(kind);
    if (sel) {
      var current = sel.value || "";
      sel.innerHTML = "";
      var ph = document.createElement("option");
      ph.value = "";
      ph.textContent = t(WORKSPACE_PLACEHOLDER[kind]);
      sel.appendChild(ph);
      (st.files || []).forEach(function (name) {
        var o = document.createElement("option");
        o.value = name;
        o.textContent = name;
        sel.appendChild(o);
      });
      sel.value = (st.files || []).indexOf(current) >= 0 ? current : "";
    }
    var status = workspaceStatusEl(kind);
    if (!status) return;
    status.className = "picker-status";
    if (st.readError && st.readError.id) {
      status.textContent = t(st.readError.id, st.readError.params);
      status.className = "picker-status failed";
      return;
    }
    if (st.status === "loading") {
      status.textContent = t("ui.workspace_loading");
    } else if (st.status === "empty") {
      status.textContent = t("ui.workspace_empty");
    } else if (st.status === "failed" || st.status === "unavailable") {
      status.textContent = st.errorID
        ? t(st.errorID, st.errorParams)
        : t("input.list_failed", { dir: kind + "/" });
      status.className = "picker-status failed";
    } else if (st.status === "ok") {
      status.textContent = t("ui.workspace_count", { count: (st.files || []).length });
    } else {
      status.textContent = "";
    }
  }
  function renderAllWorkspaceLists() {
    WORKSPACE_KINDS.forEach(function (kind) { renderWorkspaceList(kind); });
  }

  // 桌面 binding 的列表/读取共享后端“一次一个”的原子协调；前端串行排队，
  // 避免启动时四个列表并发被后端忙态互相拒绝。
  var workspaceQueue = Promise.resolve();
  function queueWorkspaceTask(fn) {
    var next = workspaceQueue.then(fn, fn);
    workspaceQueue = next.then(function () {}, function () {});
    return next;
  }

  // 显式刷新只更新该列表，不重读当前文件、不清空输入/结果；各自递增序号防旧响应覆盖。
  function refreshWorkspaceList(kind) {
    var st = workspaceListState(kind);
    st.seq++;
    var seq = st.seq;
    st.readError = null;
    var app = workspaceBinding();
    if (!app) {
      st.files = [];
      st.status = "unavailable";
      st.errorID = "input.root_unavailable";
      st.errorParams = null;
      renderWorkspaceList(kind);
      return;
    }
    st.status = "loading";
    st.errorID = null;
    st.errorParams = null;
    renderWorkspaceList(kind);
    queueWorkspaceTask(function () {
      return Promise.resolve(app.ListWorkspaceFiles(kind)).then(function (r) {
        if (seq !== st.seq) return; // 旧列表响应：不得覆盖更新的列表
        if (r && (r.status === "ok" || r.status === "empty")) {
          st.files = (r.files || []).slice();
          st.status = st.files.length ? "ok" : "empty";
          st.errorID = null;
          st.errorParams = null;
        } else {
          st.files = [];
          st.status = (r && r.status === "unavailable") ? "unavailable" : "failed";
          st.errorID = (r && r.message_id) ? r.message_id : "input.list_failed";
          st.errorParams = (r && r.params) ? r.params : { dir: kind + "/" };
        }
        renderWorkspaceList(kind);
      }, function () {
        if (seq !== st.seq) return;
        st.files = [];
        st.status = "failed";
        st.errorID = "input.list_failed";
        st.errorParams = { dir: kind + "/" };
        renderWorkspaceList(kind);
      });
    });
  }

  function setWorkspaceError(kind, id, params) {
    var st = workspaceListState(kind);
    st.readError = id ? { id: id, params: params || null } : null;
    renderWorkspaceList(kind);
  }

  // 选择占位/取消（value 为空）不清空已有输入；只有明确选择才读取。
  function onWorkspaceSelect(kind) {
    var sel = workspaceSelect(kind);
    if (!sel || !sel.value) return;
    loadWorkspaceFile(kind, sel.value);
  }

  // 显式读取期间进入全局忙态；请求序号保证迟到响应不覆盖较新的选择。
  function loadWorkspaceFile(kind, name) {
    if (!name) return;
    if (appState.busy) { showGlobalStatus(t("input.busy"), 4000); return; }
    var app = workspaceBinding();
    if (!app) { setWorkspaceError(kind, "input.root_unavailable"); return; }
    var seq = ++appState.workspaceReadSeq[kind];
    var op = beginOperation();
    if (!op) return;
    var released = false;
    function finish() { if (!released) { released = true; endOperation(); } }
    queueWorkspaceTask(function () {
      return Promise.resolve(app.ReadWorkspaceFile(kind, name)).then(function (r) {
        if (seq !== appState.workspaceReadSeq[kind]) return null; // 过期响应：不覆盖新选择
        if (!r || r.status !== "ok" || !r.name) {
          setWorkspaceError(kind, (r && r.message_id) ? r.message_id : "input.read_failed", r && r.params);
          return null;
        }
        var bytes = b64ToBytes(r.data_b64 || "");
        return applyWorkspacePayload(kind, r.name, bytes);
      }, function () {
        if (seq === appState.workspaceReadSeq[kind]) setWorkspaceError(kind, "input.read_failed");
      });
    }).then(finish, finish);
  }

  // 字节快照交给既有四类输入处理器；不自动选择、不复制到固定目录、不猜配对。
  function applyWorkspacePayload(kind, name, bytes) {
    var file = new File([bytes], name);
    clearViewError(kind === "original" || kind === "cards" ? "mask" : "restore");
    setWorkspaceError(kind, null);
    if (kind === "original") { onMaskFile(file, "workspace"); return null; }
    if (kind === "pending") { onRestoreFile(file, "workspace"); return null; }
    if (kind === "cards") { return onImportWordFile(file, "workspace"); }
    if (kind === "mapping") {
      state.restore.mapping = {
        source: "workspace", name: name, displayName: name,
        file: file, bytes: bytes, metadata: detectMappingMeta(bytes)
      };
      renderMappingSource();
      return null;
    }
    return null;
  }

  function renderFileOrigins() {
    setText("#mask-file-origin", state.mask.fileOrigin === "workspace" ? t("ui.workspace_origin", { dir: "original" }) : "");
    setText("#restore-file-origin", state.restore.fileOrigin === "workspace" ? t("ui.workspace_origin", { dir: "pending" }) : "");
    setText("#word-file-origin", state.mask.wordOrigin === "workspace" ? t("ui.workspace_origin", { dir: "cards" }) : "");
  }

  function initWorkspacePickers() {
    var hasBinding = !!workspaceBinding();
    WORKSPACE_KINDS.forEach(function (kind) {
      var sel = workspaceSelect(kind);
      if (sel && sel.addEventListener) {
        sel.addEventListener("change", function () { onWorkspaceSelect(kind); });
      }
      var btn = workspaceRefreshEl(kind);
      if (btn && btn.addEventListener) {
        btn.addEventListener("click", function () {
          if (appState.busy) { showGlobalStatus(t("input.busy"), 4000); return; }
          refreshWorkspaceList(kind);
        });
      }
      var group = workspaceGroupEl(kind);
      // 非桌面/无 binding：隐藏快捷组，保留全部手动入口作为回退。
      if (group) {
        if (!IS_DESKTOP || !hasBinding) group.hidden = true;
        else if (kind === "pending") group.hidden = state.restore.inputMode !== "file";
      }
      if (!hasBinding) {
        var st = workspaceListState(kind);
        st.files = [];
        st.status = "unavailable";
        st.errorID = "input.root_unavailable";
        st.errorParams = null;
      }
      renderWorkspaceList(kind);
    });
    if (!IS_DESKTOP || !hasBinding) return;
    WORKSPACE_KINDS.forEach(function (kind) { refreshWorkspaceList(kind); });
  }

  // ---- 提交构造 ----
  function buildTextCustomValue() {
    var ta = $("#custom");
    var total = ta ? String(ta.value).split(/\r\n|\n/).length : 0;
    var out = new Array(total);
    for (var i = 0; i < total; i++) out[i] = "";
    var has = false;
    state.mask.words.forEach(function (e) {
      if (e.source !== "text" || !e.checked) return;
      if (e.line >= 1 && e.line <= total) { out[e.line - 1] = e.w; has = true; }
    });
    return has ? out.join("\n") : "";
  }
  function buildMaskFormData(confirmedWords) {
    var fd = new FormData();
    if (state.mask.file) fd.append("file", state.mask.file, state.mask.file.name || "input.txt");
    fd.append("profile", state.mask.profile);
    if (state.mask.profile === CUSTOM_TOKEN) {
      RULE_ORDER.forEach(function (id) { fd.append("rule_" + id, state.mask.customRules[id] ? "true" : "false"); });
    }
    if (effectiveCustomRule()) {
      var textValue = buildTextCustomValue();
      if (textValue !== "") fd.append("custom", textValue);
      var checked = state.mask.words.filter(function (e) { return e.checked; });
      var fileAll = state.mask.words.filter(function (e) { return e.source === "file"; });
      var fileChecked = checked.filter(function (e) { return e.source === "file"; });
      if (fileChecked.length > 0) {
        if (fileChecked.length === fileAll.length && state.mask.importFile) {
          fd.append("custom_file", state.mask.importFile, state.mask.importFile.name || "words.txt");
        } else {
          var body = fileChecked.map(function (e) { return e.w; }).join("\n") + "\n";
          fd.append("custom_file", new Blob([body], { type: "text/plain;charset=utf-8" }), "words-selected.txt");
        }
      }
    }
    (confirmedWords || []).forEach(function (w) { fd.append("confirmed", w); });
    return fd;
  }

  // ---- 多部分传输兼容（保留 serializeMultipart/postMultipart）----
  function serializeMultipart(fd) {
    return Promise.resolve().then(function () {
      var resp = new Response(fd);
      var contentType = resp.headers.get("Content-Type");
      return resp.arrayBuffer().then(function (buffer) { return { buffer: buffer, contentType: contentType }; });
    });
  }
  function postMultipart(path, fd) {
    return serializeMultipart(fd).then(function (payload) {
      var init = { method: "POST", body: payload.buffer };
      if (payload.contentType) init.headers = { "Content-Type": payload.contentType };
      return fetch(path, init);
    });
  }

  // ---- 操作串行与 busy ----
  function setBusy(v) { appState.busy = !!v; updateActionState(); }
  function updateActionState() {
    var b = appState.busy || anyArtifactBusy();
    var ms = $("#mask-submit"); if (ms) ms.disabled = b;
    var rs = $("#restore-submit"); if (rs) rs.disabled = b;
    var um = $("#use-session-mapping"); if (um) um.disabled = b || !hasSessionMapping();
    $$(".save-btn").forEach(function (btn) { btn.disabled = b; });
    // 刷新在处理/保存忙时禁用；词卡入口在 custom 关闭时禁用；选择本身在忙时不可用。
    WORKSPACE_KINDS.forEach(function (kind) {
      var btn = workspaceRefreshEl(kind);
      if (btn) btn.disabled = b || (kind === "cards" && !effectiveCustomRule());
      var sel = workspaceSelect(kind);
      if (sel) sel.disabled = b || (kind === "cards" && !effectiveCustomRule());
    });
  }
  function beginOperation() {
    if (appState.busy) { showGlobalStatus(t("ui.busy_execute"), 4000); return null; }
    var op = { id: ++appState.opSeq, view: appState.activeView };
    setBusy(true);
    return op;
  }
  function endOperation() { setBusy(false); }

  // ---- 脱敏流程（3.4/3.5/3.6/5.4）----
  function maskSubmit(confirmedWords) {
    clearViewError("mask");
    if (!state.mask.file) { showViewError("mask", t("http.mask_file_missing")); return; }
    var op = beginOperation();
    if (!op) return;

    if (effectiveCustomRule()) {
      var selected = state.mask.words.filter(function (e) { return e.checked; }).length;
      if (selected > MAX_WORD_COUNT) { showViewError("mask", t("ui.word_selected_too_many")); endOperation(); return; }
    }
    var fd = buildMaskFormData(confirmedWords);
    state.mask.status = "processing";
    updateMaskStatus();

    postMultipart("/api/mask", fd)
      .then(function (r) { return r.json().then(function (j) { return { status: r.status, body: j }; }); })
      .then(function (res) {
        if (res.status === 409 && res.body && res.body.error === "preview_required") {
          state.mask.preview = res.body.hits || {};
          state.mask.status = "confirmation-required";
          renderPreview();
          renderMaskResults(); // 不吞旧结果：恢复旧产物展示；无旧结果则清除"处理中"占位
          endOperation();
          scrollTo("#mask-preview");
          return;
        }
        if (!res.body || !res.body.ok) {
          state.mask.status = "failed";
          showViewError("mask", presentationMessage(res.body) || (res.body && res.body.message) || t("error.generic"));
          renderMaskResults(); // 失败保留旧结果：重绘旧产物；无旧结果则隐藏处理中占位
          endOperation();
          return;
        }
        // 成功：仅替换来源视图结果；保留旧结果直到此刻。
        var body = res.body;
        state.mask.lastBody = body;
        state.mask.artifacts = {
          masked: { id: "masked", view: "mask", label: t("ui.artifact_masked"), name: body.masked_name, b64: body.masked_b64, status: "unsaved", busy: false },
          mapping: { id: "mapping", view: "mask", label: t("ui.artifact_mapping"), name: body.mapping_name, b64: body.mapping_b64, status: "unsaved", busy: false }
        };
        noteGeneration(body.unsaved_generation);
        state.mask.status = "success";
        if (!appState.workspaceEnabled) refreshWorkspaceHint();
        renderMaskResults();
        updateUnsavedBinding();
        notifyIfHidden("mask");
        autoSaveMask(op, state.mask.artifacts.masked, state.mask.artifacts.mapping);
      })
      .catch(function (err) {
        state.mask.status = "failed";
        showViewError("mask", t("ui.request_failed", { detail: String(err) }));
        renderMaskResults(); // 传输失败同样保留旧结果/隐藏占位
        endOperation();
      });
  }
  function updateMaskStatus() {
    var el = $("#mask-result");
    if (!el) return;
    if (state.mask.status === "processing") { el.hidden = false; el.innerHTML = '<p class="hint">' + esc(t("ui.result_processing")) + "</p>"; }
  }
  function updateRestoreStatus() {
    var el = $("#restore-result");
    if (!el) return;
    if (state.restore.status === "processing") { el.hidden = false; el.innerHTML = '<p class="hint">' + esc(t("ui.result_processing")) + "</p>"; }
  }
  function notifyIfHidden(view) {
    if (appState.activeView !== view) {
      showGlobalStatus(t("ui.result_done_other_view"), 8000, t("ui.view_" + view), function () { setView(view); });
    } else {
      showGlobalStatus(t("ui.narrow_result_hint"), 4000);
    }
  }
  function scrollTo(sel) {
    var el = $(sel);
    if (el && el.scrollIntoView) { try { el.scrollIntoView({ behavior: "smooth", block: "center" }); } catch (e) { el.scrollIntoView(); } }
  }
  function presentationMessage(body) {
    if (body && body.presentation && body.presentation.message_id) {
      return t(body.presentation.message_id, body.presentation.params);
    }
    return "";
  }

  // 高频确认：本次词可取消、常备词不可取消；主按钮“确认并继续”。
  function renderPreview() {
    var panel = $("#mask-preview");
    if (!panel) return;
    if (!state.mask.preview) { panel.hidden = true; panel.innerHTML = ""; return; }
    var hits = state.mask.preview;
    var extra = hits.extra || {}, builtin = hits.builtin || {};
    var html = "<h3>" + th("ui.preview_title") + "</h3><p>" + th("ui.preview_desc") + "</p>";
    html += '<div class="preview-group"><strong>' + th("ui.preview_extra_group") + '</strong><ul class="preview-list">';
    var ek = Object.keys(extra);
    if (ek.length) {
      ek.forEach(function (w) {
        var e = state.mask.words.filter(function (x) { return x.w === w; })[0];
        var checked = e ? e.checked : true;
        html += '<li><label><input type="checkbox" class="pv-extra" data-word="' + esc(w) + '"' + (checked ? " checked" : "") + "> " + esc(w) + " × " + extra[w] + "</label></li>";
      });
    } else html += '<li class="hint">' + th("preview.empty_group") + "</li>";
    html += "</ul></div>";
    html += '<div class="preview-group"><strong>' + th("ui.preview_builtin_group") + '</strong><ul class="preview-list">';
    var bk = Object.keys(builtin);
    if (bk.length) bk.forEach(function (w) { html += "<li>" + esc(w) + " × " + builtin[w] + "</li>"; });
    else html += '<li class="hint">' + th("preview.empty_group") + "</li>";
    html += '</ul><p class="hint">' + th("ui.preview_builtin_hint") + "</p></div>";
    html += '<div class="preview-actions"><button type="button" id="pv-continue" class="primary">' + th("ui.preview_continue") + '</button>' +
      '<button type="button" id="pv-cancel" class="chip">' + th("ui.preview_cancel") + "</button></div>";
    panel.innerHTML = html;
    panel.hidden = false;
    $$("#mask-preview .pv-extra").forEach(function (cb) {
      cb.addEventListener("change", function () {
        var w = cb.getAttribute("data-word");
        state.mask.words.forEach(function (e) { if (e.w === w) e.checked = cb.checked; });
        renderWords();
      });
    });
    var cont = $("#pv-continue"); if (cont) cont.addEventListener("click", function () {
      var extraW = Object.keys(extra).filter(function (w) {
        var e = state.mask.words.filter(function (x) { return x.w === w; })[0];
        return !e || e.checked;
      });
      state.mask.preview = null;
      renderPreview();
      maskSubmit(Object.keys(builtin).concat(extraW));
    });
    var cancel = $("#pv-cancel"); if (cancel) cancel.addEventListener("click", function () {
      state.mask.preview = null;
      state.mask.status = "idle";
      renderPreview();
    });
  }

  // ---- 自动落盘与手动保存（3.6/6.4）----
  function applyWorkspaceSave(art, res) {
    if (!art || !res) return;
    if (res.status === "saved") { art.status = "saved"; art.autoPath = res.path || ""; appState.workspaceEnabled = true; }
    else if (res.status === "failed" || res.status === "busy") { art.status = "failed"; art.autoError = res.message || ""; art.autoErrorID = res.message_id || ""; appState.workspaceEnabled = true; }
  }
  function autoSaveMask(op, masked, mapping) {
    var app = bindingApp();
    if (!app || !app.AutoSaveMask) { endOperation(); return; }
    Promise.resolve(app.AutoSaveMask(masked.name, masked.b64, mapping.name, mapping.b64)).then(function (r) {
      if (r) { applyWorkspaceSave(masked, r.masked); applyWorkspaceSave(mapping, r.mapping); }
      renderMaskResults();
      updateUnsavedBinding();
      endOperation();
    }).catch(function () { updateUnsavedBinding(); endOperation(); });
  }
  function autoSaveRestored(op, art) {
    var app = bindingApp();
    if (!app || !app.AutoSaveRestored) { endOperation(); return; }
    Promise.resolve(app.AutoSaveRestored(art.name, art.b64)).then(function (r) {
      if (r) applyWorkspaceSave(art, r);
      renderRestoreResults();
      updateUnsavedBinding();
      endOperation();
    }).catch(function () { updateUnsavedBinding(); endOperation(); });
  }
  function refreshWorkspaceHint() {
    // 工作区不可用/降级时不自动落盘；此处不报错，仅保持未保存，仍可手动另存。
  }
  function nativeSave(art) {
    var app = bindingApp();
    if (!app || !app.SaveFile) { showViewError(art.view, t("ui.request_failed", { detail: "native save unavailable" })); return; }
    if (art.busy || appState.busy) return;
    clearViewError(art.view);
    art.busy = true;
    renderResults(art.view);
    updateActionState();
    Promise.resolve(app.SaveFile(art.name, art.b64)).then(function (r) {
      art.busy = false;
      if (!r) {
        art.status = "failed"; art.autoErrorID = "error.generic";
        art.autoError = ""; art.autoErrorParams = null;
        art.saveWarningID = null; art.saveWarningParams = null;
      } else if (r.status === "saved") {
        art.status = "saved"; art.autoPath = r.destination || art.autoPath;
        // 成功清除旧失败，避免重试/普通成功仍误显旧 error；告警按本次结果更新。
        art.autoErrorID = ""; art.autoError = ""; art.autoErrorParams = null;
        if (r.cleanup_warning) {
          art.saveWarningID = r.cleanup_warning_id || "save.cleanup_warning";
          art.saveWarningParams = r.params || null;
        } else {
          art.saveWarningID = null; art.saveWarningParams = null;
        }
      } else if (r.status === "cancelled") { /* 保持原状态与已有告警（取消不改变已保存 warning） */ }
      else {
        art.status = "failed"; art.autoErrorID = r.message_id || "error.generic"; art.autoErrorParams = r.params;
        art.saveWarningID = null; art.saveWarningParams = null;
      }
      renderResults(art.view);
      updateActionState();
      updateUnsavedBinding();
    }).catch(function () {
      art.busy = false; art.status = "failed"; art.autoErrorID = "error.generic";
      art.saveWarningID = null; art.saveWarningParams = null;
      renderResults(art.view);
      updateActionState();
      updateUnsavedBinding();
    });
  }
  function statusText(art) {
    if (art.busy) return t("ui.status_saving");
    if (art.status === "saved") return t("ui.status_saved");
    if (art.status === "failed") return t("ui.status_failed");
    return t("ui.status_unsaved");
  }
  // 11.1/决策10：产物类型按 art.id 与当前语言取稳定键，不用创建时缓存的 art.label。
  var ARTIFACT_TYPE_KEYS = { masked: "ui.artifact_masked", mapping: "ui.artifact_mapping", restored: "ui.artifact_restored" };
  function artifactTypeLabel(id) {
    var key = ARTIFACT_TYPE_KEYS[id] || ("ui.artifact_" + id);
    return t(key);
  }
  // 无背景结果行：保存/另存按钮最左，名称/状态/路径/备注在独立说明容器。
  // 已保存有实际路径（autoPath=最新成功 WorkspaceSave.path 或 SaveResult.destination）时仅一条成功句，
  // 不显建议名/独立已保存状态/旧自动保存行；busy/failed/未保存/缺路径保留必要反馈与入口。
  function artifactCard(art, opts) {
    opts = opts || {};
    var typeLabel = artifactTypeLabel(art.id);
    var statusCls = art.status === "saved" ? "saved" : (art.status === "failed" ? "failed" : (art.busy ? "busy" : ""));
    var label = art.status === "saved" ? t("ui.save_as") : t("ui.save");
    var html = '<div class="result-row">' +
      '<button type="button" class="save-btn ' + (art.status === "saved" ? "saved" : "") + '" data-art="' + esc(art.id) + '" data-view="' + esc(art.view) + '"' +
      ((appState.busy || art.busy) ? " disabled" : "") + ">" + esc(label) + "</button>" +
      '<div class="result-meta">';
    // 保存中且无上一成功路径：保留类型/建议名，不得把未完成目标称为已保存。
    if (art.busy && !(art.status === "saved" && art.autoPath)) {
      html += '<span class="artifact-name">' + esc(typeLabel) + "：" + esc(art.name) + "</span>";
    }
    if (art.status === "saved" && art.autoPath) {
      // 唯一成功句：路径原样安全转义，自动/手动一致，不写“自动”。
      html += '<span class="artifact-path">' + esc(t("ui.saved_to", { artifact: typeLabel, path: art.autoPath })) + "</span>";
    } else if (!art.busy) {
      // 未保存/失败/已保存缺路径：保留当前语言类型+建议名及必要状态。
      html += '<span class="artifact-name">' + esc(typeLabel) + "：" + esc(art.name) + "</span>";
    }
    // C1：已保存且有实际路径且非本次保存中时，不渲染独立“已保存”状态 chip，只留单一成功句。
    // busy 仍显示“保存中…”，failed/unsaved/已保存缺路径保留各自状态。
    if (!(art.status === "saved" && art.autoPath && !art.busy)) {
      html += '<span class="artifact-status ' + statusCls + '">' + esc(statusText(art)) + "</span>";
    }
    if (art.status === "failed") {
      var note = art.autoErrorID ? t(art.autoErrorID, art.autoErrorParams) : (art.autoError ? t("ui.auto_save_failed", { message: art.autoError }) : t("error.generic"));
      html += '<span class="artifact-note">' + esc(note) + "</span>";
    }
    // W1：成功保存附带的 cleanup_warning 用独立状态，不与陈旧失败 error 混淆，按当前语言就近显示，
    // 且不遮挡单一成功句。
    if (art.saveWarningID) {
      html += '<span class="artifact-note">' + esc(t(art.saveWarningID, art.saveWarningParams)) + "</span>";
    }
    if (opts.noteID) html += '<span class="result-note">' + esc(t(opts.noteID, opts.noteParams)) + "</span>";
    html += "</div></div>";
    return html;
  }
  function warningLocation(w) {
    if (w.line) return "L" + w.line + (w.column ? ":" + w.column : "");
    if (w.part) return w.part + (w.paragraph != null ? " P" + w.paragraph : "") + (w.segment != null ? " S" + w.segment : "") + (w.column ? " C" + w.column : "");
    return "";
  }
  function warningsHTML(list) {
    if (!list || !list.length) return "";
    var items = list.map(function (w) {
      var known = w.message_id && isKnownID(w.message_id);
      var msg = known ? t(w.message_id, w.params) : (w.message_id ? t("error.unknown_id", { id: w.message_id }) : (w.message || t("warning.generic")));
      var loc = warningLocation(w);
      if (!known && loc) msg = msg + " " + t("ui.location_text", { loc: loc });
      return "<li>" + esc(msg) + "</li>";
    }).join("");
    // 无“警告（不阻断）”统一标题/大框：仅本次实际反馈的就近简短列表。
    return '<ul class="feedback-list">' + items + "</ul>";
  }
  function cleanupHTML(c) {
    if (!c) return "";
    var parts = [
      t("ui.cleanup_comments", { count: c.comments_deleted || 0 }),
      t("ui.cleanup_revisions", { count: c.revisions_accepted || 0 })
    ];
    if (c.parts_removed) parts.push(t("ui.cleanup_parts", { count: c.parts_removed }));
    if (c.resources_removed) parts.push(t("ui.cleanup_resources", { count: c.resources_removed }));
    return '<p class="feedback-line detail">' + esc(t("ui.cleanup_title") + parts.join("；") + "。") + "</p>";
  }
  function renderMaskResults() {
    var result = $("#mask-result");
    var b = state.mask.lastBody;
    if (!result) return;
    if (!b) {
      // 无旧结果：仅处理中保留占位；其余（失败/409/空闲）清除并隐藏，绝不残留"处理中"。
      if (state.mask.status === "processing") return;
      result.hidden = true;
      result.innerHTML = "";
      return;
    }
    result.hidden = false;
    var masked = state.mask.artifacts.masked, mapping = state.mask.artifacts.mapping;
    var html = "<h2>" + th("ui.result_mask_title") + "</h2>";
    html += '<p class="hint">' + (b.format === "docx" ? th("ui.input_docx") : th("ui.detected_encoding", { encoding: b.encoding || "UTF-8" })) + "</p>";
    // mapping 短提醒仅出现在脱敏成功映射行一次（重绘整体重建，不累积）。
    html += artifactCard(masked) + artifactCard(mapping, { noteID: "ui.mapping_reminder_short" });
    if (masked && mapping) {
      if (masked.status === "saved" && mapping.status !== "saved") html += '<p class="feedback-line">' + th("ui.partial_masked_saved") + "</p>";
      if (mapping.status === "saved" && masked.status !== "saved") html += '<p class="feedback-line">' + th("ui.partial_mapping_saved") + "</p>";
    }
    if (b.format === "docx") {
      html += cleanupHTML(b.cleanup);
      html += '<p class="feedback-line detail">' + th("ui.docx_unchecked_short") + "</p>";
    } else {
      html += cleanupHTML(b.cleanup);
    }
    if (b.audit_failed) html += '<p class="feedback-line">' + th("feedback.audit_failed") + "</p>";
    html += warningsHTML(b.warnings);
    result.innerHTML = html;
    bindSaveButtons();
  }
  function renderRestoreResults() {
    var result = $("#restore-result");
    var b = state.restore.lastBody;
    if (!result) return;
    if (!b) {
      if (state.restore.status === "processing") return;
      result.hidden = true;
      result.innerHTML = "";
      return;
    }
    result.hidden = false;
    var restored = state.restore.artifacts.restored;
    var fmt = b.format === "docx" ? th("ui.output_docx") : th("ui.output_text", { encoding: b.encoding || "UTF-8" });
    var html = "<h2>" + th("ui.result_restore_title") + "</h2>" +
      '<p class="hint">' + esc(t("ui.restored_count", { count: b.restored_count || 0 })) + "；" + fmt + "</p>" +
      artifactCard(restored) + cleanupHTML(b.cleanup) + warningsHTML(b.warnings);
    if (b.audit_failed) html += '<p class="feedback-line">' + th("feedback.audit_failed") + "</p>";
    result.innerHTML = html;
    bindSaveButtons();
  }
  function renderResults(view) { if (view === "restore") renderRestoreResults(); else renderMaskResults(); }
  function bindSaveButtons() {
    $$(".save-btn").forEach(function (btn) {
      if (btn.getAttribute("data-bound") === "1") return;
      btn.setAttribute("data-bound", "1");
      btn.addEventListener("click", function () {
        var view = btn.getAttribute("data-view") === "restore" ? "restore" : "mask";
        var art = state[view].artifacts[btn.getAttribute("data-art")];
        if (art) nativeSave(art);
      });
    });
  }

  // ---- 还原流程（6.1/6.2/6.3）----
  function hasSessionMapping() {
    return !!(state.mask.artifacts && state.mask.artifacts.mapping && state.mask.artifacts.mapping.b64);
  }
  function setRestoreMode(mode) {
    state.restore.inputMode = mode;
    var fb = $("#restore-mode-file"), tb = $("#restore-mode-text");
    if (fb && fb.classList) fb.classList.toggle("active", mode === "file");
    if (tb && tb.classList) tb.classList.toggle("active", mode === "text");
    setHidden("#restore-file-area", mode !== "file");
    // 文档格式说明只属于文件模式；粘贴模式不重复文档选择说明。
    setHidden("#restore-format-note", mode !== "file");
    // pending/ 快捷选择属于文件模式；粘贴模式或缺少 binding 时不展示隐藏文件。
    setHidden("#pending-picker", !IS_DESKTOP || !workspaceBinding() || mode !== "file");
    setHidden("#restore-text-label", mode !== "text");
    // 隐藏侧内容保留，不交换状态；仅展示切换。还原视图无常驻范围块。
  }
  function initRestore() {
    var form = $("#restore-form");
    if (!form) return;
    var fm = $("#restore-mode-file"), tm = $("#restore-mode-text");
    if (fm) fm.addEventListener("click", function () { setRestoreMode("file"); });
    if (tm) tm.addEventListener("click", function () { setRestoreMode("text"); });
    setRestoreMode(state.restore.inputMode);
    var rf = $("#restore-file");
    if (rf) {
      wireFileArea("#restore-file-area", rf, onRestoreFile, "restore");
    }
    var mf = $("#restore-mapping");
    if (mf) {
      wireFileArea("#restore-mapping-area", mf, onMappingFile, "restore");
    }
    var useSess = $("#use-session-mapping");
    if (useSess) useSess.addEventListener("click", useSessionMapping);
    form.addEventListener("submit", function (e) { e.preventDefault(); runRestore(); });
    renderMappingSource();
  }
  function onRestoreFile(file, origin) {
    state.restore.file = file;
    state.restore.fileName = file.name;
    state.restore.fileOrigin = origin || "manual";
    setText("#restore-file-name", file.name);
    renderFileOrigins();
  }
  function onMappingFile(file) {
    var fr = new FileReader();
    fr.onload = function () {
      var bytes = new Uint8Array(fr.result);
      state.restore.mapping = { source: "manual", name: file.name, displayName: file.name, file: file, bytes: bytes, metadata: detectMappingMeta(bytes) };
      renderMappingSource();
    };
    fr.onerror = function () { showViewError("restore", t("error.generic")); };
    fr.readAsArrayBuffer(file);
  }
  // 12.1/决策11：无 I/O 纯词法文件名提取，只处理后端完整文件路径。
  // Windows 盘符绝对（盘符后紧跟 / 或 \）与 \\ 开头的 UNC/扩展路径按两种分隔取末段；
  // Linux 以 / 开头的绝对路径仅按 / 取末段，保留文件名内合法反斜杠。
  // 非字符串/空/相对/尾分隔/末段空或 . / .. 均返回 null，由调用方回退建议名。
  // 不用 UA/拖拽能力判断平台，不读盘、不猜编号、不从建议名重建路径。
  function extractDisplayName(path) {
    if (typeof path !== "string" || path === "") return null;
    var winDrive = /^[A-Za-z]:[\\/]/.test(path);
    var uncLike = path.slice(0, 2) === "\\\\";
    var separators;
    if (winDrive || uncLike) separators = /[\\/]/;
    else if (path.charAt(0) === "/") separators = /\//;
    else return null;
    var parts = path.split(separators);
    var last = parts[parts.length - 1];
    if (last === "" || last === "." || last === "..") return null;
    return last;
  }
  function useSessionMapping() {
    if (appState.busy || !hasSessionMapping()) return;
    var m = state.mask.artifacts.mapping;
    // 解析真实会话 mapping 快照元数据，供边界提示；core 仍做最终校验。
    var metadata = null;
    try { metadata = detectMappingMeta(b64ToBytes(m.b64)); } catch (e) { metadata = null; }
    // 显示名与上传名独立：displayName 取最近成功 autoPath 的末文件名，失败回退建议名 name。
    // name/b64 为独立拷贝，后续新脱敏/保存回调不得改写已带入快照；只有再次点击才替换。
    var displayName = extractDisplayName(m.autoPath) || m.name;
    state.restore.mapping = { source: "session", name: m.name, displayName: displayName, b64: m.b64, metadata: metadata };
    renderMappingSource();
    clearViewError("restore");
  }
  function detectMappingMeta(bytes) {
    try {
      var text = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
      var obj = JSON.parse(text);
      return { version: obj.version == null ? 1 : obj.version, sourceFormat: obj.source_format || "" };
    } catch (e) { return null; }
  }
  function renderMappingSource() {
    var el = $("#mapping-source");
    var nameEl = $("#restore-mapping-name");
    var m = state.restore.mapping;
    if (!el) return;
    if (!m) {
      el.textContent = "";
      if (nameEl) nameEl.textContent = "";
      return;
    }
    if (m.source === "session") el.textContent = t("ui.mapping_source_session", { name: m.displayName || m.name });
    else if (m.source === "workspace") el.textContent = t("ui.mapping_source_workspace", { name: m.displayName || m.name });
    else el.textContent = t("ui.mapping_source_manual", { name: m.displayName || m.name });
    // 选中名称独立第二行，长文字换行不挤动作行；两节点统一用 displayName||name。
    if (nameEl) nameEl.textContent = m.displayName || m.name || "";
    var um = $("#use-session-mapping");
    if (um) um.disabled = appState.busy || !hasSessionMapping();
  }
  function buildRestoreFormData() {
    var fd = new FormData();
    if (state.restore.inputMode === "file") {
      if (state.restore.file) fd.append("file", state.restore.file, state.restore.file.name || "input.txt");
    } else {
      var ta = $("#restore-text");
      if (ta) fd.append("text", ta.value);
    }
    var m = state.restore.mapping;
    if (m) {
      if ((m.source === "manual" || m.source === "workspace") && m.file) fd.append("mapping", m.file, m.name || "mapping.json");
      else if (m.b64) fd.append("mapping", new Blob([b64ToBytes(m.b64)], { type: "application/json" }), m.name || "mapping.json");
      else if (m.bytes) fd.append("mapping", new Blob([m.bytes], { type: "application/json" }), m.name || "mapping.json");
    }
    return fd;
  }
  function runRestore() {
    clearViewError("restore");
    var m = state.restore.mapping;
    if (!m) { showViewError("restore", t("ui.mapping_required")); return; }
    if (state.restore.inputMode === "file" && !state.restore.file) { showViewError("restore", t("http.restore_input_missing")); return; }
    if (state.restore.inputMode === "text") {
      var ta = $("#restore-text");
      if (!ta || String(ta.value) === "") { showViewError("restore", t("http.restore_input_missing")); return; }
    }
    // 手动/目录来源都提示就近来源；会话带入不重复确认。
    if (!confirmReplace("restore")) return;
    if ((m.source === "manual" || m.source === "workspace") && typeof window.confirm === "function") {
      if (!window.confirm(t("ui.mapping_confirm_manual"))) return;
    }
    var op = beginOperation();
    if (!op) return;
    var fd = buildRestoreFormData();
    state.restore.status = "processing";
    updateRestoreStatus();
    postMultipart("/api/restore", fd)
      .then(function (r) { return r.json().then(function (j) { return { status: r.status, body: j }; }); })
      .then(function (res) {
        if (!res.body || !res.body.ok) {
          state.restore.status = "failed";
          showViewError("restore", presentationMessage(res.body) || (res.body && res.body.message) || t("error.generic"));
          renderRestoreResults(); // 失败保留旧结果：重绘旧产物；无旧结果则隐藏处理中占位
          endOperation();
          return;
        }
        var body = res.body;
        state.restore.lastBody = body;
        state.restore.artifacts = {
          restored: { id: "restored", view: "restore", label: t("ui.artifact_restored"), name: body.name, b64: body.restored_b64, status: "unsaved", busy: false }
        };
        noteGeneration(body.unsaved_generation);
        state.restore.status = "success";
        renderRestoreResults();
        updateUnsavedBinding();
        notifyIfHidden("restore");
        autoSaveRestored(op, state.restore.artifacts.restored);
      })
      .catch(function (err) {
        state.restore.status = "failed";
        showViewError("restore", t("ui.request_failed", { detail: String(err) }));
        renderRestoreResults();
        endOperation();
      });
  }

  // ---- 退出（3.3/3.5）----
  function confirmExit(useWorkspaceExit) {
    if (!anyUnsaved()) return true;
    var msg = (useWorkspaceExit && appState.workspaceEnabled) ? t("native.confirm_close_workspace") : t("native.confirm_close_legacy");
    return window.confirm(msg);
  }
  // 兼容测试钩子：仅退出走关闭文案；重执行改用 confirmReplace。
  function confirmDiscard(_action, useWorkspaceExit) { return confirmExit(useWorkspaceExit); }
  // 重新执行仅确认替换“本视图”未保存结果，使用独立的重执行语义文案（不复用关闭文案）。
  function confirmReplace(view) {
    var arts = state[view].artifacts;
    var unsaved = Object.keys(arts).some(function (k) { return arts[k].status !== "saved"; });
    if (!unsaved) return true;
    return window.confirm(t("ui.confirm_reexecute"));
  }
  function requestExit() {
    if (appState.busy || anyArtifactBusy()) { showGlobalStatus(t("ui.exit_busy"), 5000); return; }
    if (!confirmExit(true)) return;
    fetch("/api/exit", { method: "POST" }).then(function (r) {
      return r.json().then(function (b) { return { status: r.status, body: b }; });
    }).then(function (res) {
      if (!res.body || res.body.ok === false) {
        showGlobalStatus(presentationMessage(res.body) || t("exit.blocked"), 6000);
        return;
      }
      var app = bindingApp();
      if (IS_DESKTOP && app && app.SetExitConfirmed) { try { app.SetExitConfirmed(); } catch (e) {} }
      state.mask.artifacts = {};
      state.restore.artifacts = {};
      if (document.body) document.body.innerHTML = "<main><h1>" + esc(t("ui.exited_title")) + "</h1><p>" + esc(t("native.desktop_hint")) + "</p></main>";
    }).catch(function (err) { showGlobalStatus(t("ui.request_failed", { detail: String(err) }), 6000); });
  }
  function initExit() {
    var btn = $("#exit-btn");
    if (btn) btn.addEventListener("click", requestExit);
  }

  function initAddress() {
    var addr = $("#addr");
    if (!addr) return;
    if (IS_DESKTOP) { addr.hidden = true; addr.textContent = ""; return; }
    addr.textContent = "本服务仅监听本机回环地址：" + (cfg.url || location.origin);
  }

  // ---- 脱敏文件选择/入口（5.1/8.2）----
  function onMaskFile(file, origin) {
    state.mask.file = file;
    state.mask.fileOrigin = origin || "manual";
    setText("#mask-file-name", file.name);
    renderFileOrigins();
    clearViewError("mask");
  }
  function initMask() {
    initProfiles();
    initWords();
    var form = $("#mask-form");
    var mf = $("#mask-file");
    if (mf) {
      wireFileArea("#mask-file-area", mf, onMaskFile, "mask");
    }
    if (form) form.addEventListener("submit", function (e) {
      e.preventDefault();
      clearViewError("mask");
      if (!state.mask.file) { showViewError("mask", t("http.mask_file_missing")); return; }
      if (!confirmReplace("mask")) return;
      state.mask.preview = null;
      renderPreview();
      maskSubmit([]);
    });
    renderProfile();
    renderWords();
  }

  document.addEventListener("DOMContentLoaded", function () {
    applyTheme();
    initTheme();
    initPrefs();
    initViewSwitch();
    setView(appState.activeView);
    initAddress();
    initExit();
    initMask();
    initRestore();
    initWorkspacePickers();
    preventNonTargetDrop();
    applyI18n();
    updateUnsavedBinding();
  });

  // 测试钩子：仅当宿主预置 window.__DM_TEST_HOOKS__（Node vm 回归用）时导出真实函数。
  if (window.__DM_TEST_HOOKS__ && typeof window.__DM_TEST_HOOKS__ === "object") {
    window.__DM_TEST_HOOKS__.serializeMultipart = serializeMultipart;
    window.__DM_TEST_HOOKS__.postMultipart = postMultipart;
    window.__DM_TEST_HOOKS__.maskSubmit = maskSubmit;
    window.__DM_TEST_HOOKS__.runRestore = runRestore;
    window.__DM_TEST_HOOKS__.setRestoreMode = setRestoreMode;
    window.__DM_TEST_HOOKS__.useSessionMapping = useSessionMapping;
    window.__DM_TEST_HOOKS__.extractDisplayName = extractDisplayName;
    window.__DM_TEST_HOOKS__.onMappingFile = onMappingFile;
    window.__DM_TEST_HOOKS__.confirmDiscard = confirmDiscard;
    window.__DM_TEST_HOOKS__.setView = setView;
    window.__DM_TEST_HOOKS__.onMaskFile = onMaskFile;
    window.__DM_TEST_HOOKS__.setPreferences = setPreferences;
    window.__DM_TEST_HOOKS__.applyI18n = applyI18n;
    window.__DM_TEST_HOOKS__.t = t;
    window.__DM_TEST_HOOKS__.renderWords = renderWords;
    window.__DM_TEST_HOOKS__.selectMatched = setMatchedChecked;
    window.__DM_TEST_HOOKS__.applyImportedWords = applyImportedWords;
    window.__DM_TEST_HOOKS__.nativeSave = nativeSave;
    window.__DM_TEST_HOOKS__.autoSaveMask = autoSaveMask;
    window.__DM_TEST_HOOKS__.autoSaveRestored = autoSaveRestored;
    window.__DM_TEST_HOOKS__.receiveFiles = receiveFiles;
    window.__DM_TEST_HOOKS__.isFileAreaTarget = isFileAreaTarget;
    window.__DM_TEST_HOOKS__.confirmReplace = confirmReplace;
    window.__DM_TEST_HOOKS__.artifactCard = artifactCard;
    window.__DM_TEST_HOOKS__.updateRestoreStatus = updateRestoreStatus;
    window.__DM_TEST_HOOKS__.buildMaskFormData = buildMaskFormData;
    window.__DM_TEST_HOOKS__.buildRestoreFormData = buildRestoreFormData;
    window.__DM_TEST_HOOKS__.fileDropEnabled = function () { return FILE_DROP_ENABLED; };
    window.__DM_TEST_HOOKS__.fileHintKey = function () { return currentFileHintKey(); };
    window.__DM_TEST_HOOKS__.fileHint = function () { return t(currentFileHintKey()); };
    window.__DM_TEST_HOOKS__.renderMaskResults = renderMaskResults;
    window.__DM_TEST_HOOKS__.renderRestoreResults = renderRestoreResults;
    window.__DM_TEST_HOOKS__.warningsHTML = warningsHTML;
    window.__DM_TEST_HOOKS__.refreshWorkspaceList = refreshWorkspaceList;
    window.__DM_TEST_HOOKS__.onWorkspaceSelect = onWorkspaceSelect;
    window.__DM_TEST_HOOKS__.loadWorkspaceFile = loadWorkspaceFile;
    window.__DM_TEST_HOOKS__.initWorkspacePickers = initWorkspacePickers;
    window.__DM_TEST_HOOKS__.initRestore = initRestore;
    window.__DM_TEST_HOOKS__.initMask = initMask;
    window.__DM_TEST_HOOKS__.requestExit = requestExit;
    window.__DM_TEST_HOOKS__.renderWorkspaceList = renderWorkspaceList;
    window.__DM_TEST_HOOKS__.workspaceListState = workspaceListState;
    window.__DM_TEST_HOOKS__.applyWorkspacePayload = applyWorkspacePayload;
    window.__DM_TEST_HOOKS__.state = state;
    window.__DM_TEST_HOOKS__.appState = appState;
  }
})();
