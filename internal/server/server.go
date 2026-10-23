// Package server 提供业务 handler 与仅绑定 127.0.0.1 的本地网页服务。
//
// 两套路径共用同一 handler 与三个 JSON API：
//   - 桌面宿主（发布形态）：通过 Handler() 进程内复用页面与 API，不调用 Start()；
//     页面路由经 internal/desktop 的 DesktopHandler 适配（/restore/ 规范路径、未知 index 路径 400）。
//   - 遗留 browser 生命周期（仅回归测试保留）：Start()/OpenBrowser()/idleLoop()/instance.lock
//     等仅供既有测试覆盖，无发布入口，不得由参数或错误路径回退到浏览器。
package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pandatoufu/data-masking/internal/audit"
	"github.com/pandatoufu/data-masking/internal/config"
	"github.com/pandatoufu/data-masking/internal/core"
	"github.com/pandatoufu/data-masking/internal/detector"
	"github.com/pandatoufu/data-masking/internal/i18n"
	"github.com/pandatoufu/data-masking/internal/loader"
	"github.com/pandatoufu/data-masking/internal/uiprefs"
	"github.com/pandatoufu/data-masking/web"
)

// Server 是本地网页服务。
type Server struct {
	cfg     *config.Config
	audit   *audit.Log
	handler http.Handler
	srv     *http.Server
	ln      net.Listener
	url     string

	openBrowser func(string) error
	fallback    io.Writer
	idle        time.Duration
	lockPath    string

	desktopMode bool
	exitGuard   func() error
	// opGuard 提供处理/保存共享的原子操作保留；begin 返回 false 表示忙。
	opGuardBegin func() bool
	opGuardEnd   func()
	// theme/language 是启动期 UI 偏好，随页面注入供首帧应用。
	theme    string
	language string
	// onArtifacts 在一次成功的脱敏/还原产出产物后调用（在返回响应前），
	// 供桌面宿主权威地把“存在未保存结果”置为 true，并返回本次原子的未保存 generation，
	// 随业务成功响应下发前端携带，避免前端二次 RPC 读取未来 token 或仅靠 fire-and-forget。
	onArtifacts func() uint64

	lastActivity atomic.Int64
	done         chan struct{}
	once         sync.Once
}

// Option 配置 Server。
type Option func(*Server)

// WithOpenBrowser 注入打开浏览器函数（测试用）。
func WithOpenBrowser(fn func(string) error) Option { return func(s *Server) { s.openBrowser = fn } }

// WithIdleTimeout 设置空闲关停时长。
func WithIdleTimeout(d time.Duration) Option { return func(s *Server) { s.idle = d } }

// WithLockPath 设置双开锁文件路径（测试用）。
func WithLockPath(p string) Option { return func(s *Server) { s.lockPath = p } }

// WithFallbackWriter 设置浏览器打开失败时的兜底输出目标。
func WithFallbackWriter(w io.Writer) Option { return func(s *Server) { s.fallback = w } }

// WithDesktopMode 标记桌面宿主：页面注入展示标志并在桌面隐藏回环地址，
// 不改变三个业务 API 的语义，也不影响 Start/idle（桌面入口不会调用 Start）。
func WithDesktopMode() Option { return func(s *Server) { s.desktopMode = true } }

// WithExitGuard 注入桌面退出前置守卫。守卫返回错误时退出请求被拒绝且不触发关停，
// 用于在保存进行中阻止所有关停入口。nil 守卫等同于允许退出。
func WithExitGuard(fn func() error) Option { return func(s *Server) { s.exitGuard = fn } }

// WithArtifactsCallback 注入“本次请求产出了待保存产物”的后端权威回调；
// 返回本次原子的未保存 generation，用于随成功响应下发前端。
func WithArtifactsCallback(fn func() uint64) Option { return func(s *Server) { s.onArtifacts = fn } }

// WithOperationGuard 注入处理/保存/关闭共享的原子保留。begin 在 core 调用前获取，
// 返回 false 表示已有处理/保存/关闭保留，应拒绝本次请求；end 在完整响应后释放。
func WithOperationGuard(begin func() bool, end func()) Option {
	return func(s *Server) {
		s.opGuardBegin = begin
		s.opGuardEnd = end
	}
}

// WithUIPrefs 注入启动期 UI 偏好（theme/language），随页面首帧下发。
// 非法值回退默认，避免页面首帧出现未定义主题/语言。
func WithUIPrefs(theme, language string) Option {
	return func(s *Server) {
		s.theme = theme
		s.language = language
	}
}

// New 构造服务。
func New(cfg *config.Config, auditLog *audit.Log, opts ...Option) *Server {
	if cfg == nil {
		cfg = config.Defaults()
	}
	s := &Server{
		cfg:         cfg,
		audit:       auditLog,
		openBrowser: OpenBrowser,
		fallback:    os.Stderr,
		idle:        time.Duration(cfg.IdleTimeoutMin) * time.Minute,
		done:        make(chan struct{}),
	}
	for _, o := range opts {
		o(s)
	}
	if s.idle <= 0 {
		s.idle = 30 * time.Minute
	}
	if !uiprefs.ValidTheme(s.theme) {
		s.theme = uiprefs.ThemeSystem
	}
	if !uiprefs.ValidLanguage(s.language) {
		s.language = string(i18n.ZH)
	}
	s.touch()
	s.handler = s.routes()
	return s
}

// URL 返回当前服务地址。
func (s *Server) URL() string { return s.url }

// Handler 返回 http.Handler（测试用）。
func (s *Server) Handler() http.Handler { return s.handler }

// Done 在服务退出（显式退出或空闲关停）时关闭。
func (s *Server) Done() <-chan struct{} { return s.done }

// Start 启动服务。alreadyRunning 为 true 表示检测到存活实例，只打开了新标签。
func (s *Server) Start() (url string, alreadyRunning bool, err error) {
	lp, err := s.resolveLockPath()
	if err != nil {
		return "", false, err
	}
	s.lockPath = lp
	if existing, alive := existingInstance(lp); alive {
		if err := s.openBrowser(existing); err != nil {
			s.reportFallback(existing)
		}
		return existing, true, nil
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", false, fmt.Errorf("无法在 127.0.0.1 上监听空闲端口：%w", err)
	}
	s.ln = ln
	s.url = "http://" + ln.Addr().String()
	s.srv = &http.Server{Handler: s.handler}
	_ = writeLock(lp, instanceInfo{PID: os.Getpid(), URL: s.url})

	go func() { _ = s.srv.Serve(ln) }()
	if err := s.openBrowser(s.url); err != nil {
		s.reportFallback(s.url)
	}
	go s.idleLoop()
	return s.url, false, nil
}

// Shutdown 释放端口并清理锁文件。
func (s *Server) Shutdown(ctx context.Context) {
	s.triggerShutdown()
	if s.srv != nil {
		_ = s.srv.Shutdown(ctx)
	}
}

func (s *Server) triggerShutdown() {
	s.once.Do(func() {
		close(s.done)
		if s.lockPath != "" {
			_ = os.Remove(s.lockPath)
		}
		if s.srv != nil {
			go func() { _ = s.srv.Shutdown(context.Background()) }()
		}
	})
}

func (s *Server) idleLoop() {
	interval := time.Minute
	if s.idle < 2*time.Minute {
		interval = s.idle / 2
	}
	if interval < 20*time.Millisecond {
		interval = 20 * time.Millisecond
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
			if time.Since(time.Unix(0, s.lastActivity.Load())) >= s.idle {
				s.triggerShutdown()
				return
			}
		}
	}
}

func (s *Server) touch() { s.lastActivity.Store(time.Now().UnixNano()) }

func (s *Server) resolveLockPath() (string, error) {
	if s.lockPath != "" {
		return s.lockPath, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "data-masking", "instance.lock"), nil
}

func (s *Server) reportFallback(url string) {
	fmt.Fprintf(s.fallback, "无法自动打开浏览器。请在浏览器中手动访问以下地址（可复制）：\n  %s\n提示：若仍无法访问，请确认本机防火墙未阻止本地回环地址。\n", url)
}

// OpenBrowser 调用各平台系统打开机制（无 shell 脚本依赖）。
func OpenBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return startCommand("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		return startCommand("open", url)
	default:
		return startCommand("xdg-open", url)
	}
}

func startCommand(name string, args ...string) error {
	cmd := command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.serveIndex)
	// 桌面宿主只对以 "/" 或 "/index.html" 结尾的 GET 路径注入 Wails runtime/bindings，
	// 故还原页的规范路径为 /restore/；同时保留 /restore 供既有浏览器回归与桌面 wrapper 重定向。
	mux.HandleFunc("/restore", s.serveRestore)
	mux.HandleFunc("/restore/", s.serveRestore)
	mux.HandleFunc("/style.css", s.serveStatic("style.css", "text/css; charset=utf-8"))
	mux.HandleFunc("/app.js", s.serveStatic("app.js", "application/javascript; charset=utf-8"))
	mux.HandleFunc("/api/mask", s.handleMask)
	mux.HandleFunc("/api/restore", s.handleRestore)
	mux.HandleFunc("/api/exit", s.handleExit)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.touch()
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) serveStatic(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := web.FS.ReadFile("static/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(data)
	}
}

func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := web.FS.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "页面缺失", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(injectConfig(data, s.cfg, s.url, s.desktopMode, s.theme, s.language, ""))
}

// serveRestore 返回同一双视图壳并注入初始 restore 视图，保留 /restore/ 路由兼容。
func (s *Server) serveRestore(w http.ResponseWriter, r *http.Request) {
	data, err := web.FS.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "页面缺失", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(injectConfig(data, s.cfg, s.url, s.desktopMode, s.theme, s.language, "restore"))
}

type pageConfig struct {
	Profiles       []config.ProfileInfo `json:"profiles"`
	DefaultProfile string               `json:"default_profile"`
	URL            string               `json:"url"`
	Mode           string               `json:"mode,omitempty"`
	MaxFileSize    int64                `json:"max_file_size"`
	HighFreq       int                  `json:"high_freq_threshold"`
	Theme          string               `json:"theme"`
	Language       string               `json:"language"`
	InitialView    string               `json:"initial_view,omitempty"`
	// FileDropEnabled 为只读平台能力：仅桌面 Windows 支持按钮拖入。两个页面路由同值，
	// 由实际运行目标 GOOS 而非浏览器 UA 推导；缺字段时前端保守按 false 处理。
	FileDropEnabled bool                         `json:"file_drop_enabled"`
	I18n            map[string]map[string]string `json:"i18n"`
}

// fileDropEnabled 为可注入纯函数：桌面且运行目标为 Windows 才允许拖入；
// 非桌面、Linux/其他未知目标一律 false。
func fileDropEnabled(desktop bool, goos string) bool {
	return desktop && goos == "windows"
}

func injectConfig(page []byte, cfg *config.Config, url string, desktop bool, theme, language, initialView string) []byte {
	pc := pageConfig{
		Profiles:        cfg.ProfileInfos(),
		DefaultProfile:  config.DefaultProfile,
		URL:             url,
		MaxFileSize:     cfg.MaxFileSize,
		HighFreq:        cfg.HighFreqThreshold,
		Theme:           theme,
		Language:        language,
		InitialView:     initialView,
		FileDropEnabled: fileDropEnabled(desktop, runtime.GOOS),
		I18n:            i18n.AllMessages(),
	}
	if desktop {
		pc.Mode = "desktop"
		pc.URL = ""
	}
	js, _ := json.Marshal(pc)
	out := string(page)
	marker := "/*__DM_CONFIG__*/null"
	if i := indexOf(out, marker); i >= 0 {
		out = out[:i] + string(js) + out[i+len(marker):]
	}
	return []byte(out)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// profileDisplayNames 返回给用户看的可用 profile 显示名列表。
func (s *Server) profileDisplayNames() []string {
	infos := s.cfg.ProfileInfos()
	names := make([]string, 0, len(infos))
	for _, in := range infos {
		names = append(names, in.DisplayName)
	}
	return names
}

// parseRuleOverrides 解析 `/api/mask` 的 rule_<id> 布尔单值覆盖字段。
// 仅 `profile==__custom` 接受；其余 profile 携带任意 rule_* 字段即报错；
// 畸形值（非 true/false）或重复值报错；未知 <id> 报错。
func parseRuleOverrides(values map[string][]string, profile string) (map[detector.RuleID]bool, *loader.HumanError) {
	hasAny := false
	for key := range values {
		if strings.HasPrefix(key, "rule_") {
			hasAny = true
			break
		}
	}
	if !hasAny {
		return nil, nil
	}
	if profile != config.CustomProfile {
		return nil, &loader.HumanError{
			Reason:    "规则覆盖字段仅 custom 档可用",
			Advice:    "请切换到 custom 档，或移除 rule_* 字段",
			MessageID: "rule.only_custom",
		}
	}
	out := map[detector.RuleID]bool{}
	for key, vals := range values {
		if !strings.HasPrefix(key, "rule_") {
			continue
		}
		id := detector.RuleID(strings.TrimPrefix(key, "rule_"))
		if !detector.ValidRule(id) {
			return nil, &loader.HumanError{
				Reason:    "未知规则覆盖字段 " + strconv.Quote(key),
				Advice:    "可用字段：rule_phone、rule_idcard、rule_email、rule_ip、rule_api_key、rule_custom",
				MessageID: "rule.unknown_field",
				Params:    map[string]any{"field": key},
			}
		}
		if len(vals) != 1 {
			return nil, &loader.HumanError{
				Reason:    "规则覆盖字段 " + strconv.Quote(key) + " 须为布尔单值",
				Advice:    "请只提交一次 true 或 false",
				MessageID: "rule.need_single_bool",
				Params:    map[string]any{"field": key},
			}
		}
		switch vals[0] {
		case "true":
			out[id] = true
		case "false":
			out[id] = false
		default:
			return nil, &loader.HumanError{
				Reason:    "规则覆盖字段 " + strconv.Quote(key) + " 的值 " + strconv.Quote(vals[0]) + " 不是布尔值",
				Advice:    "规则覆盖字段须为布尔值：true 或 false",
				MessageID: "rule.bad_bool",
				Params:    map[string]any{"field": key, "value": vals[0]},
			}
		}
	}
	return out, nil
}

// ruleEnabled 报告某规则在 profile 基础开关 + 覆盖字段下的最终状态。
func ruleEnabled(base, overrides map[detector.RuleID]bool, id detector.RuleID) bool {
	if v, ok := overrides[id]; ok {
		return v
	}
	return base[id]
}

// beginOperation 获取处理/保存/关闭共享的原子保留；无守卫时始终成功。
func (s *Server) beginOperation() bool {
	if s.opGuardBegin == nil {
		return true
	}
	return s.opGuardBegin()
}

func (s *Server) endOperation() {
	if s.opGuardEnd != nil {
		s.opGuardEnd()
	}
}

func (s *Server) handleMask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrPres(w, http.StatusMethodNotAllowed, "仅支持 POST", "请从脱敏页面执行操作。",
			i18n.P("http.method_post_only", map[string]any{"scope": "脱敏接口"}))
		return
	}
	if !s.beginOperation() {
		writeErrPres(w, http.StatusConflict, "有操作正在进行", "请等待当前处理或保存完成后再试。",
			i18n.P("http.operation_busy", nil))
		return
	}
	defer s.endOperation()
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeErrPres(w, http.StatusBadRequest, "表单解析失败", "请重新选择文件后再试。", i18n.P("http.form_parse_failed", nil))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErrPres(w, http.StatusBadRequest, "缺少待脱敏文件", "请在脱敏页选择纯文本或 .docx 文件。", i18n.P("http.mask_file_missing", nil))
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		writeErrPres(w, http.StatusBadRequest, "读取文件失败", "请重试。", i18n.P("http.file_read_failed", nil))
		return
	}
	profileName := r.FormValue("profile")
	if profileName == "" {
		profileName = config.DefaultProfile
	}
	if _, ok := s.cfg.Profiles[profileName]; !ok {
		writeErrPres(w, http.StatusBadRequest, "未知 profile "+strconv.Quote(r.FormValue("profile")),
			"可用选项："+strings.Join(s.profileDisplayNames(), "、"),
			i18n.P("profile.unknown", map[string]any{"profile": r.FormValue("profile"), "options": strings.Join(s.profileDisplayNames(), "、")}))
		return
	}
	overrides, oerr := parseRuleOverrides(r.MultipartForm.Value, profileName)
	if oerr != nil {
		writeCoreErr(w, oerr)
		return
	}
	opts := core.MaskOptions{
		Profile:         profileName,
		RuleOverrides:   overrides,
		ConfirmedCustom: r.MultipartForm.Value["confirmed"],
	}
	// 规则关 + 词提交：保持静默忽略（不解析、不校验、不 400，兼容现网）。
	if ruleEnabled(s.cfg.RulesForProfile(profileName), overrides, detector.RuleCustom) {
		var wordFile []byte
		filePresent := false
		if cf, _, ferr := r.FormFile("custom_file"); ferr == nil {
			defer cf.Close()
			wordFile, _ = io.ReadAll(cf)
			filePresent = true // 0 字节文件也算提交，交由 core 报「0 合法词」
		}
		words, werr := core.ParseCustomWords(r.MultipartForm.Value["custom"], wordFile, filePresent)
		if werr != nil {
			writeCoreErr(w, werr)
			return
		}
		opts.ExtraCustom = words
	}
	out, err := core.MaskFile(header.Filename, data, opts, s.cfg, s.audit)
	if err != nil {
		if pre, ok := err.(*core.PreviewRequiredError); ok {
			writeJSON(w, http.StatusConflict, map[string]any{
				"ok":           false,
				"error":        "preview_required",
				"message":      pre.Error(),
				"hits":         pre,
				"presentation": pre.Presentation(),
			})
			return
		}
		writeCoreErr(w, err)
		return
	}
	resp := map[string]any{
		"ok":           true,
		"masked_name":  out.MaskedName,
		"mapping_name": out.MappingName,
		"encoding":     string(out.Encoding),
		"format":       out.Format,
		"masked_b64":   base64.StdEncoding.EncodeToString(out.MaskedBytes),
		"mapping_b64":  base64.StdEncoding.EncodeToString(out.MappingBytes),
		"warnings":     out.Warnings,
		"cleanup":      out.Cleanup,
		"audit_failed": out.AuditFailed,
	}
	// 桌面模式下随成功响应下发本次原子的未保存 generation（浏览器模式不新增字段）。
	if s.desktopMode && s.onArtifacts != nil {
		resp["unsaved_generation"] = s.onArtifacts()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrPres(w, http.StatusMethodNotAllowed, "仅支持 POST", "请从还原页面执行操作。",
			i18n.P("http.method_post_only", map[string]any{"scope": "还原接口"}))
		return
	}
	if !s.beginOperation() {
		writeErrPres(w, http.StatusConflict, "有操作正在进行", "请等待当前处理或保存完成后再试。",
			i18n.P("http.operation_busy", nil))
		return
	}
	defer s.endOperation()
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeErrPres(w, http.StatusBadRequest, "表单解析失败", "请重新提交后再试。", i18n.P("http.form_parse_failed", nil))
		return
	}
	mf, _, err := r.FormFile("mapping")
	if err != nil {
		writeErrPres(w, http.StatusBadRequest, "缺少 mapping 文件", "请选择本机上次脱敏后下载的 .mapping.json 文件。", i18n.P("http.mapping_missing", nil))
		return
	}
	defer mf.Close()
	mappingData, err := io.ReadAll(mf)
	if err != nil {
		writeErrPres(w, http.StatusBadRequest, "读取 mapping 失败", "请重试。", i18n.P("http.mapping_read_failed", nil))
		return
	}

	var input []byte
	fileName := ""
	if file, header, ferr := r.FormFile("file"); ferr == nil {
		defer file.Close()
		input, _ = io.ReadAll(file)
		fileName = header.Filename
	} else if text := r.FormValue("text"); text != "" {
		input = []byte(text)
		fileName = "paste.txt"
	} else {
		writeErrPres(w, http.StatusBadRequest, "缺少待还原内容", "请上传 LLM 成品文件，或在文本框中粘贴内容。", i18n.P("http.restore_input_missing", nil))
		return
	}

	out, err := core.RestoreFile(fileName, input, mappingData, s.cfg, s.audit)
	if err != nil {
		writeCoreErr(w, err)
		return
	}
	resp := map[string]any{
		"ok":             true,
		"name":           out.Name,
		"encoding":       string(out.Encoding),
		"format":         out.Format,
		"restored_b64":   base64.StdEncoding.EncodeToString(out.Bytes),
		"restored_count": out.RestoredCount,
		"warnings":       out.Warnings,
		"cleanup":        out.Cleanup,
		"audit_failed":   out.AuditFailed,
	}
	if s.desktopMode && s.onArtifacts != nil {
		resp["unsaved_generation"] = s.onArtifacts()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleExit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrPres(w, http.StatusMethodNotAllowed, "仅支持 POST", "退出接口只接受 POST。",
			i18n.P("http.method_post_only", map[string]any{"scope": "退出接口"}))
		return
	}
	// 桌面退出守卫：处理/保存进行中时拒绝关停，保证写入与临时清理正常完成。
	if s.exitGuard != nil {
		if err := s.exitGuard(); err != nil {
			writeErrPres(w, http.StatusConflict, err.Error(), "请等待当前操作完成后再退出。",
				i18n.P("exit.blocked", nil))
			return
		}
	}
	msg := "服务正在退出，端口即将释放。"
	if s.desktopMode {
		msg = "应用正在退出。"
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg, "presentation": i18n.P("exit.ok", nil)})
	go func() {
		time.Sleep(150 * time.Millisecond)
		s.triggerShutdown()
	}()
}

// presenter 是带展示元数据的错误接口。
type presenter interface {
	Presentation() *i18n.Presentation
}

func writeCoreErr(w http.ResponseWriter, err error) {
	var pres *i18n.Presentation
	if p, ok := err.(presenter); ok {
		pres = p.Presentation()
	}
	if he, ok := err.(*loader.HumanError); ok {
		writeErrPres(w, http.StatusBadRequest, he.Reason, he.Advice, pres)
		return
	}
	writeErrPres(w, http.StatusBadRequest, err.Error(), "请检查输入后重试。", pres)
}

func writeErr(w http.ResponseWriter, status int, reason, advice string) {
	writeErrPres(w, status, reason, advice, nil)
}

// writeErrPres 写入错误响应并追加 presentation {message_id, params}；保留 error/message 兼容字段。
func writeErrPres(w http.ResponseWriter, status int, reason, advice string, pres *i18n.Presentation) {
	body := map[string]any{
		"ok":      false,
		"error":   reason,
		"message": reason + "。" + advice,
	}
	if pres != nil {
		body["presentation"] = pres
	}
	writeJSON(w, status, body)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
