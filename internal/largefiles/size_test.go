//go:build large

package largefiles

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/pandatoufu/data-masking/internal/config"
	"github.com/pandatoufu/data-masking/internal/core"
)

// 4.1 三种格式的真实文件大小边界：>10MiB、>32MiB、接近/等于/超过 50MiB。
// 断言使用真实字节长度与拒绝原因，不使用文件名或声明大小代替；默认快速回归不运行本文件。
func TestLargeSizeBoundaries(t *testing.T) {
	cfg := config.Defaults()
	if cfg.MaxFileSize != size50MiB {
		t.Fatalf("默认文件上限应为 %d，got %d", size50MiB, cfg.MaxFileSize)
	}

	formats := []struct {
		name  string
		file  string
		build func(target int) []byte
	}{
		{"text", "边界.txt", func(target int) []byte { return largeText(target) }},
		{"docx", "边界.docx", func(target int) []byte { return sizedDocx(t, target) }},
		{"pptx", "边界.pptx", func(target int) []byte { return sizedPptx(t, target) }},
	}
	sizes := []int{
		size10MiB + 1,
		size32MiB + 1,
		size50MiB - 1,
		size50MiB,
		size50MiB + 1,
	}
	for _, f := range formats {
		for _, size := range sizes {
			t.Run(fmt.Sprintf("%s_%d", f.name, size), func(t *testing.T) {
				data := f.build(size)
				orig := append([]byte(nil), data...)
				if len(data) != size {
					t.Fatalf("样本真实长度 %d != 目标 %d", len(data), size)
				}
				if len(data) > size50MiB && size != size50MiB+1 {
					t.Fatalf("用例设计错误：%d", size)
				}
				out, err := core.MaskFile(f.file, data, core.MaskOptions{Profile: "strict"}, cfg, nil)
				if size > size50MiB {
					if err == nil || out != nil {
						t.Fatalf("%d 字节应超过默认上限并拒绝：out=%v err=%v", size, out, err)
					}
					assertHumanID(t, err, "file.too_large")
					return
				}
				if err != nil {
					t.Fatalf("%d 字节应通过文件门禁并完成处理：%v", size, err)
				}
				if out == nil || len(out.MaskedBytes) == 0 || len(out.MappingBytes) == 0 {
					t.Fatalf("%d 字节成功时应有完整产物", size)
				}
				if !bytes.Equal(data, orig) {
					t.Fatalf("%d 字节输入被修改", size)
				}
			})
		}
	}
}
