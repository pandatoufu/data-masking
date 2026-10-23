// Command genicon 在项目 Go 步骤中生成桌面应用图标，避免手工提交二进制资源。
//
// 图案为「脱敏文档」：蓝色圆角底、居中白色折角文档、两条长度不同的深蓝粗遮罩，
// 以及下方一条短辅助文字线。扁平风格，无文字、渐变或阴影，圆角外透明。
// 布局参数见 openspec/changes/windows-cross-build-icon/design.md §1。
//
// 用法（Windows PowerShell 与 Linux 通用）：
//
//	go run ./scripts/genicon
//
// 产物：build/appicon.png（Wails 构建时据此生成各平台图标）。
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

const size = 256

var (
	bgBlue   = color.RGBA{R: 0x25, G: 0x63, B: 0xEB, A: 0xFF}
	docWhite = color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
	maskDark = color.RGBA{R: 0x16, G: 0x3B, B: 0x80, A: 0xFF}
)

func main() {
	out := filepath.Join("build", "appicon.png")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "创建 build 目录失败：", err)
		os.Exit(1)
	}
	f, err := os.Create(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "创建图标失败：", err)
		os.Exit(1)
	}
	defer f.Close()
	if err := png.Encode(f, generate()); err != nil {
		fmt.Fprintln(os.Stderr, "编码图标失败：", err)
		os.Exit(1)
	}
	fmt.Println("已生成", out)
}

// generate 以确定性方式绘制 256 × 256 的脱敏文档图案。
// 相同输入只依赖常量，不含时间、随机数或外部图像，重复调用结果一致。
func generate() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	// 蓝色圆角底；圆角半径约为边长的 18%，圆角外保持透明。
	radius := float64(size) * 0.18
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if insideRoundedRect(x, y, 0, 0, size, size, radius) {
				img.SetRGBA(x, y, bgBlue)
			}
		}
	}

	// 居中白色文档，宽约 55%、高约 68%，右上角折角。
	docW := int(math.Round(0.55 * size))
	docH := int(math.Round(0.68 * size))
	docX := (size - docW) / 2
	docY := (size - docH) / 2
	cut := int(math.Round(0.14 * size))
	for y := docY; y < docY+docH; y++ {
		for x := docX; x < docX+docW; x++ {
			if inFoldedCorner(x, y, docX, docY, docW, cut) {
				continue
			}
			img.SetRGBA(x, y, docWhite)
		}
	}

	// 两条深蓝粗遮罩：高度约图宽 8%，间隔不小于图宽 6%，长度略有差异。
	barH := int(math.Round(0.078 * size))
	gap := int(math.Round(0.07 * size))
	total := 2*barH + gap
	barY0 := docY + (docH-total)/2
	centerX := docX + docW/2
	bar1Len := int(math.Round(0.44 * size))
	bar2Len := int(math.Round(0.36 * size))
	fillRect(img, centerX-bar1Len/2, barY0, bar1Len, barH, maskDark)
	fillRect(img, centerX-bar2Len/2, barY0+barH+gap, bar2Len, barH, maskDark)

	// 短辅助文字线：位于遮罩下方，与遮罩及文档边缘均不相连。
	lineLen := int(math.Round(0.24 * size))
	lineH := int(math.Round(0.02 * size))
	lineY := barY0 + 2*barH + gap + int(math.Round(0.055*size))
	fillRect(img, centerX-lineLen/2, lineY, lineLen, lineH, maskDark)

	return img
}

func fillRect(img *image.RGBA, x0, y0, w, h int, c color.RGBA) {
	for y := y0; y < y0+h; y++ {
		for x := x0; x < x0+w; x++ {
			if x >= 0 && y >= 0 && x < size && y < size {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

// inFoldedCorner 判断点是否落在文档右上角被折掉的三角形内。
// 折角三角形以文档右上角为直角，两条直角边长度均为 cut。
func inFoldedCorner(x, y, docX, docY, docW, cut int) bool {
	right := docX + docW
	if x <= right-cut || y >= docY+cut {
		return false
	}
	return (x-(right-cut))+(y-docY) >= cut
}

func insideRoundedRect(x, y, rx, ry, w, h int, r float64) bool {
	fx, fy := float64(x), float64(y)
	left, top := float64(rx)+r, float64(ry)+r
	right, bottom := float64(rx+w)-r, float64(ry+h)-r
	if fx >= left && fx <= right && fy >= float64(ry) && fy <= float64(ry+h) {
		return true
	}
	if fy >= top && fy <= bottom && fx >= float64(rx) && fx <= float64(rx+w) {
		return true
	}
	cx := left
	if fx > right {
		cx = right
	}
	cy := top
	if fy > bottom {
		cy = bottom
	}
	dx, dy := fx-cx, fy-cy
	return dx*dx+dy*dy <= r*r
}
