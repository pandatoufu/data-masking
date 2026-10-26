package pptx

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"testing"

	"github.com/pandatoufu/data-masking/internal/pptxtest"
)

func openFixture(t *testing.T, data []byte) *Document {
	t.Helper()
	doc, err := Open(data, DefaultLimits())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return doc
}

func unitMap(doc *Document) map[string]string {
	out := map[string]string{}
	for _, u := range doc.Units() {
		out[fmt.Sprintf("%s|%s|%d|%d", u.Part, u.PartKind, u.Paragraph, u.Segment)] = u.Text
	}
	return out
}

func TestOpenMinimalAndUnits(t *testing.T) {
	doc := openFixture(t, pptxtest.Minimal("标题 13800138000"))
	if _, err := doc.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	units := doc.Units()
	if len(units) == 0 {
		t.Fatal("no units")
	}
	for _, u := range units {
		t.Logf("%s %s P%d S%d %q", u.Part, u.PartKind, u.Paragraph, u.Segment, u.Text)
	}
}

func TestRichFixtureCoverage(t *testing.T) {
	doc := openFixture(t, pptxtest.Rich())
	summary, err := doc.Prepare()
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	t.Logf("cleanup=%+v", summary)
	units := doc.Units()
	texts := unitMap(doc)
	for _, u := range units {
		t.Logf("%s %s P%d S%d %q", u.Part, u.PartKind, u.Paragraph, u.Segment, u.Text)
	}
	joined := strings.Join(values(texts), "\n")
	for _, want := range []string{
		"季度报告 13800138000", "备注也含 13800138000", "组合内 13800138000",
		"单元格 13800138000", "Choice 13800138000", "Fallback 13800138000",
		"跨 run 13800138000", "隐藏页 13900139000",
		"版式 LAYOUT 13800138000", "母版 MASTER 13800138000",
		"备注母版 NOTESMASTER 13800138000", "讲义母版 HANDOUT 13800138000",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing unit text %q", want)
		}
	}
	for _, forbidden := range []string{"批注旧值", "图表内", "SmartArt 内", "嵌入对象内"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("forbidden text leaked into units: %q", forbidden)
		}
	}
	if summary.CommentsDeleted != 4 {
		t.Errorf("comments deleted = %d, want 4", summary.CommentsDeleted)
	}
	if summary.PeoplePartsRemoved != 2 {
		t.Errorf("people parts removed = %d, want 2", summary.PeoplePartsRemoved)
	}
}

func values(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func TestZIPOrderIndependence(t *testing.T) {
	data := pptxtest.Rich()
	reordered := reverseZIP(t, data)
	d1 := openFixture(t, data)
	d2 := openFixture(t, reordered)
	if _, err := d1.Prepare(); err != nil {
		t.Fatal(err)
	}
	if _, err := d2.Prepare(); err != nil {
		t.Fatal(err)
	}
	if got, want := fmt.Sprint(d2.Units()), fmt.Sprint(d1.Units()); got != want {
		t.Errorf("units changed with ZIP order")
	}
	if TextSHA256(d1.Units()) != TextSHA256(d2.Units()) {
		t.Errorf("fingerprint changed with ZIP order")
	}
}

func reverseZIP(t *testing.T, data []byte) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := make([]*zip.File, len(zr.File))
	copy(files, zr.File)
	for i, j := 0, len(files)-1; i < j; i, j = i+1, j-1 {
		files[i], files[j] = files[j], files[i]
	}
	for _, f := range files {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.Name, Method: zip.Deflate})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func partByName(t *testing.T, data []byte, name string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer rc.Close()
			b, _ := io.ReadAll(rc)
			return b
		}
	}
	t.Fatalf("part %s not found", name)
	return nil
}

func relsPathFor(src string) string { return path.Join(path.Dir(src), "_rels", path.Base(src)+".rels") }

var _ = relsPathFor
