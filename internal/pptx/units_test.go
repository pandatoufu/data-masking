package pptx

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/pandatoufu/data-masking/internal/pptxtest"
)

func findUnit(doc *Document, text string) TextUnit {
	for _, u := range doc.Units() {
		if u.Text == text {
			return u
		}
	}
	return TextUnit{Index: -1}
}

func prepareFixture(t *testing.T, data []byte) *Document {
	t.Helper()
	doc := openFixture(t, data)
	if _, err := doc.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	return doc
}

func TestUnitsHardBoundaries(t *testing.T) {
	data := pptxtest.Build(pptxtest.Options{Slides: []pptxtest.Slide{{
		Title:     "标题",
		Body:      []string{"", "第二段"},
		Table:     [][]string{{"AAA", "BBB"}},
		SplitRuns: []string{"138", "00138000"},
		Field:     "9",
		Choice:    "Choice 值",
		Fallback:  "Fallback 值",
	}}})
	doc := prepareFixture(t, data)
	texts := map[string]bool{}
	for _, u := range doc.Units() {
		texts[u.Text] = true
	}
	if texts["AAABBB"] || texts["BBBAAA"] {
		t.Fatalf("adjacent table cells were concatenated: %#v", texts)
	}
	if !texts["AAA"] || !texts["BBB"] {
		t.Fatalf("table cells missing: %#v", texts)
	}
	if !texts["13800138000"] {
		t.Fatalf("cross-run text missing: %#v", texts)
	}
	if texts["页码 9 结束"] {
		t.Fatalf("field crossed hard boundary: %#v", texts)
	}
	if !texts["页码 "] || !texts["9"] || !texts[" 结束"] {
		t.Fatalf("field display not separate: %#v", texts)
	}
	if !texts["Choice 值"] || !texts["Fallback 值"] || texts["Choice 值Fallback 值"] {
		t.Fatalf("compatibility branches not isolated: %#v", texts)
	}
	// 空段仍占据段落编号：标题 P1、空正文 P2、第二段 P3。
	for _, u := range doc.Units() {
		if u.Text == "第二段" && u.Paragraph != 3 {
			t.Fatalf("empty paragraph numbering wrong: %+v", u)
		}
	}
}

func TestUnitsUnreferencedSlideAndSharedMaster(t *testing.T) {
	data := pptxtest.Build(pptxtest.Options{Slides: []pptxtest.Slide{
		{Title: "第一页", Notes: []string{"备注一"}},
		{Title: "第二页", Notes: []string{"备注二"}},
	}})
	// 从主序列移除第二页，但保留其部件：仍应被覆盖。
	data = mutate(t, data, func(parts map[string][]byte) {
		replaceInPart(t, parts, "ppt/presentation.xml", `<p:sldId id="257" r:id="rId2"/>`, "")
	})
	doc := prepareFixture(t, data)
	texts := map[string]bool{}
	masterCount := 0
	for _, u := range doc.Units() {
		texts[u.Text] = true
		if u.Part == "ppt/slideMasters/slideMaster1.xml" {
			masterCount++
		}
	}
	if !texts["第二页"] || !texts["备注二"] {
		t.Fatalf("unreferenced slide/notes not covered: %#v", texts)
	}
	if masterCount != 1 {
		t.Fatalf("shared master counted %d times", masterCount)
	}
}

func TestUnitsRejectUnenumerableText(t *testing.T) {
	data := mutate(t, pptxtest.Minimal("x"), func(parts map[string][]byte) {
		replaceInPart(t, parts, "ppt/slides/slide1.xml", `<p:grpSpPr/>`, `<p:grpSpPr/><a:t>orphan</a:t>`)
	})
	doc := openFixture(t, data)
	if _, err := doc.Prepare(); err == nil || !strings.Contains(err.Error(), "无法可靠枚举") {
		t.Fatalf("expected unenumerable text rejection, got %v", err)
	}
}

func TestFingerprintFixedVector(t *testing.T) {
	units := []TextUnit{{
		Part: "ppt/slides/slide1.xml", Paragraph: 1, Segment: 1, Text: "A",
	}}
	got := TextFingerprint(units)
	var want []byte
	want = appendLP(want, []byte("pptx-text-v1"))
	var cnt [8]byte
	binary.BigEndian.PutUint64(cnt[:], 1)
	want = append(want, cnt[:]...)
	want = appendLP(want, []byte("ppt/slides/slide1.xml"))
	want = appendLP(want, []byte("1"))
	want = appendLP(want, []byte("1"))
	want = appendLP(want, []byte("A"))
	if string(got) != string(want) {
		t.Fatalf("unexpected pptx-text-v1 byte layout")
	}
	sum := sha256.Sum256(want)
	if TextSHA256(units) != hex.EncodeToString(sum[:]) {
		t.Fatalf("fingerprint SHA mismatch")
	}
	if len(TextSHA256(units)) != 64 {
		t.Fatalf("fingerprint must be 64 lowercase hex")
	}
}

func TestFingerprintIgnoresRunSplitButNotOrder(t *testing.T) {
	a := []TextUnit{{Part: "p", Paragraph: 1, Segment: 1, Text: "13800138000", pieces: []unitPiece{{text: "138"}, {text: "00138000"}}}}
	b := []TextUnit{{Part: "p", Paragraph: 1, Segment: 1, Text: "13800138000", pieces: []unitPiece{{text: "13800138000"}}}}
	if TextSHA256(a) != TextSHA256(b) {
		t.Fatal("run split changed fingerprint")
	}
	c := []TextUnit{{Part: "p", Paragraph: 1, Segment: 1, Text: "A"}, {Part: "p", Paragraph: 1, Segment: 2, Text: "B"}}
	d := []TextUnit{{Part: "p", Paragraph: 1, Segment: 1, Text: "B"}, {Part: "p", Paragraph: 1, Segment: 2, Text: "A"}}
	if TextSHA256(c) == TextSHA256(d) {
		t.Fatal("unit order must affect fingerprint")
	}
	e := []TextUnit{{Part: "q", Paragraph: 1, Segment: 1, Text: "A"}}
	if TextSHA256(c) == TextSHA256(e) {
		t.Fatal("part name must affect fingerprint")
	}
}
