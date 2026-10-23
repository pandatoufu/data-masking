package docx

import (
	"archive/zip"
	"bytes"
	"github.com/pandatoufu/data-masking/internal/docxtest"
	"io"
	"strings"
	"testing"
	"time"
)

func fullFixture(t *testing.T, strict bool) []byte {
	t.Helper()
	data, err := docxtest.BuildFullFixture(docxtest.FixtureOptions{
		Strict:         strict,
		Body:           []string{"正文 13812345678"},
		Table:          [][]string{{"单元格A", "单元格B"}, {"单元格C", "单元格D"}},
		Headers:        []string{"默认页眉", "首页页眉", "偶数页页眉"},
		Footers:        []string{"默认页脚", "首页页脚", "偶数页页脚"},
		Footnotes:      []string{"脚注一", "脚注二"},
		Endnotes:       []string{"尾注一"},
		DrawingTextbox: "DrawingML 文本框",
		VMLTextbox:     "VML 文本框",
		ChoiceText:     "Choice 分支",
		FallbackText:   "Fallback 分支",
		Hidden:         "隐藏文字",
		Image:          []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 1, 2, 3},
	})
	if err != nil {
		t.Fatalf("BuildFullFixture: %v", err)
	}
	return data
}

// 1.1 fixture 构造器：正文/表格/全部页眉页脚/脚注尾注/两种文本框 + Transitional/Strict。
func TestFixtureBuilderAllRegionsBothFlavors(t *testing.T) {
	for _, strict := range []bool{false, true} {
		data := fullFixture(t, strict)
		doc, err := Open(data, DefaultLimits())
		if err != nil {
			t.Fatalf("strict=%v Open: %v", strict, err)
		}
		if doc.MainPart() != "word/document.xml" {
			t.Fatalf("主部件错误：%q", doc.MainPart())
		}
		// 声明与关系：页眉/页脚/脚注/尾注部件均按 content type 发现。
		for _, name := range []string{"word/header1.xml", "word/header2.xml", "word/header3.xml", "word/footer1.xml", "word/footnotes.xml", "word/endnotes.xml"} {
			if !doc.HasPart(name) {
				t.Errorf("strict=%v 缺少部件 %s", strict, name)
			}
		}
		if _, err := doc.Prepare(); err != nil {
			t.Fatalf("strict=%v Prepare: %v", strict, err)
		}
		joined := ""
		for _, u := range doc.Units() {
			joined += u.Text + "|"
		}
		for _, want := range []string{
			"正文 13812345678", "单元格A", "单元格D", "默认页眉", "首页页眉", "偶数页页眉",
			"默认页脚", "首页页脚", "偶数页页脚", "脚注一", "脚注二", "尾注一",
			"DrawingML 文本框", "VML 文本框", "Choice 分支", "Fallback 分支", "隐藏文字",
		} {
			if !strings.Contains(joined, want) {
				t.Errorf("strict=%v 缺少预期文字 %q；got %s", strict, want, joined)
			}
		}
		// 图片关系存在且为内部目标。
		found := false
		for _, r := range doc.rels["word/document.xml"] {
			if strings.HasSuffix(r.Type, "/image") && r.Resolved == "word/media/image1.png" {
				found = true
			}
		}
		if !found {
			t.Errorf("strict=%v 缺少图片关系", strict)
		}
	}
}

// 1.2 结构快照比较：改文字允许，删图片/改样式失败；期望值来自独立构造的 fixture。
func TestSnapshotComparison(t *testing.T) {
	base := fullFixture(t, false)
	baseSnap, err := Snapshot(base)
	if err != nil {
		t.Fatal(err)
	}

	// 仅文字不同：结构差异应为空。
	textChanged, err := docxtest.BuildFullFixture(docxtest.FixtureOptions{
		Body:           []string{"正文 00000000000"},
		Table:          [][]string{{"单元格A", "单元格B"}, {"单元格C", "单元格D"}},
		Headers:        []string{"默认页眉", "首页页眉", "偶数页页眉"},
		Footers:        []string{"默认页脚", "首页页脚", "偶数页页脚"},
		Footnotes:      []string{"脚注一", "脚注二"},
		Endnotes:       []string{"尾注一"},
		DrawingTextbox: "DrawingML 文本框",
		VMLTextbox:     "VML 文本框",
		ChoiceText:     "Choice 分支",
		FallbackText:   "Fallback 分支",
		Hidden:         "隐藏文字",
		Image:          []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 1, 2, 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	snap2, err := Snapshot(textChanged)
	if err != nil {
		t.Fatal(err)
	}
	if diffs := baseSnap.Diff(snap2); len(diffs) != 0 {
		t.Fatalf("仅文字变化不应产生结构差异：%+v", diffs)
	}

	// 删除图片：应报告 image 差异。
	noImage, err := docxtest.BuildFullFixture(docxtest.FixtureOptions{
		Body: []string{"正文 13812345678"},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapNoImg, err := Snapshot(noImage)
	if err != nil {
		t.Fatal(err)
	}
	if diffs := baseSnap.Diff(snapNoImg); !hasDiffKind(diffs, "image") {
		t.Fatalf("删除图片应报告 image 差异：%+v", diffs)
	}

	// 改样式（表格列宽）：应报告 structure 差异。
	tampered := replacePartInZip(t, base, "word/document.xml", func(b []byte) []byte {
		return bytes.ReplaceAll(b, []byte(`w:w="2000"`), []byte(`w:w="4000"`))
	})
	snapTamper, err := Snapshot(tampered)
	if err != nil {
		t.Fatal(err)
	}
	if diffs := baseSnap.Diff(snapTamper); !hasDiffKind(diffs, "structure") {
		t.Fatalf("改样式应报告 structure 差异：%+v", diffs)
	}
}

func hasDiffKind(diffs []SnapshotDiff, kind string) bool {
	for _, d := range diffs {
		if d.Kind == kind {
			return true
		}
	}
	return false
}

// replacePartInZip 以新内容替换 ZIP 中某部件，用于负样本构造（不复用生产写回）。
func replacePartInZip(t *testing.T, data []byte, name string, fn func([]byte) []byte) []byte {
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
		if f.Name == name {
			content = fn(content)
		}
		fh := &zip.FileHeader{Name: f.Name, Method: zip.Deflate, Modified: fixed}
		w, _ := zw.CreateHeader(fh)
		w.Write(content)
	}
	zw.Close()
	return buf.Bytes()
}
