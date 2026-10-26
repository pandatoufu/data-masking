package docx

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pandatoufu/data-masking/internal/docxtest"
)

// 默认预算数字：总解压 200MiB，条目/单 XML/深度不随总量扩容放大。
func TestDefaultLimitsNumbers(t *testing.T) {
	d := DefaultLimits()
	if d.MaxTotalUncompressed != 209715200 {
		t.Fatalf("默认总解压预算应为 209715200 字节，got %d", d.MaxTotalUncompressed)
	}
	if d.MaxEntries != 2048 {
		t.Fatalf("条目上限应保持 2048，got %d", d.MaxEntries)
	}
	if d.MaxXMLPart != 20*1024*1024 {
		t.Fatalf("单 XML 上限应保持 20MiB，got %d", d.MaxXMLPart)
	}
	if d.MaxDepth != 128 {
		t.Fatalf("XML 深度上限应保持 128，got %d", d.MaxDepth)
	}
	if z := (Limits{}).normalize(); z != d {
		t.Fatalf("零值 normalize 应回填默认预算：%+v != %+v", z, d)
	}
}

// 精确总量边界：等于实际读取总量通过，少 1 字节拒绝；总量由实际读取字节核算，不看声明大小。
func TestTotalUncompressedExactBoundary(t *testing.T) {
	data := fullFixture(t, false)
	entries, err := openZip(data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, e := range entries {
		total += int64(len(e.data))
	}
	if total <= 0 {
		t.Fatal("样本应有非零实际解压总量")
	}

	lim := DefaultLimits()
	lim.MaxTotalUncompressed = total
	if _, err := Open(data, lim); err != nil {
		t.Fatalf("恰好等于实际总量应通过：%v", err)
	}

	lim.MaxTotalUncompressed = total - 1
	_, err = Open(data, lim)
	if err == nil {
		t.Fatal("超过实际总量 1 字节应拒绝")
	}
	if !strings.Contains(err.Error(), "实际解压总量") {
		t.Fatalf("拒绝原因应为总解压预算，got %v", err)
	}
	// 其他门禁未误触发：单 XML 与条目数远超本样本所需。
	if lim.MaxEntries != 2048 || lim.MaxXMLPart != 20*1024*1024 || lim.MaxDepth != 128 {
		t.Fatal("边界测试不应更改其他默认预算")
	}
}

// 输出预算回归：去程/回程式回写使实际解压总量超过注入预算时必须失败且不返回产物，
// 原件字节与保留资源不被修改；失败输出重开校验同样阻断。
func TestOutputBudgetForwardAndReverse(t *testing.T) {
	img := make([]byte, 16*1024)
	for i := range img {
		img[i] = byte(i*31 + 7)
	}
	data, err := docxtest.BuildFullFixture(docxtest.FixtureOptions{Body: []string{"电话 13800138000"}, Image: img})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := openZip(data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, e := range entries {
		total += int64(len(e.data))
	}
	// 输入恰好用完预算：任何文字膨胀都必然让输出超过同一预算。
	lim := DefaultLimits()
	lim.MaxTotalUncompressed = total

	for _, c := range []struct {
		name string
		repl string
	}{
		{"mask-style", strings.Repeat("X", 4096)},
		{"restore-style", strings.Repeat("长", 4096)},
	} {
		t.Run(c.name, func(t *testing.T) {
			doc, err := Open(data, lim)
			if err != nil {
				t.Fatalf("输入恰好等于预算应可打开：%v", err)
			}
			if _, err := doc.Prepare(); err != nil {
				t.Fatal(err)
			}
			u := doc.Units()[0]
			if err := doc.Apply(u, []Edit{{Start: 0, End: len(u.Text), Replacement: c.repl}}); err != nil {
				t.Fatal(err)
			}
			out, err := doc.Bytes()
			if err == nil || out != nil {
				t.Fatalf("输出超过预算应失败且不返回产物：len=%d err=%v", len(out), err)
			}
			if !bytes.Equal(doc.original, data) {
				t.Fatal("失败不得修改原件字节")
			}
			// 保留资源检查：原始媒体部件仍可读出且字节一致。
			orig, err := openZip(data, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, e := range orig {
				if e.name == "word/media/image1.png" {
					found = true
					if !bytes.Equal(e.data, img) {
						t.Fatal("保留资源在失败路径被修改")
					}
				}
			}
			if !found {
				t.Fatal("样本应含保留媒体部件")
			}
		})
	}
}

// 输出重开失败必须阻断，不返回重开后的文档。
func TestReopenValidatedRejectsTamperedOutput(t *testing.T) {
	data := fullFixture(t, false)
	doc, err := Open(data, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doc.Prepare(); err != nil {
		t.Fatal(err)
	}
	out, err := doc.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	broken := replacePartInZip(t, out, "word/document.xml", func(b []byte) []byte {
		if len(b) < 4 {
			return b
		}
		return b[:len(b)/2]
	})
	if reopened, err := doc.ReopenValidated(broken); err == nil || reopened != nil {
		t.Fatalf("截断 XML 的重开校验应失败：%v", err)
	}
}
