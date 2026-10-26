//go:build large

package largefiles

import (
	"strings"
	"testing"

	"github.com/pandatoufu/data-masking/internal/config"
	"github.com/pandatoufu/data-masking/internal/core"
)

// 4.2 DOCX/PPTX 实际解压总量边界与输出膨胀：
//   - >100MiB 与恰好 200MiB 的资源型样本可通过（文件体积仍 <50MiB）；
//   - 200MiB+1 拒绝且无产物；
//   - 文字替换使输出超过 200MiB 时拒绝且不返回本次文件/mapping，原件不变；
//   - 命中密集样本可正常完成（观察重复扫描成本，不在本测试断言耗时）。
func TestLargeBudgets(t *testing.T) {
	cfg := config.Defaults()
	hits := 20000
	hitText := strings.Repeat("甲", hits)
	words := []string{"甲"}

	cases := []struct {
		name    string
		file    string
		build   func(target int, body []string) []byte
		limitID string
	}{
		{"docx", "资源.docx", func(target int, body []string) []byte { return docxWithTotal(t, target, body) }, "docx.zip_budget"},
		{"pptx", "资源.pptx", func(target int, body []string) []byte { return pptxWithTotal(t, target, body) }, "pptx.zip_budget"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// 超过旧 100MiB 预算的样本仍可通过新预算。
			data150 := c.build(150<<20, []string{"边界样本 13800138000"})
			if got := totalUncompressed(t, data150); got != 150<<20 {
				t.Fatalf("150MiB 样本实际总量 %d", got)
			}
			if len(data150) >= size50MiB {
				t.Fatalf("150MiB 样本文件体积应远小于 50MiB：%d", len(data150))
			}
			if got := entryCount(t, data150); got >= entryCap {
				t.Fatalf("样本条目数 %d 不应触及 2048 上限", got)
			}
			if out, err := core.MaskFile(c.file, data150, core.MaskOptions{Profile: "strict"}, cfg, nil); err != nil || out == nil {
				t.Fatalf("150MiB 解压样本应处理成功：out=%v err=%v", out, err)
			}

			// 恰好 200MiB：总量门禁通过。
			data200 := c.build(budget200MiB, []string{"边界样本 13800138000"})
			if got := totalUncompressed(t, data200); got != budget200MiB {
				t.Fatalf("200MiB 样本实际总量 %d", got)
			}
			out200, err := core.MaskFile(c.file, data200, core.MaskOptions{Profile: "strict"}, cfg, nil)
			if err != nil || out200 == nil {
				t.Fatalf("恰好 200MiB 应通过：out=%v err=%v", out200, err)
			}
			if len(out200.MaskedBytes) >= size50MiB {
				t.Fatalf("200MiB 样本输出文件体积应小于 50MiB：%d", len(out200.MaskedBytes))
			}

			// 200MiB + 1：拒绝且无产物。
			data201 := c.build(budget200MiB+1, []string{"边界样本 13800138000"})
			if got := totalUncompressed(t, data201); got != budget200MiB+1 {
				t.Fatalf("200MiB+1 样本实际总量 %d", got)
			}
			out201, err := core.MaskFile(c.file, data201, core.MaskOptions{Profile: "strict"}, cfg, nil)
			if err == nil || out201 != nil {
				t.Fatalf("200MiB+1 应拒绝且无产物：out=%v err=%v", out201, err)
			}
			assertHumanID(t, err, c.limitID)

			// 输出膨胀：输入恰好 200MiB，替换后输出超过预算 → 无文件/mapping，原件不变。
			dataExpand := c.build(budget200MiB, []string{hitText})
			orig := append([]byte(nil), dataExpand...)
			outExpand, err := core.MaskFile(c.file, dataExpand, core.MaskOptions{
				Profile: "strict", ExtraCustom: words, ConfirmedCustom: words,
			}, cfg, nil)
			if err == nil || outExpand != nil {
				t.Fatalf("输出超过 200MiB 应拒绝且不返回产物：out=%v err=%v", outExpand, err)
			}
			if !strings.Contains(err.Error(), "预算") && !strings.Contains(err.Error(), "大小上限") {
				t.Fatalf("输出膨胀拒绝原因应为包预算，got %v", err)
			}
			if string(dataExpand) != string(orig) {
				t.Fatal("失败路径不得修改输入字节")
			}
		})
	}
}

// 命中密集样本（多词、多次命中）可完成且确实替换；作为扫描成本观察样本。
func TestLargeHitDense(t *testing.T) {
	cfg := config.Defaults()
	const total = 30 << 20
	var words []string
	var body []string
	for i := 0; i < 50; i++ {
		// 使用无私人信息的合成 CJK 词，50 个不同词各重复 400 次。
		w := string(rune(0x4E00 + i))
		words = append(words, w)
		body = append(body, strings.Repeat(w, 400))
	}
	for _, c := range []struct {
		name  string
		file  string
		build func(target int, body []string) []byte
	}{
		{"docx", "命中.docx", func(target int, body []string) []byte { return docxWithTotal(t, target, body) }},
		{"pptx", "命中.pptx", func(target int, body []string) []byte { return pptxWithTotal(t, target, body) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			data := c.build(total, body)
			out, err := core.MaskFile(c.file, data, core.MaskOptions{Profile: "strict", ExtraCustom: words, ConfirmedCustom: words}, cfg, nil)
			if err != nil {
				t.Fatalf("命中密集样本应处理成功：%v", err)
			}
			if out == nil || len(out.MaskedBytes) == 0 {
				t.Fatal("命中密集样本应返回产物")
			}
			totalHits := 0
			for _, n := range out.Coverage {
				totalHits += n
			}
			if totalHits == 0 {
				t.Fatalf("应至少命中一次：%+v", out.Coverage)
			}
		})
	}
}
