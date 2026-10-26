package pptx

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/pandatoufu/data-masking/internal/pptxtest"
)

func TestOpenTransitionalStrictAndCustomPaths(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"transitional", pptxtest.Minimal("标题")},
		{"strict", pptxtest.StrictRich()},
		{"custom-prefix", pptxtest.Build(pptxtest.Options{Prefix: "x", Slides: []pptxtest.Slide{{Title: "自定义前缀"}}})},
		{"custom-main-path", pptxtest.Build(pptxtest.Options{MainPath: "deck/main.xml", Slides: []pptxtest.Slide{{Title: "自定义主路径"}}})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc := openFixture(t, c.data)
			if _, err := doc.Prepare(); err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			if len(doc.Units()) == 0 {
				t.Fatal("no units")
			}
		})
	}
}

func TestOpenRejectsNonPackage(t *testing.T) {
	_, err := Open([]byte("plain text renamed to pptx"), DefaultLimits())
	if err == nil || !strings.Contains(err.Error(), "PPTX") {
		t.Fatalf("expected not-pptx error, got %v", err)
	}
	ole := append([]byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, make([]byte, 16)...)
	if _, err := Open(ole, DefaultLimits()); err == nil || !strings.Contains(err.Error(), "加密") {
		t.Fatalf("expected encrypted error, got %v", err)
	}
}

func TestOpenRejectsMissingTarget(t *testing.T) {
	data := mutate(t, pptxtest.Minimal("x"), func(parts map[string][]byte) {
		delete(parts, "ppt/slides/slide1.xml")
	})
	if _, err := Open(data, DefaultLimits()); err == nil {
		t.Fatal("expected missing target error")
	}
}

func TestOpenRejectsDuplicateRelationshipID(t *testing.T) {
	data := mutate(t, pptxtest.Minimal("x"), func(parts map[string][]byte) {
		parts["ppt/slides/_rels/slide1.xml.rels"] = []byte(`<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="/ppt/media/x.png"/><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="/ppt/media/x.png"/></Relationships>`)
		parts["ppt/media/x.png"] = []byte("x")
	})
	if _, err := Open(data, DefaultLimits()); err == nil {
		t.Fatal("expected duplicate relationship error")
	}
}

func TestOpenRejectsDuplicateAndAliasPartNames(t *testing.T) {
	base := readZip(t, pptxtest.Minimal("x"))
	base["ppt/slides/../slides/slide1.xml"] = base["ppt/slides/slide1.xml"]
	if _, err := Open(writeZip(t, base), DefaultLimits()); err == nil {
		t.Fatal("expected alias part name rejection")
	}

	dup := readZip(t, pptxtest.Minimal("x"))
	dup["ppt/slides/slide1.xml"] = dup["ppt/slides/slide1.xml"]
	// 修改大小写只为触发大小写不敏感重复检查。
	dup["ppt/slides/SLIDE1.XML"] = dup["ppt/slides/slide1.xml"]
	if _, err := Open(writeZip(t, dup), DefaultLimits()); err == nil {
		t.Fatal("expected duplicate part name rejection")
	}
}

func TestOpenRejectsMacroAndSignature(t *testing.T) {
	macro := mutate(t, pptxtest.Minimal("x"), func(parts map[string][]byte) {
		replaceInPart(t, parts, "[Content_Types].xml",
			"application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml",
			"application/vnd.ms-powerpoint.presentation.macroEnabled.main+xml")
	})
	if _, err := Open(macro, DefaultLimits()); err == nil || !strings.Contains(err.Error(), "宏") {
		t.Fatalf("expected macro rejection, got %v", err)
	}

	sig := mutate(t, pptxtest.Minimal("x"), func(parts map[string][]byte) {
		parts["_xmlsignatures/sig1.xml"] = []byte("<Signature/>")
		replaceInPart(t, parts, "[Content_Types].xml", "</Types>",
			`<Override PartName="/_xmlsignatures/sig1.xml" ContentType="application/vnd.openxmlformats-package.digital-signature-xmlsignature+xml"/></Types>`)
	})
	if _, err := Open(sig, DefaultLimits()); err == nil || !strings.Contains(err.Error(), "数字签名") {
		t.Fatalf("expected signature rejection, got %v", err)
	}
}

func TestOpenRejectsUnknownCommentFamily(t *testing.T) {
	unknownCT := mutate(t, pptxtest.Minimal("x"), func(parts map[string][]byte) {
		parts["ppt/comments/odd.xml"] = []byte("<odd/>")
		replaceInPart(t, parts, "[Content_Types].xml", "</Types>",
			`<Override PartName="/ppt/comments/odd.xml" ContentType="application/vnd.contoso.comments+xml"/></Types>`)
	})
	if _, err := Open(unknownCT, DefaultLimits()); err == nil || !strings.Contains(err.Error(), "批注家族") {
		t.Fatalf("expected unknown comment content type rejection, got %v", err)
	}

	unknownRel := mutate(t, pptxtest.Minimal("x"), func(parts map[string][]byte) {
		parts["ppt/slides/_rels/slide1.xml.rels"] = []byte(`<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rIdOdd" Type="http://schemas.contoso.com/relationships/comments2" Target="/ppt/comments/odd.xml"/></Relationships>`)
		parts["ppt/comments/odd.xml"] = []byte("<odd/>")
	})
	if _, err := Open(unknownRel, DefaultLimits()); err == nil || !strings.Contains(err.Error(), "批注家族") {
		t.Fatalf("expected unknown comment rel rejection, got %v", err)
	}
}

func TestOpenRejectsModernAnchorMarkup(t *testing.T) {
	data := mutate(t, pptxtest.Minimal("x"), func(parts map[string][]byte) {
		replaceInPart(t, parts, "ppt/slides/slide1.xml", `<p:grpSpPr/>`,
			`<p:grpSpPr/><p:extLst><p:ext uri="{X}"><p188:cm xmlns:p188="http://schemas.microsoft.com/office/powerpoint/2018/8/main"/></p:ext></p:extLst>`)
	})
	if _, err := Open(data, DefaultLimits()); err == nil || !strings.Contains(err.Error(), "批注家族") {
		t.Fatalf("expected modern anchor rejection, got %v", err)
	}
}

func TestOpenBudgets(t *testing.T) {
	data := pptxtest.Rich()
	cases := []struct {
		name   string
		limits Limits
	}{
		{"entries", Limits{MaxEntries: 5}},
		{"total", Limits{MaxTotalUncompressed: 200}},
		{"xml-size", Limits{MaxXMLPart: 100}},
		{"depth", Limits{MaxDepth: 4}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Open(data, c.limits); err == nil {
				t.Fatalf("expected budget rejection for %s", c.name)
			}
		})
	}
}

func TestOpenRejectsCorruptCRCAndUnsupportedCompression(t *testing.T) {
	valid := pptxtest.Minimal("x")
	zr, err := zip.NewReader(bytes.NewReader(valid), int64(len(valid)))
	if err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), valid...)
	for _, f := range zr.File {
		if f.Name != "ppt/slides/slide1.xml" {
			continue
		}
		off, err := f.DataOffset()
		if err != nil {
			t.Fatal(err)
		}
		if off+2 >= int64(len(corrupt)) {
			t.Fatal("bad data offset")
		}
		corrupt[off+2] ^= 0xFF
	}
	if _, err := Open(corrupt, DefaultLimits()); err == nil {
		t.Fatal("expected CRC rejection")
	}

	var buf2 bytes.Buffer
	zw2 := zip.NewWriter(&buf2)
	zw2.RegisterCompressor(93, func(w io.Writer) (io.WriteCloser, error) { return nopWriteCloser{w}, nil })
	w, err := zw2.CreateHeader(&zip.FileHeader{Name: "x.xml", Method: 93})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("<x/>"))
	_ = zw2.Close()
	if _, err := Open(buf2.Bytes(), DefaultLimits()); err == nil {
		t.Fatal("expected unsupported compression rejection")
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func TestOpenRejectsTruncatedZIP(t *testing.T) {
	data := pptxtest.Minimal("x")
	if _, err := Open(data[:len(data)/2], DefaultLimits()); err == nil {
		t.Fatal("expected truncated zip rejection")
	}
	if _, err := Open(nil, DefaultLimits()); err == nil {
		t.Fatal("expected empty input rejection")
	}
}
