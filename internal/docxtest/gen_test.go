package docxtest

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// realPNG 生成一个真正可打开的小 PNG（非伪造字节）。
func realPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.Set(x, y, color.RGBA{R: uint8(30 * x), G: uint8(60 * y), B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestWriteAcceptanceFixtures 仅在 DM_ACCEPT_FIXTURES 指向目录时写出人工/浏览器验收样本。
// 这是测试专用辅助，不参与生产构建。
func TestWriteAcceptanceFixtures(t *testing.T) {
	dir := os.Getenv("DM_ACCEPT_FIXTURES")
	if dir == "" {
		t.Skip("DM_ACCEPT_FIXTURES 未设置")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pngBytes := realPNG(t)

	// contract.docx：Transitional，全支持部位 + 表格合并/样式 + mixed font + 兼容分支 +
	// 书签锚点 + 批注 + 行内修订 + 真实 PNG。
	contract, err := BuildFullFixture(FixtureOptions{
		Body:            []string{"合同联系人 13812345678", "备用 13900000000"},
		Table:           [][]string{{"甲方 13812345678", "乙方"}, {"合并", "单元格"}},
		TableMerge:      true,
		Headers:         []string{"页眉 13812345678"},
		Footers:         []string{"页脚"},
		Footnotes:       []string{"脚注 13812345678"},
		Endnotes:        []string{"尾注"},
		DrawingTextbox:  "文本框 13812345678",
		VMLTextbox:      "VML 文本",
		ChoiceText:      "兼容文本框 13812345678",
		FallbackText:    "兼容文本框 13812345678",
		MixedFontNumber: "13812345678",
		CompatText:      "13812345678",
		Bookmark:        "书签锚点 13812345678",
		Hidden:          "隐藏 13812345678",
		InlineInsert:    "新增 13700000000",
		InlineDelete:    "删除旧值 13600000000",
		Comments:        &CommentsFixture{Text: "批注含 13500000000", Extended: true, IDs: true, Extensible: true, People: true},
		Image:           pngBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	write("contract.docx", contract)

	// mixedfont.docx：单独样本，号码跨三种 rPr runs（首 run 为粗体）。
	mixed, err := BuildFullFixture(FixtureOptions{
		Body:            []string{"混合字体样本"},
		MixedFontNumber: "13812345678",
	})
	if err != nil {
		t.Fatal(err)
	}
	write("mixedfont.docx", mixed)

	// compat-branches.docx：Choice/Fallback 两个存储分支均含同一号码。
	compat, err := BuildFullFixture(FixtureOptions{
		Body:       []string{"兼容分支样本"},
		CompatText: "13812345678",
	})
	if err != nil {
		t.Fatal(err)
	}
	write("compat-branches.docx", compat)

	// contract-strict.docx：Strict 命名空间样本（WML 主体/表格/页眉页脚/脚注尾注，无 transitional DrawingML）。
	strict, err := BuildFullFixture(FixtureOptions{
		Strict:          true,
		Body:            []string{"严格格式联系人 13812345678"},
		Table:           [][]string{{"甲 13812345678", "乙"}},
		TableMerge:      true,
		Headers:         []string{"严格页眉 13812345678"},
		Footers:         []string{"严格页脚"},
		Footnotes:       []string{"严格脚注 13812345678"},
		Endnotes:        []string{"严格尾注"},
		MixedFontNumber: "13812345678",
	})
	if err != nil {
		t.Fatal(err)
	}
	write("contract-strict.docx", strict)

	// 高频：30 次本次词 + 30 次常备词。
	var hfBody []string
	for i := 0; i < 30; i++ {
		hfBody = append(hfBody, "内部词")
	}
	for i := 0; i < 30; i++ {
		hfBody = append(hfBody, "常备词")
	}
	hf, err := BuildFullFixture(FixtureOptions{Body: hfBody})
	if err != nil {
		t.Fatal(err)
	}
	write("highfreq.docx", hf)

	// 跨 run 拆分占位符的回复，用于 DOCX 回程。
	reply, err := BuildFullFixture(FixtureOptions{BodyRuns: []string{"合同联系人 [PHO", "NE_1] 已确认"}})
	if err != nil {
		t.Fatal(err)
	}
	write("reply-split.docx", reply)

	// 纯文本样本。
	write("notes.txt", []byte("联系人 13812345678\n备用 13900000000\n"))
}
