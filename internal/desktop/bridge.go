package desktop

// Bridge 是暴露给前端的窄绑定面：保存、未保存状态上报、退出确认、受限 UI 偏好、
// 固定目录无覆盖自动落盘，以及四类固定用途的直属文件列表/显式单文件读取。
// 不暴露配置/注入 setter 等内部方法，避免整包 App 经反射进入 JS。
type Bridge struct {
	app *App
}

// NewBridge 构造窄绑定桥。
func NewBridge(app *App) *Bridge { return &Bridge{app: app} }

// SaveFile 执行一次原生另存。suggestedName 必须是单个文件名，base64Data 为产物字节。
func (b *Bridge) SaveFile(suggestedName string, base64Data string) SaveResult {
	return b.app.Saver.Save(suggestedName, base64Data)
}

// ReportUnsaved 由前端携带业务成功响应下发的 unsaved_generation 上报是否存在未保存产物；
// generation 早于当前值时被忽略，避免旧 false 覆盖新结果 true。
func (b *Bridge) ReportUnsaved(gen uint64, hasUnsaved bool) {
	b.app.Life.SetUnsaved(gen, hasUnsaved)
}

// SetExitConfirmed 在前端完成放弃确认且退出请求已被后端接受后调用，
// 提交“已确认退出”状态，避免窗口关闭二次确认。被拒绝的退出不得调用。
func (b *Bridge) SetExitConfirmed() {
	b.app.Life.MarkConfirmedExit()
}

// SetUIPreferences 更新受限 UI 偏好（仅主题/语言合法值），返回 effective 与持久化状态。
// 不接受路径或任意配置键；非法值拒绝且不写入。
func (b *Bridge) SetUIPreferences(theme, language string) UIPrefResult {
	return b.app.SetUIPreferences(theme, language)
}

// AutoSaveMask 把脱敏配对产物自动落盘到工作区：masked/ 与 mapping/。
// 前端只提交单个文件名与字节，目标目录由方法固定决定，无法指定任意路径。
func (b *Bridge) AutoSaveMask(maskedName, maskedB64, mappingName, mappingB64 string) WorkspacePairResult {
	if b.app.Workspace == nil {
		return WorkspacePairResult{
			Masked:  WorkspaceSave{Status: workspaceStatusDisabled},
			Mapping: WorkspaceSave{Status: workspaceStatusDisabled},
		}
	}
	return b.app.Workspace.AutoSaveMask(maskedName, maskedB64, mappingName, mappingB64)
}

// AutoSaveRestored 把还原产物自动落盘到工作区 restored/。
// 前端只提交单个文件名与字节，目标目录由方法固定决定，无法指定任意路径。
func (b *Bridge) AutoSaveRestored(name, b64 string) WorkspaceSave {
	if b.app.Workspace == nil {
		return WorkspaceSave{Status: workspaceStatusDisabled}
	}
	return b.app.Workspace.AutoSaveRestored(name, b64)
}

// ListWorkspaceFiles 列举固定用途目录（original/cards/pending/mapping）的直属安全
// 文件名；只返回名称与本地化反馈，不返回正文，也不接受 root/路径/子目录。
// 与处理/保存/关闭共享原子协调：忙碌或已接受退出时返回可重试忙态，错误一定释放。
func (b *Bridge) ListWorkspaceFiles(kind string) WorkspaceFileList {
	input := b.app.Input
	if input == nil {
		return WorkspaceFileList{Status: inputStatusUnavailable, Kind: kind, Files: []string{}, MessageID: "input.root_unavailable"}
	}
	if !b.app.reserveInputOperation() {
		return WorkspaceFileList{Status: inputStatusBusy, Kind: kind, Files: []string{}, MessageID: "input.busy"}
	}
	defer b.app.releaseInputOperation()
	return input.List(kind)
}

// ReadWorkspaceFile 读取固定用途目录内用户显式选中的单个文件，返回真实文件名与
// 原始字节的 Base64；kind 受固定用途集合约束，名称按用途策略与单文件安全规则校验。
// 忙碌时返回忙态，不读取文件。
func (b *Bridge) ReadWorkspaceFile(kind, name string) WorkspaceFileContent {
	input := b.app.Input
	if input == nil {
		return WorkspaceFileContent{Status: inputStatusFailed, Kind: kind, MessageID: "input.root_unavailable"}
	}
	if !b.app.reserveInputOperation() {
		return WorkspaceFileContent{Status: inputStatusBusy, Kind: kind, MessageID: "input.busy"}
	}
	defer b.app.releaseInputOperation()
	return input.Read(kind, name)
}

// reserveInputOperation 复用保存/关闭协调状态：仅空闲时允许开始列表或读取，
// 已保留关闭后不再开始新的目录访问。它不改变未保存 generation。
func (a *App) reserveInputOperation() bool {
	if a.Saver == nil {
		return true
	}
	return a.Saver.ReserveOperation()
}

func (a *App) releaseInputOperation() {
	if a.Saver != nil {
		a.Saver.ReleaseOperation()
	}
}
