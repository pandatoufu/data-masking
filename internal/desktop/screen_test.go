package desktop

import "testing"

func TestClampNormalWorkspace(t *testing.T) {
	initial, min := ClampWindowSize(WindowSize{Width: 1920, Height: 1080})
	if initial.Width != InitialWidth || initial.Height != InitialHeight {
		t.Fatalf("正常工作区应使用初始尺寸：%+v", initial)
	}
	if min.Width != RegularMinWidth || min.Height != RegularMinHeight {
		t.Fatalf("正常工作区应使用常规最小尺寸：%+v", min)
	}
}

func TestClampSmallWorkspaceShrinks(t *testing.T) {
	initial, min := ClampWindowSize(WindowSize{Width: 500, Height: 380})
	if initial.Width != 500 || initial.Height != 380 {
		t.Fatalf("初始尺寸不应超过工作区：%+v", initial)
	}
	if min.Width > initial.Width || min.Height > initial.Height {
		t.Fatalf("有效最小不应超过初始：min=%+v initial=%+v", min, initial)
	}
}

func TestClampTinyWorkspaceNotClipped(t *testing.T) {
	initial, min := ClampWindowSize(WindowSize{Width: 300, Height: 200})
	if initial.Width != 300 || initial.Height != 200 {
		t.Fatalf("极小工作区初始应贴合工作区：%+v", initial)
	}
	if min.Width != 300 || min.Height != 200 {
		t.Fatalf("极小工作区有效最小应贴合初始：%+v", min)
	}
}

func TestClampZeroFallsBack(t *testing.T) {
	initial, _ := ClampWindowSize(WindowSize{})
	if initial.Width != InitialWidth || initial.Height != InitialHeight {
		t.Fatalf("未知工作区应回退初始尺寸：%+v", initial)
	}
}
