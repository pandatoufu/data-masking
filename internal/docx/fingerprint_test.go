package docx

import (
	"encoding/hex"
	"testing"

	"github.com/pandatoufu/data-masking/internal/docxtest"
)

// 6.2 docx-text-v1 固定已知向量。
func TestTextFingerprintKnownVector(t *testing.T) {
	units := []TextUnit{{Part: "word/document.xml", Paragraph: 1, Segment: 1, Text: "A"}}
	wantHex := "000000000000000c646f63782d746578742d763100000000000000010000000000000011776f72642f646f63756d656e742e786d6c000000000000000131000000000000000131000000000000000141"
	got := TextFingerprint(units)
	if hex.EncodeToString(got) != wantHex {
		t.Fatalf("指纹编码不符：\n got %x\nwant %s", got, wantHex)
	}
	if sha := TextSHA256(units); sha != "2c8b5de1acf57301cef391632aac4f066a8700862242231dc96881c9304732e5" {
		t.Fatalf("已知向量 SHA 不符：%s", sha)
	}
}

// 6.2 空单元（空段不产生 unit）与数量编码。
func TestTextFingerprintZeroUnits(t *testing.T) {
	got := TextSHA256(nil)
	if got != "2361958e97e48cff2ea7fe2afa51f06ad105506f7dcc86dbf7b97ddca0e2e000" {
		t.Fatalf("零单元向量不符：%s", got)
	}
}

// 6.2 非 BMP 长度按 UTF-8 字节数编码。
func TestTextFingerprintNonBMPLength(t *testing.T) {
	units := []TextUnit{{Part: "p", Paragraph: 1, Segment: 1, Text: "😀"}}
	fp := TextFingerprint(units)
	// 最后一个 Text 的长度前缀应为 4（😀 的 UTF-8 字节数）。
	last := fp[len(fp)-12:]
	if hex.EncodeToString(last) != "0000000000000004f09f9880" {
		t.Fatalf("非 BMP 长度编码错误：%x", last)
	}
}

// 6.2 run 拆分不改变参考；覆盖文字改变会改变参考。
func TestTextFingerprintRunSplitInvariant(t *testing.T) {
	build := func(runs ...string) *Document {
		data, err := docxtest.BuildFullFixture(docxtest.FixtureOptions{BodyRuns: runs})
		if err != nil {
			t.Fatal(err)
		}
		doc, err := Open(data, DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := doc.Prepare(); err != nil {
			t.Fatal(err)
		}
		return doc
	}
	whole := build("13812345678")
	split := build("138", "1234", "5678")
	if TextSHA256(whole.Units()) != TextSHA256(split.Units()) {
		t.Fatal("run 拆分不应改变文字参考指纹")
	}
	changed := build("13900000000")
	if TextSHA256(whole.Units()) == TextSHA256(changed.Units()) {
		t.Fatal("覆盖文字改变应改变参考指纹")
	}
	// 1-based 编号进入指纹：同文本不同段落应不同。
	a := []TextUnit{{Part: "p", Paragraph: 1, Segment: 1, Text: "x"}}
	b := []TextUnit{{Part: "p", Paragraph: 2, Segment: 1, Text: "x"}}
	if TextSHA256(a) == TextSHA256(b) {
		t.Fatal("Paragraph 应纳入指纹")
	}
}
