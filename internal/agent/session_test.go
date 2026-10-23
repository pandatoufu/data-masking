package agent

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/pandatoufu/data-masking/internal/workspace"
)

func TestSecondSessionBusyAndReleaseAllowsRestart(t *testing.T) {
	root := t.TempDir()
	first, err := OpenSession(root)
	if err != nil {
		t.Fatalf("首个会话应可用：%v", err)
	}
	_, err = OpenSession(root)
	var ae *Error
	if !errors.As(err, &ae) || ae.Code != CodeBusy {
		t.Fatalf("第二个 headless 应 busy，got %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("关闭失败：%v", err)
	}
	second, err := OpenSession(root)
	if err != nil {
		t.Fatalf("释放后应可重启：%v", err)
	}
	_ = second.Close()
}

// GUI 不使用会话锁：headless 持有时 GUI 的工作区发布锁仍可获取。
func TestSessionLockDoesNotBlockGUIWorkspaceLock(t *testing.T) {
	root := t.TempDir()
	session, err := OpenSession(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	lock, err := workspace.AcquireLock(filepath.Join(root, workspace.LockFileName))
	if err != nil {
		t.Fatalf("GUI 发布锁不应被会话锁阻塞：%v", err)
	}
	_ = lock.Release()
}

func TestToolGateNonBlockingBusy(t *testing.T) {
	root := t.TempDir()
	session, err := OpenSession(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	if err := session.BeginTool(); err != nil {
		t.Fatalf("首次调用应进入：%v", err)
	}
	err = session.BeginTool()
	var ae *Error
	if !errors.As(err, &ae) || ae.Code != CodeBusy {
		t.Fatalf("并发调用应 busy，got %v", err)
	}
	session.EndTool()
	if err := session.BeginTool(); err != nil {
		t.Fatalf("结束后应可再次进入：%v", err)
	}
	session.EndTool()
}

func TestSessionRecoveryOnlyByHolder(t *testing.T) {
	root := t.TempDir()
	session, err := OpenSession(root)
	if err != nil {
		t.Fatal(err)
	}
	if !session.CanRecover() {
		t.Fatal("会话锁持有者应可恢复")
	}
	_ = session.Close()
	// 未持有会话锁的实例不得执行恢复。
	var nilSession *Session
	if nilSession.CanRecover() {
		t.Fatal("无会话锁不得恢复")
	}
}
