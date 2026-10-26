package core

import (
	"bytes"
	"errors"
	"testing"

	"github.com/pandatoufu/data-masking/internal/config"
	"github.com/pandatoufu/data-masking/internal/docxtest"
	"github.com/pandatoufu/data-masking/internal/loader"
	"github.com/pandatoufu/data-masking/internal/pptxtest"
)

// 三种格式共用同一有效文件大小上限：签署/回程都按注入预算精确等于通过、加 1 拒绝，
// 且超限不返回本次产物。预算缩小到样本真实字节数，避免默认回归生成大文件。
func TestEffectiveFileSizeConsistentAcrossFormats(t *testing.T) {
	docxData, err := docxtest.BuildFullFixture(docxtest.FixtureOptions{Body: []string{"电话 13800138000"}})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		file    string
		data    []byte
		profile string
	}{
		{"text", "notes.txt", []byte("电话 13800138000\n"), "strict"},
		{"docx", "合同.docx", docxData, "strict"},
		{"pptx", "报告.pptx", pptxtest.Minimal("电话 13800138000"), "strict"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// 去程：等于样本字节数通过。
			limit := int64(len(c.data))
			cfg := config.Defaults()
			cfg.MaxFileSize = limit
			out, err := MaskFile(c.file, c.data, MaskOptions{Profile: c.profile, MaxSize: limit}, cfg, nil)
			if err != nil {
				t.Fatalf("等于上限应通过：%v", err)
			}
			if out == nil || len(out.MaskedBytes) == 0 || len(out.MappingBytes) == 0 {
				t.Fatalf("成功应返回完整产物：%+v", out)
			}

			// 回程：等于产物字节数通过。
			restoreLimit := int64(len(out.MaskedBytes))
			cfg.MaxFileSize = restoreLimit
			restored, err := RestoreFile(out.MaskedName, out.MaskedBytes, out.MappingBytes, cfg, nil)
			if err != nil {
				t.Fatalf("回程等于上限应通过：%v", err)
			}
			if restored == nil || len(restored.Bytes) == 0 {
				t.Fatal("回程成功应返回产物")
			}

			// 去程超限 1 字节：拒绝且无产物、原件字节不变。
			over := append([]byte(nil), c.data...)
			cfg.MaxFileSize = limit - 1
			got, err := MaskFile(c.file, over, MaskOptions{Profile: c.profile, MaxSize: limit - 1}, cfg, nil)
			if err == nil || got != nil {
				t.Fatalf("超过上限应拒绝且不返回产物：out=%v err=%v", got, err)
			}
			assertTooLarge(t, err)
			if !bytes.Equal(over, c.data) {
				t.Fatal("失败不得修改输入字节")
			}

			// 回程超限 1 字节：拒绝且无产物，配对 mapping 不被本次返回。
			cfg.MaxFileSize = restoreLimit - 1
			gotRestore, err := RestoreFile(out.MaskedName, out.MaskedBytes, out.MappingBytes, cfg, nil)
			if err == nil || gotRestore != nil {
				t.Fatalf("回程超过上限应拒绝且不返回产物：out=%v err=%v", gotRestore, err)
			}
			assertTooLarge(t, err)
		})
	}
}

func assertTooLarge(t *testing.T, err error) {
	t.Helper()
	var he *loader.HumanError
	if !errors.As(err, &he) || he.MessageID != "file.too_large" {
		t.Fatalf("应返回 file.too_large，got %#v", err)
	}
}
