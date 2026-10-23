//go:build desktop && windows

package desktop

import (
	"syscall"
	"unsafe"
)

// Windows API 常量。
const (
	monitorDefaultToNearest = 2
	mdtEffectiveDPI         = 0
)

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	shcore               = syscall.NewLazyDLL("shcore.dll")
	procMonitorFromPoint = user32.NewProc("MonitorFromPoint")
	procGetMonitorInfoW  = user32.NewProc("GetMonitorInfoW")
	procGetDpiForMonitor = shcore.NewProc("GetDpiForMonitor")
)

type point struct{ X, Y int32 }

type rect struct{ Left, Top, Right, Bottom int32 }

type monitorInfo struct {
	CbSize    uint32
	RcMonitor rect
	RcWork    rect
	DwFlags   uint32
}

const monitorInfoPrimary = 1

// packPoint 把 POINT 按 Windows amd64 ABI 打包为单个 uintptr 值参数。
// POINT 是两个 int32（8 字节），Win64 按值放入寄存器；syscall 只能传 uintptr，
// 因此低 32 位 = X、高 32 位 = Y。绝不能传结构体地址（那样会被解释为坐标）。
func packPoint(p point) uintptr {
	return uintptr(uint32(p.X)) | uintptr(uint32(p.Y))<<32
}

// platformWorkAreaAt 返回包含坐标 (x,y) 的显示器可用工作区矩形 rcWork，
// 并按该显示器 DPI 把物理像素换算为 Wails 逻辑像素（96 DPI 基准）。
// rcWork 已排除任务栏/停靠栏，故不额外做固定像素扣除；支持负坐标与多屏。
// Windows 的 Wails SetSize/SetPosition 使用外框尺寸与屏幕坐标，故位置同样按逻辑坐标换算。
func platformWorkAreaAt(x, y int) (WorkAreaRect, bool) {
	hmon, _, _ := procMonitorFromPoint.Call(packPoint(point{X: int32(x), Y: int32(y)}), monitorDefaultToNearest)
	if hmon == 0 {
		return WorkAreaRect{}, false
	}
	var mi monitorInfo
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	ret, _, _ := procGetMonitorInfoW.Call(hmon, uintptr(unsafe.Pointer(&mi)))
	if ret == 0 {
		return WorkAreaRect{}, false
	}
	workW := int(mi.RcWork.Right - mi.RcWork.Left)
	workH := int(mi.RcWork.Bottom - mi.RcWork.Top)
	if workW <= 0 || workH <= 0 {
		return WorkAreaRect{}, false
	}

	dpiX, dpiY := uint32(96), uint32(96)
	if procGetDpiForMonitor.Find() == nil {
		var dx, dy uint32
		if r, _, _ := procGetDpiForMonitor.Call(hmon, mdtEffectiveDPI, uintptr(unsafe.Pointer(&dx)), uintptr(unsafe.Pointer(&dy))); r == 0 && dx > 0 && dy > 0 {
			dpiX, dpiY = dx, dy
		}
	}
	logicalW := scalePhysicalToLogical(workW, dpiX)
	logicalH := scalePhysicalToLogical(workH, dpiY)
	logicalX := scalePhysicalToLogical(int(mi.RcWork.Left), dpiX)
	logicalY := scalePhysicalToLogical(int(mi.RcWork.Top), dpiY)
	if logicalW <= 0 || logicalH <= 0 {
		return WorkAreaRect{}, false
	}
	return WorkAreaRect{X: logicalX, Y: logicalY, Width: logicalW, Height: logicalH, DpiX: int(dpiX), DpiY: int(dpiY)}, true
}

// platformWorkArea 以窗口所在坐标为基准查询工作区；无坐标时回退到 (0,0)。
func platformWorkArea(x, y int) (WorkAreaRect, bool) {
	return platformWorkAreaAt(x, y)
}

// scalePhysicalToLogical 把物理像素按 DPI 换算为 96 DPI 基准的逻辑像素。
func scalePhysicalToLogical(physical int, dpi uint32) int {
	if dpi == 0 {
		return physical
	}
	return int(float64(physical) * 96.0 / float64(dpi))
}
