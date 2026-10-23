package core

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/pandatoufu/data-masking/internal/config"
	"github.com/pandatoufu/data-masking/internal/loader"
)

// 1.3 纯文本 golden：UTF-8 / GBK / 混合换行 / 同值编号 / version 1 mapping 字节锁定。
// 这些常量在实现冻结时采集；任何后续改动导致不一致都应被视为兼容性回归。
const (
	goldenUTF8Masked  = "5byg5LiJIFtQSE9ORV8xXQ0K5p2O5ZubIFtQSE9ORV8yXQrnjovkupQgW1BIT05FXzFdDee7k+adnw=="
	goldenUTF8Mapping = "ewogICJ2ZXJzaW9uIjogMSwKICAic291cmNlX2ZpbGUiOiAibm90ZXMudHh0IiwKICAic291cmNlX2VuY29kaW5nIjogIlVURi04IiwKICAic291cmNlX3NoYTI1NiI6ICJhN2QwN2JiNzViZDg0NzI3MjUwMTA4ZjcxY2Q1YWU1YjI5NTczNWJmNzViODlmZjQxNzIzM2Y3ZTNmNWMyZDg3IiwKICAicGxhY2Vob2xkZXJzIjogWwogICAgewogICAgICAicGxhY2Vob2xkZXIiOiAiW1BIT05FXzFdIiwKICAgICAgImNhdGVnb3J5IjogIlBIT05FIiwKICAgICAgInZhbHVlIjogIjEzODEyMzQ1Njc4IgogICAgfSwKICAgIHsKICAgICAgInBsYWNlaG9sZGVyIjogIltQSE9ORV8yXSIsCiAgICAgICJjYXRlZ29yeSI6ICJQSE9ORSIsCiAgICAgICJ2YWx1ZSI6ICIxMzkwMDAwMDAwMCIKICAgIH0KICBdCn0="
	goldenGBKMasked   = "u+HS6bzN0qoKwarPtcjLIFtQSE9ORV8xXQo="
	goldenGBKMapping  = "ewogICJ2ZXJzaW9uIjogMSwKICAic291cmNlX2ZpbGUiOiAibm90ZXMudHh0IiwKICAic291cmNlX2VuY29kaW5nIjogIkdCSyIsCiAgInNvdXJjZV9zaGEyNTYiOiAiZTA1OGZlOGNmMDI5YzdhZDBjNmU4YzI4ZmM1ZDZiMjc4YjdjYTk3NWU3MDgwMmMwNGYyMTc0NTRiZTQxNDNkYiIsCiAgInBsYWNlaG9sZGVycyI6IFsKICAgIHsKICAgICAgInBsYWNlaG9sZGVyIjogIltQSE9ORV8xXSIsCiAgICAgICJjYXRlZ29yeSI6ICJQSE9ORSIsCiAgICAgICJ2YWx1ZSI6ICIxMzgxMjM0NTY3OCIKICAgIH0KICBdCn0="
)

func decodeGolden(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("golden 解码失败：%v", err)
	}
	return b
}

func TestGoldenPureTextUTF8MixedNewlines(t *testing.T) {
	cfg := config.Defaults()
	in := []byte("张三 13812345678\r\n李四 13900000000\n王五 13812345678\r结束")
	out, err := MaskFile("notes.txt", in, MaskOptions{Profile: "strict"}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.MaskedBytes, decodeGolden(t, goldenUTF8Masked)) {
		t.Fatalf("UTF-8 脱敏 golden 不符：%q", out.MaskedBytes)
	}
	if !bytes.Equal(out.MappingBytes, decodeGolden(t, goldenUTF8Mapping)) {
		t.Fatalf("UTF-8 mapping golden 不符：%s", out.MappingBytes)
	}
	if out.Encoding != loader.EncodingUTF8 {
		t.Fatalf("编码应为 UTF-8，got %q", out.Encoding)
	}
}

func TestGoldenPureTextGBK(t *testing.T) {
	cfg := config.Defaults()
	in := gbk(t, "会议纪要\n联系人 13812345678\n")
	out, err := MaskFile("notes.txt", in, MaskOptions{Profile: "balanced"}, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.Encoding != loader.EncodingGBK {
		t.Fatalf("编码应为 GBK，got %q", out.Encoding)
	}
	if !bytes.Equal(out.MaskedBytes, decodeGolden(t, goldenGBKMasked)) {
		t.Fatalf("GBK 脱敏 golden 不符：%x", out.MaskedBytes)
	}
	if !bytes.Equal(out.MappingBytes, decodeGolden(t, goldenGBKMapping)) {
		t.Fatalf("GBK mapping golden 不符：%s", out.MappingBytes)
	}
}
