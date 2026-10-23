package desktop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pandatoufu/data-masking/internal/workspace"
)

// Bridge 只暴露四类固定用途的列表/读取；合法请求只读允许目录内的相应用途文件。
func TestBridgeWorkspaceInputListsAndReadsAllowedFiles(t *testing.T) {
	root := t.TempDir()
	app := NewApp(nil, nil)
	app.SetWorkspace(newEnabledWorkspace(t, root, nil))
	seedInputDir(t, root, workspace.DirOriginal, map[string][]byte{"notes.txt": []byte("original-bytes")})
	seedInputDir(t, root, workspace.DirCards, map[string][]byte{"words.txt": []byte("内部代号\n")})
	seedInputDir(t, root, workspace.DirPending, map[string][]byte{"reply.txt": []byte("reply-bytes")})
	seedInputDir(t, root, workspace.DirMapping, map[string][]byte{"pair.json": []byte("{}")})

	for _, kind := range []string{WorkspaceInputOriginal, WorkspaceInputCards, WorkspaceInputPending, WorkspaceInputMapping} {
		list := app.Bridge().ListWorkspaceFiles(kind)
		if list.Status != inputStatusOK || len(list.Files) != 1 {
			t.Fatalf("%s 列表应只有一个文件：%+v", kind, list)
		}
		if list.Kind != kind {
			t.Fatalf("列表应回显 kind：%+v", list)
		}
	}

	read := app.Bridge().ReadWorkspaceFile(WorkspaceInputOriginal, "notes.txt")
	if read.Status != inputStatusOK || read.Name != "notes.txt" || read.Kind != WorkspaceInputOriginal {
		t.Fatalf("应读取固定目录内的单个文件：%+v", read)
	}
	if got := decodedContent(t, read); string(got) != "original-bytes" {
		t.Fatalf("应返回真实字节：%q", got)
	}
}

// 伪造任意路径、子目录、未知用途都在 binding 层被拒绝，不提供任意路径访问。
func TestBridgeWorkspaceInputRejectsArbitraryPaths(t *testing.T) {
	root := t.TempDir()
	app := NewApp(nil, nil)
	app.SetWorkspace(newEnabledWorkspace(t, root, nil))
	secret := filepath.Join(filepath.Dir(root), "secret.txt")
	if err := os.WriteFile(secret, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"../original", "Original", "", workspace.AgentStateDir, "tasks"} {
		if got := app.Bridge().ListWorkspaceFiles(kind); got.Status != inputStatusFailed || got.MessageID != "input.bad_kind" {
			t.Fatalf("未知用途 %q 应拒绝：%+v", kind, got)
		}
		if got := app.Bridge().ReadWorkspaceFile(kind, "anything.txt"); got.Status != inputStatusFailed || got.MessageID != "input.bad_kind" {
			t.Fatalf("未知用途 %q 读取应拒绝：%+v", kind, got)
		}
	}
	for _, name := range []string{"../secret.txt", secret, "nested/notes.txt", `nested\notes.txt`, "notes.txt:ads", "NUL.txt"} {
		got := app.Bridge().ReadWorkspaceFile(WorkspaceInputOriginal, name)
		if got.Status != inputStatusFailed || got.DataB64 != "" {
			t.Fatalf("任意路径 %q 应拒绝：%+v", name, got)
		}
	}
	if _, err := os.Stat(secret); err != nil {
		t.Fatalf("拒绝请求不得产生副作用：%v", err)
	}
}

// 列表/读取复用保存与关闭的原子协调：忙时返回可重试忙态，失败/成功都释放保留，
// 且不推进未保存 generation。
func TestBridgeWorkspaceInputCoordinationAndNoGenerationSideEffects(t *testing.T) {
	root := t.TempDir()
	app := NewApp(nil, nil)
	app.SetWorkspace(newEnabledWorkspace(t, root, nil))
	seedInputDir(t, root, workspace.DirCards, map[string][]byte{"words.txt": []byte("w\n")})
	gen := app.Life.UnsavedGeneration()

	if !app.Saver.ReserveOperation() {
		t.Fatal("前置：应可保留一次操作")
	}
	if got := app.Bridge().ListWorkspaceFiles(WorkspaceInputCards); got.Status != inputStatusBusy || got.MessageID != "input.busy" {
		t.Fatalf("处理中列举应返回忙态：%+v", got)
	}
	if got := app.Bridge().ReadWorkspaceFile(WorkspaceInputCards, "words.txt"); got.Status != inputStatusBusy || got.DataB64 != "" {
		t.Fatalf("处理中读取应返回忙态：%+v", got)
	}
	app.Saver.ReleaseOperation()
	if app.Saver.IsProcessing() {
		t.Fatal("释放后不应仍处于处理中")
	}

	if got := app.Bridge().ListWorkspaceFiles(WorkspaceInputCards); got.Status != inputStatusOK {
		t.Fatalf("释放后应可列举：%+v", got)
	}
	if app.Saver.IsProcessing() {
		t.Fatal("列举结束应释放操作保留（包括错误路径）")
	}
	if got := app.Bridge().ReadWorkspaceFile(WorkspaceInputCards, "../x.txt"); got.Status != inputStatusFailed {
		t.Fatalf("非法读取应失败：%+v", got)
	}
	if app.Saver.IsProcessing() {
		t.Fatal("读取失败也必须释放操作保留")
	}

	// 已接受退出（关闭保留）后不再开始新的目录访问。
	if !app.Saver.ReserveClose() {
		t.Fatal("前置：应可保留关闭")
	}
	if got := app.Bridge().ListWorkspaceFiles(WorkspaceInputCards); got.Status != inputStatusBusy {
		t.Fatalf("关闭保留后列举应返回忙态：%+v", got)
	}
	if got := app.Bridge().ReadWorkspaceFile(WorkspaceInputCards, "words.txt"); got.Status != inputStatusBusy {
		t.Fatalf("关闭保留后读取应返回忙态：%+v", got)
	}
	app.Saver.ReleaseClose()

	if app.Life.UnsavedGeneration() != gen {
		t.Fatalf("目录列表/读取不得推进未保存 generation：%d -> %d", gen, app.Life.UnsavedGeneration())
	}
	if app.Life.HasUnsaved() {
		t.Fatal("目录列表/读取不得产生未保存产物标记")
	}
}

// 并发列表/读取不会卡死协调状态：忙态可重试，结束后仍可正常访问。
func TestBridgeWorkspaceInputConcurrentAccessReleasesState(t *testing.T) {
	root := t.TempDir()
	app := NewApp(nil, nil)
	app.SetWorkspace(newEnabledWorkspace(t, root, nil))
	seedInputDir(t, root, workspace.DirOriginal, map[string][]byte{"notes.txt": []byte("ok")})

	done := make(chan struct{}, 16)
	for i := 0; i < 16; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 20; j++ {
				if got := app.Bridge().ListWorkspaceFiles(WorkspaceInputOriginal); got.Status != inputStatusOK && got.Status != inputStatusBusy {
					t.Errorf("并发列举只应成功或可重试忙态：%+v", got)
					return
				}
				if got := app.Bridge().ReadWorkspaceFile(WorkspaceInputOriginal, "notes.txt"); got.Status != inputStatusOK && got.Status != inputStatusBusy {
					t.Errorf("并发读取只应成功或可重试忙态：%+v", got)
					return
				}
			}
		}()
	}
	for i := 0; i < 16; i++ {
		<-done
	}
	if app.Saver.IsProcessing() {
		t.Fatal("并发结束后不应残留操作保留")
	}
	if got := app.Bridge().ListWorkspaceFiles(WorkspaceInputOriginal); got.Status != inputStatusOK {
		t.Fatalf("并发结束后应可正常列举：%+v", got)
	}
}
