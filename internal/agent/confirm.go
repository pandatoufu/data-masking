package agent

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/pandatoufu/data-masking/internal/config"
	"github.com/pandatoufu/data-masking/internal/core"
)

// ConfirmRequest 是 confirm_operation 的入参。
type ConfirmRequest struct {
	JobID          string
	ConfirmationID string
	Decision       string
}

// Confirm 处理确认：类型/会话/任务凭据绑定，确认前重验原件/规则/词库/项目身份。
func (a *App) Confirm(req ConfirmRequest) Response {
	if err := a.begin(); err != nil {
		return FailureResponse(err)
	}
	defer a.end()

	resp, err := a.confirm(req)
	if err != nil {
		return FailureResponse(err)
	}
	return resp
}

func (a *App) confirm(req ConfirmRequest) (Response, error) {
	records, err := a.Store.Load(req.JobID)
	if err != nil {
		return Response{}, NewError(CodeUnknownJob, MessageFor(CodeUnknownJob))
	}
	state := DeriveState(records)
	switch state {
	case StateAwaitingFreq, StateAwaitingResid:
	default:
		return Response{}, NewError(CodeInvalidState, MessageFor(CodeInvalidState))
	}

	last, ok := latestConfirmation(records)
	if !ok {
		return Response{}, NewError(CodeInvalidState, MessageFor(CodeInvalidState))
	}
	wantKind := KindFrequency
	if state == StateAwaitingResid {
		wantKind = KindResidual
	}
	if last.ConfirmationID != req.ConfirmationID || last.ConfirmationKind != wantKind {
		return Response{}, NewError(CodeInvalidConfirm, MessageFor(CodeInvalidConfirm))
	}
	if SessionOf(records) != a.Session.ID() {
		return Response{}, NewError(CodeInvalidConfirm, MessageFor(CodeInvalidConfirm))
	}

	if req.Decision == DecisionCancel {
		if err := a.Store.Append(req.JobID, Record{Type: RecCancelled}); err != nil {
			return Response{}, err
		}
		// cancel 只验证当前凭据并取消，不重读 cards，也不删除原件/历史产物。
		a.forgetCards(req.JobID)
		return Response{
			SchemaVersion: SchemaVersion,
			Status:        StatusCancelled,
			JobID:         req.JobID,
			Message:       msgCancelled,
		}, nil
	}

	if wantKind == KindFrequency && req.Decision == DecisionContinueMasking {
		return a.continueMasking(req.JobID, records)
	}
	if wantKind == KindResidual && req.Decision == DecisionDeliverResidual {
		return a.deliverResidual(req.JobID, records)
	}
	return Response{}, NewError(CodeInvalidConfirm, MessageFor(CodeInvalidConfirm))
}

// latestConfirmation 返回最后一条确认态记录（要求它就是当前状态对应的记录）。
func latestConfirmation(records []Record) (Record, bool) {
	for i := len(records) - 1; i >= 0; i-- {
		switch records[i].Type {
		case RecFrequencyRequired, RecResidualRequired:
			return records[i], true
		case RecFrequencyConfirmed, RecResidualConfirmed, RecCancelled, RecDelivered, RecRestored, RecFailed:
			return Record{}, false
		}
	}
	return Record{}, false
}

// revalidate 确认前重新验证原件/配置/cards 词库/项目身份；任一变化返回 confirmation_stale。
// 返回的重读快照（含 cards 词与身份）供本次确认后的执行直接复用，避免再次加载造成集合漂移。
func (a *App) revalidate(jobID string, records []Record) (*config.Config, *RuleSnapshot, string, string, error) {
	created, ok := createdRecord(records)
	if !ok {
		return nil, nil, "", "", NewError(CodeInvalidState, MessageFor(CodeInvalidState))
	}
	stale := NewError(CodeConfirmStale, MessageFor(CodeConfirmStale))

	cfg, err := LoadLocalConfig(a.ConfigPath)
	if err != nil {
		return nil, nil, "", "", stale
	}
	snap, err := LoadRuleSnapshot(cfg, created.Profile, a.Root)
	if err != nil {
		// 词库缺失/损坏/不可读同样按 stale，不静默使用旧快照。
		return nil, nil, "", "", stale
	}
	if configFingerprint(snap) != created.ConfigFingerprint {
		return nil, nil, "", "", stale
	}
	if !a.sameCards(jobID, snap.Cards) {
		return nil, nil, "", "", stale
	}
	data, err := ReadSource(a.Root, created.SourceName, snap.MaxFileSize)
	if err != nil || SHA256Hex(data) != created.InputSHA256 {
		return nil, nil, "", "", stale
	}
	project, err := ValidateProjectPath(a.Root, created.ProjectPath)
	if err != nil || project != created.ProjectPath {
		return nil, nil, "", "", stale
	}
	return cfg, snap, created.SourceName, created.ProjectPath, nil
}

func createdRecord(records []Record) (Record, bool) {
	for _, r := range records {
		if r.Type == RecCreated {
			return r, true
		}
	}
	return Record{}, false
}

// continueMasking 在高频授权后重算高频集合并执行 core，再走私有保存与交付/残留路径。
func (a *App) continueMasking(jobID string, records []Record) (Response, error) {
	cfg, snap, sourceName, project, err := a.revalidate(jobID, records)
	if err != nil {
		_ = a.Store.Append(jobID, Record{Type: RecFailed, FailCode: string(CodeConfirmStale)})
		a.forgetCards(jobID)
		return Response{}, err
	}
	if err := a.Store.Append(jobID, Record{Type: RecFrequencyConfirmed}); err != nil {
		return Response{}, err
	}
	data, err := ReadSource(a.Root, sourceName, snap.MaxFileSize)
	if err != nil {
		_ = a.Store.Append(jobID, Record{Type: RecFailed, FailCode: string(CodeProcessingFailed)})
		a.forgetCards(jobID)
		return Response{}, err
	}
	opts := core.MaskOptions{
		Profile:           snap.ProfileName,
		ConfirmedCustom:   append([]string(nil), snap.CustomWords...),
		MaxSize:           snap.MaxFileSize,
		HighFreqThreshold: snap.HighFreqThreshold,
	}
	out, err := a.Core.Mask(sourceName, data, opts, snap.CoreConfig(cfg), a.Audit)
	if err != nil {
		var preview *core.PreviewRequiredError
		if errors.As(err, &preview) {
			// 重算后仍触发高频：固定失败，不重复副作用。
			_ = a.Store.Append(jobID, Record{Type: RecFailed, FailCode: string(CodeProcessingFailed)})
			a.forgetCards(jobID)
			return Response{}, NewError(CodeProcessingFailed, MessageFor(CodeProcessingFailed))
		}
		_ = a.Store.Append(jobID, Record{Type: RecFailed, FailCode: string(CodeProcessingFailed)})
		a.forgetCards(jobID)
		return Response{}, mapCoreError(err)
	}
	return a.finishPrepare(jobID, project, snap, out)
}

// deliverResidual 在残留授权后校验私有配对指纹并交付项目。
func (a *App) deliverResidual(jobID string, records []Record) (Response, error) {
	_, snap, _, project, err := a.revalidate(jobID, records)
	if err != nil {
		_ = a.Store.Append(jobID, Record{Type: RecFailed, FailCode: string(CodeConfirmStale)})
		a.forgetCards(jobID)
		return Response{}, err
	}
	defer a.forgetCards(jobID)
	out, err := a.loadPrivatePair(jobID, records)
	if err != nil {
		return Response{}, err
	}
	if err := a.Store.Append(jobID, Record{Type: RecResidualConfirmed}); err != nil {
		return Response{}, err
	}
	resp, err := a.deliver(jobID, project, snap, out)
	if err != nil {
		return Response{}, err
	}
	if count := residualCountOf(records); count > 0 {
		resp.Warnings = append(resp.Warnings, Warning{Kind: "residual_forward", Message: warningMessages["residual_forward"], Count: count})
	}
	return resp, nil
}

// residualCountOf 返回残留确认记录中的残留数量。
func residualCountOf(records []Record) int {
	for i := len(records) - 1; i >= 0; i-- {
		if records[i].Type == RecResidualRequired {
			return records[i].ResidualCount
		}
	}
	return 0
}

// loadPrivatePair 从记录核对私有配对产物的存在与指纹，返回可交付的脱敏内容。
func (a *App) loadPrivatePair(jobID string, records []Record) (*core.MaskOutput, error) {
	var maskedRel, mappingRel, maskedSHA, mappingSHA string
	auditFailed := false
	for _, r := range records {
		if r.Type == RecPublishIntent {
			maskedRel, mappingRel = r.MaskedRel, r.MappingRel
			maskedSHA, mappingSHA = r.MaskedSHA256, r.MappingSHA256
		}
		if r.Type == RecMaskedSaved {
			auditFailed = r.AuditFailed
		}
	}
	if maskedRel == "" || mappingRel == "" {
		return nil, NewError(CodeInvalidState, MessageFor(CodeInvalidState))
	}
	for _, art := range []struct{ rel, sha string }{{maskedRel, maskedSHA}, {mappingRel, mappingSHA}} {
		_, complete := artifactComplete(a.Root, art.rel, art.sha)
		if !complete {
			_ = a.Store.Append(jobID, Record{Type: RecFailed, FailCode: string(CodePublishFailed)})
			return nil, NewError(CodePublishFailed, MessageFor(CodePublishFailed))
		}
	}
	masked, err := os.ReadFile(filepath.Join(a.Root, filepath.FromSlash(maskedRel)))
	if err != nil {
		return nil, NewError(CodePublishFailed, MessageFor(CodePublishFailed))
	}
	name := filepath.Base(filepath.FromSlash(maskedRel))
	return &core.MaskOutput{
		MaskedName:  name,
		MaskedBytes: masked,
		MappingName: filepath.Base(filepath.FromSlash(mappingRel)),
		AuditFailed: auditFailed,
	}, nil
}
