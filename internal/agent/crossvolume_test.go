package agent

import (
	"os"
	"syscall"
	"testing"
)

// 跨卷交付：项目位于不同文件系统时按字节复制并在目标卷内 staging 发布，不跨卷硬链接。
// 无第二个可用文件系统时跳过（不假称已覆盖）。
func TestCrossVolumeDeliveryUsesTargetStaging(t *testing.T) {
	app, root := newTestApp(t, fakeMasker{})
	seedOriginal(t, root, "notes.txt", []byte("13800138000"))

	rootDev := deviceOf(t, root)
	var project string
	for _, candidate := range []string{"/dev/shm", "/tmp", "/var/tmp"} {
		if _, err := os.Stat(candidate); err != nil {
			continue
		}
		dir, err := os.MkdirTemp(candidate, "agent-xvol-")
		if err != nil {
			continue
		}
		if deviceOf(t, dir) != rootDev {
			project = dir
			break
		}
		_ = os.RemoveAll(dir)
	}
	if project == "" {
		t.Skip("无第二个可用文件系统：跨卷交付用例按环境运行，不假称已通过")
	}
	t.Cleanup(func() { _ = os.RemoveAll(project) })

	resp := app.Prepare(PrepareRequest{SourceName: "notes.txt", ProjectPath: project, Profile: "strict"})
	if resp.Status != StatusDelivered {
		t.Fatalf("跨卷交付应成功：%+v", resp)
	}
	info, err := os.Stat(resp.ProjectFile)
	if err != nil {
		t.Fatalf("项目副本应存在：%v", err)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int64(st.Nlink) != 1 {
		t.Fatalf("跨卷交付不得使用硬链接（nlink=%d）", st.Nlink)
	}
	if got, _ := os.ReadFile(resp.ProjectFile); len(got) == 0 {
		t.Fatal("项目副本字节应为完整脱敏内容")
	}
}

func deviceOf(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev)
	}
	return 0
}
