package loader

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// deniedExtensions 是本变更确认的已知不支持格式完整清单；
// 与 loader.deniedExts 双向一致，防止集合静默增减。
var deniedExtensions = []string{
	".pdf", ".doc", ".docm", ".dot", ".dotx", ".dotm",
	".odt", ".ott", ".fodt", ".rtf",
	".ppt", ".pptx", ".pptm", ".pot", ".potx", ".potm",
	".pps", ".ppsx", ".ppsm", ".odp", ".otp", ".fodp",
	".xls", ".xlsx", ".xlsm", ".xlsb", ".xlt", ".xltx", ".xltm",
	".ods", ".ots", ".fods",
	".pages", ".numbers", ".key",
	".zip", ".7z", ".rar", ".gz", ".bz2", ".xz", ".tar", ".tgz",
	".exe", ".dll", ".so", ".dylib", ".bin",
	".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".ico", ".tif", ".tiff",
}

// 拒绝集合必须与实现完全一致：多一个或少一个都视为策略漂移。
func TestDeniedExtensionTableComplete(t *testing.T) {
	want := make(map[string]bool, len(deniedExtensions))
	for _, ext := range deniedExtensions {
		if want[ext] {
			t.Errorf("测试清单重复 %q", ext)
		}
		want[ext] = true
		if !deniedExts[ext] {
			t.Errorf("实现拒绝集合缺少 %q", ext)
		}
	}
	for ext := range deniedExts {
		if !want[ext] {
			t.Errorf("实现拒绝集合多出未审定后缀 %q", ext)
		}
	}
}

// 名称分类：DOCX 优先、有限拒绝、其余纯文本候选（含无后缀与点文件）。
func TestClassifyNameTable(t *testing.T) {
	text := []string{
		"notes.txt", "A.TXT", "b.md", "c.LOG", "d.py", "e.Js",
		"data.csv", "data.JSON", "config.yaml", "main.go", "notes.xyz", "notes.txt.bak",
		"README", "readme", "noext", ".env", ".gitignore", ".env.local", ".dockerignore",
	}
	for _, name := range text {
		if got := ClassifyName(name); got != NameKindText {
			t.Errorf("ClassifyName(%q)=%q，期望纯文本候选", name, got)
		}
		if !IsTextCandidate(name) || !IsDocumentCandidate(name) {
			t.Errorf("%q 应同时为文本候选与文档候选", name)
		}
	}
	for _, name := range []string{"f.docx", "G.DOCX", ".docx", "合同.Docx"} {
		if got := ClassifyName(name); got != NameKindDocx {
			t.Errorf("ClassifyName(%q)=%q，期望 DOCX", name, got)
		}
		if !IsDocxName(name) || !IsDocumentCandidate(name) || IsTextCandidate(name) {
			t.Errorf("%q 应仅进 DOCX 流程", name)
		}
	}
	for _, name := range []string{"data.pdf", "notes.RTF", "a.tar.gz", "X.PPTX", ".ENV.LOCAL.RTF"} {
		if got := ClassifyName(name); got != NameKindDenied {
			t.Errorf("ClassifyName(%q)=%q，期望拒绝", name, got)
		}
		if IsDocumentCandidate(name) || IsTextCandidate(name) || IsDocxName(name) {
			t.Errorf("%q 不应为任何入口候选", name)
		}
	}
}

// 名称门禁：旧/新后缀、无后缀与点文件放行；拒绝集合大小写全表拒绝。
func TestValidateFileNamePolicy(t *testing.T) {
	accept := []string{
		"a.txt", "A.TXT", "b.md", "c.LOG", "d.py", "e.Js", "f.docx", "G.DOCX",
		"data.csv", "data.JSON", "config.yaml", "main.go", "notes.xyz",
		"README", "readme", "noext", ".env", ".gitignore", ".env.local",
	}
	for _, name := range accept {
		if err := ValidateFileName(name); err != nil {
			t.Errorf("期望接受 %q，却报错 %v", name, err)
		}
	}
	for _, ext := range deniedExtensions {
		for _, name := range []string{"data" + ext, "data" + strings.ToUpper(ext), "mixed" + strings.ToUpper(ext[:1]) + ext[1:]} {
			err := ValidateFileName(name)
			if err == nil {
				t.Errorf("期望拒绝 %q", name)
				continue
			}
			var he *HumanError
			if !errors.As(err, &he) || he.MessageID != "file.unsupported_format" {
				t.Errorf("%q 应返回 file.unsupported_format，got %#v", name, err)
			}
		}
	}
}

func TestValidateSizeLimit(t *testing.T) {
	if err := ValidateSize(DefaultMaxSize, DefaultMaxSize); err != nil {
		t.Errorf("恰好等于上限应通过，got %v", err)
	}
	if err := ValidateSize(DefaultMaxSize+1, DefaultMaxSize); err == nil {
		t.Error("超过上限应被拒绝")
	}
	if err := ValidateSize(5, 0); err != nil {
		t.Errorf("limit<=0 应回退默认，got %v", err)
	}
}

func TestDecodeBinaryRenamedRejected(t *testing.T) {
	exe := []byte{0x4d, 0x5a, 0x90, 0x00, 0x03, 0x00, 0x00, 0x00}
	if _, _, err := Decode(exe); err == nil {
		t.Fatal("改名二进制应被 NUL 启发式拒绝")
	}
	utf16 := []byte{0xff, 0xfe, 0x41, 0x00, 0x42, 0x00}
	if _, _, err := Decode(utf16); err == nil {
		t.Fatal("UTF-16 BOM 应被拒绝")
	}
}

func TestDecodeGBKStrict(t *testing.T) {
	src := "会议纪要：联系人 13812345678"
	gbk, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(src))
	if err != nil {
		t.Fatalf("构造 GBK 数据失败：%v", err)
	}
	enc, text, err := Decode(gbk)
	if err != nil {
		t.Fatalf("GBK 应能解码，got %v", err)
	}
	if enc != EncodingGBK {
		t.Fatalf("期望检出 GBK，got %q", enc)
	}
	if text != src {
		t.Fatalf("解码结果不一致：%q", text)
	}
}

func TestDecodeGarbledRejected(t *testing.T) {
	if _, _, err := Decode([]byte{0xff, 0xff}); err == nil {
		t.Fatal("不可解码的字节应被拒绝，而不是宽容吞掉")
	}
}

func TestLinePreservation(t *testing.T) {
	in := "a\r\nb\nc\rd"
	lines := SplitLines(in)
	if len(lines) != 3 {
		t.Fatalf("孤立 CR 不应切行，期望 3 行，got %d：%#v", len(lines), lines)
	}
	if lines[0].Text != "a" || lines[0].Term != "\r\n" {
		t.Errorf("第 1 行应为 CRLF：%#v", lines[0])
	}
	if lines[1].Text != "b" || lines[1].Term != "\n" {
		t.Errorf("第 2 行应为 LF：%#v", lines[1])
	}
	if lines[2].Text != "c\rd" || lines[2].Term != "" {
		t.Errorf("孤立 CR 应保留在行内：%#v", lines[2])
	}
	if JoinLines(lines) != in {
		t.Fatalf("逐行还原后应与原文一致：%q", JoinLines(lines))
	}
}

func TestLinePreservationTrailingNewline(t *testing.T) {
	in := "x\n"
	lines := SplitLines(in)
	if len(lines) != 1 || lines[0].Term != "\n" {
		t.Fatalf("末尾换行不应多出空行：%#v", lines)
	}
	if JoinLines(lines) != in {
		t.Fatalf("还原不一致：%q", JoinLines(lines))
	}
	if got := JoinLines(SplitLines("")); got != "" {
		t.Fatalf("空输入应还原为空，got %q", got)
	}
}

func TestLoadEndToEnd(t *testing.T) {
	doc, err := Load([]byte("hello\n世界\n"), DefaultMaxSize)
	if err != nil {
		t.Fatalf("Load 失败：%v", err)
	}
	if doc.Encoding != EncodingUTF8 {
		t.Fatalf("期望 UTF-8，got %q", doc.Encoding)
	}
	if !strings.HasSuffix(JoinLines(doc.Lines), "\n") {
		t.Fatal("末尾换行应保留")
	}
}
