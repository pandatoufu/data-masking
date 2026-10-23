package workspace

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLockBusyAndReleaseReusesResidualFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".workspace.lock")
	first, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("首次获取失败：%v", err)
	}
	if _, err := AcquireLock(path); !errors.Is(err, ErrBusy) {
		t.Fatalf("第二次应 ErrBusy，got %v", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("释放失败：%v", err)
	}
	// 残留锁文件不等于活跃锁：释放后同一路径可再次获取。
	second, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("释放后应可获取：%v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("锁文件应保留在磁盘：%v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("二次释放失败：%v", err)
	}
}

func TestLockResidualFileWithoutHolderIsAcquirable(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".workspace.lock")
	if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("残留文件应可获取：%v", err)
	}
	_ = lock.Release()
}

func TestLockRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, ".workspace.lock")
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("Windows 无创建符号链接权限：平台特有 reparse 用例按平台运行")
		}
		t.Fatalf("创建符号链接失败：%v", err)
	}
	if _, err := AcquireLock(link); !errors.Is(err, ErrUnsafeLock) {
		t.Fatalf("符号链接应 ErrUnsafeLock，got %v", err)
	}
}

// TestLockSubprocessContentionAndCrashRelease 用真实子进程验证争用与 kill 后释放。
func TestLockSubprocessContentionAndCrashRelease(t *testing.T) {
	if os.Getenv("WORKSPACE_LOCK_HELPER") == "1" {
		t.Skip("helper 模式由子进程执行")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ".workspace.lock")

	first := startLockHelper(t, path)
	defer func() {
		if first.stdin != nil {
			_ = first.stdin.Close()
		}
		if first.cmd.Process != nil {
			_ = first.cmd.Process.Kill()
			_, _ = first.cmd.Process.Wait()
		}
	}()
	waitForLocked(t, first)

	// 第二个真实子进程应报告 busy。
	second := exec.Command(os.Args[0], "-test.run=TestLockHelperProcess", "-test.v=false")
	second.Env = append(os.Environ(),
		"WORKSPACE_LOCK_HELPER=1",
		"WORKSPACE_LOCK_HELPER_PATH="+path,
	)
	out, err := second.CombinedOutput()
	if err == nil {
		t.Fatalf("第二个子进程应因忙失败，输出=%q", out)
	}
	if !strings.Contains(string(out), "busy") {
		t.Fatalf("第二个子进程应输出 busy：%q err=%v", out, err)
	}

	// 自进程获取同样应忙。
	if _, err := AcquireLock(path); !errors.Is(err, ErrBusy) {
		t.Fatalf("持锁期间本进程应 ErrBusy，got %v", err)
	}

	// kill 掉持锁子进程：OS 释放锁，随后可重启获取。
	if err := first.cmd.Process.Kill(); err != nil {
		t.Fatalf("kill 失败：%v", err)
	}
	_, _ = first.cmd.Process.Wait()

	deadline := time.Now().Add(5 * time.Second)
	for {
		lock, err := AcquireLock(path)
		if err == nil {
			_ = lock.Release()
			break
		}
		if !errors.Is(err, ErrBusy) {
			t.Fatalf("kill 后获取应成功或暂时 busy，got %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("kill 后锁未被释放")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

type lockHelper struct {
	cmd    *exec.Cmd
	stdout *bufio.Scanner
	stdin  interface{ Close() error }
}

func startLockHelper(t *testing.T, lockPath string) *lockHelper {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestLockHelperProcess", "-test.v=false")
	cmd.Env = append(os.Environ(),
		"WORKSPACE_LOCK_HELPER=1",
		"WORKSPACE_LOCK_HELPER_PATH="+lockPath,
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe 失败：%v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe 失败：%v", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动 helper 失败：%v", err)
	}
	return &lockHelper{cmd: cmd, stdout: bufio.NewScanner(stdout), stdin: stdin}
}

func waitForLocked(t *testing.T, h *lockHelper) {
	t.Helper()
	done := make(chan string, 1)
	go func() {
		if !h.stdout.Scan() {
			done <- ""
			return
		}
		done <- h.stdout.Text()
	}()
	select {
	case line := <-done:
		if strings.TrimSpace(line) != "locked" {
			t.Fatalf("helper 未持锁：%q", line)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("等待 helper 持锁超时")
	}
}

// TestLockHelperProcess 是子进程入口：获取锁，输出状态，阻塞到 stdin 关闭。
func TestLockHelperProcess(t *testing.T) {
	if os.Getenv("WORKSPACE_LOCK_HELPER") != "1" {
		return
	}
	path := os.Getenv("WORKSPACE_LOCK_HELPER_PATH")
	lock, err := AcquireLock(path)
	if err != nil {
		if errors.Is(err, ErrBusy) {
			fmt.Println("busy")
			os.Exit(3)
		}
		fmt.Println("error")
		os.Exit(4)
	}
	fmt.Println("locked")
	_ = os.Stdout.Sync()
	var b [1]byte
	_, _ = os.Stdin.Read(b[:])
	_ = lock.Release()
	os.Exit(0)
}
