package agent

import (
	"io"
	"os"
	"path/filepath"

	"github.com/pandatoufu/data-masking/internal/core"
	"github.com/pandatoufu/data-masking/internal/loader"
	"github.com/pandatoufu/data-masking/internal/workspace"
)

// RestoreRequest 是 restore_document 的入参。
type RestoreRequest struct {
	JobID        string
	ResponsePath string
}

// Restore 执行本地回程：只接受该任务 project 子树内常规文件，先校验扩展名再限量快照，
// 写 pending/<job_id>/<attempt_id>/，内部用配对 mapping 还原到 restored/<job_id>/<attempt_id>/。
func (a *App) Restore(req RestoreRequest) Response {
	if err := a.begin(); err != nil {
		return FailureResponse(err)
	}
	defer a.end()

	resp, err := a.restore(req)
	if err != nil {
		return FailureResponse(err)
	}
	return resp
}

func (a *App) restore(req RestoreRequest) (Response, error) {
	records, err := a.Store.Load(req.JobID)
	if err != nil {
		return Response{}, NewError(CodeUnknownJob, MessageFor(CodeUnknownJob))
	}
	state := DeriveState(records)
	switch state {
	case StateDelivered:
	case StateRestored:
		// restored 只接受相同字节幂等返回；不同字节返回 invalid_state 且不改变状态。
		return a.restoreIdempotent(req, records)
	default:
		return Response{}, NewError(CodeInvalidState, MessageFor(CodeInvalidState))
	}

	created, ok := createdRecord(records)
	if !ok {
		return Response{}, NewError(CodeInvalidState, MessageFor(CodeInvalidState))
	}
	project, err := ValidateProjectPath(a.Root, created.ProjectPath)
	if err != nil {
		return Response{}, err
	}
	responsePath, err := ValidateResponsePath(project, req.ResponsePath)
	if err != nil {
		return Response{}, err
	}
	cfg, err := LoadLocalConfig(a.ConfigPath)
	if err != nil {
		return Response{}, err
	}
	data, err := readLimited(responsePath, cfg.MaxFileSize)
	if err != nil {
		return Response{}, err
	}
	mappingData, err := a.loadMapping(req.JobID, records)
	if err != nil {
		return Response{}, err
	}

	attemptID, err := NewAttemptID()
	if err != nil {
		return Response{}, NewError(CodeInternalError, MessageFor(CodeInternalError))
	}
	// 先校验通过后才写 pending 快照。
	if err := a.writePending(req.JobID, attemptID, responsePath, data); err != nil {
		return Response{}, err
	}

	out, err := a.Core.Restore(filepath.Base(responsePath), data, mappingData, cfg, a.Audit)
	if err != nil {
		// 失败 attempt 隔离，任务保持 delivered 可重试。
		_ = a.Store.Append(req.JobID, Record{Type: RecFailed, AttemptID: attemptID, FailCode: string(mapCoreErrorCode(err))})
		return Response{}, mapCoreError(err)
	}
	if out == nil {
		return Response{}, NewError(CodeProcessingFailed, MessageFor(CodeProcessingFailed))
	}
	return a.publishRestored(req.JobID, attemptID, SHA256Hex(data), out)
}

// readLimited 按上限读取一次快照（上限+1 探测），超限拒绝。
func readLimited(path string, maxSize int64) ([]byte, error) {
	limit := maxSize
	if limit <= 0 {
		limit = loader.DefaultMaxSize
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, NewError(CodeInvalidProject, MessageFor(CodeInvalidProject))
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, NewError(CodeInvalidProject, MessageFor(CodeInvalidProject))
	}
	if int64(len(data)) > limit {
		return nil, NewError(CodeInputTooLarge, MessageFor(CodeInputTooLarge))
	}
	return data, nil
}

// writePending 把回复快照写入 pending/<job_id>/<attempt_id>/response.<ext>（无覆盖）。
func (a *App) writePending(jobID, attemptID, responsePath string, data []byte) error {
	dir := filepath.Join(a.Root, workspace.DirPending, jobID, attemptID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return NewError(CodeWorkspaceUnavail, MessageFor(CodeWorkspaceUnavail))
	}
	name := "response" + filepath.Ext(responsePath)
	pub := workspace.NewPublisher(workspace.OSFileOps{})
	if _, err := pub.Publish(dir, name, data); err != nil {
		return publishError(err)
	}
	return nil
}

// loadMapping 从任务记录读取配对 mapping，核对存在与指纹。
func (a *App) loadMapping(jobID string, records []Record) ([]byte, error) {
	var mappingRel, mappingSHA string
	for _, r := range records {
		if r.Type == RecPublishIntent {
			mappingRel, mappingSHA = r.MappingRel, r.MappingSHA256
		}
	}
	if mappingRel == "" {
		return nil, NewError(CodeInvalidState, MessageFor(CodeInvalidState))
	}
	if _, complete := artifactComplete(a.Root, mappingRel, mappingSHA); !complete {
		_ = a.Store.Append(jobID, Record{Type: RecFailed, FailCode: string(CodePublishFailed)})
		return nil, NewError(CodePublishFailed, MessageFor(CodePublishFailed))
	}
	data, err := os.ReadFile(filepath.Join(a.Root, filepath.FromSlash(mappingRel)))
	if err != nil {
		return nil, NewError(CodePublishFailed, MessageFor(CodePublishFailed))
	}
	return data, nil
}

// publishRestored 先记发布意图，再把还原结果无覆盖发布到 restored/<job_id>/<attempt_id>/。
func (a *App) publishRestored(jobID, attemptID, responseSHA string, out *core.RestoreOutput) (Response, error) {
	name := out.Name
	if err := ValidateArtifactName(name); err != nil {
		return Response{}, NewError(CodeInternalError, MessageFor(CodeInternalError))
	}
	rel := filepath.ToSlash(filepath.Join(workspace.DirRestored, jobID, attemptID, name))
	if err := a.Store.Append(jobID, Record{
		Type:           RecRestoreIntent,
		AttemptID:      attemptID,
		RestoredRel:    rel,
		RestoredSHA256: SHA256Hex(out.Bytes),
	}); err != nil {
		return Response{}, err
	}

	dir := filepath.Join(a.Root, workspace.DirRestored, jobID, attemptID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Response{}, NewError(CodeWorkspaceUnavail, MessageFor(CodeWorkspaceUnavail))
	}
	pub := workspace.NewPublisher(workspace.OSFileOps{})
	if _, err := pub.Publish(dir, name, out.Bytes); err != nil {
		_ = a.Store.Append(jobID, Record{Type: RecFailed, AttemptID: attemptID, FailCode: string(CodePublishFailed)})
		return Response{}, publishError(err)
	}
	warnings, err := publicWarnings(out.Warnings)
	if err != nil {
		return Response{}, err
	}
	warnings = append(warnings, docxWarnings(out.Cleanup, out.Format == "docx")...)
	warnings = append(warnings, pptxWarnings(out.PPTXCleanup, out.Format == "pptx")...)
	warnings = append(warnings, auditWarning(out.AuditFailed)...)
	if err := a.Store.Append(jobID, Record{
		Type:           RecRestored,
		AttemptID:      attemptID,
		RestoredRel:    rel,
		RestoredSHA256: SHA256Hex(out.Bytes),
		ResponseSHA256: responseSHA,
		RestoredCount:  out.RestoredCount,
		Warnings:       warnings,
		AuditFailed:    out.AuditFailed,
	}); err != nil {
		return Response{}, err
	}
	return Response{
		SchemaVersion: SchemaVersion,
		Status:        StatusRestored,
		JobID:         jobID,
		RestoredCount: out.RestoredCount,
		Warnings:      warnings,
		AuditFailed:   out.AuditFailed,
		Message:       msgRestored,
	}, nil
}

// restoreIdempotent 处理已还原任务：相同字节幂等返回，不同字节拒绝且保持 restored。
func (a *App) restoreIdempotent(req RestoreRequest, records []Record) (Response, error) {
	created, ok := createdRecord(records)
	if !ok {
		return Response{}, NewError(CodeInvalidState, MessageFor(CodeInvalidState))
	}
	project, err := ValidateProjectPath(a.Root, created.ProjectPath)
	if err != nil {
		return Response{}, err
	}
	responsePath, err := ValidateResponsePath(project, req.ResponsePath)
	if err != nil {
		return Response{}, err
	}
	data, err := readLimited(responsePath, 0)
	if err != nil {
		return Response{}, err
	}
	prev, ok := lastRestored(records)
	if !ok || prev.ResponseSHA256 == "" {
		return Response{}, NewError(CodeInvalidState, MessageFor(CodeInvalidState))
	}
	if SHA256Hex(data) != prev.ResponseSHA256 {
		// 单任务一次回程：不同字节拒绝，不覆盖已有结果、不改变 restored。
		return Response{}, NewError(CodeInvalidState, MessageFor(CodeInvalidState))
	}
	// 幂等：不再次写盘、不重复审计；结果文件缺失则明确拒绝（不猜测重建）。
	if _, complete := artifactComplete(a.Root, prev.RestoredRel, prev.RestoredSHA256); !complete {
		return Response{}, NewError(CodeInvalidState, MessageFor(CodeInvalidState))
	}
	// 同字节重试保留原一致性告警分类/固定文案与 audit_failed（不新增文件、不重复正常审计）。
	warnings, err := publicWarningsFromRecord(prev.Warnings)
	if err != nil {
		return Response{}, err
	}
	return Response{
		SchemaVersion: SchemaVersion,
		Status:        StatusRestored,
		JobID:         req.JobID,
		RestoredCount: prev.RestoredCount,
		Warnings:      warnings,
		AuditFailed:   prev.AuditFailed,
		Message:       msgRestored,
	}, nil
}

// lastRestored 返回最后一次成功还原的记录。
func lastRestored(records []Record) (Record, bool) {
	for i := len(records) - 1; i >= 0; i-- {
		if records[i].Type == RecRestored {
			return records[i], true
		}
	}
	return Record{}, false
}

// mapCoreErrorCode 返回 core 失败对应的稳定失败代码。
func mapCoreErrorCode(err error) Code {
	if ae := AsError(err); ae != nil {
		return ae.Code
	}
	return CodeProcessingFailed
}
