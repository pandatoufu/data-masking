//go:build large

package largefiles

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/pandatoufu/data-masking/internal/docxtest"
	"github.com/pandatoufu/data-masking/internal/loader"
	"github.com/pandatoufu/data-masking/internal/pptxtest"
)

// 功能边界与预算数字（与实现/specs 一致；由各包默认值测试另行断言）。
const (
	size10MiB    = 10 << 20
	size32MiB    = 32 << 20
	size50MiB    = 52428800
	budget200MiB = 209715200
	xmlCap       = 20 << 20
	entryCap     = 2048
)

// patternBytes 生成确定性、无私人信息的字节序列。
func patternBytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*131 + 17)
	}
	return b
}

// largeText 返回指定真实字节长度的纯文本（无私人信息）。
func largeText(n int) []byte {
	return bytes.Repeat([]byte("a"), n)
}

// totalUncompressed 以实际解压读取核算包内所有条目总量，不依赖声明大小。
func totalUncompressed(t testing.TB, data []byte) int64 {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("解析样本 ZIP 失败：%v", err)
	}
	var total int64
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("打开条目 %s 失败：%v", f.Name, err)
		}
		n, err := io.Copy(io.Discard, rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("读取条目 %s 失败：%v", f.Name, err)
		}
		total += n
	}
	return total
}

// entryCount 返回非目录条目数（用于确认样本未误触 2048 上限）。
func entryCount(t testing.TB, data []byte) int {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("解析样本 ZIP 失败：%v", err)
	}
	n := 0
	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, "/") {
			n++
		}
	}
	return n
}

// largestXMLPart 返回包内最大的 .xml/.rels 实际解压字节数（用于确认样本未误触单 XML 预算）。
func largestXMLPart(t testing.TB, data []byte) int64 {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("解析样本 ZIP 失败：%v", err)
	}
	var max int64
	for _, f := range zr.File {
		name := strings.ToLower(f.Name)
		if !strings.HasSuffix(name, ".xml") && !strings.HasSuffix(name, ".rels") {
			continue
		}
		if f.UncompressedSize64 > uint64(max) {
			max = int64(f.UncompressedSize64)
		}
	}
	return max
}

// sizedDocx 生成文件字节数精确等于 target 的有效 DOCX：使用 Store 填充部件，
// 迭代修正 ZIP 固定开销，使 len(data) 精确命中边界。
func sizedDocx(t testing.TB, target int) []byte {
	t.Helper()
	if target <= 0 {
		t.Fatalf("目标大小非法：%d", target)
	}
	pad := target / 2
	for i := 0; i < 8; i++ {
		data, err := docxtest.BuildFullFixture(docxtest.FixtureOptions{
			Body:        []string{"边界样本 13800138000"},
			StoredExtra: map[string][]byte{"word/media/pad.bin": patternBytes(pad)},
		})
		if err != nil {
			t.Fatalf("构造 DOCX 样本失败：%v", err)
		}
		if len(data) == target {
			return data
		}
		pad += target - len(data)
		if pad < 0 {
			pad = 0
		}
	}
	t.Fatalf("DOCX 样本未能精确命中 %d 字节", target)
	return nil
}

// sizedPptx 生成文件字节数精确等于 target 的有效 PPTX。
func sizedPptx(t testing.TB, target int) []byte {
	t.Helper()
	if target <= 0 {
		t.Fatalf("目标大小非法：%d", target)
	}
	pad := target / 2
	for i := 0; i < 8; i++ {
		data := pptxtest.Build(pptxtest.Options{
			Slides:     []pptxtest.Slide{{Title: "边界样本 13800138000"}},
			StoreParts: map[string][]byte{"ppt/media/pad.png": patternBytes(pad)},
		})
		if len(data) == target {
			return data
		}
		pad += target - len(data)
		if pad < 0 {
			pad = 0
		}
	}
	t.Fatalf("PPTX 样本未能精确命中 %d 字节", target)
	return nil
}

// docxWithTotal 生成实际解压总量精确等于 target 的有效 DOCX：正文可带文字样本，
// 其余总量由高压缩填充部件补齐（文件体积仍远低于 50 MiB）。
func docxWithTotal(t testing.TB, target int, body []string) []byte {
	t.Helper()
	bulk := target / 2
	for i := 0; i < 8; i++ {
		data, err := docxtest.BuildFullFixture(docxtest.FixtureOptions{
			Body:       body,
			ExtraParts: map[string][]byte{"word/media/bulk.bin": make([]byte, bulk)},
		})
		if err != nil {
			t.Fatalf("构造 DOCX 解压样本失败：%v", err)
		}
		total := totalUncompressed(t, data)
		if total == int64(target) {
			return data
		}
		bulk += int(int64(target) - total)
		if bulk < 0 {
			t.Fatalf("DOCX 解压样本无法在目标内构造：base 已超过 %d", target)
		}
	}
	t.Fatalf("DOCX 解压样本未能精确命中 %d 字节总量", target)
	return nil
}

// pptxWithTotal 生成实际解压总量精确等于 target 的有效 PPTX。
func pptxWithTotal(t testing.TB, target int, body []string) []byte {
	t.Helper()
	bulk := target / 2
	for i := 0; i < 8; i++ {
		data := pptxtest.Build(pptxtest.Options{
			Slides:     []pptxtest.Slide{{Title: "边界样本 13800138000", Body: body}},
			ExtraParts: map[string][]byte{"ppt/media/bulk.bin": make([]byte, bulk)},
		})
		total := totalUncompressed(t, data)
		if total == int64(target) {
			return data
		}
		bulk += int(int64(target) - total)
		if bulk < 0 {
			t.Fatalf("PPTX 解压样本无法在目标内构造：base 已超过 %d", target)
		}
	}
	t.Fatalf("PPTX 解压样本未能精确命中 %d 字节总量", target)
	return nil
}

func assertHumanID(t *testing.T, err error, want string) {
	t.Helper()
	var he *loader.HumanError
	if !errors.As(err, &he) {
		t.Fatalf("应返回 HumanError，got %#v", err)
	}
	if want != "" && he.MessageID != want {
		t.Fatalf("message_id=%q want %q（%s）", he.MessageID, want, he.Error())
	}
}
