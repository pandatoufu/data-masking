package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pandatoufu/data-masking/internal/config"
)

type filePart struct {
	name string
	data []byte
}

func makeMultipart(t *testing.T, fields map[string][]string, files map[string]filePart) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, vs := range fields {
		for _, v := range vs {
			if err := w.WriteField(k, v); err != nil {
				t.Fatal(err)
			}
		}
	}
	for k, fp := range files {
		fw, err := w.CreateFormFile(k, fp.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(fp.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

func newTestServer(t *testing.T, opts ...Option) *Server {
	t.Helper()
	base := []Option{
		WithOpenBrowser(func(string) error { return nil }),
		WithLockPath(filepath.Join(t.TempDir(), "instance.lock")),
		WithIdleTimeout(time.Hour),
		WithFallbackWriter(&bytes.Buffer{}),
	}
	return New(config.Defaults(), nil, append(base, opts...)...)
}

func doJSON(t *testing.T, h http.Handler, method, path string, body *bytes.Buffer, ct string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应不是 JSON：%s", rec.Body.String())
	}
	return rec.Code, out
}

func TestMaskAndRestoreAPIConsistent(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	orig := []byte("联系人 13812345678\n")

	body, ct := makeMultipart(t, map[string][]string{"profile": {"balanced"}},
		map[string]filePart{"file": {"notes.txt", orig}})
	code, resp := doJSON(t, h, "POST", "/api/mask", body, ct)
	if code != http.StatusOK || resp["ok"] != true {
		t.Fatalf("脱敏失败：%d %v", code, resp)
	}
	masked, _ := base64.StdEncoding.DecodeString(resp["masked_b64"].(string))
	if !bytes.Contains(masked, []byte("[PHONE_1]")) {
		t.Fatalf("脱敏输出应含占位符：%q", masked)
	}
	mappingJSON, _ := base64.StdEncoding.DecodeString(resp["mapping_b64"].(string))

	rb, rct := makeMultipart(t, nil, map[string]filePart{
		"file":    {resp["masked_name"].(string), masked},
		"mapping": {"notes.masked.mapping.json", mappingJSON},
	})
	code, rresp := doJSON(t, h, "POST", "/api/restore", rb, rct)
	if code != http.StatusOK || rresp["ok"] != true {
		t.Fatalf("还原失败：%d %v", code, rresp)
	}
	restored, _ := base64.StdEncoding.DecodeString(rresp["restored_b64"].(string))
	if !bytes.Equal(restored, orig) {
		t.Fatalf("API 往返应与页面一致：%q vs %q", restored, orig)
	}
}

func TestRestorePaste(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	body, ct := makeMultipart(t, map[string][]string{"profile": {"balanced"}},
		map[string]filePart{"file": {"a.txt", []byte("电话 13812345678")}})
	_, resp := doJSON(t, h, "POST", "/api/mask", body, ct)
	mappingJSON, _ := base64.StdEncoding.DecodeString(resp["mapping_b64"].(string))

	rb, rct := makeMultipart(t, map[string][]string{"text": {"回复内容 [PHONE_1]"}},
		map[string]filePart{"mapping": {"a.masked.mapping.json", mappingJSON}})
	code, rresp := doJSON(t, h, "POST", "/api/restore", rb, rct)
	if code != http.StatusOK || rresp["ok"] != true {
		t.Fatalf("粘贴还原失败：%d %v", code, rresp)
	}
	restored, _ := base64.StdEncoding.DecodeString(rresp["restored_b64"].(string))
	if string(restored) != "回复内容 13812345678" {
		t.Fatalf("粘贴还原结果错误：%q", restored)
	}
}

func TestMaskPreviewRequired(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	text := bytes.Repeat([]byte("内部词\n"), 30)
	body, ct := makeMultipart(t, map[string][]string{"profile": {"balanced"}, "custom": {"内部词"}},
		map[string]filePart{"file": {"a.txt", text}})
	code, resp := doJSON(t, h, "POST", "/api/mask", body, ct)
	if code != http.StatusConflict || resp["error"] != "preview_required" {
		t.Fatalf("高频命中应返回预览确认：%d %v", code, resp)
	}
}

func TestHumanReadableError(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	body, ct := makeMultipart(t, nil, map[string]filePart{"file": {"data.docm", []byte("x")}})
	code, resp := doJSON(t, h, "POST", "/api/mask", body, ct)
	if code != http.StatusBadRequest {
		t.Fatalf("应返回 400，got %d", code)
	}
	msg, _ := resp["message"].(string)
	if !strings.Contains(msg, "不支持的文件格式") || !strings.Contains(msg, "纯文本") || !strings.Contains(msg, ".docx") {
		t.Fatalf("错误应含中文格式指引：%q", msg)
	}
}

func TestOnlyThreeAPIs(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	for _, p := range []string{"/api/config", "/api/unknown", "/api/ping", "/api/workspace/files", "/api/workspace/read"} {
		req := httptest.NewRequest("POST", p, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("不应存在额外 API %s（got %d）", p, rec.Code)
		}
	}
}

func TestPageCarriesProfileList(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, want := range []string{"balanced", "strict", "minimal", "还原映射含原文"} {
		if !strings.Contains(body, want) {
			t.Errorf("页面应含 %q", want)
		}
	}
}

func TestPageCarriesProfileDetails(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()

	const marker = "window.__DM_CONFIG__ = "
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("页面未注入 pageConfig：%q", body)
	}
	rest := body[i+len(marker):]
	end := strings.Index(rest, ";</script>")
	if end < 0 {
		t.Fatalf("pageConfig 注入格式不符：%q", rest)
	}
	var pc struct {
		Profiles []struct {
			Name        string          `json:"name"`
			DisplayName string          `json:"display_name"`
			Description string          `json:"description"`
			Rules       map[string]bool `json:"rules"`
		} `json:"profiles"`
		DefaultProfile string `json:"default_profile"`
	}
	if err := json.Unmarshal([]byte(rest[:end]), &pc); err != nil {
		t.Fatalf("pageConfig 不是合法 JSON：%v\n%s", err, rest[:end])
	}
	if pc.DefaultProfile != "balanced" {
		t.Fatalf("默认 profile 应为 balanced，got %q", pc.DefaultProfile)
	}
	byName := map[string]int{}
	for i, p := range pc.Profiles {
		byName[p.Name] = i
	}
	for _, want := range []string{"strict", "balanced", "minimal", "__custom"} {
		if _, ok := byName[want]; !ok {
			t.Fatalf("pageConfig 应含 profile %q：%#v", want, pc.Profiles)
		}
	}
	bal := pc.Profiles[byName["balanced"]]
	if bal.DisplayName != "balanced" || !strings.Contains(bal.Description, "email/IP") {
		t.Fatalf("balanced 应下发显示名与意图描述：%#v", bal)
	}
	if len(bal.Rules) != 6 {
		t.Fatalf("规则矩阵应为 6 类：%#v", bal.Rules)
	}
	if !bal.Rules["phone"] || bal.Rules["email"] {
		t.Fatalf("balanced 规则矩阵应反映实际开关：%#v", bal.Rules)
	}
	cu := pc.Profiles[byName["__custom"]]
	if cu.DisplayName != "custom" || len(cu.Rules) != 6 {
		t.Fatalf("custom 档应映射显示名与全 6 类开关：%#v", cu)
	}
}

func TestMaskCustomRuleOverride(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	fields := map[string][]string{
		"profile":      {"__custom"},
		"rule_email":   {"true"},
		"rule_phone":   {"false"},
		"rule_idcard":  {"false"},
		"rule_ip":      {"false"},
		"rule_api_key": {"false"},
		"rule_custom":  {"false"},
	}
	body, ct := makeMultipart(t, fields, map[string]filePart{
		"file": {"a.txt", []byte("邮箱 a.b@example.com 手机 13812345678")},
	})
	code, resp := doJSON(t, h, "POST", "/api/mask", body, ct)
	if code != http.StatusOK || resp["ok"] != true {
		t.Fatalf("custom 覆盖应成功：%d %v", code, resp)
	}
	masked, _ := base64.StdEncoding.DecodeString(resp["masked_b64"].(string))
	if !bytes.Contains(masked, []byte("[EMAIL_1]")) {
		t.Fatalf("rule_email=true 应替换邮箱：%q", masked)
	}
	if !bytes.Contains(masked, []byte("13812345678")) {
		t.Fatalf("rule_phone=false 应保留手机号：%q", masked)
	}
}

func TestMaskOverrideRejectedForNonCustom(t *testing.T) {
	s := newTestServer(t)
	body, ct := makeMultipart(t, map[string][]string{"profile": {"balanced"}, "rule_email": {"true"}},
		map[string]filePart{"file": {"a.txt", []byte("x")}})
	code, resp := doJSON(t, s.Handler(), "POST", "/api/mask", body, ct)
	if code != http.StatusBadRequest {
		t.Fatalf("非 custom 档携带覆盖字段应 400，got %d %v", code, resp)
	}
	if msg, _ := resp["message"].(string); !strings.Contains(msg, "仅 custom") {
		t.Fatalf("应提示覆盖字段仅 custom 档可用：%q", msg)
	}
}

func TestMaskOverrideMalformed(t *testing.T) {
	s := newTestServer(t)
	body, ct := makeMultipart(t, map[string][]string{"profile": {"__custom"}, "rule_email": {"abc"}},
		map[string]filePart{"file": {"a.txt", []byte("x")}})
	code, resp := doJSON(t, s.Handler(), "POST", "/api/mask", body, ct)
	if code != http.StatusBadRequest {
		t.Fatalf("畸形覆盖值应 400，got %d %v", code, resp)
	}
	if msg, _ := resp["message"].(string); !strings.Contains(msg, "布尔") {
		t.Fatalf("应提示须为布尔值：%q", msg)
	}
}

func TestMaskUnknownProfile(t *testing.T) {
	s := newTestServer(t)
	body, ct := makeMultipart(t, map[string][]string{"profile": {"nope"}},
		map[string]filePart{"file": {"a.txt", []byte("x")}})
	code, resp := doJSON(t, s.Handler(), "POST", "/api/mask", body, ct)
	if code != http.StatusBadRequest {
		t.Fatalf("未知 profile 应 400，got %d %v", code, resp)
	}
	if msg, _ := resp["message"].(string); !strings.Contains(msg, "未知 profile") {
		t.Fatalf("应提示未知 profile：%q", msg)
	}
}

func TestMaskEmptyProfileFallsBack(t *testing.T) {
	s := newTestServer(t)
	body, ct := makeMultipart(t, nil, map[string]filePart{"file": {"a.txt", []byte("电话 13812345678")}})
	code, resp := doJSON(t, s.Handler(), "POST", "/api/mask", body, ct)
	if code != http.StatusOK || resp["ok"] != true {
		t.Fatalf("空 profile 应回退默认：%d %v", code, resp)
	}
	masked, _ := base64.StdEncoding.DecodeString(resp["masked_b64"].(string))
	if !bytes.Contains(masked, []byte("[PHONE_1]")) {
		t.Fatalf("应走 balanced 脱敏手机号：%q", masked)
	}
}

func TestMaskRuleOffIgnoresCustom(t *testing.T) {
	s := newTestServer(t)
	text := bytes.Repeat([]byte("内部词\n"), 30)
	fields := map[string][]string{
		"profile": {"minimal"},
		"custom":  {"内部词", strings.Repeat("x", 300)}, // 含超长非法词也应被静默忽略
	}
	body, ct := makeMultipart(t, fields, map[string]filePart{"file": {"a.txt", text}})
	code, resp := doJSON(t, s.Handler(), "POST", "/api/mask", body, ct)
	if code != http.StatusOK || resp["ok"] != true {
		t.Fatalf("规则关 + 词提交应静默忽略并成功：%d %v", code, resp)
	}
	masked, _ := base64.StdEncoding.DecodeString(resp["masked_b64"].(string))
	if bytes.Contains(masked, []byte("[CUSTOM")) {
		t.Fatalf("custom 关时不应替换自定义词：%q", masked)
	}
}

func TestMaskCustomFileImport(t *testing.T) {
	s := newTestServer(t)
	body, ct := makeMultipart(t, map[string][]string{"profile": {"balanced"}},
		map[string]filePart{
			"file":        {"a.txt", []byte("包含 自定义甲 与 自定义乙")},
			"custom_file": {"words.txt", []byte("自定义甲\n自定义乙\n")},
		})
	code, resp := doJSON(t, s.Handler(), "POST", "/api/mask", body, ct)
	if code != http.StatusOK || resp["ok"] != true {
		t.Fatalf("词文件导入应成功：%d %v", code, resp)
	}
	masked, _ := base64.StdEncoding.DecodeString(resp["masked_b64"].(string))
	if bytes.Contains(masked, []byte("自定义甲")) || bytes.Contains(masked, []byte("自定义乙")) {
		t.Fatalf("导入词应参与替换：%q", masked)
	}
}

func TestMaskCustomFileInvalidLine(t *testing.T) {
	s := newTestServer(t)
	body, ct := makeMultipart(t, map[string][]string{"profile": {"balanced"}},
		map[string]filePart{
			"file":        {"a.txt", []byte("内容")},
			"custom_file": {"words.txt", []byte("ok\n\n" + strings.Repeat("x", 300) + "\n")},
		})
	code, resp := doJSON(t, s.Handler(), "POST", "/api/mask", body, ct)
	if code != http.StatusBadRequest {
		t.Fatalf("非法行应整批拒绝并 400，got %d %v", code, resp)
	}
	if msg, _ := resp["message"].(string); !strings.Contains(msg, "第 3 行") {
		t.Fatalf("应报行号：%q", msg)
	}
}

func TestMaskCustomFileEmptyRejected(t *testing.T) {
	s := newTestServer(t)
	body, ct := makeMultipart(t, map[string][]string{"profile": {"balanced"}},
		map[string]filePart{
			"file":        {"a.txt", []byte("内容")},
			"custom_file": {"empty.txt", []byte{}},
		})
	code, resp := doJSON(t, s.Handler(), "POST", "/api/mask", body, ct)
	if code != http.StatusBadRequest {
		t.Fatalf("0 字节真空词文件应显式报错，got %d %v", code, resp)
	}
	if msg, _ := resp["message"].(string); !strings.Contains(msg, "未解析到任何有效自定义词") {
		t.Fatalf("应报 0 合法词：%q", msg)
	}
}

func TestBindLoopbackDynamicPort(t *testing.T) {
	s := newTestServer(t)
	url, already, err := s.Start()
	if err != nil || already {
		t.Fatalf("启动失败：%v already=%v", err, already)
	}
	defer s.Shutdown(context.Background())
	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Fatalf("应仅绑定 127.0.0.1 动态端口，got %q", url)
	}
	if strings.Contains(url, "0.0.0.0") || strings.HasSuffix(url, ":0") {
		t.Fatalf("不得监听 0.0.0.0 或固定 0 端口：%q", url)
	}
}

func TestSecondLaunchOnlyOpensTab(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "instance.lock")
	cfg := config.Defaults()
	s1 := New(cfg, nil, WithLockPath(lock), WithOpenBrowser(func(string) error { return nil }), WithIdleTimeout(time.Hour))
	url1, already1, err := s1.Start()
	if err != nil || already1 {
		t.Fatalf("首个实例启动失败：%v", err)
	}
	defer s1.Shutdown(context.Background())

	var opened string
	s2 := New(cfg, nil, WithLockPath(lock), WithOpenBrowser(func(u string) error { opened = u; return nil }), WithIdleTimeout(time.Hour))
	url2, already2, err := s2.Start()
	if err != nil {
		t.Fatal(err)
	}
	if !already2 {
		t.Fatal("双开应检测到已运行实例")
	}
	if url2 != url1 || opened != url1 {
		t.Fatalf("应复用同一地址并只开标签：url1=%s url2=%s opened=%s", url1, url2, opened)
	}
}

func TestStaleLockSelfHeals(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "instance.lock")
	if err := writeLock(lock, instanceInfo{PID: 999999, URL: "http://127.0.0.1:1"}); err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t, WithLockPath(lock))
	url, already, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	if already {
		t.Fatal("崩溃残留锁不应把用户锁死在外")
	}
	if url == "http://127.0.0.1:1" {
		t.Fatal("应接管并监听新端口")
	}
}

func TestIdleShutdown(t *testing.T) {
	s := newTestServer(t, WithIdleTimeout(80*time.Millisecond))
	if _, _, err := s.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("空闲超时应自动关停")
	}
}

func TestExplicitExitReleasesPort(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "instance.lock")
	cfg := config.Defaults()
	s := New(cfg, nil, WithLockPath(lock), WithOpenBrowser(func(string) error { return nil }), WithIdleTimeout(time.Hour))
	url, _, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	body, ct := makeMultipart(t, nil, nil)
	code, resp := doJSON(t, s.Handler(), "POST", "/api/exit", body, ct)
	if code != http.StatusOK || resp["ok"] != true {
		t.Fatalf("退出接口失败：%d %v", code, resp)
	}
	select {
	case <-s.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("显式退出应关停服务")
	}
	// 端口释放后再次启动不受影响。
	s2 := New(cfg, nil, WithLockPath(lock), WithOpenBrowser(func(string) error { return nil }), WithIdleTimeout(time.Hour))
	defer s2.Shutdown(context.Background())
	url2, already, err := s2.Start()
	if err != nil || already {
		t.Fatalf("重启应不受影响：%v already=%v", err, already)
	}
	// 端口可能被复用，仅要求可再次监听。
	_ = url2
	_ = url
}

func TestBrowserFallbackShowsAddress(t *testing.T) {
	var buf bytes.Buffer
	s := New(config.Defaults(), nil,
		WithLockPath(filepath.Join(t.TempDir(), "instance.lock")),
		WithOpenBrowser(func(string) error { return errors.New("no browser") }),
		WithFallbackWriter(&buf),
		WithIdleTimeout(time.Hour),
	)
	url, _, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	if !strings.Contains(buf.String(), "无法自动打开浏览器") || !strings.Contains(buf.String(), url) {
		t.Fatalf("浏览器打不开时应给出可复制地址兜底：%q", buf.String())
	}
}

func TestPortCollisionPicksFreePort(t *testing.T) {
	s1 := newTestServer(t)
	url1, _, err := s1.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer s1.Shutdown(context.Background())

	s2 := newTestServer(t) // 独立锁文件，不触发双开复用
	url2, already, err := s2.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Shutdown(context.Background())
	if already {
		t.Fatal("独立实例不应被判为双开")
	}
	if url1 == url2 {
		t.Fatalf("动态端口应各自不同：%s", url1)
	}
	if !IsAlive(url1) || !IsAlive(url2) {
		t.Fatal("两个实例都应可访问")
	}
}

func TestRestorePageConfirmsMappingSource(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("GET", "/restore", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "确认") || !strings.Contains(body, "mapping") {
		t.Fatalf("还原页应提示确认 mapping 来源：%q", body)
	}
}

// 4.2 真实 multipart 名称与字节沿用原链：新后缀、无后缀与点文件可脱敏并还原；
// mapping 保留真实 source_file；已知不支持格式在读取内容前拒绝。
func TestNewTextNamesMultipartChain(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	orig := []byte("phone,13800138000\n")
	cases := []struct {
		name     string
		masked   string
		restored string
	}{
		{"data.csv", "data.masked.csv", "data.restored.csv"},
		{"README", "README.masked.txt", "README.restored.txt"},
		{".env", ".env.masked.txt", ".env.restored.txt"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body, ct := makeMultipart(t, map[string][]string{"profile": {"strict"}},
				map[string]filePart{"file": {c.name, orig}})
			code, resp := doJSON(t, h, "POST", "/api/mask", body, ct)
			if code != http.StatusOK {
				t.Fatalf("新名称应脱敏成功：%d %v", code, resp)
			}
			if resp["masked_name"] != c.masked {
				t.Fatalf("masked_name=%v want %q", resp["masked_name"], c.masked)
			}
			masked, err := base64.StdEncoding.DecodeString(resp["masked_b64"].(string))
			if err != nil {
				t.Fatal(err)
			}
			mapping, err := base64.StdEncoding.DecodeString(resp["mapping_b64"].(string))
			if err != nil {
				t.Fatal(err)
			}
			var mp struct {
				SourceFile     string `json:"source_file"`
				SourceEncoding string `json:"source_encoding"`
			}
			if err := json.Unmarshal(mapping, &mp); err != nil {
				t.Fatal(err)
			}
			if mp.SourceFile != c.name || mp.SourceEncoding != "UTF-8" {
				t.Fatalf("mapping 应保留真实名称与编码：%+v", mp)
			}

			body, ct = makeMultipart(t, nil, map[string]filePart{
				"mapping": {"pair.mapping.json", mapping},
				"file":    {c.masked, masked},
			})
			code, resp = doJSON(t, h, "POST", "/api/restore", body, ct)
			if code != http.StatusOK {
				t.Fatalf("新名称应还原成功：%d %v", code, resp)
			}
			if resp["name"] != c.restored {
				t.Fatalf("还原名=%v want %q", resp["name"], c.restored)
			}
			restored, err := base64.StdEncoding.DecodeString(resp["restored_b64"].(string))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(restored, orig) {
				t.Fatalf("还原字节不一致：%q", restored)
			}
		})
	}

	// 已知不支持格式按名称拒绝，presentation 保持 file.unsupported_format。
	body, ct := makeMultipart(t, nil, map[string]filePart{"file": {"notes.rtf", []byte("{\\rtf1 13800138000}")}})
	code, resp := doJSON(t, h, "POST", "/api/mask", body, ct)
	if code != http.StatusBadRequest {
		t.Fatalf("RTF 应拒绝：%d %v", code, resp)
	}
	pres, _ := resp["presentation"].(map[string]any)
	if pres["message_id"] != "file.unsupported_format" {
		t.Fatalf("应返回 file.unsupported_format：%v", pres)
	}
}
