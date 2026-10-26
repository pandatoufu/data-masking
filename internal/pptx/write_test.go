package pptx

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pandatoufu/data-masking/internal/pptxtest"
)

func replaceUnit(t *testing.T, doc *Document, old, replacement string) {
	t.Helper()
	u := findUnit(doc, old)
	if u.Index < 0 {
		t.Fatalf("unit %q not found", old)
	}
	if err := doc.Apply(u, []Edit{{Start: 0, End: len(u.Text), Replacement: replacement}}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
}

func TestApplyCrossRunAndRoundTrip(t *testing.T) {
	doc := prepareFixture(t, pptxtest.Minimal("标题 13800138000"))
	replaceUnit(t, doc, "标题 13800138000", "[PHONE_1]")
	out, err := doc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	if err := doc.ValidateOutput(out); err != nil {
		t.Fatalf("ValidateOutput: %v", err)
	}
	reopened, err := Open(out, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Prepare(); err != nil {
		t.Fatal(err)
	}
	if findUnit(reopened, "[PHONE_1]").Index < 0 {
		t.Fatal("replacement not present after reopen")
	}
	doc2 := prepareFixture(t, pptxtest.Build(pptxtest.Options{Slides: []pptxtest.Slide{{SplitRuns: []string{"1380", "0138000"}}}}))
	replaceUnit(t, doc2, "13800138000", "[PHONE_1]")
	out2, err := doc2.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	sl := string(partFromZip(t, out2, "ppt/slides/slide1.xml"))
	if !strings.Contains(sl, "<a:t>[PHONE_1]</a:t>") {
		t.Fatalf("first run did not receive placeholder: %s", sl)
	}
	if strings.Contains(sl, "0138000") {
		t.Fatalf("covered second run text not cleared: %s", sl)
	}
	if err := doc2.ValidateOutput(out2); err != nil {
		t.Fatal(err)
	}
}

func TestApplyKeepsStylesAndEscapes(t *testing.T) {
	doc := prepareFixture(t, pptxtest.Build(pptxtest.Options{Slides: []pptxtest.Slide{{SplitRuns: []string{"A", "B"}}}}))
	u := findUnit(doc, "AB")
	if err := doc.Apply(u, []Edit{{Start: 0, End: 2, Replacement: `A&B <C>`}}); err != nil {
		t.Fatal(err)
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	xml := string(partFromZip(t, out, "ppt/slides/slide1.xml"))
	if !strings.Contains(xml, "A&amp;B &lt;C&gt;") {
		t.Fatalf("replacement not escaped: %s", xml)
	}
	if !strings.Contains(xml, `<a:rPr lang="en-US"/>`) {
		t.Fatalf("run properties were dropped: %s", xml)
	}
	if err := doc.ValidateOutput(out); err != nil {
		t.Fatal(err)
	}
}

func TestApplyPreservesWhitespace(t *testing.T) {
	doc := prepareFixture(t, pptxtest.Minimal("标题"))
	replaceUnit(t, doc, "标题", " 前后 ")
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	xml := string(partFromZip(t, out, "ppt/slides/slide1.xml"))
	if !strings.Contains(xml, `xml:space="preserve"`) {
		t.Fatalf("missing xml:space preserve: %s", xml)
	}
	reopened, err := Open(out, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Prepare(); err != nil {
		t.Fatal(err)
	}
	if findUnit(reopened, " 前后 ").Index < 0 {
		t.Fatal("whitespace lost after reopen")
	}
}

func TestBytesDeterministic(t *testing.T) {
	build := func() []byte {
		doc := prepareFixture(t, pptxtest.Rich())
		replaceUnit(t, doc, "季度报告 13800138000", "[CUSTOM_1]")
		out, err := doc.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	a, b := build(), build()
	if !bytes.Equal(a, b) {
		t.Fatal("same input produced different bytes")
	}
	doc := prepareFixture(t, pptxtest.Minimal("x"))
	o1, _ := doc.Bytes()
	o2, _ := doc.Bytes()
	if !bytes.Equal(o1, o2) {
		t.Fatal("repeated Bytes differ")
	}
}

func TestValidateBlocksTampering(t *testing.T) {
	doc := prepareFixture(t, pptxtest.Rich())
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.ValidateOutput(out); err != nil {
		t.Fatalf("clean output should validate: %v", err)
	}

	tamperedText := mutate(t, out, func(parts map[string][]byte) {
		replaceInPart(t, parts, "ppt/slides/slide1.xml", "季度报告 13800138000", "季度报告 99999999999")
	})
	if err := doc.ValidateOutput(tamperedText); err == nil {
		t.Fatal("text tampering was not blocked")
	}

	tamperedImage := mutate(t, out, func(parts map[string][]byte) {
		parts["ppt/media/image1.png"] = []byte("changed-image")
	})
	if err := doc.ValidateOutput(tamperedImage); err == nil {
		t.Fatal("image tampering was not blocked")
	}

	tamperedStructure := mutate(t, out, func(parts map[string][]byte) {
		replaceInPart(t, parts, "ppt/slides/slide1.xml", `id="2" name="Title"`, `id="99" name="Title"`)
	})
	if err := doc.ValidateOutput(tamperedStructure); err == nil {
		t.Fatal("structure tampering was not blocked")
	}

	tamperedUnprocessed := mutate(t, out, func(parts map[string][]byte) {
		replaceInPart(t, parts, "ppt/charts/chart1.xml", "图表内 13600136000", "图表内 00000000000")
	})
	if err := doc.ValidateOutput(tamperedUnprocessed); err == nil {
		t.Fatal("unprocessed XML byte tampering was not blocked")
	}

	missingPart := mutate(t, out, func(parts map[string][]byte) {
		delete(parts, "ppt/slides/slide2.xml")
	})
	if err := doc.ValidateOutput(missingPart); err == nil {
		t.Fatal("missing part was not blocked")
	}

	missingImage := mutate(t, out, func(parts map[string][]byte) {
		delete(parts, "ppt/media/image1.png")
	})
	if err := doc.ValidateOutput(missingImage); err == nil {
		t.Fatal("missing image was not blocked")
	}
}

func partFromZip(t *testing.T, data []byte, name string) []byte {
	t.Helper()
	parts := readZip(t, data)
	body, ok := parts[name]
	if !ok {
		t.Fatalf("missing part %s", name)
	}
	return body
}

func TestUnprocessedPartsKeepBytes(t *testing.T) {
	src := pptxtest.Rich()
	doc := prepareFixture(t, src)
	replaceUnit(t, doc, "季度报告 13800138000", "[PHONE_1]")
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.ValidateOutput(out); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"ppt/charts/chart1.xml",
		"ppt/diagrams/Dm1.xml",
		"ppt/embeddings/oleObject1.bin",
		"ppt/media/image1.png",
	} {
		if !bytes.Equal(partFromZip(t, src, name), partFromZip(t, out, name)) {
			t.Errorf("unprocessed part %s changed bytes", name)
		}
	}
	if !strings.Contains(string(partFromZip(t, out, "ppt/charts/chart1.xml")), "图表内 13600136000") {
		t.Error("chart text should remain untouched")
	}
	if !strings.Contains(string(partFromZip(t, out, "ppt/slides/slide1.xml")), "<p:timing>") {
		t.Error("animation markup should be preserved")
	}
}

func TestBytesEnforcesOutputBudget(t *testing.T) {
	src := pptxtest.Minimal("x")
	parts := readZip(t, src)
	var total int64
	for _, b := range parts {
		total += int64(len(b))
	}
	lim := DefaultLimits()
	lim.MaxTotalUncompressed = total + 64
	doc, err := Open(src, lim)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Prepare(); err != nil {
		t.Fatal(err)
	}
	u := doc.Units()[0]
	if err := doc.Apply(u, []Edit{{Start: 0, End: len(u.Text), Replacement: strings.Repeat("X", 4096)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Bytes(); err == nil {
		t.Fatal("expected output budget failure")
	}
}

func TestApplyReplacesExistingSpaceAttribute(t *testing.T) {
	src := mutate(t, pptxtest.Minimal("标题"), func(parts map[string][]byte) {
		replaceInPart(t, parts, "ppt/slides/slide1.xml", `<a:t>标题</a:t>`, `<a:t xml:space="default">标题</a:t>`)
	})
	doc := prepareFixture(t, src)
	replaceUnit(t, doc, "标题", " 前后 ")
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	xml := string(partFromZip(t, out, "ppt/slides/slide1.xml"))
	if strings.Count(xml, "xml:space") != 1 || !strings.Contains(xml, `xml:space="preserve"`) {
		t.Fatalf("xml:space 应被替换而不是重复：%s", xml)
	}
	if err := doc.ValidateOutput(out); err != nil {
		t.Fatal(err)
	}
}
