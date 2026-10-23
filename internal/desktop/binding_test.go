package desktop

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// R2-WARNING：绑定面必须窄：仅 Bridge 暴露保存/未保存状态/退出确认，
// 不把 App 的注入 setter（SetWorkAreaProvider 等）经反射带入 JS。
func TestBridgeBindingSurfaceIsNarrow(t *testing.T) {
	bt := reflect.TypeOf(&Bridge{})
	got := map[string]bool{}
	for i := 0; i < bt.NumMethod(); i++ {
		got[bt.Method(i).Name] = true
	}
	want := []string{
		"AutoSaveMask", "AutoSaveRestored", "ListWorkspaceFiles", "ReadWorkspaceFile",
		"ReportUnsaved", "SaveFile", "SetExitConfirmed", "SetUIPreferences",
	}
	if len(got) != len(want) {
		t.Fatalf("Bridge 绑定方法应仅 %v，实际 %v", want, methodNames(bt))
	}
	for _, w := range want {
		if !got[w] {
			t.Fatalf("Bridge 缺少导出方法 %q", w)
		}
	}
}

// App 不应作为绑定对象：其注入 setter 不在 Bridge 上。
func TestAppNotBoundDirectly(t *testing.T) {
	at := reflect.TypeOf(&App{})
	for _, forbidden := range []string{"SetWorkAreaProvider", "SetWindowPositionProvider", "SetWindow", "SetConfirm", "SetSessionBusCheck", "RunStartupPreflight", "SetWorkspaceRoot", "SetWorkspace"} {
		if _, ok := at.MethodByName(forbidden); !ok {
			t.Fatalf("App 应仍有内部方法 %q（仅供测试/适配，不绑定）", forbidden)
		}
		if _, ok := reflect.TypeOf(&Bridge{}).MethodByName(forbidden); ok {
			t.Fatalf("Bridge 不得暴露内部注入方法 %q", forbidden)
		}
	}
}

func methodNames(t reflect.Type) []string {
	var names []string
	for i := 0; i < t.NumMethod(); i++ {
		names = append(names, t.Method(i).Name)
	}
	sort.Strings(names)
	return names
}

// AutoSave* 在未初始化/非 Windows 工作区下返回 disabled，且不创建任何文件。
func TestBridgeAutoSaveDisabledWithoutWorkspace(t *testing.T) {
	app := NewApp(nil, nil)
	res := app.Bridge().AutoSaveMask("notes.masked.txt", b64("m"), "notes.masked.mapping.json", b64("{}"))
	if res.Masked.Status != workspaceStatusDisabled || res.Mapping.Status != workspaceStatusDisabled {
		t.Fatalf("未初始化工作区应返回 disabled：%+v", res)
	}
	if got := app.Bridge().AutoSaveRestored("notes.restored.txt", b64("r")); got.Status != workspaceStatusDisabled {
		t.Fatalf("未初始化工作区应返回 disabled：%+v", got)
	}
}

// AutoSaveMask 由方法决定目标目录，前端只给文件名；配对同号落盘。
func TestBridgeAutoSaveMaskWritesPairDirs(t *testing.T) {
	root := t.TempDir()
	app := NewApp(nil, nil)
	app.SetWorkspace(newEnabledWorkspace(t, root, nil))

	res := app.Bridge().AutoSaveMask("notes.masked.txt", b64("masked-bytes"), "notes.masked.mapping.json", b64("mapping-bytes"))
	if res.Masked.Status != workspaceStatusSaved || res.Mapping.Status != workspaceStatusSaved {
		t.Fatalf("应成对落盘：%+v", res)
	}
	if res.Masked.Path != filepath.Join(root, "masked", "notes.masked.txt") {
		t.Fatalf("masked 路径应由方法决定，got %q", res.Masked.Path)
	}
	if res.Mapping.Path != filepath.Join(root, "mapping", "notes.masked.mapping.json") {
		t.Fatalf("mapping 路径应由方法决定，got %q", res.Mapping.Path)
	}
	if got, err := os.ReadFile(res.Masked.Path); err != nil || string(got) != "masked-bytes" {
		t.Fatalf("masked 字节应完整：%q err=%v", got, err)
	}
}

// 非法文件名被拒绝、返回 failed，且不产生目录逃逸文件。
func TestBridgeAutoSaveRejectsInvalidNames(t *testing.T) {
	root := t.TempDir()
	app := NewApp(nil, nil)
	app.SetWorkspace(newEnabledWorkspace(t, root, nil))

	res := app.Bridge().AutoSaveMask("../escape.txt", b64("m"), "ok.mapping.json", b64("{}"))
	if res.Masked.Status != workspaceStatusFailed || res.Mapping.Status != workspaceStatusFailed {
		t.Fatalf("非法 masked 名应拒绝：%+v", res)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape.txt")); err == nil {
		t.Fatal("不得产生目录逃逸文件")
	}
	if got := app.Bridge().AutoSaveRestored("a/b.txt", b64("x")); got.Status != workspaceStatusFailed {
		t.Fatalf("非法 restored 名应拒绝：%+v", got)
	}
}
