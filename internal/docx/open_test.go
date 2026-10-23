package docx

import (
	"archive/zip"
	"bytes"
	"errors"
	"hash/crc32"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/pandatoufu/data-masking/internal/docxtest"
	"github.com/pandatoufu/data-masking/internal/loader"
)

func craftZip(t *testing.T, name string, method, flags uint16, content []byte, declared uint64) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fh := &zip.FileHeader{Name: name, Method: method, Flags: flags, Modified: time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)}
	fh.CRC32 = crc32.ChecksumIEEE(content)
	fh.UncompressedSize64 = declared
	fh.CompressedSize64 = uint64(len(content))
	w, err := zw.CreateRaw(fh)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// 2.1 预算边界、伪造声明大小、CRC、加密旗标、压缩方法。
func TestZipBudgetAndIntegrity(t *testing.T) {
	valid := fullFixture(t, false)

	// 条目数边界：恰好等于上限通过，超过拒绝。
	lim := DefaultLimits()
	lim.MaxEntries = len(mustEntries(t, valid))
	if _, err := Open(valid, lim); err != nil {
		t.Fatalf("恰好等于条目上限应通过：%v", err)
	}
	lim.MaxEntries = len(mustEntries(t, valid)) - 1
	if _, err := Open(valid, lim); err == nil {
		t.Fatal("超过条目上限应拒绝")
	}

	// 总解压预算：设极小上限应拒绝。
	lim = DefaultLimits()
	lim.MaxTotalUncompressed = 16
	if _, err := Open(valid, lim); err == nil {
		t.Fatal("超过总解压预算应拒绝")
	}

	// 单 XML 上限：设极小上限应拒绝。
	lim = DefaultLimits()
	lim.MaxXMLPart = 8
	if _, err := Open(valid, lim); err == nil {
		t.Fatal("超过单 XML 上限应拒绝")
	}

	// 伪造声明大小。
	forged := craftZip(t, "x", zip.Store, 0, []byte("hello"), 100)
	if _, err := Open(forged, DefaultLimits()); err == nil {
		t.Fatal("伪造声明大小应拒绝")
	}

	// CRC 错误：翻转某条目的压缩数据字节。
	corrupt := corruptFirstEntryData(t, valid)
	if _, err := Open(corrupt, DefaultLimits()); err == nil {
		t.Fatal("CRC 错误应拒绝")
	}

	// 加密旗标。
	enc := craftZip(t, "x", zip.Store, 0x1, []byte("hello"), 5)
	if _, err := Open(enc, DefaultLimits()); err == nil {
		t.Fatal("加密旗标应拒绝")
	}

	// 不支持的压缩方法。
	unsup := craftZip(t, "x", 99, 0, []byte("hello"), 5)
	if _, err := Open(unsup, DefaultLimits()); err == nil {
		t.Fatal("不支持的压缩方法应拒绝")
	}

	// Store 与 Deflate 均接受。
	if _, err := Open(recompressStore(t, valid), DefaultLimits()); err != nil {
		t.Fatalf("Store 方法应被接受：%v", err)
	}
}

func mustEntries(t *testing.T, data []byte) []*zip.File {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	return zr.File
}

func corruptFirstEntryData(t *testing.T, data []byte) []byte {
	t.Helper()
	out := append([]byte(nil), data...)
	i := bytes.Index(out, []byte("PK\x03\x04"))
	if i < 0 {
		t.Fatal("找不到本地头")
	}
	nameLen := int(out[i+26]) | int(out[i+27])<<8
	extraLen := int(out[i+28]) | int(out[i+29])<<8
	dataStart := i + 30 + nameLen + extraLen
	if dataStart+2 >= len(out) {
		t.Fatal("数据区太短")
	}
	out[dataStart+1] ^= 0xFF
	return out
}

func recompressStore(t *testing.T, data []byte) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	fixed := time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		fh := &zip.FileHeader{Name: f.Name, Method: zip.Store, Modified: fixed}
		w, _ := zw.CreateHeader(fh)
		w.Write(content)
	}
	zw.Close()
	return buf.Bytes()
}

// 2.2 OPC 部件名规范化、别名、非法路径与关系 URI 解析。
func TestOPCPathAndAliasGates(t *testing.T) {
	badNames := []string{"/abs.xml", "word\\bad.xml", "word/../evil.xml", "word/a/../b.xml", "word//double.xml"}
	for _, name := range badNames {
		b := docxtest.NewBuilder(false)
		b.SetMain("word/document.xml")
		b.AddPart("word/document.xml", ctMain, b.WMLDocument(docxtest.Para("x"), nil))
		b.AddPart(name, "application/xml", []byte("<x/>"))
		if _, err := Open(b.Bytes(), DefaultLimits()); err == nil {
			t.Errorf("非法部件名应拒绝：%q", name)
		}
	}

	// 别名/大小写重复。
	b := docxtest.NewBuilder(false)
	b.SetMain("word/document.xml")
	b.AddPart("word/document.xml", ctMain, b.WMLDocument(docxtest.Para("x"), nil))
	b.AddPart("Word/Document.xml", ctMain, b.WMLDocument(docxtest.Para("y"), nil))
	if _, err := Open(b.Bytes(), DefaultLimits()); err == nil {
		t.Fatal("别名/大小写重复部件应拒绝")
	}

	// 合法包内相对 ../ 关系可解析。
	b = docxtest.NewBuilder(false)
	b.AddPart("word/document.xml", ctMain, b.WMLDocument(docxtest.Para("x"), nil))
	b.SetMain("word/document.xml")
	b.AddPart("word/header1.xml", ctHeader, b.WMLPart("hdr", docxtest.Para("页眉")))
	b.AddRel("word/document.xml", "rIdH", b.RelNS()+"/header", "../word/header1.xml", false)
	doc, err := Open(b.Bytes(), DefaultLimits())
	if err != nil {
		t.Fatalf("合法相对 ../ 关系应可解析：%v", err)
	}
	if !doc.HasPart("word/header1.xml") {
		t.Fatal("页眉部件应存在")
	}
}

// 2.3 content type、唯一主文档、关系图与外部链接。
func TestOPCContentTypesAndGraph(t *testing.T) {
	// 自定义主部件路径。
	b := docxtest.NewBuilder(false)
	b.AddPart("custom/doc.xml", ctMain, b.WMLDocument(docxtest.Para("自定义主部件"), nil))
	b.SetMain("custom/doc.xml")
	doc, err := Open(b.Bytes(), DefaultLimits())
	if err != nil {
		t.Fatalf("自定义主部件路径应通过：%v", err)
	}
	if doc.MainPart() != "custom/doc.xml" {
		t.Fatalf("主部件路径错误：%q", doc.MainPart())
	}

	// 重复关系 ID。
	b = docxtest.NewBuilder(false)
	b.AddPart("word/document.xml", ctMain, b.WMLDocument(docxtest.Para("x"), nil))
	b.SetMain("word/document.xml")
	b.AddRel("word/document.xml", "rIdX", b.RelNS()+"/header", "header1.xml", false)
	b.AddRel("word/document.xml", "rIdX", b.RelNS()+"/header", "header1.xml", false)
	if _, err := Open(b.Bytes(), DefaultLimits()); err == nil {
		t.Fatal("重复关系 ID 应拒绝")
	}

	// 缺失内部目标。
	b = docxtest.NewBuilder(false)
	b.AddPart("word/document.xml", ctMain, b.WMLDocument(docxtest.Para("x"), nil))
	b.SetMain("word/document.xml")
	b.AddRel("word/document.xml", "rIdM", b.RelNS()+"/header", "missing.xml", false)
	if _, err := Open(b.Bytes(), DefaultLimits()); err == nil {
		t.Fatal("缺失内部关系目标应拒绝")
	}

	// 外部超链接保留且不解引用。
	b = docxtest.NewBuilder(false)
	b.AddPart("word/document.xml", ctMain, b.WMLDocument(`<w:p><w:hyperlink r:id="rIdL"><w:r><w:t>链接</w:t></w:r></w:hyperlink></w:p>`, nil))
	b.SetMain("word/document.xml")
	b.AddRel("word/document.xml", "rIdL", b.RelNS()+"/hyperlink", "https://example.com/secret", true)
	doc, err = Open(b.Bytes(), DefaultLimits())
	if err != nil {
		t.Fatalf("外部超链接应可处理：%v", err)
	}
	found := false
	for _, r := range doc.rels["word/document.xml"] {
		if r.ID == "rIdL" && r.External {
			found = true
		}
	}
	if !found {
		t.Fatal("外部关系应保留为 External")
	}
}

// 2.6 加密 Word、宏、数字签名及错误包的中文反馈。
func TestRejectEncryptedMacroSignature(t *testing.T) {
	// 加密 OLE 容器。
	ole := append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, make([]byte, 64)...)
	_, err := Open(ole, DefaultLimits())
	assertHumanError(t, err, "加密")

	// 宏内容。
	b := docxtest.NewBuilder(false)
	b.AddPart("word/document.xml", ctMain, b.WMLDocument(docxtest.Para("x"), nil))
	b.SetMain("word/document.xml")
	b.AddPart("word/vbaProject.bin", ctVbaProject, []byte("macro"))
	_, err = Open(b.Bytes(), DefaultLimits())
	assertHumanError(t, err, "宏")

	// 数字签名。
	b = docxtest.NewBuilder(false)
	b.AddPart("word/document.xml", ctMain, b.WMLDocument(docxtest.Para("x"), nil))
	b.SetMain("word/document.xml")
	b.AddPart("_xmlsignatures/sig1.xml", ctDigitalSigXML, []byte("<sig/>"))
	_, err = Open(b.Bytes(), DefaultLimits())
	assertHumanError(t, err, "签名")

	// 纯文本改名。
	_, err = Open([]byte("这不是一个 ZIP 文档，只是普通文本内容。"), DefaultLimits())
	assertHumanError(t, err, "DOCX")
}

func assertHumanError(t *testing.T, err error, contains string) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望拒绝（含 %q）", contains)
	}
	var he *loader.HumanError
	if !errors.As(err, &he) {
		t.Fatalf("错误应为人类可读错误：%T %v", err, err)
	}
	if !strings.Contains(he.Error(), contains) {
		t.Fatalf("错误文案应含 %q：%v", contains, he)
	}
	if he.Advice == "" {
		t.Fatalf("错误应给出下一步做法：%+v", he)
	}
}
