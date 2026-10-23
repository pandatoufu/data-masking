package desktop

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pandatoufu/data-masking/internal/core"
	"github.com/pandatoufu/data-masking/internal/loader"
	"github.com/pandatoufu/data-masking/internal/workspace"
)

// GUI 专用输入目录类型：只允许四类用途，前端不能提交 root、绝对路径或子目录。
const (
	WorkspaceInputOriginal = "original"
	WorkspaceInputCards    = "cards"
	WorkspaceInputPending  = "pending"
	WorkspaceInputMapping  = "mapping"
)

// 目录输入列表/读取状态（与自动落盘 saved/failed/disabled/busy 不同）。
const (
	inputStatusOK          = "ok"
	inputStatusEmpty       = "empty"
	inputStatusFailed      = "failed"
	inputStatusUnavailable = "unavailable"
	inputStatusBusy        = "busy"
)

// workspaceInputDirs 固定 kind→目录名映射；未知 kind 一律拒绝。
var workspaceInputDirs = map[string]string{
	WorkspaceInputOriginal: workspace.DirOriginal,
	WorkspaceInputCards:    workspace.DirCards,
	WorkspaceInputPending:  workspace.DirPending,
	WorkspaceInputMapping:  workspace.DirMapping,
}

// workspaceMappingInputLimit 是本轮快捷读取为 mapping 增加的唯一传输保护上限
// （32 MiB）。它不改变既有业务/手动入口接受范围，超限时引导用户使用手动选择。
const workspaceMappingInputLimit int64 = 32 << 20

// WorkspaceFileList 是固定目录直属文件名列表结果：只含状态、名称与本地化反馈，
// 不返回正文、Base64 或 root 路径。
type WorkspaceFileList struct {
	Status    string         `json:"status"`
	Kind      string         `json:"kind"`
	Files     []string       `json:"files"`
	MessageID string         `json:"message_id,omitempty"`
	Params    map[string]any `json:"params,omitempty"`
}

// WorkspaceFileContent 是显式选中单文件的读取结果：成功只返回真实文件名与原始字节
// 的 Base64；失败只返回受控状态与下一步 message_id，不回显文件内容。
type WorkspaceFileContent struct {
	Status    string         `json:"status"`
	Kind      string         `json:"kind"`
	Name      string         `json:"name,omitempty"`
	DataB64   string         `json:"data_b64,omitempty"`
	MessageID string         `json:"message_id,omitempty"`
	Params    map[string]any `json:"params,omitempty"`
}

// WorkspaceInput 是 GUI 专用固定目录输入服务：只列举/读取 original、cards、pending、
// mapping 的直属安全普通文件，不递归、不跟随链接、不读取 .agent-state 或任务记录。
// 它不依赖 internal/agent 或任何 MCP 业务流程。
type WorkspaceInput struct {
	root          func() string
	ensurePending func() error
	maxDoc        int64
	openFile      func(string) (*os.File, error)
	readUpTo      func(io.Reader, int64) ([]byte, error)
}

// NewWorkspaceInput 构造输入服务。root 为当前工作区根目录提供者（不可由用户配置），
// ensurePending 用于刷新时重试 pending/ 的安全补建，maxDoc 是文档类用途的有效上限
// （沿用配置 MaxFileSize，<=0 时取 loader 默认值）。
func NewWorkspaceInput(root func() string, ensurePending func() error, maxDoc int64) *WorkspaceInput {
	return &WorkspaceInput{
		root:          root,
		ensurePending: ensurePending,
		maxDoc:        maxDoc,
		openFile:      os.Open,
		readUpTo:      readUpToLimit,
	}
}

// readUpToLimit 读取至多 limit+1 字节，供调用方判断是否超限，避免无界 ReadAll。
func readUpToLimit(r io.Reader, limit int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, limit+1))
}

func (s *WorkspaceInput) rootDir() string {
	if s == nil || s.root == nil {
		return ""
	}
	return s.root()
}

func (s *WorkspaceInput) opFile(path string) (*os.File, error) {
	if s.openFile != nil {
		return s.openFile(path)
	}
	return os.Open(path)
}

func (s *WorkspaceInput) readFile(r io.Reader, limit int64) ([]byte, error) {
	if s.readUpTo != nil {
		return s.readUpTo(r, limit)
	}
	return readUpToLimit(r, limit)
}

func (s *WorkspaceInput) limitFor(kind string) int64 {
	switch kind {
	case WorkspaceInputCards:
		return core.MaxWordFileSize
	case WorkspaceInputMapping:
		return workspaceMappingInputLimit
	default:
		if s.maxDoc > 0 {
			return s.maxDoc
		}
		return loader.DefaultMaxSize
	}
}

// dirName 返回 kind 对应的固定目录名；未知 kind 返回 false。
func dirName(kind string) (string, bool) {
	name, ok := workspaceInputDirs[kind]
	return name, ok
}

// acceptsName 报告文件名是否属于该用途的固定格式（扩展名大小写不敏感）。
// original/pending 复用 loader 的统一文档候选策略（DOCX 与纯文本候选，含无后缀与点文件）；
// cards 仍仅 TXT、mapping 仍仅 JSON。只按名称判断，不读取内容。
func acceptsName(kind, name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	switch kind {
	case WorkspaceInputOriginal, WorkspaceInputPending:
		return loader.IsDocumentCandidate(name)
	case WorkspaceInputCards:
		return ext == ".txt"
	case WorkspaceInputMapping:
		return ext == ".json"
	}
	return false
}

func listFailure(status, id string, params map[string]any) WorkspaceFileList {
	return WorkspaceFileList{Status: status, Files: []string{}, MessageID: id, Params: params}
}

func contentFailure(kind, id string) WorkspaceFileContent {
	return WorkspaceFileContent{Status: inputStatusFailed, Kind: kind, MessageID: id}
}

func contentFailureParams(kind, id string, params map[string]any) WorkspaceFileContent {
	res := contentFailure(kind, id)
	res.Params = params
	return res
}

// inputDirUnavailable 构造固定目录不可用的列表结果；params 只给目录名，不给 root。
func inputDirUnavailable(dir string) WorkspaceFileList {
	return listFailure(inputStatusUnavailable, "input.dir_unavailable", map[string]any{"dir": dir + "/"})
}

// List 列举固定用途目录的直属安全文件名：只读取目录项名称与元数据，不读正文、
// 不递归、不返回链接目标；单个不安全条目不会阻断其他合法条目。
func (s *WorkspaceInput) List(kind string) WorkspaceFileList {
	name, ok := dirName(kind)
	if !ok {
		return listFailure(inputStatusFailed, "input.bad_kind", map[string]any{"kind": kind})
	}
	root := s.rootDir()
	if root == "" {
		return listFailure(inputStatusUnavailable, "input.root_unavailable", nil)
	}
	if kind == WorkspaceInputPending && s.ensurePending != nil {
		if err := s.ensurePending(); err != nil {
			return inputDirUnavailable(name)
		}
	}
	dir := filepath.Join(root, name)
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return inputDirUnavailable(name)
		}
		return listFailure(inputStatusFailed, "input.list_failed", map[string]any{"dir": name + "/"})
	}
	if !dirInfo.IsDir() || workspace.IsLinkOrReparse(dir, dirInfo) {
		return inputDirUnavailable(name)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return listFailure(inputStatusFailed, "input.list_failed", map[string]any{"dir": name + "/"})
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		entryName := entry.Name()
		if !acceptsName(kind, entryName) {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if workspace.IsLinkOrReparse(filepath.Join(dir, entryName), info) {
			continue
		}
		files = append(files, entryName)
	}
	sort.Strings(files)
	res := WorkspaceFileList{Status: inputStatusOK, Kind: kind, Files: files}
	if len(files) == 0 {
		res.Status = inputStatusEmpty
	}
	return res
}

// Read 读取固定用途目录内的单个直属文件：先做单文件名与格式校验，再按平台复核
// 目录/文件身份与链接状态，最后按用途上限限量读取。任何失败都不回显文件内容。
func (s *WorkspaceInput) Read(kind, name string) WorkspaceFileContent {
	dirNameValue, ok := dirName(kind)
	if !ok {
		return contentFailure(kind, "input.bad_kind")
	}
	if !validWorkspaceInputName(name) {
		return contentFailure(kind, "input.bad_name")
	}
	if !acceptsName(kind, name) {
		return contentFailure(kind, "input.unsupported_format")
	}
	root := s.rootDir()
	if root == "" {
		return contentFailure(kind, "input.root_unavailable")
	}
	if kind == WorkspaceInputPending && s.ensurePending != nil {
		if err := s.ensurePending(); err != nil {
			return contentFailure(kind, "input.dir_unavailable")
		}
	}
	dir := filepath.Join(root, dirNameValue)
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		return contentFailure(kind, "input.dir_unavailable")
	}
	if !dirInfo.IsDir() || workspace.IsLinkOrReparse(dir, dirInfo) {
		return contentFailure(kind, "input.dir_unavailable")
	}
	path := filepath.Join(dir, name)
	entryInfo, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return contentFailure(kind, "input.file_missing")
		}
		return contentFailure(kind, "input.read_failed")
	}
	if !entryInfo.Mode().IsRegular() || workspace.IsLinkOrReparse(path, entryInfo) {
		return contentFailure(kind, "input.not_regular")
	}

	limit := s.limitFor(kind)
	if entryInfo.Size() > limit {
		return contentFailureParams(kind, "input.too_large", map[string]any{
			"size":  inputHumanSize(entryInfo.Size()),
			"limit": inputHumanSize(limit),
		})
	}

	f, err := s.opFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return contentFailure(kind, "input.file_missing")
		}
		return contentFailure(kind, "input.read_failed")
	}
	defer func() { _ = f.Close() }()

	// 打开后复核：目录未被替换、文件仍是同一普通对象且未被替换为链接。
	opened, err := f.Stat()
	if err != nil {
		return contentFailure(kind, "input.changed")
	}
	dirAfter, err := os.Lstat(dir)
	if err != nil || !os.SameFile(dirInfo, dirAfter) {
		return contentFailure(kind, "input.changed")
	}
	afterInfo, err := os.Lstat(path)
	if err != nil || !afterInfo.Mode().IsRegular() || workspace.IsLinkOrReparse(path, afterInfo) {
		return contentFailure(kind, "input.changed")
	}
	if !opened.Mode().IsRegular() || !os.SameFile(opened, afterInfo) {
		return contentFailure(kind, "input.changed")
	}
	if opened.Size() > limit {
		return contentFailureParams(kind, "input.too_large", map[string]any{
			"size":  inputHumanSize(opened.Size()),
			"limit": inputHumanSize(limit),
		})
	}

	data, err := s.readFile(f, limit)
	if err != nil {
		return contentFailure(kind, "input.read_failed")
	}
	if int64(len(data)) > limit {
		return contentFailureParams(kind, "input.too_large", map[string]any{
			"size":  inputHumanSize(int64(len(data))),
			"limit": inputHumanSize(limit),
		})
	}
	return WorkspaceFileContent{
		Status:  inputStatusOK,
		Kind:    kind,
		Name:    name,
		DataB64: base64.StdEncoding.EncodeToString(data),
	}
}

// validWorkspaceInputName 报告名称是否为单个安全文件名：拒绝空名、遍历、两类分隔符、
// 盘符/UNC、ADS（冒号）、控制字符、尾随点/空格与 Windows 危险设备名；
// 合法中文、空格和 HTML 字面字符原样保留。
func validWorkspaceInputName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, ":") {
		return false
	}
	if strings.HasSuffix(name, " ") || strings.HasSuffix(name, ".") {
		return false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	// Windows 设备名不区分大小写，且带扩展名（NUL.txt）同样危险。
	stem := name
	if i := strings.Index(stem, "."); i >= 0 {
		stem = stem[:i]
	}
	switch strings.ToUpper(stem) {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return false
	}
	return true
}

// inputHumanSize 把字节数表示为便于提示的十进制可读值。
func inputHumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f%cB", float64(n)/float64(div), "KMGT"[exp])
}
