package desktop

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/pandatoufu/data-masking/internal/workspace"
)

// SaveStatus 是一次原生另存的结果状态。
type SaveStatus string

const (
	// SaveStatusSaved 表示产品字节已无覆盖地发布到用户选择的新文件。
	SaveStatusSaved SaveStatus = "saved"
	// SaveStatusCancelled 表示用户取消了另存对话框，未创建任何文件。
	SaveStatusCancelled SaveStatus = "cancelled"
	// SaveStatusError 表示验证/对话框/写入/发布失败，目标未被覆盖且无可用的半成品。
	SaveStatusError SaveStatus = "error"
)

// SaveResult 是 SaveFile binding 的返回结构，前端据此更新单项保存状态。
type SaveResult struct {
	Status SaveStatus `json:"status"`
	// Destination 仅在 saved 时给出实际写入路径。
	Destination string `json:"destination,omitempty"`
	// Message 是面向用户的中文反馈（旧字段兼容）。
	Message string `json:"message"`
	// MessageID/Params 是追加的展示元数据，前端按当前语言渲染。
	MessageID string         `json:"message_id,omitempty"`
	Params    map[string]any `json:"params,omitempty"`
	// CleanupWarning 在发布成功但临时文件清理失败时非空：如实报告已保存 + 清理警告。
	CleanupWarning   string `json:"cleanup_warning,omitempty"`
	CleanupWarningID string `json:"cleanup_warning_id,omitempty"`
}

// saveErr 构造带稳定 message_id 的失败结果。
func saveErr(msg, id string) SaveResult {
	return SaveResult{Status: SaveStatusError, Message: msg, MessageID: id}
}

// SaveDialog 让用户为一次另存选择目的路径。suggestedName 为不含路径分隔符的建议文件名。
// cancelled 为 true 时 path 为空且未创建任何文件。
type SaveDialog interface {
	Choose(title, suggestedName string) (path string, cancelled bool, err error)
}

// SaveCoordinator 串行协调“验证 → 对话框 → 写入 → 清理”全周期。
// 其互斥是权威的：重复请求直接返回“保存进行中”，不产生并行对话框或写入。
// 它不接受前端传入的目的绝对路径，仅接受建议文件名与 base64 数据。
// saveState 是保存/关闭共享的原子协调状态。
type saveState int

const (
	stateIdle saveState = iota
	stateSaving
	stateCloseReserved
	stateProcessing
)

type SaveCoordinator struct {
	ops    FileOps
	dialog SaveDialog
	// dialogTitle 返回另存对话框标题（展示时读取当前语言）。
	dialogTitle func() string

	state atomic.Int32 // saveState
	mu    sync.Mutex   // 串行化对话框/写入/清理
}

// NewSaveCoordinator 构造保存协调器。ops 为 nil 时使用本地文件系统实现。
func NewSaveCoordinator(dialog SaveDialog, ops FileOps) *SaveCoordinator {
	if ops == nil {
		ops = OSFileOps{}
	}
	return &SaveCoordinator{ops: ops, dialog: dialog}
}

// IsSaving 报告保存对话框/写入/清理是否仍在进行，供退出协调器阻止关停。
func (c *SaveCoordinator) IsSaving() bool { return saveState(c.state.Load()) == stateSaving }

// IsProcessing 报告业务处理（脱敏/还原 core 调用）是否在进行。
func (c *SaveCoordinator) IsProcessing() bool { return saveState(c.state.Load()) == stateProcessing }

// ReserveOperation 原子保留一次业务处理：仅当空闲时成功。
// 处理期间保存与关闭保留均失败，且关闭保留期间无法开始处理。
func (c *SaveCoordinator) ReserveOperation() bool {
	return c.state.CompareAndSwap(int32(stateIdle), int32(stateProcessing))
}

// ReleaseOperation 释放业务处理保留（完整响应后调用）。
func (c *SaveCoordinator) ReleaseOperation() {
	c.state.CompareAndSwap(int32(stateProcessing), int32(stateIdle))
}

// IsCloseReserved 报告是否已原子保留关闭/退出（此后不可再开始保存）。
func (c *SaveCoordinator) IsCloseReserved() bool {
	return saveState(c.state.Load()) == stateCloseReserved
}

// ReserveClose 原子保留关闭：仅当空闲（非保存、非已保留）时成功。
// 成功后新的 Save 一律被拒绝，直到 ReleaseClose；用于关闭检查后不可再开始保存。
func (c *SaveCoordinator) ReserveClose() bool {
	return c.state.CompareAndSwap(int32(stateIdle), int32(stateCloseReserved))
}

// ReleaseClose 释放关闭保留（关闭被用户取消或被后端拒绝退出时调用）。
func (c *SaveCoordinator) ReleaseClose() {
	c.state.CompareAndSwap(int32(stateCloseReserved), int32(stateIdle))
}

// SetDialog 注入平台另存对话框实现（在启动时注入，之后不再变更）。
func (c *SaveCoordinator) SetDialog(d SaveDialog) { c.dialog = d }

// SetDialogTitle 注入另存对话框标题提供者；nil 回退既有中文标题。
func (c *SaveCoordinator) SetDialogTitle(fn func() string) { c.dialogTitle = fn }

func (c *SaveCoordinator) saveDialogTitle() string {
	if c.dialogTitle != nil {
		return c.dialogTitle()
	}
	return "另存为"
}

// Save 执行一次另存；suggestedName 与 base64Data 均来自前端，目的路径由对话框决定。
func (c *SaveCoordinator) Save(suggestedName, base64Data string) SaveResult {
	// 与关闭共享原子状态：仅空闲可开始；保存中或已保留关闭一律拒绝。
	if !c.state.CompareAndSwap(int32(stateIdle), int32(stateSaving)) {
		return saveErr("保存进行中或正在退出，请稍后再试。", "save.in_progress")
	}
	defer c.state.Store(int32(stateIdle))
	c.mu.Lock()
	defer c.mu.Unlock()

	name, nameID, err := validateSuggestedName(suggestedName)
	if err != nil {
		return saveErr(err.Error(), nameID)
	}
	data, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return saveErr("保存失败：产物数据不是有效的 base64，请重试。", "save.bad_base64")
	}
	if c.dialog == nil {
		return saveErr("保存失败：当前环境没有可用的另存对话框。", "save.no_dialog")
	}
	dest, cancelled, err := c.dialog.Choose(c.saveDialogTitle(), name)
	if err != nil {
		return saveErr("保存失败：无法打开另存对话框，请重试。", "save.dialog_failed")
	}
	if cancelled || strings.TrimSpace(dest) == "" {
		return SaveResult{Status: SaveStatusCancelled, Message: "已取消保存，结果仍可再次保存。", MessageID: "save.cancelled"}
	}
	return c.publish(dest, data)
}

func (c *SaveCoordinator) publish(dest string, data []byte) SaveResult {
	dir := filepath.Dir(dest)
	dirInfo, err := c.ops.Lstat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return saveErr("保存失败：目的目录不存在，请另选一个本地目录。", "save.dir_missing")
		}
		return saveErr("保存失败：无法访问目的目录，请重试或另选目录。", "save.dir_unreadable")
	}
	if !dirInfo.IsDir() {
		return saveErr("保存失败：目的路径的父级不是目录。", "save.parent_not_dir")
	}

	// 目标存在（含符号链接）即拒绝，绝不覆盖源文件或已有输出。
	if _, err := c.ops.Lstat(dest); err == nil {
		return saveErr("已存在同名文件，未覆盖。请换一个文件名。", "save.target_exists")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return saveErr("保存失败：无法检查目标文件，请重试或另选文件名。", "save.target_uncheckable")
	}

	f, err := workspace.WriteTempFile(c.ops, dir, data)
	if err != nil {
		return saveTempFailure(err)
	}
	temp := f.Name()

	// 无覆盖发布：目标在检查后被其他进程创建时 Link 仍失败，不会覆盖目标。
	if err := c.ops.Link(temp, dest); err != nil {
		_ = c.ops.Remove(temp)
		if errors.Is(err, fs.ErrExist) {
			return saveErr("目标文件已被其他程序创建，未覆盖。请换一个文件名重试。", "save.race_exists")
		}
		return saveErr(linkErrorMessage(err), linkErrorID(err))
	}

	if err := c.ops.Remove(temp); err != nil {
		// 发布已成功但临时清理失败：必须如实报告已保存 + 清理警告，不能误报未保存。
		return SaveResult{
			Status:           SaveStatusSaved,
			Destination:      dest,
			Message:          "已保存。",
			MessageID:        "save.saved",
			CleanupWarning:   "已保存，但临时文件清理失败，请手动检查目的目录。",
			CleanupWarningID: "save.cleanup_warning",
		}
	}
	return SaveResult{Status: SaveStatusSaved, Destination: dest, Message: "已保存。", MessageID: "save.saved"}
}

func saveTempFailure(err error) SaveResult {
	var pe *workspace.PublishError
	if errors.As(err, &pe) {
		switch pe.Kind {
		case workspace.PublishKindWrite:
			return saveErr("保存失败：写入临时文件出错，已清理，请重试。", "save.write_failed")
		case workspace.PublishKindSync:
			return saveErr("保存失败：同步数据出错，已清理，请重试。", "save.sync_failed")
		case workspace.PublishKindClose:
			return saveErr("保存失败：关闭临时文件出错，已清理，请重试。", "save.close_failed")
		}
	}
	return saveErr("保存失败：无法在目的目录创建临时文件，请检查权限或另选目录。", "save.temp_failed")
}

// linkErrorMessage 把无覆盖发布失败翻译为中文指引；无法识别时给出保守的文件系统提示。
func linkErrorMessage(err error) string {
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return fmt.Sprintf("保存失败：该目录不支持安全的无覆盖发布（%v），请另选本地支持的目录。", linkErr.Err)
	}
	return "保存失败：无法在该目录安全发布文件，请另选本地支持的目录。"
}

// linkErrorID 返回无覆盖发布失败的稳定 message_id。
func linkErrorID(err error) string {
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return "save.link_unsupported"
	}
	return "save.link_failed"
}

// validateSuggestedName 拒绝空名、路径分隔符与目录跳转，保证建议名是单个文件名。
// id 为失败时对应的稳定 message_id。
func validateSuggestedName(name string) (string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "save.name_missing", errors.New("保存失败：缺少建议文件名。")
	}
	if strings.ContainsAny(name, `/\`) {
		return "", "save.name_has_separator", errors.New("保存失败：建议文件名不能包含路径分隔符。")
	}
	if name == "." || name == ".." {
		return "", "save.name_invalid", errors.New("保存失败：建议文件名无效。")
	}
	return name, "", nil
}
