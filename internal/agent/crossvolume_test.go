package agent

import (
	"os"
	"testing"
)

// 跨卷交付：项目位于不同文件系统时按字节复制并在目标卷内 staging 发布，不跨卷硬链接。
// 设备号与链接数的读取按平台拆分在 crossvolume_fs_*.go：
// POSIX 读取 syscall.Stat_t，Windows 读取卷序列号与句柄链接数。
// 无第二个可用文件系统时跳过（不假称已覆盖）。
func TestCrossVolumeDeliveryUsesTargetStaging(t *testing.T) {
	app, root := newTestApp(t, fakeMasker{})
	seedOriginal(t, root, "notes.txt", []byte("13800138000"))

	rootDev := fsDeviceID(t, root)
	var project string
	for _, candidate := range crossVolumeCandidates() {
		if _, err := os.Stat(candidate); err != nil {
			continue
		}
		dir, err := os.MkdirTemp(candidate, "agent-xvol-")
		if err != nil {
			continue
		}
		if fsDeviceID(t, dir) != rootDev {
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
	assertNotHardLink(t, resp.ProjectFile, info)
	if got, _ := os.ReadFile(resp.ProjectFile); len(got) == 0 {
		t.Fatal("项目副本字节应为完整脱敏内容")
	}
}
