package agent

// RecoverAll 在会话锁持有者启动时执行恢复：
// 先按发布意图校验物理完成度（补记成功或记中断），再把不同 session_id 的未确认任务
// 追加 failed/code=interrupted 并作废旧凭据。不扫描 original/cards/pending 或项目找配对。
func (s *Store) RecoverAll(root, currentSession string) ([]RecoveryResult, error) {
	jobs, err := s.Jobs()
	if err != nil {
		return nil, NewError(CodeWorkspaceUnavail, MessageFor(CodeWorkspaceUnavail))
	}
	results := make([]RecoveryResult, 0, len(jobs))
	for _, job := range jobs {
		res, err := s.RecoverJob(root, job)
		if err != nil {
			if err == ErrUnknownStateVersion {
				results = append(results, RecoveryResult{JobID: job, Action: RecoveryNone, State: StateFailed})
				continue
			}
			return results, err
		}
		if res.Action == RecoveryNone {
			// 旧会话的未确认任务：作废凭据并标记中断。
			records, lerr := s.Load(job)
			if lerr != nil {
				return results, lerr
			}
			state := DeriveState(records)
			if (state == StateAwaitingFreq || state == StateAwaitingResid) && SessionOf(records) != currentSession {
				if err := s.Append(job, Record{Type: RecFailed, FailCode: string(CodeInterrupted)}); err != nil {
					return results, err
				}
				res = RecoveryResult{JobID: job, Action: RecoveryInterrupted, State: StateFailed}
			}
		}
		results = append(results, res)
	}
	return results, nil
}

// PreviousSessionsInvalidated 报告某个任务是否属于其他会话且未确认（测试/诊断辅助）。
func PreviousSessionsInvalidated(records []Record, currentSession string) bool {
	state := DeriveState(records)
	if state != StateAwaitingFreq && state != StateAwaitingResid {
		return false
	}
	return SessionOf(records) != currentSession
}
