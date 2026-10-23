package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestGenerateSizeAndTransparentCorners(t *testing.T) {
	img := generate()
	b := img.Bounds()
	if b.Dx() != 256 || b.Dy() != 256 {
		t.Fatalf("尺寸应为 256×256，实际 %dx%d", b.Dx(), b.Dy())
	}
	for _, corner := range [][2]int{{0, 0}, {255, 0}, {0, 255}, {255, 255}} {
		if c := img.RGBAAt(corner[0], corner[1]); c.A != 0 {
			t.Fatalf("圆角外坐标 %v 应透明，实际 %+v", corner, c)
		}
	}
	// 圆角内部应为背景蓝色。
	if c := img.RGBAAt(6, 128); !isBackground(c) {
		t.Fatalf("左侧圆角内应为背景蓝，实际 %+v", c)
	}
}

func TestGenerateDocumentAndDoubleMask(t *testing.T) {
	img := generate()
	counts := map[string]int{}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			switch c := img.RGBAAt(x, y); {
			case isWhite(c):
				counts["white"]++
			case isMask(c):
				counts["mask"]++
			case isBackground(c):
				counts["bg"]++
			}
		}
	}
	if counts["white"] < size*size/4 {
		t.Fatalf("白色文档区域过小：%d", counts["white"])
	}
	if counts["bg"] < size*size*2/5 {
		t.Fatalf("蓝色背景区域过小：%d", counts["bg"])
	}
	if counts["mask"] < 3000 {
		t.Fatalf("深蓝遮罩 / 辅助线像素过少：%d", counts["mask"])
	}
	// 至少两条厚遮罩行带彼此分离，另有细辅助线，合计不连成一片。
	runs := darkRowRuns(img)
	thick := 0
	for _, h := range runs {
		if h >= 10 {
			thick++
		}
	}
	if thick < 2 {
		t.Fatalf("应有两条分离的粗遮罩，实际厚行带=%d，全部行带=%v", thick, runs)
	}
	if len(runs) < 3 {
		t.Fatalf("遮罩与辅助线应至少形成 3 段深色行带，实际 %v", runs)
	}
}

func TestGenerateSmallMasksRemainSeparated(t *testing.T) {
	img := generate()
	for _, n := range []int{16, 32} {
		small := downscaleNN(img, n)
		runs := darkRowRuns(small)
		thick := 0
		for _, h := range runs {
			if h >= 1 {
				thick++
			}
		}
		if thick < 2 {
			t.Fatalf("%dpx 缩略图应保留两条分离遮罩，实际行带=%v", n, runs)
		}
		// 文档主体仍存在。
		if !hasWhite(small) {
			t.Fatalf("%dpx 缩略图缺少白色文档主体", n)
		}
	}
}

func TestGenerateDeterministic(t *testing.T) {
	encode := func() []byte {
		var buf bytes.Buffer
		if err := png.Encode(&buf, generate()); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	first, second := encode(), encode()
	if !bytes.Equal(first, second) {
		t.Fatalf("相同输入两次生成应一致：长度 %d vs %d", len(first), len(second))
	}
}

// --- helpers ---

func isWhite(c color.RGBA) bool {
	return c.A == 0xFF && c.R >= 0xF0 && c.G >= 0xF0 && c.B >= 0xF0
}

func isMask(c color.RGBA) bool {
	// 深蓝遮罩 #163B80。
	return c == maskDark
}

func isBackground(c color.RGBA) bool {
	return c == bgBlue
}

func hasWhite(img *image.RGBA) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if isWhite(img.RGBAAt(x, y)) {
				return true
			}
		}
	}
	return false
}

// darkRowRuns 返回含深蓝遮罩像素的连续行数（按行高排序为出现顺序）。
func darkRowRuns(img *image.RGBA) []int {
	b := img.Bounds()
	var runs []int
	current := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		rowDark := false
		for x := b.Min.X; x < b.Max.X; x++ {
			if isMask(img.RGBAAt(x, y)) {
				rowDark = true
				break
			}
		}
		if rowDark {
			current++
		} else if current > 0 {
			runs = append(runs, current)
			current = 0
		}
	}
	if current > 0 {
		runs = append(runs, current)
	}
	return runs
}

func downscaleNN(src *image.RGBA, n int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, n, n))
	sb := src.Bounds()
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			sx := sb.Min.X + x*sb.Dx()/n
			sy := sb.Min.Y + y*sb.Dy()/n
			dst.SetRGBA(x, y, src.RGBAAt(sx, sy))
		}
	}
	return dst
}
