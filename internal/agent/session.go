package agent

import (
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/pandatoufu/data-masking/internal/workspace"
)

// Session 持有 headless 会话级 OS 锁与进程内非阻塞工具闸门。
// 同一 root 仅一个活跃 headless 会话；GUI 不获取会话锁，两者互不影响。
type Session struct {
	root string
	id   string
	lock *workspace.Lock
	busy atomic.Bool
}

// OpenSession 打开会话：建私有状态目录并以非阻塞方式获取 .agent-state/session.lock，
// 同时生成进程级随机 session_id（确认凭据绑定用）。
// 已被另一个活跃 headless 持有时返回 busy（残留锁文件不阻止启动，OS 锁随进程退出释放）。
func OpenSession(root string) (*Session, error) {
	if root == "" {
		return nil, NewError(CodeWorkspaceUnavail, MessageFor(CodeWorkspaceUnavail))
	}
	stateDir := filepath.Join(root, workspace.AgentStateDir)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, NewError(CodeWorkspaceUnavail, MessageFor(CodeWorkspaceUnavail))
	}
	lock, err := workspace.AcquireLock(filepath.Join(stateDir, workspace.SessionLockFileName))
	if err != nil {
		if err == workspace.ErrBusy {
			return nil, NewError(CodeBusy, MessageFor(CodeBusy))
		}
		if err == workspace.ErrUnsafeLock {
			return nil, NewError(CodeWorkspaceUnavail, MessageFor(CodeWorkspaceUnavail))
		}
		return nil, NewError(CodeWorkspaceUnavail, MessageFor(CodeWorkspaceUnavail))
	}
	id, err := NewSessionID()
	if err != nil {
		_ = lock.Release()
		return nil, NewError(CodeInternalError, MessageFor(CodeInternalError))
	}
	return &Session{root: root, id: id, lock: lock}, nil
}

// ID 返回本会话随机 session_id。
func (s *Session) ID() string {
	if s == nil {
		return ""
	}
	return s.id
}

// Close 释放会话锁；重复调用安全。
func (s *Session) Close() error {
	if s == nil || s.lock == nil {
		return nil
	}
	return s.lock.Release()
}

// CanRecover 报告当前进程是否持有会话锁（只有持有者才可执行启动恢复）。
func (s *Session) CanRecover() bool {
	return s != nil && s.lock != nil
}

// BeginTool 以非阻塞方式进入一次工具调用；已有调用进行中时返回 busy，
// 且不改变任何任务状态。core 扫描与等待用户确认期间不占用工作区发布锁。
func (s *Session) BeginTool() error {
	if s == nil {
		return NewError(CodeBusy, MessageFor(CodeBusy))
	}
	if !s.busy.CompareAndSwap(false, true) {
		return NewError(CodeBusy, MessageFor(CodeBusy))
	}
	return nil
}

// EndTool 结束一次工具调用。
func (s *Session) EndTool() {
	if s == nil {
		return
	}
	s.busy.Store(false)
}
