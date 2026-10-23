package desktop

import (
	"encoding/base64"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/pandatoufu/data-masking/internal/workspace"
)

const (
	workspaceDirCards    = workspace.DirCards
	workspaceDirMasked   = workspace.DirMasked
	workspaceDirMapping  = workspace.DirMapping
	workspaceDirOriginal = workspace.DirOriginal
	workspaceDirPending  = workspace.DirPending
	workspaceDirRestored = workspace.DirRestored

	workspaceStatusSaved    = "saved"
	workspaceStatusFailed   = "failed"
	workspaceStatusDisabled = "disabled"
	workspaceStatusBusy     = "busy"

	workspaceMaxAttempts = 1000

	workspaceUnavailableMessage = "自动落盘不可用：无法在程序所在目录创建工作区目录。仍可使用“保存”按钮手动另存产物。"

	workspaceUnresolvedRootMessage = "自动落盘不可用：无法确定程序所在目录。仍可使用“保存”按钮手动另存产物。"
)

// workspaceDirs 是 GUI 自动保存判定的五目录集合；cards/、original/ 只由用户
// 显式选择文件时读取，不自动导入。pending/ 由 Init 独立安全补建，失败只影响
// 快捷输入入口，不参与该判定；.agent-state 仍不由 GUI 创建、读取或修改。
var workspaceDirs = workspace.GUIDirs()

// WorkspaceSave 是单件自动落盘的结果状态。
type WorkspaceSave struct {
	Status    string         `json:"status"`
	Path      string         `json:"path,omitempty"`
	Message   string         `json:"message,omitempty"`
	MessageID string         `json:"message_id,omitempty"`
	Params    map[string]any `json:"params,omitempty"`
}

// WorkspacePairResult 是脱敏配对的成对自动落盘结果。
type WorkspacePairResult struct {
	Masked  WorkspaceSave `json:"masked"`
	Mapping WorkspaceSave `json:"mapping"`
}

// Workspace 在 exe 所在目录建立固定工作区并执行无覆盖自动落盘。
// Windows/Linux 共用同一契约；根目录与文件原语可注入，仅供测试与适配，不是用户可配置项。
type Workspace struct {
	mu      sync.Mutex
	root    string
	ops     FileOps
	exeDir  func() string
	inited  bool
	enabled bool
	reason  string
	// reasonID 是失败原因对应的稳定 message_id（用于本地化原生提示）。
	reasonID string
	// pendingErr 是 GUI 输入目录 pending/ 的独立初始化结果；非 nil 只表示
	// pending 快捷入口不可用，不影响 enabled/reasonID 或自动落盘判定。
	pendingErr error
}

// NewWorkspace 构造跨平台工作区（尚未初始化）。
func NewWorkspace() *Workspace {
	return &Workspace{
		ops:    OSFileOps{},
		exeDir: executableDir,
	}
}

// SetRoot 注入工作区根目录（测试/适配用，非用户配置）。空串回退 exe 所在目录。
func (w *Workspace) SetRoot(root string) {
	w.mu.Lock()
	w.root = root
	w.mu.Unlock()
}

// Root 返回当前工作区根目录。
func (w *Workspace) Root() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.root
}

// SetFileOps 注入文件系统原语（测试用）。nil 时使用本地实现。
func (w *Workspace) SetFileOps(ops FileOps) {
	w.mu.Lock()
	w.ops = ops
	w.mu.Unlock()
}

func (w *Workspace) fileOps() FileOps {
	if w.ops == nil {
		return OSFileOps{}
	}
	return w.ops
}

// SetExecutableDir 注入 exe 所在目录解析器（测试/适配用）。nil 回退平台实现。
func (w *Workspace) SetExecutableDir(fn func() string) {
	w.mu.Lock()
	w.exeDir = fn
	w.mu.Unlock()
}

// resolveExecutableDir 返回注入解析器的结果；未注入时用平台实现。
func (w *Workspace) resolveExecutableDir() string {
	if w.exeDir != nil {
		return w.exeDir()
	}
	return executableDir()
}

// Init 解析根目录并创建 GUI 五目录，同时在已解析根目录下独立尝试补建 pending/
// （Windows/Linux 契约相同）。pending/ 失败不改变自动保存判定；.agent-state 仍
// 只由 headless 管理，GUI 不检查或触碰。
func (w *Workspace) Init() {
	w.mu.Lock()
	root := w.root
	if root == "" {
		root = w.resolveExecutableDir()
	}
	w.root = root
	w.mu.Unlock()
	w.initAt(root)
}

func (w *Workspace) initAt(root string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.inited = true
	if root == "" {
		w.enabled = false
		w.reason = workspaceUnresolvedRootMessage
		w.reasonID = "workspace.unresolved_root"
		w.pendingErr = workspace.ErrUnresolvedRoot
		return
	}
	// pending/ 是 GUI 可选输入目录：在已解析 root 下独立补建，即使原五目录
	// 初始化失败也单独尝试，且失败不回写 enabled/reason。
	w.pendingErr = workspace.EnsureDir(root, workspaceDirPending)
	if err := workspace.EnsureGUI(root); err != nil {
		w.enabled = false
		w.reason = workspaceUnavailableMessage
		w.reasonID = "workspace.unavailable"
		return
	}
	w.enabled = true
	w.reason = ""
}

// EnsurePending 触发一次 pending/ 的安全补建重试（供快捷入口刷新使用）。
// 成功返回 nil 并清除旧错误；失败只记录在 pendingErr，不改变自动落盘可用性。
// 根目录尚未解析时明确失败，不回退 cwd 或 executable 猜测。
func (w *Workspace) EnsurePending() error {
	w.mu.Lock()
	root := w.root
	w.mu.Unlock()
	if root == "" {
		w.mu.Lock()
		w.pendingErr = workspace.ErrUnresolvedRoot
		w.mu.Unlock()
		return workspace.ErrUnresolvedRoot
	}
	err := workspace.EnsureDir(root, workspaceDirPending)
	w.mu.Lock()
	w.pendingErr = err
	w.mu.Unlock()
	return err
}

// PendingErr 返回 pending/ 最近一次初始化/重试的错误；可用时为 nil。
func (w *Workspace) PendingErr() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.pendingErr
}

// Enabled 报告工作区是否可用且已初始化。
func (w *Workspace) Enabled() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.inited && w.enabled
}

// DisabledReason 返回初始化失败时的一次性中文提示；正常可用时为空。
func (w *Workspace) DisabledReason() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.reason
}

// DisabledReasonID 返回失败原因的稳定 message_id；无失败时为空。
func (w *Workspace) DisabledReasonID() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.reasonID
}

// executableDir 解析真实 executable 所在目录；解析失败返回空串（不回退 cwd）。
func executableDir() string {
	root, err := workspace.ExecutableRoot()
	if err != nil {
		return ""
	}
	return root
}

// deriveMaskedStem 从 core 下发的脱敏名反推原词干与最终扩展名：notes.masked.txt → notes, .txt。
// 无后缀与单段点文件使用 .txt 回退产物名（README.masked.txt、.env.masked.txt），
// 因此仍可推导完整词干并参与配对编号。
func deriveMaskedStem(maskedName string) (string, string, bool) {
	ext := filepath.Ext(maskedName)
	stemPart := strings.TrimSuffix(maskedName, ext)
	if !strings.HasSuffix(stemPart, ".masked") {
		return "", ext, false
	}
	return strings.TrimSuffix(stemPart, ".masked"), ext, true
}

// maskedCandidate 生成第 n 号脱敏候选名：n=0 用 core 原名，n>0 追加 -N 到原词干。
func maskedCandidate(maskedName, stem, ext string, n int) string {
	if n == 0 {
		return maskedName
	}
	return stem + "-" + strconv.Itoa(n) + ".masked" + ext
}

// mappingCandidate 生成第 n 号配对 mapping 候选名：n=0 用 core 原名，n>0 与 masked 共用后缀。
func mappingCandidate(mappingName, stem string, n int) string {
	if n == 0 {
		return mappingName
	}
	return stem + "-" + strconv.Itoa(n) + ".masked.mapping.json"
}

// restoredCandidate 生成第 n 号还原候选名：记号匹配时在 .restored 后加号，
// 回退名（如 restored.txt）时在最终扩展名前加号。
func restoredCandidate(name string, n int) string {
	if n == 0 {
		return name
	}
	ext := filepath.Ext(name)
	stemPart := strings.TrimSuffix(name, ext)
	suffix := "-" + strconv.Itoa(n)
	if strings.HasSuffix(stemPart, ".restored") {
		return strings.TrimSuffix(stemPart, ".restored") + ".restored" + suffix + ext
	}
	return stemPart + suffix + ext
}

// AutoSaveMask 把脱敏文件写入 masked/、配对 mapping 写入 mapping/（无覆盖）。
func (w *Workspace) AutoSaveMask(maskedName, maskedB64, mappingName, mappingB64 string) WorkspacePairResult {
	if !w.Enabled() {
		return WorkspacePairResult{
			Masked:  WorkspaceSave{Status: workspaceStatusDisabled},
			Mapping: WorkspaceSave{Status: workspaceStatusDisabled},
		}
	}
	name, _, err := validateSuggestedName(maskedName)
	if err != nil {
		return failedPairID("workspace.bad_name", err.Error())
	}
	maskedName = name
	name, _, err = validateSuggestedName(mappingName)
	if err != nil {
		return failedPairID("workspace.bad_name", err.Error())
	}
	mappingName = name
	maskedData, err := decodeArtifact(maskedB64)
	if err != nil {
		return failedPairID("workspace.bad_base64", "自动落盘失败：脱敏产物数据不是有效的 base64。")
	}
	mappingData, err := decodeArtifact(mappingB64)
	if err != nil {
		return failedPairID("workspace.bad_base64", "自动落盘失败：mapping 数据不是有效的 base64。")
	}

	root := w.Root()
	maskedDir := filepath.Join(root, workspaceDirMasked)
	mappingDir := filepath.Join(root, workspaceDirMapping)
	stem, ext, derived := deriveMaskedStem(maskedName)

	for n := 0; n <= workspaceMaxAttempts; n++ {
		if n > 0 && !derived {
			return failedPairID("workspace.no_derived_name", "自动落盘失败：脱敏产物命名不符合约定，无法生成不覆盖的新名，请手动另存。")
		}
		mName := maskedCandidate(maskedName, stem, ext, n)
		mapName := mappingCandidate(mappingName, stem, n)
		mTarget := filepath.Join(maskedDir, mName)
		mapTarget := filepath.Join(mappingDir, mapName)
		exists, xerr := w.targetsExist(mTarget, mapTarget)
		if xerr != nil {
			return failedPairID("workspace.target_check_failed", "自动落盘失败：无法检查目标文件，请手动另存。")
		}
		if exists {
			continue
		}
		res, retry := w.withLock(func() (WorkspacePairResult, bool) {
			return w.publishPair(maskedDir, mName, mTarget, maskedData, mappingDir, mapName, mapTarget, mappingData)
		})
		if res.Masked.Status == workspaceStatusBusy {
			return res
		}
		if retry {
			continue
		}
		return res
	}
	return failedPairID("workspace.too_many", "自动落盘失败：目标目录同名文件过多，已超过 1000 次尝试，请手动另存。")
}

func (w *Workspace) publishPair(maskedDir, mName, mTarget string, maskedData []byte, mappingDir, mapName, mapTarget string, mappingData []byte) (WorkspacePairResult, bool) {
	pub := workspace.NewPublisher(w.fileOps())
	paths, err := pub.PublishPair(
		workspace.Artifact{Dir: maskedDir, Name: mName, Data: maskedData},
		workspace.Artifact{Dir: mappingDir, Name: mapName, Data: mappingData},
	)
	if err != nil {
		var pe *workspace.PublishError
		if errors.As(err, &pe) && pe.Kind == workspace.PublishKindCleanup {
			// 目标已完整无覆盖发布，仅临时文件清理失败：桌面自动落盘保持“已保存”语义，
			// 不误报未保存（清理警告由原生另存路径报告）。
			return WorkspacePairResult{
				Masked:  WorkspaceSave{Status: workspaceStatusSaved, Path: paths[0], Message: "已自动保存到 masked/。", MessageID: "workspace.saved"},
				Mapping: WorkspaceSave{Status: workspaceStatusSaved, Path: paths[1], Message: "已自动保存到 mapping/。", MessageID: "workspace.saved"},
			}, false
		}
		if errors.As(err, &pe) && (pe.Kind == workspace.PublishKindLink || pe.Kind == workspace.PublishKindRollback) {
			if errors.Is(err, fs.ErrExist) {
				return WorkspacePairResult{}, true
			}
			return failedPairID(linkFailureID(pe.Err), linkFailureMessage(pe.Err)), false
		}
		dir := "masked"
		if errors.As(err, &pe) && pe.Artifact == 1 {
			dir = "mapping"
		}
		return failedPairID("workspace.temp_failed", "自动落盘失败：无法在 "+dir+"/ 创建临时文件，请检查权限或手动另存。"), false
	}
	return WorkspacePairResult{
		Masked:  WorkspaceSave{Status: workspaceStatusSaved, Path: paths[0], Message: "已自动保存到 masked/。", MessageID: "workspace.saved"},
		Mapping: WorkspaceSave{Status: workspaceStatusSaved, Path: paths[1], Message: "已自动保存到 mapping/。", MessageID: "workspace.saved"},
	}, false
}

// AutoSaveRestored 把还原文件写入 restored/（无覆盖）。
func (w *Workspace) AutoSaveRestored(name, b64 string) WorkspaceSave {
	if !w.Enabled() {
		return WorkspaceSave{Status: workspaceStatusDisabled}
	}
	name, _, err := validateSuggestedName(name)
	if err != nil {
		return wsErr("workspace.bad_name", err.Error())
	}
	data, err := decodeArtifact(b64)
	if err != nil {
		return wsErr("workspace.bad_base64", "自动落盘失败：还原产物数据不是有效的 base64。")
	}
	dir := filepath.Join(w.Root(), workspaceDirRestored)
	for n := 0; n <= workspaceMaxAttempts; n++ {
		target := filepath.Join(dir, restoredCandidate(name, n))
		exists, xerr := w.targetsExist(target)
		if xerr != nil {
			return wsErr("workspace.target_check_failed", "自动落盘失败：无法检查目标文件，请手动另存。")
		}
		if exists {
			continue
		}
		path, perr := w.publishRestored(restoredCandidate(name, n), dir, data)
		if perr != nil {
			if errors.Is(perr, workspace.ErrBusy) {
				return WorkspaceSave{Status: workspaceStatusBusy, Message: "工作区正被其他程序使用，本次未自动落盘，请稍后重试或手动另存。", MessageID: "workspace.busy"}
			}
			if errors.Is(perr, workspace.ErrUnsafeLock) {
				return wsErr("workspace.unavailable", workspaceUnavailableMessage)
			}
			var pe *workspace.PublishError
			if errors.As(perr, &pe) && pe.Kind == workspace.PublishKindCleanup {
				// 目标已完整发布，仅临时清理失败：保持“已保存”语义。
				return WorkspaceSave{Status: workspaceStatusSaved, Path: path, Message: "已自动保存到 restored/。", MessageID: "workspace.saved"}
			}
			if errors.Is(perr, fs.ErrExist) {
				continue
			}
			if errors.As(perr, &pe) && pe.Kind == workspace.PublishKindTemp {
				return wsErr("workspace.temp_failed", "自动落盘失败：无法在 restored/ 创建临时文件，请检查权限或手动另存。")
			}
			return wsErr(linkFailureID(perr), linkFailureMessage(perr))
		}
		return WorkspaceSave{Status: workspaceStatusSaved, Path: path, Message: "已自动保存到 restored/。", MessageID: "workspace.saved"}
	}
	return wsErr("workspace.too_many", "自动落盘失败：目标目录同名文件过多，已超过 1000 次尝试，请手动另存。")
}

// publishRestored 在固定工作区锁内发布一件还原产物。
func (w *Workspace) publishRestored(name, dir string, data []byte) (string, error) {
	lock, err := workspace.AcquireLock(w.lockPath())
	if err != nil {
		return "", err
	}
	defer func() { _ = lock.Release() }()
	return workspace.NewPublisher(w.fileOps()).Publish(dir, name, data)
}

// lockPath 返回固定工作区锁文件路径。
func (w *Workspace) lockPath() string {
	return filepath.Join(w.Root(), workspace.LockFileName)
}

// withLock 在固定工作区锁内执行一次短临界发布（验证状态→发布→记录）。
// 被其他进程持有时立即返回可重试忙态，不死等；core 处理与等待用户确认不持锁。
func (w *Workspace) withLock(fn func() (WorkspacePairResult, bool)) (WorkspacePairResult, bool) {
	lock, err := workspace.AcquireLock(w.lockPath())
	if err != nil {
		if errors.Is(err, workspace.ErrBusy) {
			return busyPair(), false
		}
		if errors.Is(err, workspace.ErrUnsafeLock) {
			return failedPairID("workspace.unavailable", workspaceUnavailableMessage), false
		}
		return failedPairID("workspace.target_check_failed", "自动落盘失败：无法获取工作区锁，请手动另存。"), false
	}
	defer func() { _ = lock.Release() }()
	return fn()
}

// busyPair 构造可重试忙态的成对结果。
func busyPair() WorkspacePairResult {
	s := WorkspaceSave{Status: workspaceStatusBusy, Message: "工作区正被其他程序使用，本次未自动落盘，请稍后重试或手动另存。", MessageID: "workspace.busy"}
	return WorkspacePairResult{Masked: s, Mapping: s}
}

func (w *Workspace) targetsExist(paths ...string) (bool, error) {
	ops := w.fileOps()
	for _, p := range paths {
		_, err := ops.Lstat(p)
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return false, err
		}
	}
	return false, nil
}

func decodeArtifact(b64 string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(b64)
}

func failedPair(msg string) WorkspacePairResult {
	return failedPairID("workspace.target_check_failed", msg)
}

// failedPairID 构造带稳定 message_id 的成对失败结果。
func failedPairID(id, msg string) WorkspacePairResult {
	return WorkspacePairResult{
		Masked:  WorkspaceSave{Status: workspaceStatusFailed, Message: msg, MessageID: id},
		Mapping: WorkspaceSave{Status: workspaceStatusFailed, Message: msg, MessageID: id},
	}
}

// wsErr 构造带稳定 message_id 的单件失败结果。
func wsErr(id, msg string) WorkspaceSave {
	return WorkspaceSave{Status: workspaceStatusFailed, Message: msg, MessageID: id}
}

// linkFailureID 返回无覆盖发布失败的稳定 message_id。
func linkFailureID(err error) string {
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return "workspace.link_unsupported"
	}
	return "workspace.link_unsupported"
}

func linkFailureMessage(err error) string {
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return "自动落盘失败：目标目录不支持安全的无覆盖发布（" + linkErr.Err.Error() + "），请手动另存到受支持的本地目录。"
	}
	return "自动落盘失败：无法安全发布文件，请手动另存到受支持的本地目录。"
}
