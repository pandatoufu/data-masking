package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"

	"github.com/pandatoufu/data-masking/internal/audit"
	"github.com/pandatoufu/data-masking/internal/config"
	"github.com/pandatoufu/data-masking/internal/detector"
	"github.com/pandatoufu/data-masking/internal/loader"
	"github.com/pandatoufu/data-masking/internal/mapping"
)

func gbk(t *testing.T, s string) []byte {
	t.Helper()
	b, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatalf("构造 GBK 失败：%v", err)
	}
	return b
}

func newAudit(t *testing.T) (*audit.Log, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "audit.jsonl")
	return audit.New(p), p
}

func TestAtomicWriteSuccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "out.txt")
	if err := AtomicWriteFile(path, []byte("hello")); err != nil {
		t.Fatalf("原子写失败：%v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "hello" {
		t.Fatalf("内容不一致：%q", got)
	}
}

func TestAtomicWriteFailureLeavesNoPartial(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "targetdir")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := AtomicWriteFile(target, []byte("data"))
	if err == nil {
		t.Fatal("目标为非空目录时应失败")
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".dm-") {
			t.Fatalf("失败后不应留下临时半成品：%s", e.Name())
		}
	}
}

func TestCollisionPrecheckRejects(t *testing.T) {
	cfg := config.Defaults()
	_, err := MaskFile("notes.txt", []byte("原文里已有 [PHONE_1]"), MaskOptions{Profile: "balanced"}, cfg, nil)
	if err == nil {
		t.Fatal("原文含占位符文法应被拒绝")
	}
	var he *loader.HumanError
	if !errors.As(err, &he) {
		t.Fatalf("错误应为人类可读错误：%T", err)
	}
}

func TestHighFreqCustomPreviewConfirm(t *testing.T) {
	cfg := config.Defaults()
	text := []byte(strings.Repeat("内部词\n", 50))
	_, err := MaskFile("a.txt", text, MaskOptions{Profile: "balanced", ExtraCustom: []string{"内部词"}}, cfg, nil)
	var pre *PreviewRequiredError
	if !errors.As(err, &pre) {
		t.Fatalf("50 次命中应先要求预览确认，got %v", err)
	}
	if pre.Extra["内部词"] != 50 {
		t.Fatalf("预览应含本次词命中计数 50，got %#v", pre)
	}
	if len(pre.Builtin) != 0 {
		t.Fatalf("常备词组应为空，got %#v", pre.Builtin)
	}
	out, err := MaskFile("a.txt", text, MaskOptions{
		Profile: "balanced", ExtraCustom: []string{"内部词"}, ConfirmedCustom: []string{"内部词"},
	}, cfg, nil)
	if err != nil {
		t.Fatalf("确认后应成功：%v", err)
	}
	if !bytes.Contains(out.MaskedBytes, []byte("[CUSTOM_1]")) {
		t.Fatal("确认后应执行替换")
	}
}

func TestDeterministicRerun(t *testing.T) {
	cfg := config.Defaults()
	in := []byte("张三 13812345678\nid 110101199003078515 key sk-abcdefghijklmnop\n")
	o1, err := MaskFile("notes.txt", in, MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	o2, err := MaskFile("notes.txt", in, MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(o1.MaskedBytes, o2.MaskedBytes) {
		t.Fatal("同输入脱敏文件应字节级一致")
	}
	if !bytes.Equal(o1.MappingBytes, o2.MappingBytes) {
		t.Fatal("同输入 mapping 应字节级一致")
	}
}

func TestFourInputClasses(t *testing.T) {
	cfg := config.Defaults()
	utf8in := []byte("联系人 13812345678\n")
	if out, err := MaskFile("a.txt", utf8in, MaskOptions{}, cfg, nil); err != nil || out.Encoding != loader.EncodingUTF8 {
		t.Fatalf("合法 UTF-8 应跑通：%v %v", err, out)
	}
	gbkin := gbk(t, "联系人 13812345678\n")
	if out, err := MaskFile("a.txt", gbkin, MaskOptions{}, cfg, nil); err != nil || out.Encoding != loader.EncodingGBK {
		t.Fatalf("GBK 应跑通：%v %v", err, out)
	}
	if _, err := MaskFile("app.txt", []byte{0x4d, 0x5a, 0x00, 0x03, 0x10}, MaskOptions{}, cfg, nil); err == nil {
		t.Fatal("改名二进制应被拒绝")
	}
	if _, err := MaskFile("b.txt", []byte("已有 [PHONE_1]"), MaskOptions{}, cfg, nil); err == nil {
		t.Fatal("碰撞原文应被拒绝")
	}
}

func TestBalancedLeavesIDCardUntouched(t *testing.T) {
	cfg := config.Defaults()
	in := []byte("身份证 110101199003078515 电话 13812345678")
	out, err := MaskFile("n.txt", in, MaskOptions{}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out.MaskedBytes, []byte("13812345678")) {
		t.Fatal("balanced 下手机号应被替换")
	}
	if !bytes.Contains(out.MaskedBytes, []byte("110101199003078515")) {
		t.Fatal("balanced 下 idcard 关闭，应保留原文")
	}
}

func TestRoundTripUTF8(t *testing.T) {
	cfg := config.Defaults()
	orig := []byte("联系人 13812345678\n备用 13812345678\n")
	out, err := MaskFile("notes.txt", orig, MaskOptions{}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out.MaskedBytes, []byte("13812345678")) {
		t.Fatal("脱敏文件不应含原文号码")
	}
	res, err := RestoreFile(out.MaskedName, out.MaskedBytes, out.MappingBytes, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(res.Bytes, orig) {
		t.Fatalf("还原后应字节级等于原文：\n got %q\nwant %q", res.Bytes, orig)
	}
}

func TestRoundTripGBKSameEncoding(t *testing.T) {
	cfg := config.Defaults()
	orig := gbk(t, "会议纪要\n联系人 13812345678\n")
	out, err := MaskFile("notes.txt", orig, MaskOptions{}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Encoding != loader.EncodingGBK {
		t.Fatalf("应检出 GBK，got %q", out.Encoding)
	}
	res, err := RestoreFile(out.MaskedName, out.MaskedBytes, out.MappingBytes, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Encoding != loader.EncodingGBK {
		t.Fatalf("回程应按原文编码写回，got %q", res.Encoding)
	}
	if !bytes.Equal(res.Bytes, orig) {
		t.Fatalf("GBK 还原后应字节级等于原文：\n got %x\nwant %x", res.Bytes, orig)
	}
}

func TestWrongMappingWarnsNotBlocked(t *testing.T) {
	cfg := config.Defaults()
	outA, err := MaskFile("a.txt", []byte("A 13812345678"), MaskOptions{}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 用 A 的 mapping 还原含 B 占位符的文本。
	res, err := RestoreFile("b.txt", []byte("B 的内容 [PHONE_9]"), outA.MappingBytes, cfg, nil)
	if err != nil {
		t.Fatalf("拿错 mapping 不应阻断：%v", err)
	}
	if len(res.Warnings) == 0 || res.Warnings[0].Kind != "residual_reverse" {
		t.Fatalf("应报告反向残留警告，got %#v", res.Warnings)
	}
	if !bytes.Contains(res.Bytes, []byte("[PHONE_9]")) {
		t.Fatal("无法还原的占位符应原样保留")
	}
	if !strings.Contains(res.Warnings[0].Message, "不构成") && !strings.Contains(res.Warnings[0].Message, "只做") {
		t.Fatalf("警告应内联能力边界：%q", res.Warnings[0].Message)
	}
}

func TestRestoreNormalization(t *testing.T) {
	cfg := config.Defaults()
	out, err := MaskFile("a.txt", []byte("电话 13812345678"), MaskOptions{}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := RestoreFile("reply.txt", []byte("电话 [phone_1] 全角 [ＰＨＯＮＥ_１]"), out.MappingBytes, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(res.Bytes); got != "电话 13812345678 全角 13812345678" {
		t.Fatalf("大小写/全角归一化还原失败：%q", got)
	}
	if res.RestoredCount != 2 {
		t.Fatalf("还原计数应为 2，got %d", res.RestoredCount)
	}
}

func TestAuditNoPlaintext(t *testing.T) {
	cfg := config.Defaults()
	log, path := newAudit(t)
	if _, err := MaskFile("notes.txt", []byte("电话 13812345678"), MaskOptions{}, cfg, log); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("13812345678")) {
		t.Fatalf("审计日志不得含原文：%s", data)
	}
	if !bytes.Contains(data, []byte("PHONE_1")) || !bytes.Contains(data, []byte(`"phone"`)) {
		t.Fatalf("审计应含占位符与 rule_id：%s", data)
	}
}

func TestAuditFailureBestEffort(t *testing.T) {
	cfg := config.Defaults()
	dir := t.TempDir()
	badPath := filepath.Join(dir, "audit.jsonl")
	if err := os.Mkdir(badPath, 0o755); err != nil {
		t.Fatal(err)
	}
	log := audit.New(badPath)
	out, err := MaskFile("notes.txt", []byte("电话 13812345678"), MaskOptions{}, cfg, log)
	if err != nil {
		t.Fatalf("审计写失败不应阻断脱敏：%v", err)
	}
	if !out.AuditFailed {
		t.Fatal("应标记审计写失败")
	}
	if len(out.MaskedBytes) == 0 {
		t.Fatal("脱敏结果仍应正常交付")
	}
}

func TestMaskRejectsBadExtensionAndSize(t *testing.T) {
	cfg := config.Defaults()
	if _, err := MaskFile("data.docm", []byte("x"), MaskOptions{}, cfg, nil); err == nil {
		t.Fatal("已知不支持格式应被拒绝")
	}
	big := bytes.Repeat([]byte("a"), int(cfg.MaxFileSize)+1)
	if _, err := MaskFile("a.log", big, MaskOptions{}, cfg, nil); err == nil {
		t.Fatal("超限文件应被拒绝")
	}
}

// 1.2 名称放宽不放宽内容门禁：新增后缀、无后缀与点文件走既有 strict 编码链；
// 含 NUL、UTF-16 BOM、坏编码或超限时仍拒绝且不产出任何产物。
func TestMaskNewTextNamesAndContentGates(t *testing.T) {
	cfg := config.Defaults()
	cases := []struct {
		name string
		data []byte
		enc  loader.Encoding
	}{
		{"data.csv", []byte("phone,13812345678\n"), loader.EncodingUTF8},
		{"data.JSON", []byte("{\"phone\":\"13812345678\"}\n"), loader.EncodingUTF8},
		{"config.yaml", []byte("phone: 13812345678\n"), loader.EncodingUTF8},
		{"main.go", []byte("var phone = \"13812345678\"\n"), loader.EncodingUTF8},
		{"notes.xyz", []byte("电话 13812345678\n"), loader.EncodingUTF8},
		{"README", []byte("联系人 13812345678\n"), loader.EncodingUTF8},
		{".env", []byte("PHONE=13812345678\n"), loader.EncodingUTF8},
		{".gitignore", gbk(t, "会议纪要 13812345678\n"), loader.EncodingGBK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := MaskFile(c.name, c.data, MaskOptions{Profile: "strict"}, cfg, nil)
			if err != nil {
				t.Fatalf("新增文本名称应处理：%v", err)
			}
			if out.Encoding != c.enc {
				t.Fatalf("编码应为 %q，got %q", c.enc, out.Encoding)
			}
			if !bytes.Contains(out.MaskedBytes, []byte("[PHONE_1]")) || bytes.Contains(out.MaskedBytes, []byte("13812345678")) {
				t.Fatalf("应按纯文本替换手机号：%q", out.MaskedBytes)
			}
			if out.MaskedName != mapping.MaskedName(c.name) || out.MappingName != mapping.MappingName(c.name) {
				t.Fatalf("产物命名不符：%q %q", out.MaskedName, out.MappingName)
			}
			mp, err := mapping.Unmarshal(out.MappingBytes)
			if err != nil {
				t.Fatalf("mapping 应为合法 JSON：%v", err)
			}
			if mp.Version != 1 || mp.SourceFile != c.name || mp.SourceEncoding != string(c.enc) {
				t.Fatalf("mapping 应保留真实源名/编码：%+v", mp)
			}
		})
	}

	over := bytes.Repeat([]byte("a"), int(cfg.MaxFileSize)+1)
	rejects := []struct {
		name string
		data []byte
	}{
		{"README", []byte("abc\x00def")},
		{"data.xyz", []byte{0xff, 0xfe, 'A', 0x00, 'B', 0x00}},
		{".env", []byte{0xff, 0xff}},
		{"data.xyz", over},
	}
	for _, c := range rejects {
		if out, err := MaskFile(c.name, c.data, MaskOptions{}, cfg, nil); err == nil || out != nil {
			t.Fatalf("%q 坏内容/超限应拒绝且无产物：out=%+v err=%v", c.name, out, err)
		}
	}
}

// 2.1 新增名称 version 1 去回程：mapping 保留真实 source_file、原编码与原指纹；
// 普通后缀原样保留，无后缀与单段点文件产物回退 .txt。
func TestNewTextNamesRoundTripV1(t *testing.T) {
	cfg := config.Defaults()
	cases := []struct {
		name     string
		data     []byte
		enc      loader.Encoding
		masked   string
		mapping  string
		restored string
	}{
		{"data.JSON", []byte("{\"phone\":\"13812345678\"}\n"), loader.EncodingUTF8,
			"data.masked.JSON", "data.masked.mapping.json", "data.restored.JSON"},
		{"README", []byte("联系人 13812345678\n"), loader.EncodingUTF8,
			"README.masked.txt", "README.masked.mapping.json", "README.restored.txt"},
		{".env", gbk(t, "口令 13812345678\n"), loader.EncodingGBK,
			".env.masked.txt", ".env.masked.mapping.json", ".env.restored.txt"},
		{".gitignore", []byte("phone=13812345678\n"), loader.EncodingUTF8,
			".gitignore.masked.txt", ".gitignore.masked.mapping.json", ".gitignore.restored.txt"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := MaskFile(c.name, c.data, MaskOptions{Profile: "strict"}, cfg, nil)
			if err != nil {
				t.Fatalf("脱敏失败：%v", err)
			}
			if out.MaskedName != c.masked || out.MappingName != c.mapping {
				t.Fatalf("产物命名不符：%q %q", out.MaskedName, out.MappingName)
			}
			if out.Encoding != c.enc {
				t.Fatalf("编码应为 %q，got %q", c.enc, out.Encoding)
			}
			mp, err := mapping.Unmarshal(out.MappingBytes)
			if err != nil {
				t.Fatalf("mapping 非法：%v", err)
			}
			if mp.Version != 1 || mp.SourceFile != c.name || mp.SourceEncoding != string(c.enc) || mp.SourceSHA256 != sha256Hex(c.data) {
				t.Fatalf("mapping 应保留真实名称/编码/指纹：%+v", mp)
			}
			res, err := RestoreFile(c.masked, out.MaskedBytes, out.MappingBytes, cfg, nil)
			if err != nil {
				t.Fatalf("回程失败：%v", err)
			}
			if res.Name != c.restored || res.Encoding != c.enc || !bytes.Equal(res.Bytes, c.data) {
				t.Fatalf("回程名称/编码/字节不符：%+v", res)
			}
			if sha256Hex(res.Bytes) != mp.SourceSHA256 {
				t.Fatalf("回程字节指纹应与 mapping 一致")
			}
		})
	}
}

// 1.2 回程沿用同一名称策略与内容门禁：新名称可回程，已知不支持格式在读取前拒绝。
func TestRestoreNewTextNamesAndDeniedFormats(t *testing.T) {
	cfg := config.Defaults()
	orig := []byte("phone,13812345678\n")
	out, err := MaskFile("data.csv", orig, MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := RestoreFile("data.masked.csv", out.MaskedBytes, out.MappingBytes, cfg, nil)
	if err != nil {
		t.Fatalf("新后缀回程应成功：%v", err)
	}
	if res.Name != "data.restored.csv" || !bytes.Equal(res.Bytes, orig) || res.Encoding != loader.EncodingUTF8 {
		t.Fatalf("回程名称/字节/编码不符：%+v", res)
	}
	for _, name := range []string{"reply.rtf", "reply.pptx", "reply.pdf", "reply.zip"} {
		if got, err := RestoreFile(name, []byte("x"), out.MappingBytes, cfg, nil); err == nil || got != nil {
			t.Fatalf("%q 应在回程读取前拒绝：out=%+v err=%v", name, got, err)
		}
	}
}

// TestForwardResidualWarnsButExports 覆盖 5.1：输出仍命中启用规则 → 告警 + 定位计数，不阻断。
// 构造：custom 词恰好是 api_key 的尾部，因 custom 优先级更高而先命中，残留的 api_key 前缀仍可被规则命中。
func TestForwardResidualWarnsButExports(t *testing.T) {
	cfg := config.Defaults()
	in := []byte("token sk-abcdefghijklmnop 结束")
	out, err := MaskFile("a.txt", in, MaskOptions{
		Profile: "balanced", ExtraCustom: []string{"mnop"},
	}, cfg, nil)
	if err != nil {
		t.Fatalf("残留告警不应阻断导出：%v", err)
	}
	if len(out.Warnings) == 0 || out.Warnings[0].Kind != "residual_forward" {
		t.Fatalf("应报告正向残留警告，got %#v", out.Warnings)
	}
	w := out.Warnings[0]
	if w.Count < 1 || w.Line < 1 {
		t.Fatalf("警告应含定位与计数：%#v", w)
	}
	if !strings.Contains(w.Message, "仅验证规则覆盖率") || !strings.Contains(w.Message, "不构成语义级无泄露保证") {
		t.Fatalf("警告必须内联能力边界：%q", w.Message)
	}
	if len(out.MaskedBytes) == 0 {
		t.Fatal("警告不应阻断导出，仍应产出脱敏文件")
	}
}

func TestRestoreAuditRecord(t *testing.T) {
	cfg := config.Defaults()
	log, path := newAudit(t)
	out, err := MaskFile("notes.txt", []byte("电话 13812345678"), MaskOptions{}, cfg, log)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RestoreFile(out.MaskedName, out.MaskedBytes, out.MappingBytes, cfg, log); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"count":1`)) {
		t.Fatalf("回程应记录还原计数：%s", data)
	}
	foundRestore := false
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("审计行应为合法 JSON：%s", line)
		}
		if _, hasRule := rec["rule_id"]; !hasRule {
			_, hasCount := rec["count"]
			_, hasTS := rec["timestamp"]
			_, hasFP := rec["file_fingerprint"]
			if hasCount && hasTS && hasFP {
				foundRestore = true
			}
		}
	}
	if !foundRestore {
		t.Fatalf("回程应记录还原计数/时间戳/指纹：%s", data)
	}
	if bytes.Contains(data, []byte("13812345678")) {
		t.Fatal("审计日志不得含原文")
	}
}

// TestEndToEndWithLLMRewrite 覆盖 8.2：LLM 保留的占位符可还原，被改写的报残留。
func TestEndToEndWithLLMRewrite(t *testing.T) {
	cfg := config.Defaults()
	orig := []byte("请把 13812345678 发给对方")
	out, err := MaskFile("notes.txt", orig, MaskOptions{}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	llmOut := []byte("请把 [PHONE_1] 发给对方，另外 [PHONE_2] 已丢失")
	res, err := RestoreFile("reply.txt", llmOut, out.MappingBytes, cfg, nil)
	if err != nil {
		t.Fatalf("回程不应阻断：%v", err)
	}
	if !bytes.Contains(res.Bytes, []byte("13812345678")) {
		t.Fatalf("保留的占位符应正常还原：%q", res.Bytes)
	}
	if len(res.Warnings) == 0 || res.Warnings[0].Kind != "residual_reverse" {
		t.Fatalf("被改写的占位符应报反向残留：%#v", res.Warnings)
	}
}

// ---- 3.1a 词解析与编码链 ----

func TestParseCustomWordsTrimDedupIgnoreBlank(t *testing.T) {
	got, err := ParseCustomWords([]string{"  内部词  ", "", "内部词", "  ", "第二词"}, nil, false)
	if err != nil {
		t.Fatalf("解析不应报错：%v", err)
	}
	if strings.Join(got, ",") != "内部词,第二词" {
		t.Fatalf("应先 trim、忽略空行、去重，got %#v", got)
	}
}

func TestParseCustomWordsAtomicLineNumber(t *testing.T) {
	long := strings.Repeat("超", detector.MaxWordLength+1)
	_, err := ParseCustomWords([]string{"合法\n" + long}, nil, false)
	if err == nil {
		t.Fatal("文本第 2 行超长应整批拒绝")
	}
	var he *loader.HumanError
	if !errors.As(err, &he) || !strings.Contains(he.Reason, "文本框第 2 行") {
		t.Fatalf("应报文本框第 2 行：%v", err)
	}

	_, err = ParseCustomWords(nil, []byte("ok\n\n"+long+"\n"), true)
	if err == nil {
		t.Fatal("文件第 3 行超长应整批拒绝")
	}
	if !errors.As(err, &he) || !strings.Contains(he.Reason, "词文件第 3 行") {
		t.Fatalf("应报词文件第 3 行：%v", err)
	}
}

func TestParseCustomWordsZeroValid(t *testing.T) {
	if _, err := ParseCustomWords([]string{"   "}, nil, false); err == nil {
		t.Fatal("仅空白输入应报 0 合法词")
	}
	if _, err := ParseCustomWords(nil, []byte("\n\n  \n"), true); err == nil {
		t.Fatal("全空行文件应报 0 合法词")
	}
	got, err := ParseCustomWords(nil, nil, false)
	if err != nil || len(got) != 0 {
		t.Fatalf("完全未提供输入应返回空词表且不报错：%#v %v", got, err)
	}
}

// TestParseCustomWordsTextLineAlignment 覆盖 MINOR 1：
// 文本框按原行号占位提交（空行/未勾选行留空行），服务端行号须与用户所见一致。
func TestParseCustomWordsTextLineAlignment(t *testing.T) {
	long := strings.Repeat("x", detector.MaxWordLength+1)
	// 视觉第 1 行 ok、第 2 行空行、第 3 行非法超长词、第 4 行未勾选占位空行。
	value := "ok\n\n" + long + "\n"
	_, err := ParseCustomWords([]string{value}, nil, false)
	if err == nil {
		t.Fatal("第 3 行非法应整批拒绝")
	}
	var he *loader.HumanError
	if !errors.As(err, &he) || !strings.Contains(he.Reason, "文本框第 3 行") {
		t.Fatalf("占位空行不得使行号错位，应报文本框第 3 行：%v", err)
	}

	// 占位空行不应被当作词：合法占位输入应解析出正确的词与数量。
	got, err := ParseCustomWords([]string{"甲\n\n乙\n"}, nil, false)
	if err != nil || strings.Join(got, ",") != "甲,乙" {
		t.Fatalf("占位空行应忽略，得到 %#v %v", got, err)
	}
}

// TestParseCustomWordsEmptyFileRejected 覆盖 MINOR 2：
// 0 字节真空词文件（filePresent=true）应显式报「0 合法词」，不得静默通过。
func TestParseCustomWordsEmptyFileRejected(t *testing.T) {
	_, err := ParseCustomWords(nil, []byte{}, true)
	if err == nil {
		t.Fatal("0 字节词文件应显式报错")
	}
	var he *loader.HumanError
	if !errors.As(err, &he) || !strings.Contains(he.Reason, "未解析到任何有效自定义词") {
		t.Fatalf("应报 0 合法词：%v", err)
	}
	// 未提交文件字段（filePresent=false）时不应报错。
	if got, err := ParseCustomWords(nil, nil, false); err != nil || len(got) != 0 {
		t.Fatalf("未提交文件不应报错：%#v %v", got, err)
	}
}

func TestParseCustomWordsEncodingChain(t *testing.T) {
	gbkData := gbk(t, "内部词\n第二词\n")
	got, err := ParseCustomWords(nil, gbkData, true)
	if err != nil || strings.Join(got, ",") != "内部词,第二词" {
		t.Fatalf("GBK 词文件应经 loader 链正确解码：%#v %v", got, err)
	}
	if _, err := ParseCustomWords(nil, []byte{'A', 0x00, 'B'}, true); err == nil {
		t.Fatal("含 NUL 的词文件应被拒绝")
	}
	if _, err := ParseCustomWords(nil, []byte{0xFF, 0xFE, 0x41, 0x00}, true); err == nil {
		t.Fatal("UTF-16 BOM 词文件应被拒绝")
	}
}

// ---- 3.1b 三限与双上限 ----

func TestWordFileSizeLimit(t *testing.T) {
	big := bytes.Repeat([]byte("a"), int(MaxWordFileSize)+1)
	_, err := ParseCustomWords(nil, big, true)
	if err == nil {
		t.Fatal("词文件超 1MB 应被拒绝")
	}
	var he *loader.HumanError
	if !errors.As(err, &he) || !strings.Contains(he.Reason, "1MB") {
		t.Fatalf("应报 1MB 上限：%v", err)
	}
}

func TestThisTimeWordCountLimit(t *testing.T) {
	input := make([]string, 0, MaxWordCount+1)
	for i := 0; i < MaxWordCount+1; i++ {
		input = append(input, fmt.Sprintf("w%04d", i))
	}
	_, err := ParseCustomWords(input, nil, false)
	if err == nil {
		t.Fatalf("本次词表超 %d 应被拒绝", MaxWordCount)
	}
	var he *loader.HumanError
	if !errors.As(err, &he) || !strings.Contains(he.Reason, fmt.Sprintf("%d", MaxWordCount)) {
		t.Fatalf("应报词数上限：%v", err)
	}
	words, err := ParseCustomWords(input[:MaxWordCount], nil, false)
	if err != nil || len(words) != MaxWordCount {
		t.Fatalf("恰好 %d 应通过：%d %v", MaxWordCount, len(words), err)
	}
}

func TestMergedWordCountLimit(t *testing.T) {
	base := make([]string, 0, MaxWordCount)
	for i := 0; i < MaxWordCount; i++ {
		base = append(base, fmt.Sprintf("base%04d", i))
	}
	extra := make([]string, 0, MaxWordCount)
	for i := 0; i < MaxWordCount; i++ {
		extra = append(extra, fmt.Sprintf("extra%04d", i))
	}

	cfg := config.Defaults()
	cfg.CustomWords = base
	if _, err := MaskFile("a.txt", []byte("hello"), MaskOptions{Profile: "balanced"}, cfg, nil); err != nil {
		t.Fatalf("恰好 %d 应通过：%v", MaxWordCount, err)
	}

	cfgOver := config.Defaults()
	cfgOver.CustomWords = base
	_, err := MaskFile("a.txt", []byte("hello"), MaskOptions{Profile: "balanced", ExtraCustom: extra}, cfgOver, nil)
	if err == nil {
		t.Fatalf("合并后参与集超 %d 应被拒绝", MaxWordCount)
	}
	var he *loader.HumanError
	if !errors.As(err, &he) {
		t.Fatalf("应为人类可读错误：%T", err)
	}
}

// ---- 3.2 409 分组 ----

func TestPreviewGroupingExtraAndBuiltin(t *testing.T) {
	cfg := config.Defaults()
	cfg.CustomWords = []string{"常备词"}
	text := []byte(strings.Repeat("本次词\n", 25) + strings.Repeat("常备词\n", 30))

	_, err := MaskFile("a.txt", text, MaskOptions{Profile: "balanced", ExtraCustom: []string{"本次词"}}, cfg, nil)
	var pre *PreviewRequiredError
	if !errors.As(err, &pre) {
		t.Fatalf("应要求预览确认，got %v", err)
	}
	if pre.Extra["本次词"] != 25 {
		t.Fatalf("本次词组应含 25 次命中：%#v", pre.Extra)
	}
	if pre.Builtin["常备词"] != 30 {
		t.Fatalf("常备词组应含 30 次命中：%#v", pre.Builtin)
	}
	if _, ok := pre.Extra["常备词"]; ok {
		t.Fatalf("常备词不应落入本次词组：%#v", pre.Extra)
	}
	if !strings.Contains(pre.Error(), "本次词") || !strings.Contains(pre.Error(), "常备词") {
		t.Fatalf("错误文案应标明两组：%q", pre.Error())
	}
}

// ---- 3.3 回程 SHA 参考信号 ----

func TestRestoreSHASignal(t *testing.T) {
	cfg := config.Defaults()
	orig := []byte("请把 13812345678 发给对方")
	out, err := MaskFile("notes.txt", orig, MaskOptions{}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 配对（逐字回填原脱敏文件）：SHA 一致，无 sha 参考提示。
	paired, err := RestoreFile(out.MaskedName, out.MaskedBytes, out.MappingBytes, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(paired.Bytes, orig) {
		t.Fatalf("配对还原应字节级等于原文：%q", paired.Bytes)
	}
	for _, w := range paired.Warnings {
		if w.Kind == "sha_mismatch" {
			t.Fatalf("配对还原不应触发 SHA 参考提示：%#v", w)
		}
	}

	// 配对 mapping 但正文被改写：占位符正常还原，仅给不阻断的「无法确证一致」提示。
	rewritten := []byte("好的，已转达 [PHONE_1] 给对方。")
	res, err := RestoreFile("reply.txt", rewritten, out.MappingBytes, cfg, nil)
	if err != nil {
		t.Fatalf("SHA 不一致不应阻断：%v", err)
	}
	if !bytes.Contains(res.Bytes, []byte("13812345678")) {
		t.Fatalf("占位符应正常还原：%q", res.Bytes)
	}
	var hit *Warning
	for i := range res.Warnings {
		if res.Warnings[i].Kind == "sha_mismatch" {
			hit = &res.Warnings[i]
		}
		if res.Warnings[i].Kind == "residual_reverse" {
			t.Fatalf("正文改写但占位符已还原，不应报残留：%#v", res.Warnings)
		}
	}
	if hit == nil {
		t.Fatalf("正文改写应给出 SHA 参考提示：%#v", res.Warnings)
	}
	if !strings.Contains(hit.Message, "无法确证一致") {
		t.Errorf("提示应表述为无法确证一致：%q", hit.Message)
	}
	if strings.Contains(hit.Message, "疑似") {
		t.Errorf("提示不得断言拿错 mapping：%q", hit.Message)
	}
}
