package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/pandatoufu/data-masking/internal/audit"
	"github.com/pandatoufu/data-masking/internal/buildinfo"
	"github.com/pandatoufu/data-masking/internal/core"
	"github.com/pandatoufu/data-masking/internal/loader"
	"github.com/pandatoufu/data-masking/internal/workspace"
)

// App 是 headless 应用层：组装工作区、严格配置读取、任务 store、会话锁与共享 core。
type App struct {
	Root       string
	ConfigPath string
	Store      *Store
	Session    *Session
	Audit      *audit.Log
	Core       Masker

	// snapshots 保存本次会话内 prepare 时的 cards 身份（FileInfo），
	// 仅用于确认前 os.SameFile 重验；不持久化、不序列化。
	snapMu    sync.Mutex
	snapshots map[string]*CardSnapshot
}

// NewApp 构造应用层；Core 为 nil 时使用共享 core。
func NewApp(root, configPath string) *App {
	return &App{
		Root:       root,
		ConfigPath: configPath,
		Store:      NewStore(root),
		Core:       DefaultMasker{},
		snapshots:  map[string]*CardSnapshot{},
	}
}

// rememberCards 保存本次会话的 cards 身份；只驻留内存。
func (a *App) rememberCards(jobID string, cards *CardSnapshot) {
	a.snapMu.Lock()
	defer a.snapMu.Unlock()
	if a.snapshots == nil {
		a.snapshots = map[string]*CardSnapshot{}
	}
	a.snapshots[jobID] = cards
}

// storedCards 取出本次会话保存的 cards 身份。
func (a *App) storedCards(jobID string) (*CardSnapshot, bool) {
	a.snapMu.Lock()
	defer a.snapMu.Unlock()
	cards, ok := a.snapshots[jobID]
	return cards, ok
}

// forgetCards 清除本次会话的 cards 身份（确认结束或任务进入终态）。
func (a *App) forgetCards(jobID string) {
	a.snapMu.Lock()
	defer a.snapMu.Unlock()
	delete(a.snapshots, jobID)
}

// Open 打开会话并执行启动恢复（仅会话锁持有者可恢复）。
func (a *App) Open() error {
	session, err := OpenSession(a.Root)
	if err != nil {
		return err
	}
	a.Session = session
	if _, err := a.Store.RecoverAll(a.Root, session.ID()); err != nil {
		_ = session.Close()
		a.Session = nil
		return err
	}
	return nil
}

// Close 释放会话锁。
func (a *App) Close() error {
	if a == nil || a.Session == nil {
		return nil
	}
	return a.Session.Close()
}

// PrepareRequest 是 prepare_document 的入参。
type PrepareRequest struct {
	SourceName  string
	ProjectPath string
	Profile     string
}

// Prepare 执行去程：门禁 → 严格配置 → 限量读取 → core.Mask → 高频门禁或私有保存与交付。
func (a *App) Prepare(req PrepareRequest) Response {
	if err := a.begin(); err != nil {
		return FailureResponse(err)
	}
	defer a.end()

	resp, err := a.prepare(req)
	if err != nil {
		return FailureResponse(err)
	}
	return resp
}

func (a *App) begin() error {
	if a.Session == nil {
		return NewError(CodeWorkspaceUnavail, MessageFor(CodeWorkspaceUnavail))
	}
	return a.Session.BeginTool()
}

func (a *App) end() {
	if a.Session != nil {
		a.Session.EndTool()
	}
}

func (a *App) prepare(req PrepareRequest) (Response, error) {
	if err := ValidateSourceName(req.SourceName); err != nil {
		return Response{}, err
	}
	project, err := ValidateProjectPath(a.Root, req.ProjectPath)
	if err != nil {
		return Response{}, err
	}
	cfg, err := LoadLocalConfig(a.ConfigPath)
	if err != nil {
		return Response{}, err
	}
	snap, err := LoadRuleSnapshot(cfg, req.Profile, a.Root)
	if err != nil {
		return Response{}, err
	}
	data, err := ReadSource(a.Root, req.SourceName, snap.MaxFileSize)
	if err != nil {
		return Response{}, err
	}

	jobID, err := NewJobID()
	if err != nil {
		return Response{}, NewError(CodeInternalError, MessageFor(CodeInternalError))
	}
	sessionID := a.Session.ID()
	fingerprint := configFingerprint(snap)
	if err := a.Store.Append(jobID, Record{
		Type:              RecCreated,
		SessionID:         sessionID,
		SourceName:        req.SourceName,
		Profile:           snap.ProfileName,
		ProjectPath:       project,
		ConfigFingerprint: fingerprint,
		InputSHA256:       SHA256Hex(data),
	}); err != nil {
		return Response{}, err
	}
	a.rememberCards(jobID, snap.Cards)

	opts := core.MaskOptions{
		Profile:           snap.ProfileName,
		MaxSize:           snap.MaxFileSize,
		HighFreqThreshold: snap.HighFreqThreshold,
	}
	out, err := a.Core.Mask(req.SourceName, data, opts, snap.CoreConfig(cfg), a.Audit)
	if err != nil {
		var preview *core.PreviewRequiredError
		if errors.As(err, &preview) {
			return a.awaitingFrequency(jobID, snap, preview)
		}
		a.forgetCards(jobID)
		return Response{}, mapCoreError(err)
	}
	if out == nil {
		a.forgetCards(jobID)
		return Response{}, NewError(CodeProcessingFailed, MessageFor(CodeProcessingFailed))
	}

	return a.finishPrepare(jobID, project, snap, out)
}

// awaitingFrequency 返回高频匿名确认态：只披露词数、降序频次与阈值，不产出替换结果。
func (a *App) awaitingFrequency(jobID string, snap *RuleSnapshot, preview *core.PreviewRequiredError) (Response, error) {
	counts := mergedHitCounts(preview)
	confirmationID, err := NewConfirmationID()
	if err != nil {
		return Response{}, NewError(CodeInternalError, MessageFor(CodeInternalError))
	}
	if err := a.Store.Append(jobID, Record{
		Type:             RecFrequencyRequired,
		ConfirmationID:   confirmationID,
		ConfirmationKind: KindFrequency,
	}); err != nil {
		return Response{}, err
	}
	return Response{
		SchemaVersion:    SchemaVersion,
		Status:           StatusAwaitingFrequency,
		JobID:            jobID,
		Profile:          snap.ProfileName,
		ConfirmationID:   confirmationID,
		ConfirmationKind: KindFrequency,
		AllowedDecisions: []string{DecisionContinueMasking, DecisionCancel},
		WordCount:        len(counts),
		HitCounts:        counts,
		Threshold:        snap.HighFreqThreshold,
		Message:          renderFrequencyMessage(len(counts), counts, snap.HighFreqThreshold),
	}, nil
}

// mergedHitCounts 把本次词/常备词命中合并为降序频次数组（不含词、序号或词表位置）。
func mergedHitCounts(preview *core.PreviewRequiredError) []int {
	all := make([]int, 0, len(preview.Extra)+len(preview.Builtin))
	for _, c := range preview.Extra {
		all = append(all, c)
	}
	for _, c := range preview.Builtin {
		all = append(all, c)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(all)))
	return all
}

func renderFrequencyMessage(wordCount int, counts []int, threshold int) string {
	parts := make([]string, 0, len(counts))
	for _, c := range counts {
		parts = append(parts, strconv.Itoa(c))
	}
	msg := strings.ReplaceAll(msgAwaitingFrequency, "{word_count}", strconv.Itoa(wordCount))
	msg = strings.ReplaceAll(msg, "{threshold}", strconv.Itoa(threshold))
	msg = strings.ReplaceAll(msg, "{hit_counts}", strings.Join(parts, ","))
	return msg
}

// finishPrepare 私有保存配对产物，并按正向残留决定交付或独立确认。
func (a *App) finishPrepare(jobID, project string, snap *RuleSnapshot, out *core.MaskOutput) (Response, error) {
	if err := a.savePrivatePair(jobID, out); err != nil {
		return Response{}, err
	}
	residual := forwardResidualCount(out)
	if residual > 0 {
		confirmationID, err := NewConfirmationID()
		if err != nil {
			return Response{}, NewError(CodeInternalError, MessageFor(CodeInternalError))
		}
		if err := a.Store.Append(jobID, Record{
			Type:             RecResidualRequired,
			ConfirmationID:   confirmationID,
			ConfirmationKind: KindResidual,
			ResidualCount:    residual,
		}); err != nil {
			return Response{}, err
		}
		return Response{
			SchemaVersion:    SchemaVersion,
			Status:           StatusAwaitingResidual,
			JobID:            jobID,
			Profile:          snap.ProfileName,
			ConfirmationID:   confirmationID,
			ConfirmationKind: KindResidual,
			AllowedDecisions: []string{DecisionDeliverResidual, DecisionCancel},
			ResidualCount:    residual,
			Message:          strings.ReplaceAll(msgAwaitingResidual, "{residual_count}", strconv.Itoa(residual)),
		}, nil
	}
	defer a.forgetCards(jobID)
	return a.deliver(jobID, project, snap, out)
}

// forwardResidualCount 返回正向残留告警的命中数量。
func forwardResidualCount(out *core.MaskOutput) int {
	for _, w := range out.Warnings {
		if w.Kind == "residual_forward" {
			if w.Count > 0 {
				return w.Count
			}
			return 1
		}
	}
	return 0
}

// savePrivatePair 在私有工作区保存 masked/ 与 mapping/ 配对（先记发布意图，再发布，后记完成）。
func (a *App) savePrivatePair(jobID string, out *core.MaskOutput) error {
	maskedName := out.MaskedName
	if err := ValidateArtifactName(maskedName); err != nil {
		// 核心下发的脱敏名必须是可用单文件名；否则按内部错误拒绝。
		return NewError(CodeInternalError, MessageFor(CodeInternalError))
	}
	mappingName := out.MappingName
	if err := ValidateArtifactName(mappingName); err != nil {
		return NewError(CodeInternalError, MessageFor(CodeInternalError))
	}
	maskedRel := filepath.ToSlash(filepath.Join(workspace.DirMasked, jobID, maskedName))
	mappingRel := filepath.ToSlash(filepath.Join(workspace.DirMapping, jobID, mappingName))
	if err := a.Store.Append(jobID, Record{
		Type:          RecPublishIntent,
		MaskedRel:     maskedRel,
		MappingRel:    mappingRel,
		MaskedSHA256:  SHA256Hex(out.MaskedBytes),
		MappingSHA256: SHA256Hex(out.MappingBytes),
	}); err != nil {
		return err
	}

	lock, err := workspace.AcquireLock(filepath.Join(a.Root, workspace.LockFileName))
	if err != nil {
		return publishError(err)
	}
	defer func() { _ = lock.Release() }()

	maskedDir := filepath.Join(a.Root, workspace.DirMasked, jobID)
	mappingDir := filepath.Join(a.Root, workspace.DirMapping, jobID)
	if err := os.MkdirAll(maskedDir, 0o755); err != nil {
		return NewError(CodeWorkspaceUnavail, MessageFor(CodeWorkspaceUnavail))
	}
	if err := os.MkdirAll(mappingDir, 0o755); err != nil {
		return NewError(CodeWorkspaceUnavail, MessageFor(CodeWorkspaceUnavail))
	}
	pub := workspace.NewPublisher(workspace.OSFileOps{})
	if _, err := pub.PublishPair(
		workspace.Artifact{Dir: maskedDir, Name: maskedName, Data: out.MaskedBytes},
		workspace.Artifact{Dir: mappingDir, Name: mappingName, Data: out.MappingBytes},
	); err != nil {
		_ = a.Store.Append(jobID, Record{Type: RecFailed, FailCode: string(CodePublishFailed)})
		return publishError(err)
	}
	if err := a.Store.Append(jobID, Record{Type: RecMaskedSaved, MaskedRel: maskedRel, AuditFailed: out.AuditFailed}); err != nil {
		return err
	}
	if err := a.Store.Append(jobID, Record{Type: RecMappingSaved, MappingRel: mappingRel}); err != nil {
		return err
	}
	return nil
}

// deliver 无覆盖复制单个脱敏文件到项目 .masked-input/<job_id>/input.masked.<ext>。
func (a *App) deliver(jobID, project string, snap *RuleSnapshot, out *core.MaskOutput) (Response, error) {
	dest, rel, err := a.publishProjectCopy(jobID, project, out)
	if err != nil {
		return Response{}, err
	}
	if err := a.Store.Append(jobID, Record{Type: RecDelivered, ProjectFileRel: rel, ProjectFileSHA256: SHA256Hex(out.MaskedBytes)}); err != nil {
		return Response{}, err
	}
	warnings, err := publicWarnings(out.Warnings)
	if err != nil {
		return Response{}, err
	}
	warnings = append(warnings, docxWarnings(out.Cleanup, out.Format == "docx")...)
	warnings = append(warnings, pptxWarnings(out.PPTXCleanup, out.Format == "pptx")...)
	warnings = append(warnings, auditWarning(out.AuditFailed)...)
	return Response{
		SchemaVersion: SchemaVersion,
		Status:        StatusDelivered,
		JobID:         jobID,
		Profile:       snap.ProfileName,
		ProjectFile:   dest,
		Message:       msgDelivered,
		Warnings:      warnings,
		AuditFailed:   out.AuditFailed,
	}, nil
}

// publishProjectCopy 把脱敏副本无覆盖交付到项目；先记发布意图，跨卷用目标卷内临时文件发布。
func (a *App) publishProjectCopy(jobID, project string, out *core.MaskOutput) (string, string, error) {
	ext := filepath.Ext(out.MaskedName)
	fileName := "input.masked" + ext
	jobDir := filepath.Join(project, ".masked-input", jobID)
	rel := filepath.ToSlash(filepath.Join(".masked-input", jobID, fileName))

	if _, err := os.Lstat(jobDir); err == nil {
		// 任务目标目录必须全新，禁止覆盖已有内容或链接。
		return "", "", NewError(CodePublishFailed, MessageFor(CodePublishFailed))
	}
	if err := a.Store.Append(jobID, Record{
		Type:              RecProjectIntent,
		ProjectFileRel:    rel,
		ProjectFileSHA256: SHA256Hex(out.MaskedBytes),
	}); err != nil {
		return "", "", err
	}
	parent := filepath.Join(project, ".masked-input")
	if info, err := os.Lstat(parent); err == nil {
		if !info.IsDir() || workspace.IsLinkOrReparse(parent, info) {
			return "", "", NewError(CodePublishFailed, MessageFor(CodePublishFailed))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", NewError(CodePublishFailed, MessageFor(CodePublishFailed))
	} else if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", "", NewError(CodePublishFailed, MessageFor(CodePublishFailed))
	}
	if err := os.Mkdir(jobDir, 0o755); err != nil {
		return "", "", NewError(CodePublishFailed, MessageFor(CodePublishFailed))
	}
	pub := workspace.NewPublisher(workspace.OSFileOps{})
	dest := filepath.Join(jobDir, fileName)
	if _, err := pub.Publish(jobDir, fileName, out.MaskedBytes); err != nil {
		_ = os.Remove(jobDir)
		return "", "", publishError(err)
	}
	return dest, rel, nil
}

// publishError 把无覆盖发布失败映射为稳定失败代码。
func publishError(err error) error {
	if err == nil {
		return nil
	}
	if err == workspace.ErrBusy {
		return NewError(CodeBusy, MessageFor(CodeBusy))
	}
	if err == workspace.ErrUnsafeLock {
		return NewError(CodeWorkspaceUnavail, MessageFor(CodeWorkspaceUnavail))
	}
	return NewError(CodePublishFailed, MessageFor(CodePublishFailed))
}

// mapCoreError 把 core/loader 的拒绝映射到固定失败代码，绝不回显原错误字符串。
func mapCoreError(err error) error {
	var he *loader.HumanError
	if errors.As(err, &he) {
		switch {
		case strings.HasPrefix(he.MessageID, "file.too_large"):
			return NewError(CodeInputTooLarge, MessageFor(CodeInputTooLarge))
		case strings.HasPrefix(he.MessageID, "file."), strings.HasPrefix(he.MessageID, "docx."),
			strings.HasPrefix(he.MessageID, "pptx."), strings.HasPrefix(he.MessageID, "mapping."):
			return NewError(CodeUnsupportedInput, MessageFor(CodeUnsupportedInput))
		}
	}
	var pe *core.PreviewRequiredError
	if errors.As(err, &pe) {
		return NewError(CodeProcessingFailed, MessageFor(CodeProcessingFailed))
	}
	return NewError(CodeProcessingFailed, MessageFor(CodeProcessingFailed))
}

// configFingerprint 计算实际生效规则/cards 私有指纹/阈值与构建版本的指纹。
// 不包含词值、候选名或路径：cards 仅以最终 64 字符 hex 参与摘要。
func configFingerprint(snap *RuleSnapshot) string {
	rules := make([]string, 0, len(snap.Rules))
	for k, v := range snap.Rules {
		rules = append(rules, string(k)+"="+strconv.FormatBool(v))
	}
	sort.Strings(rules)
	payload, _ := json.Marshal(map[string]any{
		"profile":   snap.ProfileName,
		"rules":     rules,
		"cards":     snap.CardsFingerprint(),
		"max_size":  snap.MaxFileSize,
		"threshold": snap.HighFreqThreshold,
		"build":     buildinfo.Effective(),
	})
	return SHA256Hex(payload)
}

// publicWarnings 由 warnings.go 提供。
