package core

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/pandatoufu/data-masking/internal/config"
	"github.com/pandatoufu/data-masking/internal/docxtest"
	"github.com/pandatoufu/data-masking/internal/mapping"
	"github.com/pandatoufu/data-masking/internal/pptx"
	"github.com/pandatoufu/data-masking/internal/pptxtest"
)

func mustDocxFixture(t *testing.T) []byte {
	t.Helper()
	data, err := docxtest.BuildFullFixture(docxtest.FixtureOptions{Body: []string{"正文 13800138000"}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func zipReader(data []byte) (map[string][]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		out[f.Name] = body
	}
	return out, nil
}

func zipWriter(t *testing.T, parts map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range parts {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
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

func testConfig() *config.Config {
	return config.Defaults()
}

func TestMaskAndRestorePPTXPathA(t *testing.T) {
	cfg := testConfig()
	src := pptxtest.Rich()
	out, err := MaskFile("报告.pptx", src, MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatalf("MaskFile: %v", err)
	}
	if out.Format != "pptx" || out.Encoding != "" {
		t.Fatalf("unexpected format/encoding: %+v", out)
	}
	if out.MaskedName != "报告.masked.pptx" || out.MappingName != "报告.masked.mapping.json" {
		t.Fatalf("unexpected names: %s %s", out.MaskedName, out.MappingName)
	}
	if out.PPTXCleanup == nil || out.PPTXCleanup.CommentsDeleted != 4 {
		t.Fatalf("missing pptx cleanup: %+v", out.PPTXCleanup)
	}
	mp, err := mapping.Unmarshal(out.MappingBytes)
	if err != nil {
		t.Fatalf("mapping: %v", err)
	}
	if mp.Version != 3 || mp.SourceFormat != "pptx" || mp.SourceEncoding != "" {
		t.Fatalf("unexpected mapping: %+v", mp)
	}
	if len(mp.Placeholders) == 0 {
		t.Fatal("no placeholders")
	}
	phone := ""
	for _, e := range mp.Placeholders {
		if e.Category == "PHONE" && e.Value == "13800138000" {
			phone = e.Placeholder
		}
	}
	if phone == "" {
		t.Fatalf("phone placeholder missing: %+v", mp.Placeholders)
	}
	// 所有支持区域共用同一占位符。
	doc, err := pptx.Open(out.MaskedBytes, pptx.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Prepare(); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, u := range doc.Units() {
		count += strings.Count(u.Text, phone)
	}
	if count < 8 {
		t.Fatalf("placeholder occurrences = %d, want all regions", count)
	}
	for _, u := range doc.Units() {
		if strings.Contains(u.Text, "13800138000") {
			t.Fatalf("plaintext left in unit: %q", u.Text)
		}
		if strings.Contains(u.Text, "批注") || strings.Contains(u.Text, "图表内") {
			t.Fatalf("excluded content leaked: %q", u.Text)
		}
	}

	restored, err := RestoreFile("回复.pptx", out.MaskedBytes, out.MappingBytes, cfg, nil)
	if err != nil {
		t.Fatalf("RestoreFile: %v", err)
	}
	if restored.Format != "pptx" || restored.Name != "报告.restored.pptx" {
		t.Fatalf("unexpected restore: %+v", restored)
	}
	if restored.PPTXCleanup == nil {
		t.Fatal("restore missing pptx_cleanup")
	}
	assertHasWarningKind(t, restored.Warnings, "pptx_history")
	rdoc, err := pptx.Open(restored.Bytes, pptx.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rdoc.Prepare(); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, u := range rdoc.Units() {
		if strings.Contains(u.Text, "备注也含 13800138000") {
			found = true
		}
	}
	if !found {
		t.Fatal("notes were not restored")
	}
}

func assertHasWarningKind(t *testing.T, ws []Warning, kind string) {
	t.Helper()
	for _, w := range ws {
		if w.Kind == kind {
			return
		}
	}
	t.Fatalf("warning %q missing in %+v", kind, ws)
}

func TestPPTXGrammarCollisionAndCommentsIgnored(t *testing.T) {
	cfg := testConfig()
	src := pptxtest.Build(pptxtest.Options{
		Slides:         []pptxtest.Slide{{Title: "正文", Notes: []string{"备注"}}},
		LegacyComments: true,
		CommentText:    "批注 [PHONE_1] 旧值",
	})
	if _, err := MaskFile("a.pptx", src, MaskOptions{Profile: "strict"}, cfg, nil); err != nil {
		t.Fatalf("comment placeholder must not trigger collision: %v", err)
	}
	colliding := pptxtest.Minimal("正文 [PHONE_1]")
	_, err := MaskFile("b.pptx", colliding, MaskOptions{Profile: "strict"}, cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "占位符文法") {
		t.Fatalf("expected grammar collision, got %v", err)
	}
}

func TestPPTXFullWidthAndSplitRestore(t *testing.T) {
	cfg := testConfig()
	src := pptxtest.Build(pptxtest.Options{Slides: []pptxtest.Slide{{SplitRuns: []string{"联系人 1380", "0138000"}}}})
	out, err := MaskFile("a.pptx", src, MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 在脱敏副本中把占位符拆成多个 run，并改为全角，模拟模型编辑。
	edited := mutateCorePPTX(t, out.MaskedBytes, func(parts map[string][]byte) {
		body := string(parts["ppt/slides/slide1.xml"])
		body = strings.ReplaceAll(body, `<a:t>[PHONE_1]</a:t>`, `<a:t>［ＰＨＯＮＥ</a:t></a:r><a:r><a:t>＿１］</a:t>`)
		parts["ppt/slides/slide1.xml"] = []byte(body)
	})
	restored, err := RestoreFile("reply.pptx", edited, out.MappingBytes, cfg, nil)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	doc, err := pptx.Open(restored.Bytes, pptx.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Prepare(); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, u := range doc.Units() {
		if strings.Contains(u.Text, "13800138000") {
			found = true
		}
	}
	if !found {
		t.Fatal("full-width split placeholder was not restored")
	}
}

func TestPPTXRestoreUnknownResidualIsNonBlocking(t *testing.T) {
	cfg := testConfig()
	out, err := MaskFile("a.pptx", pptxtest.Minimal("13800138000"), MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	edited := mutateCorePPTX(t, out.MaskedBytes, func(parts map[string][]byte) {
		body := string(parts["ppt/slides/slide1.xml"])
		body = strings.ReplaceAll(body, `</a:t></a:r></a:p></p:txBody>`, ` [PHONE_9]</a:t></a:r></a:p></p:txBody>`)
		parts["ppt/slides/slide1.xml"] = []byte(body)
	})
	restored, err := RestoreFile("reply.pptx", edited, out.MappingBytes, cfg, nil)
	if err != nil {
		t.Fatalf("unknown residual must not block: %v", err)
	}
	assertHasWarningKind(t, restored.Warnings, "residual_reverse")
}

func TestPPTXCombinationMatrix(t *testing.T) {
	cfg := testConfig()
	pptxOut, err := MaskFile("a.pptx", pptxtest.Minimal("13800138000"), MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	docxOut, err := MaskFile("a.docx", mustDocxFixture(t), MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	textOut, err := MaskFile("a.txt", []byte("联系人 13800138000"), MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		file string
		data []byte
		mp   []byte
	}{
		{"v3+docx", "a.docx", docxOut.MaskedBytes, pptxOut.MappingBytes},
		{"v3+text", "a.txt", textOut.MaskedBytes, pptxOut.MappingBytes},
		{"v1+pptx", "a.pptx", pptxOut.MaskedBytes, textOut.MappingBytes},
		{"v2+pptx", "a.pptx", pptxOut.MaskedBytes, docxOut.MappingBytes},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := RestoreFile(c.file, c.data, c.mp, cfg, nil)
			if err == nil {
				t.Fatal("expected unsupported combination")
			}
			if !strings.Contains(err.Error(), "不支持的格式组合") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}

	// 旧四格保持可用。
	if _, err := RestoreFile("a.txt", textOut.MaskedBytes, textOut.MappingBytes, cfg, nil); err != nil {
		t.Fatalf("v1+text regressed: %v", err)
	}
	if _, err := RestoreFile("a.docx", docxOut.MaskedBytes, docxOut.MappingBytes, cfg, nil); err != nil {
		t.Fatalf("v2+docx regressed: %v", err)
	}
}

func TestPPTXHighFrequencyConfirmation(t *testing.T) {
	cfg := testConfig()
	cfg.CustomWords = []string{"项目代号"}
	cfg.HighFreqThreshold = 1
	src := pptxtest.Build(pptxtest.Options{Slides: []pptxtest.Slide{{Title: "项目代号 项目代号 项目代号", Notes: []string{"项目代号"}}}})
	_, err := MaskFile("a.pptx", src, MaskOptions{Profile: "custom", ExtraCustom: []string{"项目代号"}}, cfg, nil)
	pre, ok := err.(*PreviewRequiredError)
	if !ok {
		t.Fatalf("expected preview required, got %v", err)
	}
	if pre.Extra["项目代号"] != 4 && pre.Builtin["项目代号"] != 4 {
		t.Fatalf("unexpected counts: %+v", pre)
	}
	out, err := MaskFile("a.pptx", src, MaskOptions{Profile: "custom", ExtraCustom: []string{"项目代号"}, ConfirmedCustom: []string{"项目代号"}}, cfg, nil)
	if err != nil {
		t.Fatalf("confirmed mask failed: %v", err)
	}
	if len(out.MappingBytes) == 0 {
		t.Fatal("missing mapping")
	}
}

func TestPPTXDeterminism(t *testing.T) {
	cfg := testConfig()
	src := pptxtest.Rich()
	a, err := MaskFile("a.pptx", src, MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := MaskFile("a.pptx", src, MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.MaskedBytes, b.MaskedBytes) || !bytes.Equal(a.MappingBytes, b.MappingBytes) {
		t.Fatal("mask output is not deterministic")
	}
	r1, err := RestoreFile("reply.pptx", a.MaskedBytes, a.MappingBytes, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := RestoreFile("reply.pptx", a.MaskedBytes, a.MappingBytes, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(r1.Bytes, r2.Bytes) {
		t.Fatal("restore output is not deterministic")
	}
}

func mutateCorePPTX(t *testing.T, data []byte, fn func(map[string][]byte)) []byte {
	t.Helper()
	parts := map[string][]byte{}
	zr, err := zipReader(data)
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range zr {
		parts[name] = body
	}
	fn(parts)
	return zipWriter(t, parts)
}

func TestPPTXCaseInsensitiveNames(t *testing.T) {
	cfg := testConfig()
	out, err := MaskFile("报告.PPTX", pptxtest.Minimal("13800138000"), MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatalf("大写 PPTX 应接受：%v", err)
	}
	if out.MaskedName != "报告.masked.PPTX" {
		t.Fatalf("大写扩展名应保留：%q", out.MaskedName)
	}
	restored, err := RestoreFile("REPLY.PPTX", out.MaskedBytes, out.MappingBytes, cfg, nil)
	if err != nil {
		t.Fatalf("大写回复应接受：%v", err)
	}
	if restored.Name != "报告.restored.pptx" {
		t.Fatalf("回程名应为源词干 + .restored.pptx：%q", restored.Name)
	}
}
