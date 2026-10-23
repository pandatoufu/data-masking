package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/pandatoufu/data-masking/internal/workspace"
)

// RecoveryAction 描述启动恢复对一个任务采取的动作。
type RecoveryAction string

const (
	RecoveryNone             RecoveryAction = "none"
	RecoveryCompletedProject RecoveryAction = "completed_project"
	RecoveryCompletedRestore RecoveryAction = "completed_restore"
	RecoveryInterrupted      RecoveryAction = "interrupted"
	RecoveryUnknownOwner     RecoveryAction = "unknown_ownership_rejected"
)

// RecoveryResult 是单个任务的恢复结果（不含任何私有内容）。
type RecoveryResult struct {
	JobID  string
	Action RecoveryAction
	State  State
}

// SHA256Hex 计算字节 SHA-256（小写 hex）。
func SHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// SHA256File 计算文件 SHA-256；文件必须存在且为常规文件。
func SHA256File(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || workspace.IsLinkOrReparse(path, info) {
		return "", errors.New("agent: 非常规文件")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// resolveRel 把工作区相对路径解析为绝对路径，拒绝越界、空与绝对路径。
func resolveRel(root, rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" || filepath.IsAbs(rel) {
		return "", errors.New("agent: 非法相对路径")
	}
	clean := filepath.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("agent: 相对路径越界")
	}
	abs := filepath.Join(root, clean)
	if !isWithin(root, abs) {
		return "", errors.New("agent: 相对路径越界")
	}
	return abs, nil
}

// artifactComplete 报告记录目标是否存在且与记录的 SHA 一致。
func artifactComplete(root, rel, sha string) (exists, complete bool) {
	abs, err := resolveRel(root, rel)
	if err != nil {
		return false, false
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.Mode().IsRegular() || workspace.IsLinkOrReparse(abs, info) {
		return false, false
	}
	if sha == "" {
		return true, false
	}
	got, err := SHA256File(abs)
	if err != nil {
		return true, false
	}
	return true, got == sha
}

// RecoverJob 按只追加记录校验精确产物并补记或标记中断，不自动重算/重发，不删除归属不明文件。
// 只应由持有会话锁的进程调用。
func (s *Store) RecoverJob(root, jobID string) (RecoveryResult, error) {
	records, err := s.Load(jobID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return RecoveryResult{JobID: jobID, Action: RecoveryNone, State: StateInProgress}, nil
		}
		return RecoveryResult{}, err
	}
	state := DeriveState(records)
	if state == StateRestored || state == StateCancelled || state == StateFailed {
		return RecoveryResult{JobID: jobID, Action: RecoveryNone, State: state}, nil
	}

	intent, ok := lastUnfinishedIntent(records)
	if !ok {
		if state == StateDelivered {
			// 已交付且无未完成回程：无需动作。
			return RecoveryResult{JobID: jobID, Action: RecoveryNone, State: state}, nil
		}
		// 没有发布意图的未完成任务：prepare 未发布，记中断。
		if err := s.Append(jobID, Record{Type: RecFailed, FailCode: string(CodeInterrupted)}); err != nil {
			return RecoveryResult{}, err
		}
		return RecoveryResult{JobID: jobID, Action: RecoveryInterrupted, State: StateFailed}, nil
	}

	switch intent.Type {
	case RecProjectIntent:
		_, complete := artifactComplete(root, intent.ProjectFileRel, intent.ProjectFileSHA256)
		if complete {
			if err := s.Append(jobID, Record{Type: RecDelivered, ProjectFileRel: intent.ProjectFileRel}); err != nil {
				return RecoveryResult{}, err
			}
			return RecoveryResult{JobID: jobID, Action: RecoveryCompletedProject, State: StateDelivered}, nil
		}
		if err := s.Append(jobID, Record{Type: RecFailed, FailCode: string(CodeInterrupted)}); err != nil {
			return RecoveryResult{}, err
		}
		return RecoveryResult{JobID: jobID, Action: RecoveryInterrupted, State: StateFailed}, nil

	case RecRestoreIntent:
		_, complete := artifactComplete(root, intent.RestoredRel, intent.RestoredSHA256)
		if complete {
			if err := s.Append(jobID, Record{Type: RecRestored, AttemptID: intent.AttemptID, RestoredRel: intent.RestoredRel}); err != nil {
				return RecoveryResult{}, err
			}
			return RecoveryResult{JobID: jobID, Action: RecoveryCompletedRestore, State: StateRestored}, nil
		}
		// 未完成的回程保持原 delivered 可重试（RecFailed 的 code 反映中断）。
		if err := s.Append(jobID, Record{Type: RecFailed, AttemptID: intent.AttemptID, FailCode: string(CodeInterrupted)}); err != nil {
			return RecoveryResult{}, err
		}
		return RecoveryResult{JobID: jobID, Action: RecoveryInterrupted, State: StateDelivered}, nil

	default:
		// 私有配对发布意图：完整则补记完成；半配对只删除可确定归属的文件。
		maskedExists, maskedOK := artifactComplete(root, intent.MaskedRel, intent.MaskedSHA256)
		mappingExists, mappingOK := artifactComplete(root, intent.MappingRel, intent.MappingSHA256)
		if maskedOK && mappingOK {
			if err := s.Append(jobID, Record{Type: RecMaskedSaved, MaskedRel: intent.MaskedRel}); err != nil {
				return RecoveryResult{}, err
			}
			if err := s.Append(jobID, Record{Type: RecMappingSaved, MappingRel: intent.MappingRel}); err != nil {
				return RecoveryResult{}, err
			}
		}
		// prepare 未完成交付：记中断（不自动重发项目）。
		if (!maskedOK || !mappingOK) && (maskedExists || mappingExists) {
			if removed := removeAttributablePair(root, intent); removed == unknownPair {
				if err := s.Append(jobID, Record{Type: RecFailed, FailCode: string(CodeInterrupted)}); err != nil {
					return RecoveryResult{}, err
				}
				return RecoveryResult{JobID: jobID, Action: RecoveryUnknownOwner, State: StateFailed}, nil
			}
		}
		if err := s.Append(jobID, Record{Type: RecFailed, FailCode: string(CodeInterrupted)}); err != nil {
			return RecoveryResult{}, err
		}
		return RecoveryResult{JobID: jobID, Action: RecoveryInterrupted, State: StateFailed}, nil
	}
}

type pairRemoval int

const (
	removedNone pairRemoval = iota
	removedAttributable
	unknownPair
)

// removeAttributablePair 仅删除 SHA 与记录一致的本任务文件；不一致（归属不明）时不删除。
func removeAttributablePair(root string, intent Record) pairRemoval {
	result := removedAttributable
	any := false
	for _, art := range []struct{ rel, sha string }{
		{intent.MaskedRel, intent.MaskedSHA256},
		{intent.MappingRel, intent.MappingSHA256},
	} {
		abs, err := resolveRel(root, art.rel)
		if err != nil {
			continue
		}
		info, err := os.Lstat(abs)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		any = true
		got, err := SHA256File(abs)
		if err != nil || got != art.sha {
			result = unknownPair
			continue
		}
		if err := os.Remove(abs); err != nil {
			result = unknownPair
		}
	}
	if !any {
		return removedNone
	}
	return result
}

// lastUnfinishedIntent 返回最后一个尚无对应完成事件的发布意图。
func lastUnfinishedIntent(records []Record) (Record, bool) {
	for i := len(records) - 1; i >= 0; i-- {
		after := records[i+1:]
		switch records[i].Type {
		case RecProjectIntent:
			if !hasType(after, RecDelivered) {
				return records[i], true
			}
			return Record{}, false
		case RecRestoreIntent:
			if !hasType(after, RecRestored) {
				return records[i], true
			}
			return Record{}, false
		case RecPublishIntent:
			if !(hasType(after, RecMaskedSaved) && hasType(after, RecMappingSaved)) {
				return records[i], true
			}
			return Record{}, false
		}
	}
	return Record{}, false
}

func hasType(records []Record, t RecordType) bool {
	for _, r := range records {
		if r.Type == t {
			return true
		}
	}
	return false
}
