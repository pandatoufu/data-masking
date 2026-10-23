package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pandatoufu/data-masking/internal/workspace"
)

// stateVersion 是私有任务记录的 schema 版本；未知版本明确拒绝恢复。
const stateVersion = 1

var jobIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ErrUnknownStateVersion 表示任务记录版本不受支持。
var ErrUnknownStateVersion = errors.New("agent: 未知的任务状态版本")

// ErrStateIntegrity 表示任务记录不连续或损坏。
var ErrStateIntegrity = errors.New("agent: 任务记录不完整")

// RecordType 是只追加事件的阶段标记；稳定任务状态由事件集合推导。
type RecordType string

const (
	RecCreated            RecordType = "created"
	RecFrequencyRequired  RecordType = "frequency_required"
	RecFrequencyConfirmed RecordType = "frequency_confirmed"
	RecMaskedSaved        RecordType = "masked_saved"
	RecMappingSaved       RecordType = "mapping_saved"
	RecResidualRequired   RecordType = "residual_required"
	RecResidualConfirmed  RecordType = "residual_confirmed"
	RecPublishIntent      RecordType = "publish_intent"
	RecProjectIntent      RecordType = "project_intent"
	RecDelivered          RecordType = "delivered"
	RecRestoreIntent      RecordType = "restore_intent"
	RecRestored           RecordType = "restored"
	RecCancelled          RecordType = "cancelled"
	RecFailed             RecordType = "failed"
)

// Record 是一条只追加任务事件。字段白名单固定：不含原文、词值、mapping 内容或还原正文。
type Record struct {
	Version           int        `json:"version"`
	Seq               int        `json:"seq"`
	Time              string     `json:"time"`
	JobID             string     `json:"job_id"`
	SessionID         string     `json:"session_id,omitempty"`
	Type              RecordType `json:"type"`
	SourceName        string     `json:"source_name,omitempty"`
	Profile           string     `json:"profile,omitempty"`
	ProjectPath       string     `json:"project_path,omitempty"`
	ConfigFingerprint string     `json:"config_fingerprint,omitempty"`
	InputSHA256       string     `json:"input_sha256,omitempty"`
	MaskedRel         string     `json:"masked_rel,omitempty"`
	MappingRel        string     `json:"mapping_rel,omitempty"`
	MaskedSHA256      string     `json:"masked_sha256,omitempty"`
	MappingSHA256     string     `json:"mapping_sha256,omitempty"`
	ConfirmationID    string     `json:"confirmation_id,omitempty"`
	ConfirmationKind  string     `json:"confirmation_kind,omitempty"`
	ResidualCount     int        `json:"residual_count,omitempty"`
	AttemptID         string     `json:"attempt_id,omitempty"`
	ProjectFileRel    string     `json:"project_file_rel,omitempty"`
	ProjectFileSHA256 string     `json:"project_file_sha256,omitempty"`
	RestoredRel       string     `json:"restored_rel,omitempty"`
	RestoredSHA256    string     `json:"restored_sha256,omitempty"`
	ResponseSHA256    string     `json:"response_sha256,omitempty"`
	RestoredCount     int        `json:"restored_count,omitempty"`
	// Warnings 只保存审定公开 kind/count 与固定文案（匿名，不含原文/词值/指纹），
	// 供 <job_id> 已 restored 的同字节幂等重试原样返回，不重复执行 core 或审计。
	Warnings    []Warning `json:"warnings,omitempty"`
	FailCode    string    `json:"fail_code,omitempty"`
	AuditFailed bool      `json:"audit_failed,omitempty"`
}

// Store 管理 .agent-state/<job_id>/ 下的只追加 version=1 事件文件。
type Store struct {
	stateRoot string
	now       func() time.Time
}

// NewStore 构造以工作区根为基准的任务 store。
func NewStore(root string) *Store {
	return &Store{stateRoot: filepath.Join(root, workspace.AgentStateDir), now: time.Now}
}

// StateRoot 返回私有状态目录。
func (s *Store) StateRoot() string { return s.stateRoot }

// JobDir 返回任务目录。
func (s *Store) JobDir(jobID string) string { return filepath.Join(s.stateRoot, jobID) }

// ValidJobID 报告 jobID 是否为本程序生成的合法标识（防目录跳转）。
func ValidJobID(jobID string) bool { return jobIDPattern.MatchString(jobID) }

// Append 追加一条完整事件：填写 version/seq/time/job_id，写入私有临时文件后同卷无覆盖发布。
// seq 必须紧接当前最大序号；文件已存在视为冲突。
func (s *Store) Append(jobID string, rec Record) error {
	if !ValidJobID(jobID) {
		return NewError(CodeUnknownJob, MessageFor(CodeUnknownJob))
	}
	dir := s.JobDir(jobID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return NewError(CodeWorkspaceUnavail, MessageFor(CodeWorkspaceUnavail))
	}
	existing, err := s.Load(jobID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	rec.Version = stateVersion
	rec.Seq = len(existing) + 1
	rec.JobID = jobID
	rec.Time = s.now().Format(time.RFC3339)
	data, err := json.Marshal(rec)
	if err != nil {
		return NewError(CodeInternalError, MessageFor(CodeInternalError))
	}
	target := filepath.Join(dir, eventFileName(rec.Seq))
	pub := workspace.NewPublisher(workspace.OSFileOps{})
	if _, err := pub.Publish(dir, filepath.Base(target), data); err != nil {
		return NewError(CodeBusy, MessageFor(CodeBusy))
	}
	return nil
}

// Load 按序号读取任务事件；要求从 1 起连续完整且版本受支持。
func (s *Store) Load(jobID string) ([]Record, error) {
	if !ValidJobID(jobID) {
		return nil, NewError(CodeUnknownJob, MessageFor(CodeUnknownJob))
	}
	dir := s.JobDir(jobID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	records := make([]Record, 0, len(names))
	for i, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, ErrStateIntegrity
		}
		var rec Record
		if err := json.Unmarshal(raw, &rec); err != nil {
			return nil, ErrStateIntegrity
		}
		if rec.Version != stateVersion {
			return nil, ErrUnknownStateVersion
		}
		if rec.Seq != i+1 || rec.JobID != jobID {
			return nil, ErrStateIntegrity
		}
		records = append(records, rec)
	}
	if len(records) == 0 {
		return nil, os.ErrNotExist
	}
	return records, nil
}

// Jobs 列出已有任务标识（按名排序）；不读取业务目录、不猜配对。
func (s *Store) Jobs() ([]string, error) {
	entries, err := os.ReadDir(s.stateRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() && ValidJobID(e.Name()) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// Latest 返回任务的最后一条事件。
func (s *Store) Latest(jobID string) (Record, error) {
	records, err := s.Load(jobID)
	if err != nil {
		return Record{}, err
	}
	return records[len(records)-1], nil
}

func eventFileName(seq int) string {
	return fmt.Sprintf("%06d.json", seq)
}

// State 是由事件集合推导出的稳定任务状态（响应 status 与之区分）。
type State string

const (
	StateInProgress    State = "in_progress"
	StateAwaitingFreq  State = "awaiting_frequency_confirmation"
	StateAwaitingResid State = "awaiting_residual_confirmation"
	StateDelivered     State = "delivered"
	StateRestored      State = "restored"
	StateCancelled     State = "cancelled"
	StateFailed        State = "failed"
)

// DeriveState 汇总事件得到稳定状态；终态不可回退。
func DeriveState(records []Record) State {
	state := StateInProgress
	for _, r := range records {
		switch r.Type {
		case RecFailed:
			if state != StateDelivered && state != StateRestored {
				return StateFailed
			}
			return state
		case RecCancelled:
			return StateCancelled
		case RecRestored:
			state = StateRestored
		case RecDelivered:
			if state != StateRestored {
				state = StateDelivered
			}
		case RecResidualRequired:
			if state == StateInProgress {
				state = StateAwaitingResid
			}
		case RecResidualConfirmed:
			state = StateInProgress
		case RecFrequencyRequired:
			if state == StateInProgress {
				state = StateAwaitingFreq
			}
		case RecFrequencyConfirmed:
			state = StateInProgress
		}
	}
	return state
}

// SessionOf 返回任务创建时的 session_id（无则空）。
func SessionOf(records []Record) string {
	for _, r := range records {
		if r.Type == RecCreated {
			return r.SessionID
		}
	}
	return ""
}

// SeqOf 返回当前最大序号（无记录为 0）。
func SeqOf(records []Record) int {
	if len(records) == 0 {
		return 0
	}
	return records[len(records)-1].Seq
}

// ParseSeqFromName 解析事件文件名序号（测试/恢复辅助）。
func ParseSeqFromName(name string) (int, bool) {
	if !strings.HasSuffix(name, ".json") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSuffix(name, ".json"))
	if err != nil {
		return 0, false
	}
	return n, true
}
